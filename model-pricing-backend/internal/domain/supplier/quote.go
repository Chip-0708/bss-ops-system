// Package supplier 的 quote.go：供应商报价提交链路（05-quotes.md §3/§4/§5）。
// 覆盖：提交报价（九步校验 + 单事务落库）、报价历史、报价详情。
// 金额与倍率一律 shopspring/decimal，禁止 float64 参与任何金额运算（红线 1）。
package supplier

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// ---- 领域错误（message 面向供应商可直接展示） ----

var (
	// ErrQuoteItemsEmpty items 为空。
	ErrQuoteItemsEmpty = errors.New("报价明细不能为空")
	// ErrQuoteItemsTooMany items 超过单票上限。
	ErrQuoteItemsTooMany = errors.New("单次最多提交 500 行，请拆分后重新提交")
	// ErrQuoteDuplicateSku 同一报价单内 sku_id 重复。
	ErrQuoteDuplicateSku = errors.New("同一报价单内 SKU 重复")
	// ErrQuoteSkuNotQuotable SKU 不存在或不在可报价集合内。
	ErrQuoteSkuNotQuotable = errors.New("SKU 不存在或不可报价")
	// ErrQuoteValidToInvalid valid_to 未晚于 valid_from。
	ErrQuoteValidToInvalid = errors.New("有效期止必须晚于生效时间")
	// ErrQuoteFxTierRequired USD 模型必须选择汇率档位。
	ErrQuoteFxTierRequired = errors.New("USD 模型必须选择汇率档位")
	// ErrQuoteFxTierInvalid 汇率档位不在固定 8 档内。
	ErrQuoteFxTierInvalid = errors.New("汇率档位不在可选范围内（6.5/6.7/6.75/6.8/6.85/6.9/6.95/7.0）")
	// ErrQuoteMultiplierForbidden SKU 无官方价（或该组件无官方价）时不得传倍率。
	ErrQuoteMultiplierForbidden = errors.New("该 SKU 无官方价基准，只能使用绝对价模式（multiplier 必须为 null）")
	// ErrQuotePriceInconsistent 倍率与折算价不自洽（容差 1e-4）。
	ErrQuotePriceInconsistent = errors.New("倍率与折算价不自洽")
	// ErrQuotePriceInvalid 金额/倍率字符串无法解析为数值。
	ErrQuotePriceInvalid = errors.New("金额或倍率格式非法")
	// ErrQuoteComponentEmpty 明细行组件为空或行内 component_type 重复。
	ErrQuoteComponentEmpty = errors.New("每个明细行至少 1 个组件，且组件类型不得重复")
	// ErrQuoteComponentTypeUnknown component_type 不在固定 12 个集合内。
	ErrQuoteComponentTypeUnknown = errors.New("未知的组件类型")
	// ErrQuoteConstraintKeyUnknown constraints 含未知 key。
	ErrQuoteConstraintKeyUnknown = errors.New("未知的约束字段")
	// ErrQuoteConflict 非终态互斥 / 唯一索引并发兜底。
	ErrQuoteConflict = errors.New("已存在审批中或待生效的报价单，请先等待审批完成")
	// ErrQuoteNotFound 报价单不存在或不属于当前供应商（详情越权按 404 处理，防探测）。
	ErrQuoteNotFound = errors.New("报价单不存在")
)

// ---- 固定集合（05-quotes.md §0.3.1 / §0.4 / §0.5，已定稿） ----

// maxQuoteItems 是单票报价的明细行上限（§3）。
const maxQuoteItems = 500

// priceTolerance 是倍率-价格自洽复核的绝对容差（§3 步骤 7，1e-4）。
var priceTolerance = decimal.NewFromFloat(0.0001)

// componentTypes 是 quote_component.component_type 的固定集合（§0.3.1）。
var componentTypes = map[string]bool{
	"input": true, "output": true, "cached_input": true,
	"cache_write_5m": true, "cache_write_1h": true, "reasoning": true,
	"embedding": true, "request": true,
	"image_input": true, "image_output": true,
	"audio_input": true, "audio_output": true,
}

