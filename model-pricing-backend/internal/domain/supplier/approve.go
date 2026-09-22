// Package supplier 的 approve.go：报价审批与激活链路（05-quotes.md §7/§8/§9/§10/§14）。
// 覆盖：待审批列表（行级过滤）、差异对比、审批通过/驳回、到期激活（含 SKU 可采购态联动）。
// 金额一律 shopspring/decimal；delta_pct 等比率同为 decimal 字符串。
package supplier

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

// ---- 领域错误 ----

var (
	// ErrQuoteNotApprovable 审批前置状态非法（非 APPROVING）。
	ErrQuoteNotApprovable = errors.New("报价单状态不允许审批（仅审批中可操作）")
	// ErrQuoteRejectReasonInvalid 驳回原因长度不在 10~500（按 rune 计）。
	ErrQuoteRejectReasonInvalid = errors.New("驳回原因必填，10~500 字")
	// ErrQuoteScopeForbidden 归属校验失败：该单不在当前操作员数据域内。
	ErrQuoteScopeForbidden = errors.New("无权限操作该供应商的报价单")
)

// ---- 数据域（设计 §3.2，与 middleware.Operator 字段对齐） ----

// OwnerScope 是行级过滤所需的操作员数据域视图。
// ScopePaths 元素形如 "/1/3/"（与 org_unit.path 一致，含首尾斜杠）。
type OwnerScope struct {
	DataScope  string   // SELF / DEPT / DEPT_SUB / ALL
	StaffID    int64    // internal_staff.id（owner_procurement_operator_id 的匹配键）
	MyOrgID    int64    // internal_staff.org_unit_id
	ScopePaths []string // DEPT_SUB 用：org_unit.path LIKE ANY(paths || '%')
}

// ---- DTO ----

// PendingQuoteQuery 是待审批列表查询条件（§7）。
type PendingQuoteQuery struct {
	SupplierID *int64
	Page       int
	Size       int
}

// PendingQuoteResult 是待审批分页结果。
type PendingQuoteResult struct {
	List  []PendingQuoteItem `json:"list"`
	Total int64              `json:"total"`
	Page  int                `json:"page"`
	Size  int                `json:"size"`
}

// PendingQuoteItem 是待审批列表的一行（§7 data.list[]）。
type PendingQuoteItem struct {
	ID           int64     `json:"id"`
	SupplierID   int64     `json:"supplier_id"`
	SupplierName string    `json:"supplier_name"`
	VersionNo    int       `json:"version_no"`
	ItemCount    int       `json:"item_count"`
	Source       string    `json:"source"`
	Retroactive  bool      `json:"retroactive"`
	ValidFrom    time.Time `json:"valid_from"`
	ValidTo      time.Time `json:"valid_to"`
	SubmittedAt  time.Time `json:"submitted_at"`
	WaitHours    float64   `json:"wait_hours"`
	HasPrevious  bool      `json:"has_previous"`
}

// ApproveResult 是审批通过的响应（§8 data）。
type ApproveResult struct {
	ID         int64     `json:"id"`
	Status     string    `json:"status"`
	ApprovedAt time.Time `json:"approved_at"`
	ActivateAt time.Time `json:"activate_at"`
	Immediate  bool      `json:"immediate"`
}

// RejectInput 是审批驳回的请求体（§9）。
type RejectInput struct {
	Reason string `json:"reason" binding:"required"`
}

// RejectResult 是审批驳回的响应。
type RejectResult struct {
	ID         int64     `json:"id"`
	Status     string    `json:"status"`
	RejectedAt time.Time `json:"rejected_at"`
}

// ActivateDueResult 是激活扫描的响应（§10 data）。
type ActivateDueResult struct {
	Scanned   int               `json:"scanned"`
	Activated int               `json:"activated"`
	Items     []ActivateDueItem `json:"items"`
}

