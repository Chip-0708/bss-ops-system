// Package cost 的 param_service.go：成本参数读写服务（06-cost.md §7，6d-2）。
//
// 语义裁决（全部钉死在本文件与单测，改动前先读）：
//  1. defaults 本批次**只读**：PUT body 带非空 defaults → ErrDefaultsReadOnly，
//     绝不静默忽略输入（静默吞输入在本项目等同 bug）。
//  2. overrides 全量替换 = 一个事务里「DELETE scope_type∈(MODEL,SUPPLIER) + 逐行 INSERT」；
//     GLOBAL 行**绝不被触碰**（DELETE 限定 scope_type，INSERT 集也不会含 GLOBAL）；
//     只用 DELETE+INSERT，不用 ON CONFLICT DO UPDATE（语义更直白，且 cost_param
//     没有长事务热点；unique(scope_type,scope_id) 由 000016 兜底）。
//  3. 比率读侧「真·原样」、写侧 StringFixed(4) 规整：
//     - 读：API 响应透传 numeric(8,4) 入库**原始字符串**（含尾零，"0.0300"）——
//     用 StoredParam（字符串字段）承载，绝不调 decimal.String()（String() 会
//     trim 尾零，"0.0300"→"0.03"，违反裁决）。
//     - 写：PUT 输入过 parseRate 校验后，以 formatRate（StringFixed(4)）规整落库，
//     与 numeric(8,4) 列对齐——库内恒为 4 位小数，读侧因此自然恒返 4 位小数。
//     - 输入解析用 decimal.NewFromString 严格模式（拒绝 1e-3 这类浮点写法）。
//  4. 校验全部在事务开启前完成（纯内存 + 只读查询），任一失败 → 事务根本不开。
//  5. 参数变更 → 同一事务入队 COST_RECALC（payload 带 sku_id，6d-2 新增字段）；
//     受影响 SKU = ListParamAffectedSKUs（GLOBAL 全集 + 新旧 override 逐条影响面，并集去重升序），
//     与业务写同事务提交——业务回滚绝不丢任务（红线 8），任务入队失败整体回滚。
//  6. 审计：单条 audit_log（action=COST_PARAM_UPDATE；before/after 均为完整覆盖集 JSON）。
package cost

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// ParamStore 是成本参数仓储的窄接口（不写全 Store——重算引擎与参数读写是两条路径）。
// GORM 实现见 internal/repo/cost_param.go；写方法必须经 txOf(ctx)（幂等中间件事务不被绕过）。
type ParamStore interface {
	// ListAllParams 全量读为 decimal 形态（数据量恒小：1 全局 + N 覆盖）。
	// **仅服务重算引擎与写路径审计 before**——API 读响应绝不用它（decimal 已丢尾零信息，
	// 见 StoredParam 注释）。调用方知道自己在做数值比较/计算时才调。
	ListAllParams(ctx context.Context) ([]Param, error)
	// ListStoredParams 全量读为**原样字符串**形态（Task 2 Option A）。
	// 返回的比率字段是 numeric(8,4) 入库原字符（含尾零，如 "0.0300"），
	// 读侧 API 响应必须从这里取——绝不能经 Param.LossRate.String()（String() 会 trim 尾零）。
	ListStoredParams(ctx context.Context) ([]StoredParam, error)
	// SKUExists / SupplierExists 存在性校验（MODEL.scope_id=model_sku.id、
	// SUPPLIER.scope_id=supplier_profile.id）。
	SKUExists(ctx context.Context, skuID int64) (bool, error)
	SupplierExists(ctx context.Context, supplierID int64) (bool, error)
	// ListParamAffectedSKUs 计算单个 scope 的影响面：
	// GLOBAL→SELECT DISTINCT sku_id FROM cost_baseline；
	// MODEL→cost_baseline ∩ model_sku 存在的 sku_id（scope_id 即 sku_id——遗留项 6d-2-②）；
	// SUPPLIER→cost_baseline ∩ 该供应商有 EFFECTIVE 报价的 SKU。去重升序。
	ListParamAffectedSKUs(ctx context.Context, scopeType string, scopeID int64) ([]int64, error)
	// ReplaceOverridesTx 单事务原子替换：DELETE scope_type∈deletes + 逐行 INSERT inserts
	// + audit_log(before/after) + 对每个 affectedSKUs 入队一条 COST_RECALC。
	// 返回新插入的第一行 id（无插入=0）、审计行 id、任务 id 列表。
	ReplaceOverridesTx(ctx context.Context, p ReplaceTxParams) (*ReplaceTxResult, error)
}

