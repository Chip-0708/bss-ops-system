// Package pricing 涨价传导决策队列（08-pricing.md §5）。
//
// 业务语义：成本基线上涨后，**系统不自动改价**——只生成 PENDING 队列让人来决策：
//   - FOLLOW / NOT_FOLLOW 两个终态。
//   - NOT_FOLLOW 且现价低于 floor → 409（必须走阶段 9 特价审批）。
//   - FOLLOW 当前**不自动发布价目表**（裁决 7：太危险，需人工确认后手动走 8b-1 发布流）。
//
// 数据流：
//
//	成本基线当前版 unit_cost = cost_after
//	成本基线上一个版本 unit_cost = cost_before（不存在时跳过——首次建立基线不算"涨"）
//	当前 EFFECTIVE 价目表的 unit_price = price_current（无有效价目表 → 跳过）
//	cost_delta_pct = (cost_after - cost_before) / cost_before
//	price_suggested = price_current × (1 + cost_delta_pct)
//	floor_price = Floor(cost_after, min_gross_margin)
//	margin_before = (price_current - cost_before) / price_current
//	margin_after = (price_current - cost_after) / price_current
//
// 裁决 4（margin 落库）：margin_before/margin_after 落库——决策时刻冻结口径（若实时算，
// 上下文变了之后无法复现"当时为何决策"）。读侧权限剔除走 000022 迁移把字段并入
// SALES 的 field_mask hide 列表（与 000017 剔除 unit_cost 同机制）。
//
// 裁决 5（冻结期）：默认 7 天（sys_config.price_upconduction_frozen_days 可覆盖；
// 当前默认直读常量，可配置化登记 8b-2-⑤）。超时自动 NOT_FOLLOWED 留给 worker 扫描
// （登记 8b-2-① 同款遗留）。
package pricing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"model_bss/internal/domain/cost"
)

// ============================================================
// 常量与错误
// ============================================================

// 状态字典（与 ck_price_upconduction_status 对齐——红线 4 禁止自创）。
const (
	UpconductionStatusPending     = "PENDING"
	UpconductionStatusFollowed    = "FOLLOWED"
	UpconductionStatusNotFollowed = "NOT_FOLLOWED"
)

// 决策动作（请求体 decision 字段）。
const (
	DecisionFollow    = "FOLLOW"
	DecisionNotFollow = "NOT_FOLLOW"
)

// DefaultFrozenDays 默认冻结期（裁决 5）：7 天。
const DefaultFrozenDays = 7

// 领域错误。
var (
	ErrUpconductionNotFound      = errors.New("涨价传导队列行不存在")
	ErrUpconductionNotPending    = errors.New("仅 PENDING 状态可决策")
	ErrUpconductionBelowFloor    = errors.New("NOT_FOLLOW 后售价低于 floor（红线），必须走特价审批")
	ErrUpconductionInvalidAction = errors.New("decision 非法（仅支持 FOLLOW / NOT_FOLLOW）")
)

// ============================================================
// DTO
// ============================================================

