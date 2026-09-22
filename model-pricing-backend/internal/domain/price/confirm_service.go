package price

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// ============================================================
// 7b 确认入正式版本 + 审批 + 生效连锁（契约 07 §7/§8/§9）
//
// 状态字典裁决（红线 4 / 设计 §7.3，唯一权威）：
//   - change_type ∈ {PRICE_UP, PRICE_DOWN}（提示词的 "PRICE_CHANGE" 不在 000006 DDL 枚举内，
//     已确认废弃）。审批步数按涨跌动态：降价 1 步（MODEL_OPS）、涨价 2 步（MODEL_OPS→PRICING_OP），
//     与设计 §7.2 一致；提示词"恒 2 步"自相矛盾（裁决 7 自己又说降价 1 步），不采用。
//   - effective_time 只允许 ≤now（>now → 400）；price_version 无 status 列，生命周期由
//     is_current + effective_from/to 表达；预约生效登记为遗留 7b-①。
//   - 连锁范围 = ①price_version 新版本 ②静默跟随 ⑤task_job+event_outbox ⑥cache_version+1
//     + audit；price_book 属阶段 8，不侵入。
//
// 边界：本包只做"确认 → change_request + approval_steps"的创建与"涨跌方向/合并/毛利预览"纯函数；
// 连锁生效（ApplyOfficialPriceChange）由 repo 层在审批全部通过的回调里执行（同事务）。
// ============================================================

// 状态字典常量（000006 DDL ck_change_type / ck_change_status）。
const (
	// ChangePriceUp 涨价：2 步审批（MODEL_OPS → PRICING_OP）。
	ChangePriceUp = "PRICE_UP"
	// ChangePriceDown 降价：1 步审批（MODEL_OPS）。
	ChangePriceDown = "PRICE_DOWN"
)

// 领域错误（HTTP 层经 errorcode 映射：400/404/409）。
var (
	// ErrConfirmStagingsEmpty staging_ids 必填非空。
	ErrConfirmStagingsEmpty = errors.New("staging_ids 必填且不能为空")
	// ErrConfirmEffectiveFuture effective_time 不允许未来时点（预约生效登记为遗留 7b-①）。
	ErrConfirmEffectiveFuture = errors.New("effective_time 不允许未来时点，本批只支持立即生效")
	// ErrConfirmStagingNotFound 任一 staging_id 不存在。
	ErrConfirmStagingNotFound = errors.New("暂存行不存在")
	// ErrConfirmStagingProcessed 任一 staging 已被确认（processed=true，幂等拒绝重确认）。
	ErrConfirmStagingProcessed = errors.New("暂存行已被确认，不允许重复确认")
	// ErrConfirmStagingJobMismatch staging.sync_job_id 与请求 sync_job_id 不一致。
	ErrConfirmStagingJobMismatch = errors.New("暂存行不属于该采集批次")
	// ErrConfirmNoComponent 合并后没有任何组件价格（payload 全空）。
	ErrConfirmNoComponent = errors.New("合并后无任何组件价格")
)

// ConfirmInput 是确认入正式版本的输入（契约 07 §7）。
type ConfirmInput struct {
	SyncJobID     int64   `json:"sync_job_id" binding:"required"`
	StagingIDs    []int64 `json:"staging_ids" binding:"required"`
	EffectiveTime string  `json:"effective_time" binding:"required"` // RFC3339，只允许 ≤now
}

// ConfirmResult 是确认结果。
type ConfirmResult struct {
	ChangeRequestID int64 `json:"change_request_id"`
	StepCount       int   `json:"step_count"`
}

// ConfirmStore 是 ConfirmService 依赖的窄接口（repo 层 GORM 实现）。
type ConfirmStore interface {
	// LoadStagings 批量按 id 加载 staging（含 sku_id/sync_job_id/processed/payload/currency）。
	LoadStagings(ctx context.Context, ids []int64) ([]ConfirmStagingRow, error)
	// LoadCurrentPriceMap 加载指定 SKU 当前官方价（is_current=true）组件 map：sku_id → component_type → unit_price。
	// 与 7a LoadCurrentPriceVersions 同口径（只读，不 N+1）。
	LoadCurrentPriceMap(ctx context.Context, skuIDs []int64) (map[int64]map[string]decimal.Decimal, error)
	// LoadCostParams 加载 GLOBAL + 指定 MODEL/MODEL scope 的成本参数（供 margin_preview floor 计算）。
	// 返回 ResolveParams 可直接消费的行集。
	LoadCostParams(ctx context.Context, skuIDs []int64) ([]CostParamRow, error)
	// LoadMinGrossMargin 读 sys_config.min_gross_margin（与 6b-4 同口径；读失败返回 error 由上层降级）。
	LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error)
	// ConfirmStaging 单事务：创建 change_request + approval_steps + 标记 staging.processed=true。
	// 返回新 change_request 的 id。
	ConfirmStaging(ctx context.Context, p ConfirmStagingParams) (int64, error)
}