// ActivateDueItem 是单条激活结果。
type ActivateDueItem struct {
	ID               int64  `json:"id"`
	SupplierID       int64  `json:"supplier_id"`
	VersionNo        int    `json:"version_no"`
	ClosedPreviousID *int64 `json:"closed_previous_id"`
}

// QuoteDiff 是差异对比的响应（§14 data）。
type QuoteDiff struct {
	QuoteSheetID   int64      `json:"quote_sheet_id"`
	SupplierID     int64      `json:"supplier_id"`
	VersionNo      int        `json:"version_no"`
	Distortion     bool       `json:"distortion"`
	DistortionNote string     `json:"distortion_note"`
	Items          []DiffItem `json:"items"`
}

// DiffItem 是一个 SKU 的三方对照。
type DiffItem struct {
	SKUID         int64           `json:"sku_id"`
	SKUCode       string          `json:"sku_code"`
	Currency      string          `json:"currency"`
	Components    []DiffComponent `json:"components"`
	MarginPreview *MarginPreview  `json:"margin_preview"`
}

// DiffComponent 是一个组件的对照行。价格/比率一律字符串（decimal）。
type DiffComponent struct {
	ComponentType    string  `json:"component_type"`
	UnitPrice        string  `json:"unit_price"`
	Multiplier       *string `json:"multiplier"`
	PrevPrice        *string `json:"prev_price"`
	PrevDeltaPct     *string `json:"prev_delta_pct"`
	OfficialPrice    *string `json:"official_price"`
	OfficialDeltaPct *string `json:"official_delta_pct"`
	MarketBest       *string `json:"market_best"`
	MarketDeltaPct   *string `json:"market_delta_pct"`
}

// MarginPreview 是毛利简化预演（§14：floor = unit_price / (1 − min_gross_margin)）。
type MarginPreview struct {
	FloorPrice         string  `json:"floor_price"`
	ReferenceSellPrice *string `json:"reference_sell_price"`
	MarginOK           *bool   `json:"margin_ok"`
	Note               string  `json:"note"`
}

// ---- 仓储接口 ----

// ApproveStore 是审批/激活链路的仓储接口（GORM 实现见 internal/repo/supplier_approve.go）。
type ApproveStore interface {
	// ListPendingQuotes 待审批列表（status='APPROVING'），按 OwnerScope 行级过滤。
	ListPendingQuotes(ctx context.Context, scope OwnerScope, q PendingQuoteQuery, now time.Time) (*PendingQuoteResult, error)
	// CheckQuoteScope 归属校验：该报价单的供应商 owner 是否落在 scope 内。
	// found=false 表示单不存在；inScope=false → 403。
	CheckQuoteScope(ctx context.Context, quoteID int64, scope OwnerScope) (found bool, inScope bool, err error)
	// ApproveQuote 事务内：APPROVING→APPROVED_PENDING（条件更新，0 行 → ErrQuoteNotApprovable）
	// + todo_task DONE + task_job(ACTIVATE_QUOTE) 入队 + audit_log(QUOTE_APPROVE)。
	// immediate=true 由 service 在事务提交后另开事务同步激活（契约 §8 实现要求）。
	ApproveQuote(ctx context.Context, p ApproveParams) (*ApproveResult, error)
	// RejectQuote 事务内：APPROVING→REJECTED（条件更新）+ rejected_at=now()
	// + todo_task DONE + audit_log(QUOTE_REJECT)。
	RejectQuote(ctx context.Context, p RejectParams) (*RejectResult, error)
	// ListDueQuotes 取 status='APPROVED_PENDING' AND valid_from<=now 的待激活单。
	ListDueQuotes(ctx context.Context, now time.Time) ([]DueQuote, error)
	// ActivateOne 单条激活（独立事务）：旧 EFFECTIVE→EXPIRED(valid_to=新 valid_from)
	// → 本单 EFFECTIVE → PENDING_VERIFY SKU 置 PURCHASABLE → COST_RECALC 入队
	// → event_outbox(quote.effective) → ACTIVATE_QUOTE job DONE → audit_log ×(1+N)。
	// 冲突/失败时返回 error（调用方记 last_error 并跳过，不中断整批）。
	ActivateOne(ctx context.Context, quoteID int64, p ActivateParams) (*ActivateDueItem, error)
	// MarkActivateError 把单条激活失败写回 task_job(ACTIVATE_QUOTE).last_error（尽力而为）。
	MarkActivateError(ctx context.Context, quoteID int64, cause string) error
	// LoadQuoteDiff 取 diff 所需全部原始数据（一次装配，计算在 domain 层）。
	LoadQuoteDiff(ctx context.Context, quoteID int64) (*QuoteDiffRaw, error)
	// GetSysConfigDecimal 读 sys_config 数值型配置（如 min_gross_margin）。
	GetSysConfigDecimal(ctx context.Context, key string) (decimal.Decimal, error)
}