// ParamRowInsert 是新插入行的全部可写字段（audit/任务共用同一 now）。
type ParamRowInsert struct {
	ScopeType      string
	ScopeID        int64
	LossRate       string
	ChannelRate    string
	TaxInclusive   bool
	WithholdingTax string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	RequestID      string
	CreatedBy      int64
	UpdatedBy      int64
}

// ReplaceTxParams 是 ReplaceOverridesTx 的输入（服务层已校验过，repo 只做机械写入）。
type ReplaceTxParams struct {
	Now          time.Time
	RequestID    string
	OperatorID   int64
	OperatorRole string // 审计 operator_role 用（绝不参与权限判定）
	Deletes      []string
	Inserts      []ParamRowInsert
	Before       map[string]any
	After        map[string]any
	AffectedSKUs []int64 // 已去重升序
}

// ReplaceTxResult 是 ReplaceOverridesTx 的结果（回填给调用方做 200 响应素材）。
type ReplaceTxResult struct {
	AuditLogID int64
	TaskIDs    []int64
}

// ParamService 是成本参数的读写编排（无状态，可并发使用）。
type ParamService struct {
	store ParamStore
	now   func() time.Time // 可注入时钟，测试用（默认 time.Now().UTC）
}

// NewParamService 构造参数服务。now 为 nil 时用 time.Now().UTC。
func NewParamService(store ParamStore, now func() time.Time) *ParamService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ParamService{store: store, now: now}
}

// ErrParamStoreNil 未接仓储时的防御错误（不允许空指针 panic）。
var ErrParamStoreNil = errors.New("cost.ParamService 未接 ParamStore")

// ---- 读路径 ----

// ParamViewCost 是单份比率集（API 输出的 JSON 视图，字符串原样返回——不 trim 不 round）。
type ParamViewCost struct {
	LossRate       string `json:"loss_rate"`
	ChannelRate    string `json:"channel_rate"`
	TaxInclusive   bool   `json:"tax_inclusive"`
	WithholdingTax string `json:"withholding_tax"`
}

// OverrideView 是一条覆盖（scope_type ∈ MODEL/SUPPLIER）。
type OverrideView struct {
	ScopeType      string `json:"scope_type"`
	ScopeID        int64  `json:"scope_id"`
	LossRate       string `json:"loss_rate"`
	ChannelRate    string `json:"channel_rate"`
	TaxInclusive   bool   `json:"tax_inclusive"`
	WithholdingTax string `json:"withholding_tax"`
}

// ParamsView 是 GET /cost/params 的 data：defaults + overrides[]。
type ParamsView struct {
	Defaults  ParamViewCost  `json:"defaults"`
	Overrides []OverrideView `json:"overrides"`
}