// ConfirmStagingRow 是 staging_price 的读模型（domain 层，避免 repo 行模型泄漏）。
type ConfirmStagingRow struct {
	ID         int64
	SyncJobID  int64
	SKUID      *int64
	Processed  bool
	Currency   string
	Payload    map[string]string // component_type → unit_price（字符串，保持精度）
	SourceType string
}

// CostParamRow 是 cost_param 的读模型（domain 层）。
type CostParamRow struct {
	ScopeType   string
	ScopeID     int64
	LossRate    decimal.Decimal
	ChannelRate decimal.Decimal
}

// ConfirmStagingParams 是 ConfirmStaging 的入参。
type ConfirmStagingParams struct {
	SKUID         int64 // change_request.sku_id（NOT NULL；同批多 SKU 时取首条）
	ChangeType    string
	StepCount     int
	Payload       []byte // change_request.payload（含合并后价格、来源 staging_ids、effective_time、direction）
	MarginPreview []byte // change_request.margin_preview（仅涨价；降价为 nil）
	EffectiveTime time.Time
	OperatorID    int64
	OperatorRole  string
	RequestID     string
	StagingIDs    []int64 // 待标记 processed=true 的 id 集（去重后）
}

// ConfirmService 是确认入正式版本的服务。
type ConfirmService struct {
	store ConfirmStore
	now   func() time.Time
}

// NewConfirmService 构造服务；now 可注入（测试用）。
func NewConfirmService(store ConfirmStore, now func() time.Time) *ConfirmService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ConfirmService{store: store, now: now}
}

