package pricing

// Package pricing 的 service.go：定价策略配置 + 生成价目表草稿（08-pricing.md §1/§2，阶段 8a）。
//
// 口径与裁决（全部来自 stage8a 提示词，与真实 DDL 000005_pricing.up.sql 对齐）：
//   - 字段名以 DDL 为准：price_method / param_value / rounding_rule（契约 0.1 的
//     markup_type/markup_value/floor_rule 与 DDL 不符，契约待更新——遗留 8a-⑦）。
//   - scope_type 真实枚举：ALL/VENDOR/FAMILY/SKU（MODEL_TYPE 本批不支持——遗留 8a-⑤，
//     scope_id 是 bigint 与 model_sku.model_type 是 varchar 类型不匹配）。
//   - price_method 真实枚举：MARGIN/COST_UP/OFFICIAL_ANCHOR/FIXED（TIERED 不在 DDL，
//     代码拒绝非 4 种——遗留 8a-②）。
//   - 多条策略命中只取 priority 最小的一条，不叠加（裁决 3 / 待裁决点 1）。
//   - floor = unit_cost / (1 - min_gross_margin)，与 cost.Floor() 同一份纯函数（不重写）。
//   - 草稿落 price_book(status='DRAFT')，version_no = MAX(version_no)+1（不是 0，
//     避免撞 uk_pb_ver UNIQUE(level_code, version_no)——约束 2）；created_by NOT NULL（约束 3）。
//   - blocked_count 只是 floor_violation=true 的统计，不阻止生成草稿（裁决 7，发布校验在 8b）。

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"model_bss/internal/domain/cost"
)

// ---- 枚举（真实 DDL 注释，禁止自创——红线 4）----

// scope_type 枚举（000005 注释：ALL/MODEL_TYPE/VENDOR/FAMILY/SKU）。
const (
	ScopeAll       = "ALL"
	ScopeModelType = "MODEL_TYPE" // 本批不支持（遗留 8a-⑤）
	ScopeVendor    = "VENDOR"
	ScopeFamily    = "FAMILY"
	ScopeSKU       = "SKU"
)

// price_method 枚举（000005 注释：MARGIN/COST_UP/OFFICIAL_ANCHOR/FIXED）。
const (
	MethodMargin         = "MARGIN"          // 目标毛利率：price = cost / (1 - param)
	MethodCostUp         = "COST_UP"         // 成本加成率：price = cost × (1 + param)
	MethodOfficialAnchor = "OFFICIAL_ANCHOR" // 官方价锚定：price = official × param
	MethodFixed          = "FIXED"           // 固定价：price = param（与成本无关）
)

// 策略状态（000005 DEFAULT 'ACTIVE'）。
const (
	PolicyStatusDraft    = "DRAFT"
	PolicyStatusActive   = "ACTIVE"
	PolicyStatusArchived = "ARCHIVED"
)

// price_book 状态（000005 DEFAULT 'DRAFT'）。
const (
	BookStatusDraft     = "DRAFT"
	BookStatusEffective = "EFFECTIVE"
	BookStatusExpired   = "EXPIRED"
)

// ---- 领域错误（errors.Is 可判定）----

var (
	ErrPolicyNotFound       = errors.New("定价策略不存在")                                                  //nolint:revive // 变量组错误，沿用8a命名
	ErrInvalidScopeType     = errors.New("scope_type 非法（支持 ALL/VENDOR/FAMILY/SKU）")                  //nolint:revive
	ErrInvalidPriceMethod   = errors.New("price_method 非法（支持 MARGIN/COST_UP/OFFICIAL_ANCHOR/FIXED）") //nolint:revive
	ErrInvalidParamValue    = errors.New("param_value 非法（必须是非负数字符串）")                                //nolint:revive
	ErrModelTypeUnsupported = errors.New("scope_type=MODEL_TYPE 本批不支持（遗留 8a-⑤）")                     //nolint:revive
	ErrNoCostBaseline       = errors.New("SKU 无当前成本基线，无法定价")                                         //nolint:revive
	ErrNoOfficialPrice      = errors.New("OFFICIAL_ANCHOR 策略需要当前官方价，但该 SKU 无 price_version")         //nolint:revive
)