// constraintKeys 是 quote_item.constraints_ 的固定 key 集合（§0.5）。
var constraintKeys = map[string]bool{
	"rpm": true, "tpm": true, "concurrency": true,
	"daily_quota": true, "actual_context": true, "compatibility": true,
}

// fxTiers 是汇率档位的固定 8 档（§0.4）。
// 匹配按数值等价（"6.8"/"6.80"/"6.800" 同档），命中后回显规范形式。
var fxTiers = []struct {
	Canonical string
	Value     decimal.Decimal
}{
	{"6.5", decimal.RequireFromString("6.5")},
	{"6.7", decimal.RequireFromString("6.7")},
	{"6.75", decimal.RequireFromString("6.75")},
	{"6.8", decimal.RequireFromString("6.8")},
	{"6.85", decimal.RequireFromString("6.85")},
	{"6.9", decimal.RequireFromString("6.9")},
	{"6.95", decimal.RequireFromString("6.95")},
	{"7.0", decimal.RequireFromString("7.0")},
}

// normalizeFxTier 按数值等价匹配 8 档，命中返回规范形式；未命中返回 false。
func normalizeFxTier(raw string) (string, bool) {
	v, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	for _, t := range fxTiers {
		if v.Equal(t.Value) {
			return t.Canonical, true
		}
	}
	return "", false
}

// CanonicalFxTier 把库存的 fx_tier 文本（如 "6.800"/"6.80"）规范化为档位回显形式
// （如 "6.8"）；null 保持 null。历史数据若不在 8 档内则原样返回（不静默改值）。
// 契约 §0.4：响应回显规范形式。
func CanonicalFxTier(raw *string) *string {
	if raw == nil {
		return nil
	}
	if canonical, ok := normalizeFxTier(*raw); ok {
		return &canonical
	}
	return raw
}

// ---- 输入 DTO ----

// SubmitQuoteInput 是提交报价的输入（§3 请求体）。
type SubmitQuoteInput struct {
	ValidFrom time.Time         `json:"valid_from" binding:"required"`
	ValidTo   time.Time         `json:"valid_to" binding:"required"`
	Remark    string            `json:"remark"`
	Items     []SubmitQuoteItem `json:"items" binding:"required"`
}

// SubmitQuoteItem 是一行报价明细。
type SubmitQuoteItem struct {
	SKUID       int64                  `json:"sku_id" binding:"required"`
	FxTier      *string                `json:"fx_tier"`
	Constraints map[string]any         `json:"constraints"`
	Components  []SubmitQuoteComponent `json:"components" binding:"required"`
}

// SubmitQuoteComponent 是一个组件报价。Multiplier 用 *string 区分「未传」与「传了值」。
type SubmitQuoteComponent struct {
	ComponentType string  `json:"component_type" binding:"required"`
	Multiplier    *string `json:"multiplier"`
	UnitPrice     string  `json:"unit_price" binding:"required"`
}

// ---- 输出 DTO（json tag 全小写，与契约一致） ----