// ListParams 组装 GET 响应：GLOBAL 行进 defaults（**原样透传库存字符串**，含尾零），
// MODEL/SUPPLIER 行进 overrides（scope_type, scope_id 升序——稳定序）。
// GLOBAL 缺失 = 数据事故（000016 种子保证恒在），按错误上抛不静默。
//
// ⚠️ 这里必须走 ListStoredParams（原样）而**不是** ListAllParams + formatRate：
// decimal 在存储时已丢尾零（"0.0500"→Decimal("0.05")），再 StringFixed(4) 虽然
// 恰好能补回 4 位，但那是**重造**而非**透传**。Option A 的裁决是「真·原样」——
// 直接透传 DB 返还的字符串，不经任何中间重整形。
func (s *ParamService) ListParams(ctx context.Context) (*ParamsView, error) {
	if s == nil || s.store == nil {
		return nil, ErrParamStoreNil
	}
	rows, err := s.store.ListStoredParams(ctx)
	if err != nil {
		return nil, fmt.Errorf("list cost_param: %w", err)
	}
	view := &ParamsView{Overrides: []OverrideView{}}
	globalFound := false
	for _, p := range rows {
		// 原样透传（rating 三个字段直接搬运，不做任何正则/format——
		// 库存字符串即返回字符串，尾零 1:1 保留）。
		pc := ParamViewCost{
			LossRate:       p.LossRate,
			ChannelRate:    p.ChannelRate,
			TaxInclusive:   p.TaxInclusive,
			WithholdingTax: p.WithholdingTax,
		}
		switch p.ScopeType {
		case ScopeGlobal:
			view.Defaults = pc
			globalFound = true
		case ScopeModel, ScopeSupplier:
			view.Overrides = append(view.Overrides, OverrideView{
				ScopeType:      p.ScopeType,
				ScopeID:        p.ScopeID,
				LossRate:       pc.LossRate,
				ChannelRate:    pc.ChannelRate,
				TaxInclusive:   pc.TaxInclusive,
				WithholdingTax: pc.WithholdingTax,
			})
		}
	}
	if !globalFound {
		return nil, errors.New("cost_param GLOBAL 行缺失（000016 种子应保证恒在，属数据事故）")
	}
	sort.Slice(view.Overrides, func(i, j int) bool {
		if view.Overrides[i].ScopeType != view.Overrides[j].ScopeType {
			return view.Overrides[i].ScopeType < view.Overrides[j].ScopeType
		}
		return view.Overrides[i].ScopeID < view.Overrides[j].ScopeID
	})
	return view, nil
}

// ---- 写路径 ----

// OverrideItem 是 PUT body 单条覆盖输入（字符串十进制，红线 1）。
type OverrideItem struct {
	ScopeType      string `json:"scope_type"`
	ScopeID        int64  `json:"scope_id"`
	LossRate       string `json:"loss_rate"`
	ChannelRate    string `json:"channel_rate"`
	TaxInclusive   bool   `json:"tax_inclusive"`
	WithholdingTax string `json:"withholding_tax"`
}

// PutParamsInput 是 PUT 的完整请求体。Overrides 用**指针切片**区分「key 缺失」
// （nil → 400）与「key 存在但为空集」（合法的全量清空）。
type PutParamsInput struct {
	Defaults  *ParamViewCost  `json:"defaults"` // 6d-2 只读：非 nil → 400
	Overrides *[]OverrideItem `json:"overrides"`
}

// PutOperator 是 PUT 调用人（鉴权已在中间件 + 服务层角色收敛完成，这里只要审计字段）。
type PutOperator struct {
	OperatorID   int64
	OperatorRole string // operatorRoleOf 结果（仅审计落库，绝不二次鉴权）
}

// PutResult 返回 PUT 的简要结果，供 handler 组装响应。
type PutResult struct {
	OverridesCount int     // 实际生效的覆盖条数
	SubmittedTasks int     // 入队的 COST_RECALC 条数（=受影响 SKU 并集数）
	TaskIDs        []int64 // 任务 id 列表（升序）
	AuditLogID     int64   // 审计行 id
}

// 领域错误（handler 按 errors.Is 映射 HTTP 码，全部 400）。
var (
	// ErrDefaultsReadOnly 本批次 defaults 只读：想写一律拒绝，绝不静默忽略（裁决 1）。
	ErrDefaultsReadOnly = errors.New("本批次不支持修改全局默认值，见 CLAUDE.md 遗留项 6d-2-①")
	// ErrOverridesKeyMissing 缺 overrides key（必须区分于 "overrides": [] 合法清空）。
	ErrOverridesKeyMissing = errors.New("请求缺少 overrides 字段")
	// ErrScopeTypeInvalid scope_type 只能是 MODEL / SUPPLIER；GLOBAL 走 defaults（本批次已锁）。
	ErrScopeTypeInvalid = errors.New("scope_type 只允许 MODEL 或 SUPPLIER")
	// ErrScopeNotFound scope_id 必须存在（MODEL→model_sku.id，SUPPLIER→supplier_profile.id）。
	ErrScopeNotFound = errors.New("scope_id 在对应表中不存在")
	// ErrDuplicateScope 同一 (scope_type, scope_id) 在 body 里重复（不依赖 DB unique 兜底）。
	ErrDuplicateScope = errors.New("同一 (scope_type, scope_id) 重复，请合并后重试")
	// ErrRateInvalid 比率不能解析为十进制（拒绝浮点写法），或 <0 / >1 / 小数位 >4。
	ErrRateInvalid = errors.New("loss_rate / channel_rate / withholding_tax 必须是 0~1 且小数位 ≤4 的十进制数")
)