// UpconductionItem 涨价传导队列行（读侧返回）。
// 数值字段全部字符串化对齐 `decimal.Decimal` 精度——金额红线 1。
type UpconductionItem struct {
	ID             int64      `json:"id"`
	SKUID          int64      `json:"sku_id"`
	SKUCode        string     `json:"sku_code"`
	LevelCode      string     `json:"level_code"`
	CostBefore     string     `json:"cost_before"`
	CostAfter      string     `json:"cost_after"`
	CostDeltaPct   string     `json:"cost_delta_pct"`
	PriceCurrent   string     `json:"price_current"`
	PriceSuggested string     `json:"price_suggested"`
	FloorPrice     string     `json:"floor_price"`
	MarginBefore   *string    `json:"margin_before,omitempty"` // 通过 SALES.field_mask 剔除（裁决 4）
	MarginAfter    *string    `json:"margin_after,omitempty"`
	Status         string     `json:"status"`
	FrozenUntil    *time.Time `json:"frozen_until"`
	Reason         *string    `json:"reason,omitempty"`
	DecidedBy      *int64     `json:"decided_by,omitempty"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	OverridePrice  *string    `json:"override_price,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// GenerateResult 手动触发队列生成的返回。
type GenerateResult struct {
	GeneratedCount int       `json:"generated_count"` // 实际新插入的行数
	SkippedSKUIDs  []int64   `json:"skipped_sku_ids"` // 未生成的原因（成本未上涨 / 无价目表 / 无基线历史）
	QueueIDs       []int64   `json:"queue_ids"`
	GeneratedAt    time.Time `json:"generated_at"`
}

// DecideInput 决策输入。
type DecideInput struct {
	QueueID       int64
	OperatorID    int64
	Decision      string
	OverridePrice decimal.Decimal // 可能为 0（未传）
	HasOverride   bool            // 显式区分"未传" vs "传 0"
	Reason        string
	RequestID     string
}

// DecideResult 决策返回。
type DecideResult struct {
	QueueID       int64     `json:"queue_id"`
	Status        string    `json:"status"`
	DecidedBy     int64     `json:"decided_by"`
	DecidedAt     time.Time `json:"decided_at"`
	OverridePrice *string   `json:"override_price,omitempty"`
}

// ============================================================
// 纯函数（变异验证锚点）
// ============================================================

// CalculatePriceSuggested 建议售价 = price_current × (1 + cost_delta_pct)。
// 裁决 3：按成本涨幅等比调整——简单、可解释。
func CalculatePriceSuggested(priceCurrent, costDeltaPct decimal.Decimal) decimal.Decimal {
	one := decimal.NewFromInt(1)
	return priceCurrent.Mul(one.Add(costDeltaPct))
}

// CalculateCostDeltaPct 成本涨幅 = (cost_after - cost_before) / cost_before。
// cost_before <= 0 视为非法（除零保护 + 负成本无业务意义），调用方跳过。
func CalculateCostDeltaPct(costBefore, costAfter decimal.Decimal) (decimal.Decimal, error) {
	if costBefore.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("cost_before 必须 > 0，得到 %s", costBefore.String())
	}
	return costAfter.Sub(costBefore).Div(costBefore), nil
}

// CalculateMargin 毛利 = (price - cost) / price。
// price <= 0 视为非法（除零保护）。
func CalculateMargin(price, costVal decimal.Decimal) (decimal.Decimal, error) {
	if price.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("price 必须 > 0，得到 %s", price.String())
	}
	return price.Sub(costVal).Div(price), nil
}

// ============================================================
// Service 编排
// ============================================================

// UpconductionStore 仓储依赖（由 repo.PricingUpconductionRepo 实现）。
type UpconductionStore interface {
	// LoadRisingCostSKUs 加载"当前版 unit_cost > 上一版 unit_cost"的 SKU 集合。
	// 返回每个 SKU 的 before/after/version 信息。成本未上涨 / 无历史版本的 SKU 不出现。
	LoadRisingCostSKUs(ctx context.Context) ([]CostRisePair, error)
	// LoadEffectivePrice 加载 (sku_id, level_code) 该 SKU 在该 level 的当前 EFFECTIVE 价目表售价。
	// 不存在 → ok=false（跳过而非报错——无价目表说明还没有可涨的售价）。
	LoadEffectivePrice(ctx context.Context, skuID int64, levelCode string) (price decimal.Decimal, ok bool, err error)
	// LoadMinGrossMargin 读 sys_config.min_gross_margin。
	LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error)
	// LoadSKUCode 按 sku_id 反查 sku_code（DTO 输出）。
	LoadSKUCode(ctx context.Context, skuID int64) (string, error)
	// ListEffectiveLevels 列出当前有 EFFECTIVE 价目表的 level_code 全集。
	// 生成队列时按 (sku, level_code) 维度逐 level 生成。
	ListEffectiveLevels(ctx context.Context, skuID int64) ([]string, error)
	// InsertQueueRow 单事务插入一行 + audit_log（裁决：涨价传导是价格类操作，红线 10）。
	// 幂等性：对 (sku_id, level_code, cost_baseline_version) 已存在 PENDING/FOLLOWED 行的，
	// 跳过返回 ok=false（防重复生成）。
	InsertQueueRow(ctx context.Context, row *UpconductionItem, costBaselineVersion int, operatorID int64, requestID string) (id int64, ok bool, err error)
	// ListQueue 分页查询。status 为空表示全部。
	ListQueue(ctx context.Context, status string, page, size int) (items []UpconductionItem, total int64, err error)
	// LoadQueueRowByID 决策时按 id 读行。
	// 不存在 → ErrUpconductionNotFound。
	LoadQueueRowByID(ctx context.Context, id int64) (*UpconductionItem, error)
	// Decide 单事务：UPDATE status/decided_by/decided_at/reason/override_price + audit_log。
	// 前置校验（status=PENDING + floor 校验）在 Service 层完成。
	Decide(ctx context.Context, in DecideInput, operatorRole string) error
	// LoadOperatorIDByAccountID 决策时把 handler 拿到的 OperatorID 直接当 decided_by 写入
	// （与 5b 审批的 operator_id 同口径——直接引用 subject_operator.id）。
}