// ApproveParams 是 ApproveQuote 的参数。
type ApproveParams struct {
	QuoteID      int64
	OperatorID   int64 // internal_staff.id（approved_by）
	OperatorRole string
	RequestID    string
	Now          time.Time
}

// RejectParams 是 RejectQuote 的参数。
type RejectParams struct {
	QuoteID      int64
	Reason       string
	OperatorID   int64
	OperatorRole string
	RequestID    string
	Now          time.Time
}

// ActivateParams 是 ActivateOne 的参数（worker/端点共用）。
// worker 触发：OperatorID=0 / OperatorRole='SYSTEM' / SourceType='WORKER'（本轮定值，不留 TODO）。
type ActivateParams struct {
	OperatorID   int64
	OperatorRole string
	SourceType   string // HUMAN（端点）/ WORKER（ticker，阶段 6）
	RequestID    string
	Now          time.Time
}

// DueQuote 是待激活单的最小视图。
type DueQuote struct {
	ID         int64
	SupplierID int64
	VersionNo  int
	ValidFrom  time.Time
}

// QuoteDiffRaw 是 LoadQuoteDiff 返回的原始数据（domain 层负责计算与装配）。
type QuoteDiffRaw struct {
	SheetID    int64
	SupplierID int64
	VersionNo  int
	Items      []DiffRawItem
}

// DiffRawItem 是一个明细行的原始对照数据。
type DiffRawItem struct {
	SKUID      int64
	SKUCode    string
	Currency   string
	Components []DiffRawComponent
}

// DiffRawComponent 是一个组件的原始对照数据（nil 表示该侧无基准）。
type DiffRawComponent struct {
	ComponentType string
	UnitPrice     decimal.Decimal
	Multiplier    *decimal.Decimal
	PrevPrice     *decimal.Decimal
	OfficialPrice *decimal.Decimal
	MarketBest    *decimal.Decimal
}

// ---- 领域服务 ----

// ApproveService 是审批/激活/diff 的领域服务。
type ApproveService struct {
	store ApproveStore
	now   func() time.Time
}

// NewApproveService 构造审批服务。
func NewApproveService(store ApproveStore) *ApproveService {
	return &ApproveService{store: store, now: func() time.Time { return time.Now().UTC() }}
}

// ListPending 待审批列表（§7）：行级过滤 + wait_hours/SLA。
func (s *ApproveService) ListPending(ctx context.Context, scope OwnerScope, q PendingQuoteQuery) (*PendingQuoteResult, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 || q.Size > 100 {
		q.Size = 20
	}
	return s.store.ListPendingQuotes(ctx, scope, q, s.now())
}

// checkScope 归属校验公共段：单不存在 → ErrQuoteNotFound；不在域内 → ErrQuoteScopeForbidden。
func (s *ApproveService) checkScope(ctx context.Context, quoteID int64, scope OwnerScope) error {
	found, inScope, err := s.store.CheckQuoteScope(ctx, quoteID, scope)
	if err != nil {
		return err
	}
	if !found {
		return ErrQuoteNotFound
	}
	if !inScope {
		return ErrQuoteScopeForbidden
	}
	return nil
}