// rateMax 单率上限 [0,1]。
var rateMax = decimal.NewFromInt(1)

// ratePrecision / rateExponent digits=8, exponent=-4 与 numeric(8,4) 一致。
// DB 读出的字符串恒为 4 位小数（如 "0.0300"）；写侧 StringFixed(4) 与读侧对齐。
const (
	// ratePrecision 与 numeric(8,4) 对齐——DB 读出的字符串恒为 4 位小数，
	// 写侧 StringFixed(ratePrecision) 语义也锁定为同一精度（裁决 3 的物理后果）。
	ratePrecision = 4
)

// formatRate 输出速率字符串：固定 ratePrecision 位小数。
// **仅写侧用过**——PUT 的 parseRate 校验通过后，以 StringFixed(4) 落库，与 numeric(8,4)
// 列对齐；读侧 API 响应绝不用它（读侧走 ListStoredParams 原样透传）。
// 写侧用它而非「原样入库」的原因：PUT body 可能给 "1"/"0.5" 这类简化字面量，
// 规整到 4 位与 numeric(8,4) 保持一致，物理落库值与服务层意图 1:1 对应。
func formatRate(d decimal.Decimal) string { return d.StringFixed(ratePrecision) }

// parseRate 严格校验一条比率字符串。空串按非法拒绝（成本参数没有"空 = 默认"语义）。
// 拒绝科学计数法（"1e-3" 这种）：decimal.NewFromString 会接受，但裁决 3 要求
// 输入必须是**纯小数字面量**——科学计数法在数据库 numeric(20,8) 里也不会保留
// "1e-3" 的精度语义，直接拒绝最简单。报错消息里必须带收到的**原始值**。
func parseRate(raw string) (decimal.Decimal, error) {
	if raw == "" {
		return decimal.Zero, fmt.Errorf("%w：收到空串", ErrRateInvalid)
	}
	// 拒绝科学计数法（e/E 出现在除首位外的任何位置都拒绝——"1e-3"、"0.5E2"、"1E+1"）。
	// decimal 包能解析，但这不是我们想在 API 边界收的形态。
	for _, r := range raw {
		if r == 'e' || r == 'E' {
			return decimal.Zero, fmt.Errorf("%w：%q 含科学计数法，API 只收纯小数字面量", ErrRateInvalid, raw)
		}
	}
	d, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w：%q 不是合法十进制", ErrRateInvalid, raw)
	}
	if d.IsNegative() {
		return decimal.Zero, fmt.Errorf("%w：%q 为负数", ErrRateInvalid, raw)
	}
	if d.GreaterThan(rateMax) {
		return decimal.Zero, fmt.Errorf("%w：%q 大于 1", ErrRateInvalid, raw)
	}
	if d.Exponent() < -ratePrecision {
		return decimal.Zero, fmt.Errorf("%w：%q 小数位超过 %d 位", ErrRateInvalid, raw, ratePrecision)
	}
	return d, nil
}

// validatedItem 是验收过的单条覆盖（比率已 decimal 化，事务里不再二次解析）。
type validatedItem struct {
	ScopeType      string
	ScopeID        int64
	LossRate       decimal.Decimal
	ChannelRate    decimal.Decimal
	TaxInclusive   bool
	WithholdingTax decimal.Decimal
}

// validateOverrides 纯校验（不查库）：scope_type 白名单 + (scope_type, scope_id) 判重 +
// 三个率全部 parseRate。报错带 1-based 条序，看着比 i 舒服。
func validateOverrides(items []OverrideItem) ([]validatedItem, error) {
	type scopeKey struct {
		t  string
		id int64
	}
	seen := make(map[scopeKey]struct{}, len(items))
	out := make([]validatedItem, 0, len(items))
	for i, it := range items {
		if it.ScopeType != ScopeModel && it.ScopeType != ScopeSupplier {
			return nil, fmt.Errorf("%w：第 %d 条收到 %q", ErrScopeTypeInvalid, i+1, it.ScopeType)
		}
		key := scopeKey{t: it.ScopeType, id: it.ScopeID}
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("%w：第 %d 条 scope_type=%s scope_id=%d", ErrDuplicateScope, i+1, it.ScopeType, it.ScopeID)
		}
		seen[key] = struct{}{}
		loss, err := parseRate(it.LossRate)
		if err != nil {
			return nil, fmt.Errorf("第 %d 条 loss_rate：%w", i+1, err)
		}
		channel, err := parseRate(it.ChannelRate)
		if err != nil {
			return nil, fmt.Errorf("第 %d 条 channel_rate：%w", i+1, err)
		}
		tax, err := parseRate(it.WithholdingTax)
		if err != nil {
			return nil, fmt.Errorf("第 %d 条 withholding_tax：%w", i+1, err)
		}
		out = append(out, validatedItem{
			ScopeType: it.ScopeType, ScopeID: it.ScopeID,
			LossRate: loss, ChannelRate: channel,
			TaxInclusive: it.TaxInclusive, WithholdingTax: tax,
		})
	}
	return out, nil
}