// CostRisePair 一对"成本基线前后版本"的对比。
type CostRisePair struct {
	SKUID           int64
	CostBefore      decimal.Decimal
	CostAfter       decimal.Decimal
	CurrentVersion  int // 当前版本号（入队审计时锚定）
	PreviousVersion int
}

// UpconductionService 涨价传导领域服务。
type UpconductionService struct {
	store UpconductionStore
	now   func() time.Time
}

// NewUpconductionService 构造。now 可注入供单测固定时钟。
func NewUpconductionService(store UpconductionStore, now func() time.Time) *UpconductionService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &UpconductionService{store: store, now: now}
}

var _ = cost.Floor // 保持 import：Floor 在生成时使用。

// GenerateQueue 手动生成涨价传导队列。
//
// 实现要点：
//   - 每个 (SKU, level_code) 独立插入；跳过（成本未涨/无价目表/无历史版本/重复）的只是
//     不进结果集，不报错——只有 store 层真正故障才返回 error。
//   - 幂等性：InsertQueueRow 内部以 (sku_id, level_code, cost_baseline_version) 去重，
//     同版本成本重放 generate 不会产生重复行。
func (s *UpconductionService) GenerateQueue(ctx context.Context, operatorID int64, requestID string) (*GenerateResult, error) {
	// 1. 读"成本上涨"集合。
	pairs, err := s.store.LoadRisingCostSKUs(ctx)
	if err != nil {
		return nil, fmt.Errorf("load rising cost SKUs: %w", err)
	}

	// 2. 读 min_gross_margin 一次性。
	margin, err := s.store.LoadMinGrossMargin(ctx)
	if err != nil {
		return nil, fmt.Errorf("load min_gross_margin: %w", err)
	}

	now := s.now()
	frozenUntil := now.Add(time.Duration(DefaultFrozenDays) * 24 * time.Hour)

	result := &GenerateResult{
		SkippedSKUIDs: make([]int64, 0),
		QueueIDs:      make([]int64, 0),
		GeneratedAt:   now,
	}

	// 3. 逐 SKU 处理。
	for _, p := range pairs {
		// 3.1 读 SKU 当前生效价目表的 level 集合。
		levels, err := s.store.ListEffectiveLevels(ctx, p.SKUID)
		if err != nil {
			return nil, fmt.Errorf("sku %d list effective levels: %w", p.SKUID, err)
		}
		if len(levels) == 0 {
			result.SkippedSKUIDs = append(result.SkippedSKUIDs, p.SKUID)
			continue
		}

		// 3.2 计算 delta_pct（在循环外一次）。
		deltaPct, err := CalculateCostDeltaPct(p.CostBefore, p.CostAfter)
		if err != nil {
			// before<=0 系脏数据，跳过该 SKU 但不拖垮整批。
			result.SkippedSKUIDs = append(result.SkippedSKUIDs, p.SKUID)
			continue
		}

		// 3.3 sku_code 只反查一次。
		skuCode, err := s.store.LoadSKUCode(ctx, p.SKUID)
		if err != nil {
			return nil, fmt.Errorf("sku %d load code: %w", p.SKUID, err)
		}

		// 3.4 每个 level 独立一行。
		for _, level := range levels {
			priceCurrent, ok, err := s.store.LoadEffectivePrice(ctx, p.SKUID, level)
			if err != nil {
				return nil, fmt.Errorf("sku %d level %s load price: %w", p.SKUID, level, err)
			}
			if !ok {
				// 该 level 无 EFFECTIVE 价目表——ListEffectiveLevels 不该返回它，防御性跳过。
				continue
			}

			// 3.5 计算派生字段。
			priceSuggested := CalculatePriceSuggested(priceCurrent, deltaPct)
			floorPrice, err := cost.Floor(p.CostAfter, margin)
			if err != nil {
				return nil, fmt.Errorf("sku %d floor: %w", p.SKUID, err)
			}
			marginBefore, err := CalculateMargin(priceCurrent, p.CostBefore)
			if err != nil {
				return nil, fmt.Errorf("sku %d margin_before: %w", p.SKUID, err)
			}
			marginAfter, err := CalculateMargin(priceCurrent, p.CostAfter)
			if err != nil {
				return nil, fmt.Errorf("sku %d margin_after: %w", p.SKUID, err)
			}

			// 3.6 字符串化（8 位小数，与 numeric(20,8) 对齐）。
			marginBeforeStr := marginBefore.StringFixed(6)
			marginAfterStr := marginAfter.StringFixed(6)
			row := &UpconductionItem{
				SKUID:          p.SKUID,
				SKUCode:        skuCode,
				LevelCode:      level,
				CostBefore:     p.CostBefore.StringFixed(8),
				CostAfter:      p.CostAfter.StringFixed(8),
				CostDeltaPct:   deltaPct.StringFixed(6),
				PriceCurrent:   priceCurrent.StringFixed(8),
				PriceSuggested: priceSuggested.StringFixed(8),
				FloorPrice:     floorPrice.StringFixed(8),
				MarginBefore:   &marginBeforeStr,
				MarginAfter:    &marginAfterStr,
				Status:         UpconductionStatusPending,
				FrozenUntil:    &frozenUntil,
				CreatedAt:      now,
			}

			id, inserted, err := s.store.InsertQueueRow(ctx, row, p.CurrentVersion, operatorID, requestID)
			if err != nil {
				return nil, fmt.Errorf("sku %d level %s insert queue: %w", p.SKUID, level, err)
			}
			if inserted {
				result.QueueIDs = append(result.QueueIDs, id)
			}
		}
	}

	result.GeneratedCount = len(result.QueueIDs)
	return result, nil
}