// CheckScope 暴露归属校验（供 confirm-remove 等其他生命周期接口复用）。
func (s *ApproveService) CheckScope(ctx context.Context, quoteID int64, scope OwnerScope) error {
	return s.checkScope(ctx, quoteID, scope)
}

// Approve 审批通过（§8）。
// immediate（valid_from<=now）时，**事务提交后**另开事务同步调 ActivateDueQuotes
// （契约 §8 实现要求：本阶段无 ticker，只入队会永久停在 APPROVED_PENDING）。
func (s *ApproveService) Approve(ctx context.Context, quoteID int64, scope OwnerScope, operatorID int64, operatorRole, requestID string) (*ApproveResult, error) {
	if err := s.checkScope(ctx, quoteID, scope); err != nil {
		return nil, err
	}
	res, err := s.store.ApproveQuote(ctx, ApproveParams{
		QuoteID:      quoteID,
		OperatorID:   operatorID,
		OperatorRole: operatorRole,
		RequestID:    requestID,
		Now:          s.now(),
	})
	if err != nil {
		return nil, err
	}
	if res.Immediate {
		// 另开事务同步激活；ActivateDueQuotes 幂等（条件更新，0 行跳过），失败不回滚审批。
		_, _ = s.ActivateDueQuotes(ctx, ActivateParams{
			OperatorID:   operatorID,
			OperatorRole: operatorRole,
			SourceType:   "HUMAN",
			RequestID:    requestID,
			Now:          s.now(),
		})
	}
	return res, nil
}

// Reject 审批驳回（§9）：reason 10~500 rune；写 reject_reason + rejected_at。
func (s *ApproveService) Reject(ctx context.Context, quoteID int64, scope OwnerScope, in RejectInput, operatorID int64, operatorRole, requestID string) (*RejectResult, error) {
	if n := utf8.RuneCountInString(in.Reason); n < 10 || n > 500 {
		return nil, ErrQuoteRejectReasonInvalid
	}
	if err := s.checkScope(ctx, quoteID, scope); err != nil {
		return nil, err
	}
	return s.store.RejectQuote(ctx, RejectParams{
		QuoteID:      quoteID,
		Reason:       in.Reason,
		OperatorID:   operatorID,
		OperatorRole: operatorRole,
		RequestID:    requestID,
		Now:          s.now(),
	})
}

// ActivateDueQuotes 激活全部到期的 APPROVED_PENDING 报价（§10）。
// 单条独立事务：失败记 last_error 跳过，不中断整批（uk_quote_effective 并发兜底语义）。
func (s *ApproveService) ActivateDueQuotes(ctx context.Context, p ActivateParams) (*ActivateDueResult, error) {
	if p.Now.IsZero() {
		p.Now = s.now()
	}
	if p.OperatorRole == "" {
		p.OperatorRole = "SYSTEM"
	}
	if p.SourceType == "" {
		p.SourceType = "WORKER"
	}
	due, err := s.store.ListDueQuotes(ctx, p.Now)
	if err != nil {
		return nil, err
	}
	res := &ActivateDueResult{Scanned: len(due), Items: []ActivateDueItem{}}
	for _, d := range due {
		item, err := s.store.ActivateOne(ctx, d.ID, p)
		if err != nil {
			// 契约 §10：命中 uk_quote_effective 等冲突时该条跳过并记 last_error，不中断整批
			_ = s.store.MarkActivateError(ctx, d.ID, err.Error())
			continue
		}
		res.Activated++
		res.Items = append(res.Items, *item)
	}
	return res, nil
}

// Diff 差异对比（§14）：三方对照 + 失真提示 + 毛利简化预演。
func (s *ApproveService) Diff(ctx context.Context, quoteID int64, scope OwnerScope) (*QuoteDiff, error) {
	if err := s.checkScope(ctx, quoteID, scope); err != nil {
		return nil, err
	}
	raw, err := s.store.LoadQuoteDiff(ctx, quoteID)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, ErrQuoteNotFound
	}
	margin, err := s.store.GetSysConfigDecimal(ctx, "min_gross_margin")
	if err != nil {
		return nil, err
	}
	return buildQuoteDiff(raw, margin), nil
}