// checkScopesExist 逐条 scope_id 做存在性校验（MODEL / SUPPLIER 分表）。
// 数据量级低（全量覆盖条目数量级 ≤ 几十），逐条查比一次 IN(...) 更直白、报错更精准。
func (s *ParamService) checkScopesExist(ctx context.Context, items []validatedItem) error {
	for _, it := range items {
		var (
			ok  bool
			err error
		)
		switch it.ScopeType {
		case ScopeModel:
			ok, err = s.store.SKUExists(ctx, it.ScopeID)
		case ScopeSupplier:
			ok, err = s.store.SupplierExists(ctx, it.ScopeID)
		default: // validateOverrides 已白名单，这里是防御
			return fmt.Errorf("%w：%q", ErrScopeTypeInvalid, it.ScopeType)
		}
		if err != nil {
			return fmt.Errorf("检查 scope 存在性 scope_type=%s scope_id=%d: %w", it.ScopeType, it.ScopeID, err)
		}
		if !ok {
			return fmt.Errorf("%w：scope_type=%s scope_id=%d", ErrScopeNotFound, it.ScopeType, it.ScopeID)
		}
	}
	return nil
}

// ReplaceOverrides PUT 核心编排：
// 全部校验过了再开事务（ReplaceOverridesTx 一个事务里【DELETE 全部 override →
// 逐条 INSERT → audit → 对每个受影响 SKU 入队一条 COST_RECALC】）。
// GLOBAL 行绝不动（DELETE 限定 scope_type，INSERT 集也不含 GLOBAL）。
func (s *ParamService) ReplaceOverrides(ctx context.Context, input PutParamsInput, op PutOperator, requestID string) (*PutResult, error) {
	if s == nil || s.store == nil {
		return nil, ErrParamStoreNil
	}
	if input.Overrides == nil {
		return nil, ErrOverridesKeyMissing
	}
	if input.Defaults != nil {
		return nil, ErrDefaultsReadOnly
	}

	// 1) 纯校验（含 parseRate）；2) 存在性校验（只读查询，不开事务）。
	items, err := validateOverrides(*input.Overrides)
	if err != nil {
		return nil, err
	}
	if err := s.checkScopesExist(ctx, items); err != nil {
		return nil, err
	}

	// 3) 受影响 SKU 集：GLOBAL 恒有影响（任何 override 的新增/删除都会改变
	// 「回响到默认」的 SKU 集合）；新旧 override 集逐条求影响面并集——
	// 旧集里被删除的 override 失去定制，必须重算回默认（不能漏）。
	now := s.now().UTC()
	affectedSet := make(map[int64]struct{})
	unionWith := func(list []int64) {
		for _, id := range list {
			affectedSet[id] = struct{}{}
		}
	}
	gIDs, err := s.store.ListParamAffectedSKUs(ctx, ScopeGlobal, GlobalScopeID)
	if err != nil {
		return nil, fmt.Errorf("list param affected skus GLOBAL: %w", err)
	}
	unionWith(gIDs)
	// 3b) 新集影响面。
	unionScopes := make([]struct {
		t  string
		id int64
	}, 0, len(items))
	for _, it := range items {
		unionScopes = append(unionScopes, struct {
			t  string
			id int64
		}{t: it.ScopeType, id: it.ScopeID})
	}
	// 4) 变更前完整覆盖集（审计 before_value + 旧集影响面）。
	// 这里走 ListAllParams（decimal）→ formatRate 规整，与写侧 StringFixed(4) 同口径：
	// 审计 JSON 里的字符串和写库字符串一致（都是 4 位小数），避免 before/after
	// 因尾零差异肉眼 contrasts 很难看。审计不是 API 响应——不需要「原样透传」约束，
	// 反而需要「与写库字符串对齐」的可比对性。
	before, err := s.store.ListAllParams(ctx)
	if err != nil {
		return nil, fmt.Errorf("list cost_param before: %w", err)
	}
	var beforeOverrides []OverrideView
	for _, p := range before {
		if p.ScopeType == ScopeGlobal {
			continue
		}
		// formatRate 规整：审计 JSON 与写库字符串一致，避免前后值尾零不一致。
		beforeOverrides = append(beforeOverrides, OverrideView{
			ScopeType:      p.ScopeType,
			ScopeID:        p.ScopeID,
			LossRate:       formatRate(p.LossRate),
			ChannelRate:    formatRate(p.ChannelRate),
			TaxInclusive:   p.TaxInclusive,
			WithholdingTax: formatRate(p.WithholdingTax),
		})
	}
	for _, pv := range beforeOverrides {
		unionScopes = append(unionScopes, struct {
			t  string
			id int64
		}{t: pv.ScopeType, id: pv.ScopeID})
	}
	for _, sc := range unionScopes {
		ids, err := s.store.ListParamAffectedSKUs(ctx, sc.t, sc.id)
		if err != nil {
			return nil, fmt.Errorf("list param affected skus %s/%d: %w", sc.t, sc.id, err)
		}
		unionWith(ids)
	}
	affected := make([]int64, 0, len(affectedSet))
	for id := range affectedSet {
		affected = append(affected, id)
	}
	sort.Slice(affected, func(i, j int) bool { return affected[i] < affected[j] })

	// 5) 构造插入行（审计/任务共用同一 now，保证可追溯）。
	// 比率列全部 formatRate（StringFixed(4)）——写侧规整到 4 位小数，与读侧对齐；
	// 这样下次 GET 看到的是写库的值字符串，不会因 String() 尾零差异抖动（裁决 3）。
	inserts := make([]ParamRowInsert, 0, len(items))
	for _, it := range items {
		inserts = append(inserts, ParamRowInsert{
			ScopeType: it.ScopeType, ScopeID: it.ScopeID,
			LossRate:       formatRate(it.LossRate),
			ChannelRate:    formatRate(it.ChannelRate),
			TaxInclusive:   it.TaxInclusive,
			WithholdingTax: formatRate(it.WithholdingTax),
			CreatedAt:      now,
			UpdatedAt:      now,
			RequestID:      requestID,
			CreatedBy:      op.OperatorID,
			UpdatedBy:      op.OperatorID,
		})
	}

	// 6) 构造 before/after JSON（红线 10：审计前后值完整、类型稳定——空集用 [] 而非 null）。
	afterItems := make([]OverrideView, 0, len(items))
	for _, it := range items {
		afterItems = append(afterItems, OverrideView{
			ScopeType: it.ScopeType, ScopeID: it.ScopeID,
			LossRate:       formatRate(it.LossRate),
			ChannelRate:    formatRate(it.ChannelRate),
			TaxInclusive:   it.TaxInclusive,
			WithholdingTax: formatRate(it.WithholdingTax),
		})
	}
	if beforeOverrides == nil {
		beforeOverrides = []OverrideView{}
	}

	txRes, err := s.store.ReplaceOverridesTx(ctx, ReplaceTxParams{
		Now:          now,
		RequestID:    requestID,
		OperatorID:   op.OperatorID,
		OperatorRole: op.OperatorRole,
		Deletes:      []string{ScopeModel, ScopeSupplier}, // GLOBAL 绝不在列
		Inserts:      inserts,
		Before:       map[string]any{"overrides": beforeOverrides},
		After:        map[string]any{"overrides": afterItems},
		AffectedSKUs: affected,
	})
	if err != nil {
		return nil, err
	}
	return &PutResult{
		OverridesCount: len(items),
		SubmittedTasks: len(txRes.TaskIDs),
		TaskIDs:        txRes.TaskIDs,
		AuditLogID:     txRes.AuditLogID,
	}, nil
}