// ---- 领域模型 ----

// PricingPolicy 定价策略（字段名以 DDL 为准）。
//
//nolint:revive // 命名以包前缀消歧（pricing.PricingPolicy），8a 起全链路引用，改名属破坏性重构
type PricingPolicy struct {
	ID          int64   `json:"id"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	ScopeType   string  `json:"scope_type"`
	ScopeID     *int64  `json:"scope_id"`   // ALL 时为 NULL
	LevelCode   *string `json:"level_code"` // 叠加客户等级；NULL=全部等级命中
	PriceMethod string  `json:"price_method"`
	ParamValue  string  `json:"param_value"` // 金额字符串（numeric(12,6)）
	Priority    int     `json:"priority"`
	Status      string  `json:"status"`
}

// SKUContext 策略命中判定所需的 SKU 上下文。
type SKUContext struct {
	SKUID    int64
	SKUCode  string
	VendorID int64
	FamilyID int64
	Currency string
}

// DiffItem 是 diff_report 的一行。
type DiffItem struct {
	SKUID          int64   `json:"sku_id"`
	SKUCode        string  `json:"sku_code"`
	OldPrice       *string `json:"old_price"`
	NewPrice       string  `json:"new_price"`
	DeltaPct       *string `json:"delta_pct"`
	FloorPrice     string  `json:"floor_price"`
	FloorViolation bool    `json:"floor_violation"`
}

// PriceBookDraft 是生成草稿的响应 data。
type PriceBookDraft struct {
	DraftID      int64      `json:"draft_id"`
	LevelCode    string     `json:"level_code"`
	Currency     string     `json:"currency"`
	ItemCount    int        `json:"item_count"`
	DiffReport   []DiffItem `json:"diff_report"`
	BlockedCount int        `json:"blocked_count"`
}

// ---- 纯函数 ----

// CalculatePrice 按 4 种真实 price_method 计算售价。
//
//	MARGIN:           cost / (1 - paramValue)   （paramValue 是目标毛利率，如 0.15）
//	COST_UP:          cost × (1 + paramValue)   （paramValue 是加成率，如 0.10）
//	OFFICIAL_ANCHOR:  officialPrice × paramValue（paramValue 是倍数）
//	FIXED:            paramValue                （与成本无关）
//
// officialPrice 仅 OFFICIAL_ANCHOR 用；其他方法传 decimal.Zero 即可。
func CalculatePrice(method string, costPrice, officialPrice, paramValue decimal.Decimal) (decimal.Decimal, error) {
	switch method {
	case MethodMargin:
		denom := decimal.NewFromInt(1).Sub(paramValue)
		if denom.LessThanOrEqual(decimal.Zero) {
			return decimal.Zero, fmt.Errorf("MARGIN param_value=%s 必须小于 1", paramValue)
		}
		return costPrice.Div(denom), nil
	case MethodCostUp:
		return costPrice.Mul(decimal.NewFromInt(1).Add(paramValue)), nil
	case MethodOfficialAnchor:
		return officialPrice.Mul(paramValue), nil
	case MethodFixed:
		return paramValue, nil
	default:
		return decimal.Zero, ErrInvalidPriceMethod
	}
}

// MatchPolicy 按优先级取第一条命中的策略（priority 数字越小越优先，不叠加）。
// level_code 匹配规则：策略 level_code 为 NULL（全部等级）或等于请求的 levelCode 时命中。
// 无命中返回 nil（该 SKU 跳过，不生成 diff_report 行）。
func MatchPolicy(policies []PricingPolicy, sku SKUContext, levelCode string) *PricingPolicy {
	var hit *PricingPolicy
	for i := range policies {
		p := &policies[i]
		if p.Status != PolicyStatusActive {
			continue
		}
		// level_code 叠加过滤：策略指定了等级且不等于请求等级 → 不命中。
		if p.LevelCode != nil && *p.LevelCode != levelCode {
			continue
		}
		if !scopeMatches(p, sku) {
			continue
		}
		if hit == nil || p.Priority < hit.Priority {
			hit = p
		}
	}
	return hit
}

// scopeMatches 判定策略的 scope 是否命中该 SKU（MODEL_TYPE 本批不支持，返回 false）。
func scopeMatches(p *PricingPolicy, sku SKUContext) bool {
	switch p.ScopeType {
	case ScopeAll:
		return true
	case ScopeVendor:
		return p.ScopeID != nil && *p.ScopeID == sku.VendorID
	case ScopeFamily:
		return p.ScopeID != nil && *p.ScopeID == sku.FamilyID
	case ScopeSKU:
		return p.ScopeID != nil && *p.ScopeID == sku.SKUID
	default:
		return false // MODEL_TYPE 等本批不支持
	}
}

// ---- 服务 ----

// PricingStore 是定价的仓储接口（GORM 实现见 internal/repo/pricing.go）。
//
//nolint:revive // 命名以包前缀消歧（pricing.PricingStore），8a 起全链路引用，改名属破坏性重构
type PricingStore interface {
	// 策略 CRUD
	ListPolicies(ctx context.Context, page, size int) ([]PricingPolicy, int64, error)
	CreatePolicy(ctx context.Context, p *PricingPolicy, operatorID int64, requestID string) error
	UpdatePolicy(ctx context.Context, p *PricingPolicy, operatorID int64, requestID string) error
	LoadPolicyByID(ctx context.Context, id int64) (*PricingPolicy, error)
	LoadPoliciesByIDs(ctx context.Context, ids []int64) ([]PricingPolicy, error)
	// 生成草稿的读侧
	LoadSKUContexts(ctx context.Context, skuIDs []int64) ([]SKUContext, error)                        // skuIDs 空=全量在架
	LoadCurrentUnitCosts(ctx context.Context, skuIDs []int64) (map[int64]UnitCostInfo, error)         // 代表组件 unit_cost + baseline version
	LoadCurrentOfficialPrices(ctx context.Context, skuIDs []int64) (map[int64]decimal.Decimal, error) // 代表组件官方价（OFFICIAL_ANCHOR 用）
	LoadOldPrices(ctx context.Context, levelCode string, skuIDs []int64) (map[int64]string, error)    // 上一 EFFECTIVE 版本售价
	LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error)
	// 生成草稿的写侧（单事务）
	SaveDraft(ctx context.Context, in SaveDraftInput, operatorID int64, requestID string) (int64, error)
}

// UnitCostInfo 是一个 SKU 当前成本基线的代表组件信息。
type UnitCostInfo struct {
	UnitCost        decimal.Decimal
	UnitCostBasis   string // 代表组件 component_type（落 price_book_component 用，避免硬编码 input 漂移）
	BaselineVersion int
	Currency        string
}

// SaveDraftInput 是落草稿的全部输入。
type SaveDraftInput struct {
	LevelCode  string
	Currency   string
	DiffReport []DiffItem
	// Items 每个 SKU 的落库明细（与 DiffReport 对齐，多带落库所需字段）。
	Items []DraftItem
}

// DraftItem 是一个 SKU 的落库明细。
type DraftItem struct {
	SKUID           int64
	Currency        string
	FloorPrice      string
	PolicyID        int64
	BaselineVersion int
	ComponentType   string // 代表组件类型（price_book_component.component_type，与代表组件口径一致）
	NewPrice        string // 代表组件售价（price_book_component 逐组件，本批只落代表组件）
}

// PricingService 是定价服务（无状态，可并发）。
//
//nolint:revive // 命名以包前缀消歧（pricing.PricingService），8a 起全链路引用，改名属破坏性重构
type PricingService struct {
	store PricingStore
	now   func() time.Time
}

// NewPricingService 构造服务。now 为 nil 时用 time.Now。
func NewPricingService(store PricingStore, now func() time.Time) *PricingService {
	if now == nil {
		now = time.Now
	}
	return &PricingService{store: store, now: now}
}

// ListPolicies 策略列表。
func (s *PricingService) ListPolicies(ctx context.Context, page, size int) ([]PricingPolicy, int64, error) {
	return s.store.ListPolicies(ctx, page, size)
}

// CreatePolicy 创建策略（校验后落库）。
func (s *PricingService) CreatePolicy(ctx context.Context, p *PricingPolicy, operatorID int64, requestID string) error {
	if err := validatePolicy(p); err != nil {
		return err
	}
	return s.store.CreatePolicy(ctx, p, operatorID, requestID)
}

// UpdatePolicy 更新策略。
func (s *PricingService) UpdatePolicy(ctx context.Context, p *PricingPolicy, operatorID int64, requestID string) error {
	if err := validatePolicy(p); err != nil {
		return err
	}
	if _, err := s.store.LoadPolicyByID(ctx, p.ID); err != nil {
		return err
	}
	return s.store.UpdatePolicy(ctx, p, operatorID, requestID)
}

// validatePolicy 校验策略字段（绝不静默吞字段——非法枚举直接报错）。
func validatePolicy(p *PricingPolicy) error {
	switch p.ScopeType {
	case ScopeAll, ScopeVendor, ScopeFamily, ScopeSKU:
		// ok
	case ScopeModelType:
		return ErrModelTypeUnsupported
	default:
		return ErrInvalidScopeType
	}
	switch p.PriceMethod {
	case MethodMargin, MethodCostUp, MethodOfficialAnchor, MethodFixed:
		// ok
	default:
		return ErrInvalidPriceMethod
	}
	v, err := decimal.NewFromString(p.ParamValue)
	if err != nil || v.IsNegative() {
		return ErrInvalidParamValue
	}
	return nil
}

// GenerateDraftInput 是生成草稿的入参。
type GenerateDraftInput struct {
	LevelCode string
	Currency  string
	PolicyIDs []int64
	SKUIDs    []int64 // 空 = 全量在架 SKU
}

// GenerateDraft 生成价目表草稿（不落正式版本，status='DRAFT'）。
// 对每个 SKU：取当前 cost_baseline 代表组件 unit_cost → 套用优先级最高命中策略 →
// 算售价 → 算 floor → 红线校验 → 落 price_book + price_book_item + price_book_component + diff_report。
func (s *PricingService) GenerateDraft(ctx context.Context, in GenerateDraftInput, operatorID int64, requestID string) (*PriceBookDraft, error) {
	// 1. 加载策略（policy_ids 为空 = 全部 ACTIVE 策略）。
	var policies []PricingPolicy
	var err error
	if len(in.PolicyIDs) > 0 {
		policies, err = s.store.LoadPoliciesByIDs(ctx, in.PolicyIDs)
	} else {
		policies, _, err = s.store.ListPolicies(ctx, 1, 10000)
	}
	if err != nil {
		return nil, fmt.Errorf("load policies: %w", err)
	}

	// 2. 加载 SKU 上下文（空 = 全量在架）。
	skus, err := s.store.LoadSKUContexts(ctx, in.SKUIDs)
	if err != nil {
		return nil, fmt.Errorf("load sku contexts: %w", err)
	}
	if len(skus) == 0 {
		return &PriceBookDraft{LevelCode: in.LevelCode, Currency: in.Currency, DiffReport: []DiffItem{}}, nil
	}

	skuIDs := make([]int64, 0, len(skus))
	for _, sk := range skus {
		skuIDs = append(skuIDs, sk.SKUID)
	}

	// 3. 批量读成本 / 官方价 / 旧售价 / min_gross_margin。
	unitCosts, err := s.store.LoadCurrentUnitCosts(ctx, skuIDs)
	if err != nil {
		return nil, fmt.Errorf("load unit costs: %w", err)
	}
	officialPrices, err := s.store.LoadCurrentOfficialPrices(ctx, skuIDs)
	if err != nil {
		return nil, fmt.Errorf("load official prices: %w", err)
	}
	oldPrices, err := s.store.LoadOldPrices(ctx, in.LevelCode, skuIDs)
	if err != nil {
		return nil, fmt.Errorf("load old prices: %w", err)
	}
	margin, err := s.store.LoadMinGrossMargin(ctx)
	if err != nil {
		return nil, fmt.Errorf("load min_gross_margin: %w", err)
	}

	// 4. 逐 SKU 计算（无命中策略 / 无成本基线 → 跳过，不进 diff_report）。
	diffReport := make([]DiffItem, 0, len(skus))
	items := make([]DraftItem, 0, len(skus))
	blocked := 0
	for _, sk := range skus {
		pol := MatchPolicy(policies, sk, in.LevelCode)
		if pol == nil {
			continue // 无命中策略，跳过
		}
		uc, ok := unitCosts[sk.SKUID]
		if !ok {
			continue // 无成本基线，跳过（ErrNoCostBaseline 语义并入"跳过"——草稿是预演，不阻断）
		}
		param, _ := decimal.NewFromString(pol.ParamValue)
		official := officialPrices[sk.SKUID] // 无官方价时为零值，仅 OFFICIAL_ANCHOR 用
		if pol.PriceMethod == MethodOfficialAnchor {
			if _, hasOfficial := officialPrices[sk.SKUID]; !hasOfficial {
				continue // OFFICIAL_ANCHOR 但无官方价，跳过
			}
		}
		newPrice, err := CalculatePrice(pol.PriceMethod, uc.UnitCost, official, param)
		if err != nil {
			return nil, fmt.Errorf("calculate price sku=%d: %w", sk.SKUID, err)
		}
		// CEIL 取整到 8 位（rounding_rule='CEIL'，本批按 8 位小数向上——遗留 8a-①）。
		newPrice = ceil8(newPrice)

		floor, ferr := cost.Floor(uc.UnitCost, margin)
		if ferr != nil {
			return nil, fmt.Errorf("floor sku=%d: %w", sk.SKUID, ferr)
		}
		floor = ceil8(floor)
		violation := newPrice.LessThan(floor)
		if violation {
			blocked++
		}

		// old_price / delta_pct（首次生成 old 为 NULL → delta_pct 也 NULL）。
		var oldPrice *string
		var deltaPct *string
		if op, hasOld := oldPrices[sk.SKUID]; hasOld {
			oldPrice = strPtr(op)
			oldD, _ := decimal.NewFromString(op)
			if !oldD.IsZero() {
				d := newPrice.Sub(oldD).Div(oldD)
				ds := d.StringFixed(6)
				deltaPct = &ds
			}
		}

		newPriceStr := newPrice.StringFixed(8)
		floorStr := floor.StringFixed(8)
		diffReport = append(diffReport, DiffItem{
			SKUID: sk.SKUID, SKUCode: sk.SKUCode,
			OldPrice: oldPrice, NewPrice: newPriceStr, DeltaPct: deltaPct,
			FloorPrice: floorStr, FloorViolation: violation,
		})
		items = append(items, DraftItem{
			SKUID: sk.SKUID, Currency: uc.Currency, FloorPrice: floorStr,
			PolicyID: pol.ID, BaselineVersion: uc.BaselineVersion,
			ComponentType: uc.UnitCostBasis, NewPrice: newPriceStr,
		})
	}

	// 5. 落草稿（单事务）。
	draftID, err := s.store.SaveDraft(ctx, SaveDraftInput{
		LevelCode: in.LevelCode, Currency: in.Currency, DiffReport: diffReport, Items: items,
	}, operatorID, requestID)
	if err != nil {
		return nil, fmt.Errorf("save draft: %w", err)
	}

	return &PriceBookDraft{
		DraftID: draftID, LevelCode: in.LevelCode, Currency: in.Currency,
		ItemCount: len(items), DiffReport: diffReport, BlockedCount: blocked,
	}, nil
}

// ceil8 向上取整到 8 位小数（rounding_rule='CEIL' 的本批实现——遗留 8a-①）。
// 与 numeric(20,8) 对齐：×1e8 → Ceil → ÷1e8。
func ceil8(d decimal.Decimal) decimal.Decimal {
	exp := decimal.NewFromInt(100000000) // 1e8
	return d.Mul(exp).Ceil().Div(exp)
}

func strPtr(s string) *string { return &s }