// buildQuoteDiff 纯函数：原始数据 → §14 响应（可单测）。
func buildQuoteDiff(raw *QuoteDiffRaw, minGrossMargin decimal.Decimal) *QuoteDiff {
	out := &QuoteDiff{
		QuoteSheetID: raw.SheetID,
		SupplierID:   raw.SupplierID,
		VersionNo:    raw.VersionNo,
		Items:        []DiffItem{},
	}

	// 失真判定：全单所有组件 multiplier 完全相同（存在绝对价 null 时不判定）
	allSame := true
	var firstMultiplier *decimal.Decimal
	anyComponent := false

	one := decimal.NewFromInt(1)
	for _, ri := range raw.Items {
		item := DiffItem{SKUID: ri.SKUID, SKUCode: ri.SKUCode, Currency: ri.Currency, Components: []DiffComponent{}}
		for _, rc := range ri.Components {
			dc := DiffComponent{
				ComponentType:    rc.ComponentType,
				UnitPrice:        rc.UnitPrice.String(),
				PrevPrice:        decPtrToStrPtr(rc.PrevPrice),
				OfficialPrice:    decPtrToStrPtr(rc.OfficialPrice),
				MarketBest:       decPtrToStrPtr(rc.MarketBest),
				PrevDeltaPct:     deltaPct(rc.UnitPrice, rc.PrevPrice),
				OfficialDeltaPct: deltaPct(rc.UnitPrice, rc.OfficialPrice),
				MarketDeltaPct:   deltaPct(rc.UnitPrice, rc.MarketBest),
			}
			if rc.Multiplier != nil {
				s := rc.Multiplier.String()
				dc.Multiplier = &s
				if !anyComponent {
					m := *rc.Multiplier
					firstMultiplier = &m
				} else if firstMultiplier != nil && !rc.Multiplier.Equal(*firstMultiplier) {
					allSame = false
				}
			} else {
				allSame = false // 绝对价存在 → 不判定失真
			}
			anyComponent = true
			item.Components = append(item.Components, dc)
		}

		// 毛利简化预演：以明细行首个组件的 unit_price 为成本基准（§14 简化口径，
		// 真正预演需 M5 成本参数与 M7 价目表，Stage 6 补齐）
		if len(ri.Components) > 0 {
			costBasis := ri.Components[0].UnitPrice
			if !minGrossMargin.GreaterThanOrEqual(one) {
				floor := costBasis.Div(one.Sub(minGrossMargin))
				mp := &MarginPreview{
					FloorPrice: floor.String(),
					Note:       "按官方价作为售价基准、min_gross_margin=" + minGrossMargin.String() + " 预演",
				}
				if ref := ri.Components[0].OfficialPrice; ref != nil {
					s := ref.String()
					mp.ReferenceSellPrice = &s
					ok := ref.GreaterThanOrEqual(floor)
					mp.MarginOK = &ok
				}
				item.MarginPreview = mp
			}
		}
		out.Items = append(out.Items, item)
	}

	out.Distortion = anyComponent && allSame
	if out.Distortion {
		out.DistortionNote = "本版各组件倍率完全相同，缓存维度需重点复核"
	}
	return out
}

// deltaPct 计算 (unit − base) / base，保留 4 位小数；base 为 nil 或 0 时返回 nil。
func deltaPct(unit decimal.Decimal, base *decimal.Decimal) *string {
	if base == nil || base.IsZero() {
		return nil
	}
	s := unit.Sub(*base).Div(*base).Round(4).String()
	return &s
}

// decPtrToStrPtr decimal 指针转字符串指针。
func decPtrToStrPtr(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.String()
	return &s
}