// SubmitQuoteResult 是提交报价的响应（§3 data）。
type SubmitQuoteResult struct {
	ID          int64     `json:"id"`
	SupplierID  int64     `json:"supplier_id"`
	VersionNo   int       `json:"version_no"`
	Status      string    `json:"status"`
	ValidFrom   time.Time `json:"valid_from"`
	ValidTo     time.Time `json:"valid_to"`
	Source      string    `json:"source"`
	Retroactive bool      `json:"retroactive"`
	Clamped     bool      `json:"clamped"`
	ItemCount   int       `json:"item_count"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// QuoteHistoryQuery 是报价历史查询条件（§4）。
type QuoteHistoryQuery struct {
	Status string
	SkuID  *int64
	From   *time.Time
	To     *time.Time
	Page   int
	Size   int
}

// QuoteHistoryResult 是报价历史分页结果。
type QuoteHistoryResult struct {
	List  []QuoteHistoryItem `json:"list"`
	Total int64              `json:"total"`
	Page  int                `json:"page"`
	Size  int                `json:"size"`
}

// QuoteHistoryItem 是历史列表的一行（§4 data.list[]）。
type QuoteHistoryItem struct {
	ID          int64          `json:"id"`
	VersionNo   int            `json:"version_no"`
	Status      string         `json:"status"`
	ValidFrom   time.Time      `json:"valid_from"`
	ValidTo     time.Time      `json:"valid_to"`
	Source      string         `json:"source"`
	ItemCount   int            `json:"item_count"`
	SubmittedAt *time.Time     `json:"submitted_at"`
	Decision    *QuoteDecision `json:"decision"`
}

// QuoteDecision 是审批结论（§4 decision）。result 取值 APPROVED / REJECTED；未审批为 null。
type QuoteDecision struct {
	Result    string     `json:"result"`
	Reason    *string    `json:"reason"`
	DecidedAt *time.Time `json:"decided_at"`
}

// QuoteDetail 是报价详情（§5 data）。
type QuoteDetail struct {
	ID          int64             `json:"id"`
	SupplierID  int64             `json:"supplier_id"`
	VersionNo   int               `json:"version_no"`
	Status      string            `json:"status"`
	ValidFrom   time.Time         `json:"valid_from"`
	ValidTo     time.Time         `json:"valid_to"`
	Source      string            `json:"source"`
	Retroactive bool              `json:"retroactive"`
	Remark      *string           `json:"remark"`
	SubmittedAt *time.Time        `json:"submitted_at"`
	Items       []QuoteDetailItem `json:"items"`
	Decision    *QuoteDecision    `json:"decision"`
}

// QuoteDetailItem 是详情的一行明细。
type QuoteDetailItem struct {
	ID          int64                  `json:"id"`
	SKUID       int64                  `json:"sku_id"`
	SKUCode     string                 `json:"sku_code"`
	ModelName   string                 `json:"model_name"`
	Currency    string                 `json:"currency"`
	FxTier      *string                `json:"fx_tier"`
	Constraints map[string]any         `json:"constraints"`
	Components  []QuoteDetailComponent `json:"components"`
}

// QuoteDetailComponent 是详情的一个组件。
type QuoteDetailComponent struct {
	ComponentType string  `json:"component_type"`
	Multiplier    *string `json:"multiplier"`
	UnitPrice     string  `json:"unit_price"`
}

// ---- 仓储接口 ----

// QuotableSKU 是一个可报价 SKU 的最小字段集（校验与展示用）。
type QuotableSKU struct {
	ID             int64
	SkuCode        string
	NativeCurrency string
	// ModelName LI-005：与 CSV 模板展示列比对（family_name + sku_code 拼接，与
	// FindTemplateSKUs 的 ModelName 同源——supplier_import.go:110）。不参与入库，
	// 不一致时仅 WARN。
	ModelName string
}

// OfficialComponent 是官方价当前版本的一个组件价（自洽复核基准）。
type OfficialComponent struct {
	ComponentType string
	UnitPrice     decimal.Decimal
}

// NonTerminalQuote 是阻塞新提交的非终态报价摘要，仅用于返回可行动的冲突信息。
type NonTerminalQuote struct {
	ID       int64
	SKUCount int
}

// QuoteStore 是报价链路的仓储接口（GORM 实现见 internal/repo/supplier_quote.go）。
// 与 identity.go 的 Store 并列独立，避免 fake 被迫实现无关方法。
type QuoteStore interface {
	// FindQuotableSKUs 按 ID 批量取回可报价 SKU（lifecycle ∈ PUBLISHED/PURCHASABLE/PENDING_VERIFY）。
	// 返回 map[skuID]QuotableSKU；不在可报价集合内的 ID 不出现在 map 中。
	FindQuotableSKUs(ctx context.Context, skuIDs []int64) (map[int64]QuotableSKU, error)
	// FindOfficialComponents 按 SKU 批量取回当前官方价组件（is_current=true 且 currency=native）。
	// 返回 map[skuID][]OfficialComponent；无官方价的 SKU 不出现在 map 中。
	FindOfficialComponents(ctx context.Context, skuIDs []int64) (map[int64][]OfficialComponent, error)
	// FindNonTerminalQuote 返回阻塞提交的非终态报价及 SKU 数；不存在返回 nil, nil。
	FindNonTerminalQuote(ctx context.Context, supplierID int64) (*NonTerminalQuote, error)
	// CreateQuoteWithTodo 单事务落库：quote_sheet + quote_item + quote_component + todo_task + audit_log。
	// version_no 由仓储在事务内计算（max(同供应商)+1）；唯一索引兜底命中时返回 ErrQuoteConflict。
	CreateQuoteWithTodo(ctx context.Context, p CreateQuoteParams) (*SubmitQuoteResult, error)
	// ListQuoteHistory 按供应商硬过滤的分页历史（§4）。
	ListQuoteHistory(ctx context.Context, supplierID int64, q QuoteHistoryQuery) (*QuoteHistoryResult, error)
	// GetQuoteDetail 按 id + supplier_id 取详情；不属于该供应商时返回 (nil, nil)。
	GetQuoteDetail(ctx context.Context, supplierID, quoteID int64) (*QuoteDetail, error)
}

// CreateQuoteParams 是 CreateQuoteWithTodo 的落库参数（校验已全部通过后的规范形态）。
type CreateQuoteParams struct {
	Supplier    *Supplier
	OperatorID  int64 // subject_operator.id（submitted_by / created_by / audit operator_id）
	RequestID   string
	ValidFrom   time.Time
	ValidTo     time.Time
	Clamped     bool // valid_from 被钳制为提交时刻（§3 步骤 3）
	Remark      string
	Items       []CreateQuoteItem
	SubmittedAt time.Time
	// Source 报价来源：MANUAL（手工）/ IMPORT（批量导入）/ RETRO / SILENT_FOLLOW。
	// 零值兜底 MANUAL，保证 5a-2 的调用方不传时行为不变。
	Source string
}

// CreateQuoteItem 是一行规范化的明细。
type CreateQuoteItem struct {
	SKUID       int64
	SkuCode     string
	Currency    string
	FxTier      *string // 已规范化为 8 档规范形式；CNY 模型恒为 nil
	Constraints map[string]any
	Components  []CreateQuoteComponent
}

// CreateQuoteComponent 是一个规范化的组件。
type CreateQuoteComponent struct {
	ComponentType string
	Multiplier    *decimal.Decimal // 绝对价模式为 nil
	UnitPrice     decimal.Decimal
}

// ---- 领域服务 ----

// QuoteService 是报价链路的领域服务。
type QuoteService struct {
	store QuoteStore
	now   func() time.Time
}

// NewQuoteService 构造报价服务。
func NewQuoteService(store QuoteStore) *QuoteService {
	return &QuoteService{store: store, now: func() time.Time { return time.Now().UTC() }}
}

// SubmitQuote 提交报价（§3）。校验顺序严格按契约 1→9（步骤 1 幂等由中间件完成）。
// 任一校验失败返回携带可展示 message 的领域错误；落库在单事务内完成。
func (s *QuoteService) SubmitQuote(ctx context.Context, sup *Supplier, in SubmitQuoteInput, operatorID int64, requestID string) (*SubmitQuoteResult, error) {
	return s.submitQuoteWithSource(ctx, sup, in, operatorID, requestID, "MANUAL")
}

// SubmitImportQuote 批量导入确认入库（§6.3）。与 SubmitQuote 共用同一套九步校验，
// 仅 source='IMPORT'、审计 action='QUOTE_IMPORT'。不跳过审批（同样 APPROVING + todo_task）。
func (s *QuoteService) SubmitImportQuote(ctx context.Context, sup *Supplier, in SubmitQuoteInput, operatorID int64, requestID string) (*SubmitQuoteResult, error) {
	return s.submitQuoteWithSource(ctx, sup, in, operatorID, requestID, "IMPORT")
}

// submitQuoteWithSource 是提交报价的私有实现（source 区分 MANUAL/IMPORT）。
func (s *QuoteService) submitQuoteWithSource(ctx context.Context, sup *Supplier, in SubmitQuoteInput, operatorID int64, requestID, source string) (*SubmitQuoteResult, error) {
	// 步骤 2：items 非空、行数上限、sku_id 无重复
	if len(in.Items) == 0 {
		return nil, ErrQuoteItemsEmpty
	}
	if len(in.Items) > maxQuoteItems {
		return nil, ErrQuoteItemsTooMany
	}
	seen := make(map[int64]bool, len(in.Items))
	skuIDs := make([]int64, 0, len(in.Items))
	for _, it := range in.Items {
		if seen[it.SKUID] {
			return nil, fmt.Errorf("%w：sku_id=%d", ErrQuoteDuplicateSku, it.SKUID)
		}
		seen[it.SKUID] = true
		skuIDs = append(skuIDs, it.SKUID)
		// 行内组件校验（§0.3.1：至少 1 个、行内不重复）与未知 key 校验（步骤 9 提前到此处
		// 一并做，避免先查库再因静态错误浪费一次往返；对外语义与契约顺序一致——都是 400）
		if err := validateComponents(it.Components); err != nil {
			return nil, err
		}
		if err := validateConstraints(it.Constraints); err != nil {
			return nil, err
		}
	}

	// 步骤 2 续：全部 SKU 在可报价集合内
	skus, err := s.store.FindQuotableSKUs(ctx, skuIDs)
	if err != nil {
		return nil, err
	}
	for _, id := range skuIDs {
		if _, ok := skus[id]; !ok {
			return nil, fmt.Errorf("%w：sku_id=%d", ErrQuoteSkuNotQuotable, id)
		}
	}

	// 步骤 3：过去时间静默钳制为 now（不报错，响应 clamped: true）
	now := s.now()
	validFrom := in.ValidFrom.UTC()
	clamped := false
	if validFrom.Before(now) {
		validFrom = now
		clamped = true
	}

	// 步骤 4：valid_to > valid_from
	validTo := in.ValidTo.UTC()
	if !validTo.After(validFrom) {
		return nil, ErrQuoteValidToInvalid
	}

	// 官方价基准（步骤 7 自洽复核用）
	officials, err := s.store.FindOfficialComponents(ctx, skuIDs)
	if err != nil {
		return nil, err
	}

	// 逐行规范化：币种锁定（步骤 5）、汇率档位（步骤 6）、自洽复核（步骤 7）
	normItems := make([]CreateQuoteItem, 0, len(in.Items))
	for _, it := range in.Items {
		sku := skus[it.SKUID]
		norm := CreateQuoteItem{
			SKUID:       it.SKUID,
			SkuCode:     sku.SkuCode,
			Currency:    sku.NativeCurrency, // 步骤 5：币种锁定，不接受入参
			Constraints: it.Constraints,
		}

		// 步骤 6：汇率档位
		if sku.NativeCurrency == "USD" {
			if it.FxTier == nil || strings.TrimSpace(*it.FxTier) == "" {
				return nil, fmt.Errorf("%w：sku=%s", ErrQuoteFxTierRequired, sku.SkuCode)
			}
			canonical, ok := normalizeFxTier(*it.FxTier)
			if !ok {
				return nil, fmt.Errorf("%w：sku=%s fx_tier=%q", ErrQuoteFxTierInvalid, sku.SkuCode, *it.FxTier)
			}
			norm.FxTier = &canonical
		}
		// CNY 模型传了 fx_tier：置 null 不报错（§0.4），norm.FxTier 保持 nil

		// 步骤 7：倍率-价格双向自洽复核
		officialByType := make(map[string]decimal.Decimal, len(officials[it.SKUID]))
		for _, oc := range officials[it.SKUID] {
			officialByType[oc.ComponentType] = oc.UnitPrice
		}
		for _, comp := range it.Components {
			nc := CreateQuoteComponent{ComponentType: comp.ComponentType}
			unitPrice, err := decimal.NewFromString(strings.TrimSpace(comp.UnitPrice))
			if err != nil {
				return nil, fmt.Errorf("%w：sku=%s %s unit_price=%q", ErrQuotePriceInvalid, sku.SkuCode, comp.ComponentType, comp.UnitPrice)
			}
			nc.UnitPrice = unitPrice

			official, hasOfficial := officialByType[comp.ComponentType]
			if comp.Multiplier != nil && strings.TrimSpace(*comp.Multiplier) != "" {
				// 倍率模式：SKU 无官方价（或该组件无官方价）→ 400
				if !hasOfficial {
					return nil, fmt.Errorf("%w：sku=%s %s", ErrQuoteMultiplierForbidden, sku.SkuCode, comp.ComponentType)
				}
				multiplier, err := decimal.NewFromString(strings.TrimSpace(*comp.Multiplier))
				if err != nil {
					return nil, fmt.Errorf("%w：sku=%s %s multiplier=%q", ErrQuotePriceInvalid, sku.SkuCode, comp.ComponentType, *comp.Multiplier)
				}
				// |unit_price − official × multiplier| ≤ 1e-4
				diff := unitPrice.Sub(official.Mul(multiplier)).Abs()
				if diff.Cmp(priceTolerance) > 0 {
					return nil, fmt.Errorf("%w：sku=%s %s（官方价 %s × 倍率 %s = %s，实报 %s）",
						ErrQuotePriceInconsistent, sku.SkuCode, comp.ComponentType,
						official.String(), multiplier.String(), official.Mul(multiplier).String(), unitPrice.String())
				}
				nc.Multiplier = &multiplier
			}
			// 绝对价模式（multiplier 未传/空串）：跳过自洽复核，Multiplier 保持 nil
			norm.Components = append(norm.Components, nc)
		}
		normItems = append(normItems, norm)
	}

	// 步骤 8：非终态互斥（§1.1）
	conflict, err := s.store.FindNonTerminalQuote(ctx, sup.ID)
	if err != nil {
		return nil, err
	}
	if conflict != nil {
		return nil, fmt.Errorf("%w：该供应商已有一张待审批报价（quote_sheet_id=%d，包含 %d 个 SKU），请先完成审批或等待驳回后再提交新报价", ErrQuoteConflict, conflict.ID, conflict.SKUCount)
	}

	// 落库（单事务，含 todo_task 与 audit_log）
	return s.store.CreateQuoteWithTodo(ctx, CreateQuoteParams{
		Supplier:    sup,
		OperatorID:  operatorID,
		RequestID:   requestID,
		ValidFrom:   validFrom,
		ValidTo:     validTo,
		Clamped:     clamped,
		Remark:      in.Remark,
		Items:       normItems,
		SubmittedAt: now,
		Source:      source,
	})
}

// validateComponents 校验一行明细的组件集合：非空、行内不重复、类型在固定集合内。
func validateComponents(comps []SubmitQuoteComponent) error {
	if len(comps) == 0 {
		return ErrQuoteComponentEmpty
	}
	seen := make(map[string]bool, len(comps))
	for _, c := range comps {
		if !componentTypes[c.ComponentType] {
			return fmt.Errorf("%w：%q", ErrQuoteComponentTypeUnknown, c.ComponentType)
		}
		if seen[c.ComponentType] {
			return fmt.Errorf("%w：%s 重复", ErrQuoteComponentEmpty, c.ComponentType)
		}
		seen[c.ComponentType] = true
	}
	return nil
}

// validateConstraints 校验非价格约束的 key 集合（§0.5：未知 key 拒绝写入）。
func validateConstraints(constraints map[string]any) error {
	for k := range constraints {
		if !constraintKeys[k] {
			return fmt.Errorf("%w：%q", ErrQuoteConstraintKeyUnknown, k)
		}
	}
	return nil
}

// ListQuoteHistory 报价历史（§4）：只返回登录主体自己的报价。
func (s *QuoteService) ListQuoteHistory(ctx context.Context, supplierID int64, q QuoteHistoryQuery) (*QuoteHistoryResult, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 || q.Size > 100 {
		q.Size = 20
	}
	return s.store.ListQuoteHistory(ctx, supplierID, q)
}

// GetQuoteDetail 报价详情（§5）：越权（不是自己的）返回 ErrQuoteNotFound（404，防探测）。
func (s *QuoteService) GetQuoteDetail(ctx context.Context, supplierID, quoteID int64) (*QuoteDetail, error) {
	detail, err := s.store.GetQuoteDetail(ctx, supplierID, quoteID)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, ErrQuoteNotFound
	}
	return detail, nil
}