// Confirm 校验 → 合并 → 判涨跌 → 建 change_request + 审批步。
func (s *ConfirmService) Confirm(ctx context.Context, in ConfirmInput, operatorID int64, operatorRole, requestID string) (*ConfirmResult, error) {
	// 1. staging_ids 非空（绝不静默吞输入）。
	if len(in.StagingIDs) == 0 {
		return nil, ErrConfirmStagingsEmpty
	}
	// 2. effective_time 解析 + 只允许 ≤now。
	effectiveTime, err := time.Parse(time.RFC3339, strings.TrimSpace(in.EffectiveTime))
	if err != nil {
		return nil, fmt.Errorf("effective_time 非法（需 RFC3339）：%w", err)
	}
	now := s.now()
	if effectiveTime.After(now) {
		return nil, fmt.Errorf("%w：effective_time=%s", ErrConfirmEffectiveFuture, in.EffectiveTime)
	}
	// 3. 批量加载 staging 并逐项校验存在性/归属/未确认。
	stagings, err := s.store.LoadStagings(ctx, in.StagingIDs)
	if err != nil {
		return nil, err
	}
	found := make(map[int64]ConfirmStagingRow, len(stagings))
	for _, r := range stagings {
		found[r.ID] = r
	}
	for _, id := range in.StagingIDs {
		r, ok := found[id]
		if !ok {
			return nil, fmt.Errorf("%w：staging_id=%d", ErrConfirmStagingNotFound, id)
		}
		if r.Processed {
			return nil, fmt.Errorf("%w：staging_id=%d", ErrConfirmStagingProcessed, id)
		}
		if r.SyncJobID != in.SyncJobID {
			return nil, fmt.Errorf("%w：staging_id=%d 属于 sync_job_id=%d", ErrConfirmStagingJobMismatch, id, r.SyncJobID)
		}
		if r.SKUID == nil {
			// UNMATCHED 行不可确认（提示词 §7 边界）。
			return nil, fmt.Errorf("%w：staging_id=%d 未匹配 SKU", ErrConfirmStagingNotFound, id)
		}
	}
	// 4. 同 SKU 合并：max(staging.id) 胜出（裁决：同一 SKU 重复录入以最后一条为准）。
	bySKU := mergeStagingsBySKU(stagings)
	if len(bySKU) == 0 {
		return nil, ErrConfirmNoComponent
	}
	skuIDs := make([]int64, 0, len(bySKU))
	for skuID := range bySKU {
		skuIDs = append(skuIDs, skuID)
	}
	sort.Slice(skuIDs, func(i, j int) bool { return skuIDs[i] < skuIDs[j] })
	// 5. 当前官方价（供涨跌方向判定）。
	currentMap, err := s.store.LoadCurrentPriceMap(ctx, skuIDs)
	if err != nil {
		return nil, err
	}
	// 6. 判涨跌方向：任一组件 new > old → PRICE_UP；全部 ≤ → PRICE_DOWN。
	direction := detectDirection(bySKU, currentMap)
	stepCount := 1
	if direction == ChangePriceUp {
		stepCount = 2
	}
	// 7. margin_preview 仅涨价（floor = newPrice ×(1+loss)×(1+channel)/(1−min_gross_margin)）。
	var marginPreview []byte
	if direction == ChangePriceUp {
		marginPreview, err = s.buildMarginPreview(ctx, bySKU, skuIDs)
		if err != nil {
			return nil, err
		}
	}
	// 8. change_request.payload：合并后价格 + 来源 staging_ids + effective_time + direction。
	payload, err := buildChangePayload(bySKU, in.StagingIDs, effectiveTime, direction)
	if err != nil {
		return nil, err
	}
	// 9. 事务：change_request + approval_steps + staging.processed=true。
	changeRequestID, err := s.store.ConfirmStaging(ctx, ConfirmStagingParams{
		SKUID: skuIDs[0], ChangeType: direction, StepCount: stepCount,
		Payload: payload, MarginPreview: marginPreview,
		EffectiveTime: effectiveTime, OperatorID: operatorID, OperatorRole: operatorRole,
		RequestID: requestID, StagingIDs: dedupIDs(in.StagingIDs),
	})
	if err != nil {
		return nil, err
	}
	return &ConfirmResult{ChangeRequestID: changeRequestID, StepCount: stepCount}, nil
}

// mergeStagingsBySKU 同 SKU 合并：max(staging.id) 的 payload 胜出（整行替换，不逐组件合并）。
// 返回 sku_id → 合并后的组件 map。
func mergeStagingsBySKU(stagings []ConfirmStagingRow) map[int64]map[string]decimal.Decimal {
	best := make(map[int64]ConfirmStagingRow) // sku_id → 当前最大 id 的行
	for _, r := range stagings {
		if r.SKUID == nil {
			continue
		}
		if cur, ok := best[*r.SKUID]; !ok || r.ID > cur.ID {
			best[*r.SKUID] = r
		}
	}
	out := make(map[int64]map[string]decimal.Decimal, len(best))
	for skuID, r := range best {
		m := make(map[string]decimal.Decimal, len(r.Payload))
		for ct, raw := range r.Payload {
			v, err := decimal.NewFromString(raw)
			if err == nil {
				m[ct] = v
			}
		}
		if len(m) > 0 {
			out[skuID] = m
		}
	}
	return out
}

// detectDirection 判涨跌：任一组件 new > old → PRICE_UP；否则 PRICE_DOWN。
// old 缺失（该 SKU 当前无官方价 / 该组件无官方价）按 new > 0 视为涨（新设价）。
func detectDirection(bySKU map[int64]map[string]decimal.Decimal, current map[int64]map[string]decimal.Decimal) string {
	for skuID, comps := range bySKU {
		old := current[skuID]
		for ct, nv := range comps {
			ov, ok := old[ct]
			if !ok {
				if nv.IsPositive() {
					return ChangePriceUp
				}
				continue
			}
			if nv.GreaterThan(ov) {
				return ChangePriceUp
			}
		}
	}
	return ChangePriceDown
}