// ListQueue 列表查询。
func (s *UpconductionService) ListQueue(ctx context.Context, status string, page, size int) ([]UpconductionItem, int64, error) {
	if status != "" && status != UpconductionStatusPending &&
		status != UpconductionStatusFollowed && status != UpconductionStatusNotFollowed {
		return nil, 0, fmt.Errorf("status 非法：%s", status)
	}
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 200 {
		size = 20
	}
	return s.store.ListQueue(ctx, status, page, size)
}

// Decide 决策。
//
// 校验顺序（红线"先看权限再动数据"）：
//  1. action 必须为 FOLLOW / NOT_FOLLOW；
//  2. 行必须存在且 status=PENDING；
//  3. NOT_FOLLOW 且 price_current < floor_price → ErrUpconductionBelowFloor（裁决 6）；
//  4. FOLLOW / 合法 NOT_FOLLOW → 调 store.Decide。
//
// 裁决 7：FOLLOW 只更新 status，**不**自动发布新价目表——人工确认后走 8b-1 手动发布。
func (s *UpconductionService) Decide(ctx context.Context, in DecideInput, operatorRole string) (*DecideResult, error) {
	// 1. action 校验。
	if in.Decision != DecisionFollow && in.Decision != DecisionNotFollow {
		return nil, ErrUpconductionInvalidAction
	}

	// 2. 读行 + 状态校验。
	row, err := s.store.LoadQueueRowByID(ctx, in.QueueID)
	if err != nil {
		return nil, err
	}
	if row.Status != UpconductionStatusPending {
		return nil, ErrUpconductionNotPending
	}

	// 3. floor 校验（仅 NOT_FOLLOW）。
	if in.Decision == DecisionNotFollow {
		priceCurrent, err := decimal.NewFromString(row.PriceCurrent)
		if err != nil {
			return nil, fmt.Errorf("parse price_current %q: %w", row.PriceCurrent, err)
		}
		floorPrice, err := decimal.NewFromString(row.FloorPrice)
		if err != nil {
			return nil, fmt.Errorf("parse floor_price %q: %w", row.FloorPrice, err)
		}
		if priceCurrent.LessThan(floorPrice) {
			return nil, ErrUpconductionBelowFloor
		}
	}

	// 4. 目标状态。
	var target string
	if in.Decision == DecisionFollow {
		target = UpconductionStatusFollowed
	} else {
		target = UpconductionStatusNotFollowed
	}

	// 5. 落库。
	in.Decision = target // 内部传递用最终状态——Decide 的 store 层只看 result
	if err := s.store.Decide(ctx, in, operatorRole); err != nil {
		return nil, err
	}

	// 6. 返回读取后的最新行（保证与库一致）。
	updated, err := s.store.LoadQueueRowByID(ctx, in.QueueID)
	if err != nil {
		return nil, fmt.Errorf("reload after decide: %w", err)
	}
	return &DecideResult{
		QueueID:       in.QueueID,
		Status:        updated.Status,
		DecidedBy:     in.OperatorID,
		DecidedAt:     s.now(),
		OverridePrice: updated.OverridePrice,
	}, nil
}