// buildMarginPreview 计算涨价 floor：newPrice ×(1+loss)×(1+channel)/(1−min_gross_margin)。
// 返回 change_request.margin_preview 的 JSON（含逐 SKU floor 与公式说明）。
// 任一 SKU 缺参数则该 SKU floor 记 null 并附 note（不阻断审批）。
func (s *ConfirmService) buildMarginPreview(ctx context.Context, bySKU map[int64]map[string]decimal.Decimal, skuIDs []int64) ([]byte, error) {
	margin, err := s.store.LoadMinGrossMargin(ctx)
	if err != nil {
		// 读失败降级：不写 margin_preview（nil），不阻断确认（与 6b-4 读路径降级同口径）。
		return nil, nil //nolint:nilerr // 读失败降级是显式裁决
	}
	params, err := s.store.LoadCostParams(ctx, skuIDs)
	if err != nil {
		return nil, fmt.Errorf("load cost params: %w", err)
	}
	type skuFloor struct {
		Floor *string `json:"floor_price"`
		Note  string  `json:"note,omitempty"`
	}
	floors := make(map[string]skuFloor, len(skuIDs))
	one := decimal.NewFromInt(1)
	denom := one.Sub(margin)
	if denom.IsZero() || denom.IsNegative() {
		return nil, nil // 毛利率 ≥100% 无意义，降级
	}
	for _, skuID := range skuIDs {
		p := resolveCostParam(params, skuID)
		if p == nil {
			floors[fmt.Sprint(skuID)] = skuFloor{Note: "无成本参数，floor 不可算"}
			continue
		}
		// 取该 SKU 合并后组件的"代表组件"价做 floor 基准（与 6b 同口径：input 优先否则字母序首个）。
		comp := representativeComponent(bySKU[skuID])
		if comp == nil {
			floors[fmt.Sprint(skuID)] = skuFloor{Note: "无组件价"}
			continue
		}
		// floor = price ×(1+loss)×(1+channel)/(1−margin)
		floor := comp.Mul(one.Add(p.LossRate)).Mul(one.Add(p.ChannelRate)).Div(denom)
		fs := floor.StringFixed(8)
		floors[fmt.Sprint(skuID)] = skuFloor{Floor: &fs}
	}
	out := map[string]any{
		"min_gross_margin": margin.StringFixed(4),
		"formula":          "floor = price ×(1+loss_rate)×(1+channel_rate)/(1−min_gross_margin)",
		"floors":           floors,
	}
	return json.Marshal(out)
}

// resolveCostParam 简化解析：SUPPLIER > MODEL > GLOBAL（本批 margin_preview 只需 MODEL/GLOBAL）。
func resolveCostParam(rows []CostParamRow, skuID int64) *CostParamRow {
	var global, model *CostParamRow
	for i := range rows {
		r := &rows[i]
		switch r.ScopeType {
		case "GLOBAL":
			global = r
		case "MODEL":
			if r.ScopeID == skuID {
				model = r
			}
		}
	}
	if model != nil {
		return model
	}
	return global
}

// representativeComponent 代表组件：input 优先，否则字母序首个（与 6b RepresentativeComponent 同口径）。
func representativeComponent(comps map[string]decimal.Decimal) *decimal.Decimal {
	if v, ok := comps["input"]; ok {
		return &v
	}
	keys := make([]string, 0, len(comps))
	for k := range comps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil
	}
	v := comps[keys[0]]
	return &v
}

// buildChangePayload 构造 change_request.payload JSON。
func buildChangePayload(bySKU map[int64]map[string]decimal.Decimal, stagingIDs []int64, effectiveTime time.Time, direction string) ([]byte, error) {
	skus := make(map[string]map[string]string, len(bySKU))
	skuIDs := make([]int64, 0, len(bySKU))
	for skuID := range bySKU {
		skuIDs = append(skuIDs, skuID)
	}
	sort.Slice(skuIDs, func(i, j int) bool { return skuIDs[i] < skuIDs[j] })
	for _, skuID := range skuIDs {
		m := make(map[string]string, len(bySKU[skuID]))
		for ct, v := range bySKU[skuID] {
			m[ct] = v.StringFixed(8)
		}
		skus[fmt.Sprint(skuID)] = m
	}
	out := map[string]any{
		"direction":      direction,
		"effective_time": effectiveTime.UTC().Format(time.RFC3339),
		"staging_ids":    dedupIDs(stagingIDs),
		"sku_ids":        skuIDs,
		"prices":         skus,
	}
	return json.Marshal(out)
}

// dedupIDs 去重 + 排序（保持确定性）。
func dedupIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
