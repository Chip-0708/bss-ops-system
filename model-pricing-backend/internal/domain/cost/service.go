// Package cost 的 service.go：成本重算编排（COST_RECALC 消费的领域核心）。
//
// 数据流（06-cost §1/§9，交接 §4/§6）：
//
//	COST_RECALC payload → 展开成 SKU 集合（该 sheet 覆盖的 SKU ∪ 该供应商当前 EFFECTIVE 的 SKU）
//	→ 每个 SKU：装配输入（全供应商当前 EFFECTIVE 报价 + 生效历史区间 + 冻结过滤 + 三级成本参数）
//	→ 完全成本（unit_price ×(1+loss)×(1+channel)，不含税/不乘倍率/汇率不参与）
//	→ 6d 主供应商 = 排除后集合里四因子总分最高者（平手：RepresentCost 升序 → supplier_id 升序）
//	→ 值未变判定（逐组件 decimal.Equal + 双参数 + 主供应商 + formula_version）
//	→ 变化则不可变版本切换（旧版本关闭 + 新版本 INSERT，单事务）
//
// 事务边界：仓库层读（Load*）全部走最后一条 SELECT ... FOR UPDATE 行锁串行化并发重算；
// 写（Apply*）单事务完成「旧关 + 新插 + 组件 + 审计 + 事件」。
// 撞 uk_cost_current(23505) / ex_cost_no_overlap(23P01) → ErrVersionConflict，
// 是预期重试信号（交接 §4），不要"修"逻辑。
package cost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ---- 领域错误 ----

var (
	// ErrVersionConflict 并发/约束兜底：uk_cost_current(23505) 或 ex_cost_no_overlap(23P01)
	// 被命中。由消费者（6b-3）记 FAILED 重试，属预期行为。
	ErrVersionConflict = errors.New("成本基线版本并发冲突或区间重叠")
	// ErrInvalidChangeReason change_reason 不在 §0.4 枚举内（红线 4，禁止自创状态）。
	ErrInvalidChangeReason = errors.New("非法的成本变更原因")
	// ErrNoQuoteSKU 该 SKU 当前没有任何参与计算的 EFFECTIVE 报价。
	ErrNoQuoteSKU = errors.New("该 SKU 当前无有效供应商报价，无法计算成本")
	// ErrLockedSupplierInvalid 6d-3 新增（提示词陷阱 3 的「无效 → 报错、不静默降级」分支）：
	// 前一个版本是手动锁定，但被锁的供应商此刻已失效（该 SKU 无 EFFECTIVE 报价 /
	// 资质冻结 / 结算冻结 / INACTIVE / 供应商已删除）。
	// 此时**绝不**静默回落算法结论或自动解锁——返回本错误，不产新版本，
	// 由 PROCUREMENT 显式换锁（或产品决定解锁接口开放后显式解锁，见 CLAUDE.md 遗留 6d-3-①）。
	ErrLockedSupplierInvalid = errors.New("手动锁定的主供应商已失效，需人工处理（不会自动降级/自动解锁）")
)

// ---- 装配输入 ----

// QuoteComponentInput 是一个报价组件的原始输入。
type QuoteComponentInput struct {
	ComponentType string
	// UnitPrice 供应商折算后单价（quote_component.unit_price：倍率模式=官方价×倍率，或绝对价）。
	UnitPrice decimal.Decimal
	// Multiplier 倍率（可空；仅写 calc_snapshot，不参与计算——交接 §4）。
	Multiplier *decimal.Decimal
}

// QuoteInput 是一家供应商在某 SKU 上的当前 EFFECTIVE 报价。
type QuoteInput struct {
	SupplierID   int64
	QuoteSheetID int64
	QuoteVersion int
	ValidFrom    time.Time
	Currency     string // quote_item.currency（交接 §6-5：模型币种，不从供应商/参数推）
	Constraints  map[string]any
	// EffectiveRanges 该供应商在该 SKU 上的全部生效区间（EFFECTIVE + EXPIRED，
	// 一张单覆盖多 SKU，仓储必须按本 SKU 过滤后再给——否则 sheet 19 这类混单会串数据）。
	// 6d 稳定性起算的输入，StabilitySince 负责断档聚合。
	EffectiveRanges []EffectiveRange
	Components      []QuoteComponentInput
}

// SupplierStatus 是参与计算的供应商的冻结状态（§10-6：计算时排除，不做状态机联动）。
type SupplierStatus struct {
	SupplierID   int64
	QualStatus   string // FROZEN 排除
	SettleStatus string // FROZEN 排除
	Status       string // INACTIVE 排除
}

// RecalcInput 是一个 SKU 一次重算的全部输入（全供应商视图——主供应商选择必须看全集）。
type RecalcInput struct {
	SKUID    int64
	SKUCode  string
	Currency string           // model_sku 币种；与报价行不一致时以报价行断言失败（数据事故）
	Quotes   []QuoteInput     // 该 SKU 全部当前 EFFECTIVE 报价（多供应商）
	Params   []Param          // 三级参数行（ResolveParams 输入）
	Statuses []SupplierStatus // 涉及供应商的冻结状态
	// OfficialVerNo 当前官方价版本号（price_version.is_current，可空=无官方价）。
	OfficialVerNo *int
}

// ---- 计算结果 ----

// SupplierCalcResult 是一家供应商的计算结果（排序/快照用）。
type SupplierCalcResult struct {
	SupplierID     int64
	QuoteSheetID   int64
	QuoteVersion   int
	ValidFrom      time.Time
	Components     []Component // 已算好 unit_cost
	RepresentBasis string
	RepresentCost  decimal.Decimal
	RepresentRaw   decimal.Decimal // 代表组件供应商原始价（price_raw）
	Scores         FactorScores
	ParamsScope    string
	LossRate       decimal.Decimal
	ChannelRate    decimal.Decimal
	TaxInclusive   bool
	WithholdingTax decimal.Decimal
	// ---- 四因子原始输入（评分时拎进 FactorInput；快照 scores 同源，不可追溯字段不进这里） ----
	StabilitySince *time.Time // StabilitySince(EffectiveRanges) 的聚合结果；nil = 无有效历史
	RPM            *int64     // constraints_.rpm（缺失为 nil，评分走兜底）
	TPM            *int64     // constraints_.tpm（缺失为 nil）
	Compatibility  string     // constraints_.compatibility 的归一字符串（bool/数字归一成 "true"/"false"/"0"）
}

// SkuCalcResult 是一个 SKU 的完整计算结果（CalcSKU 纯函数输出）。
type SkuCalcResult struct {
	SKUID             int64
	SKUCode           string
	Currency          string
	Primary           SupplierCalcResult
	BackupSequence    []int64 // 其余供应商按四因子 total 降序（与主选择同一排序，6d 口径）
	ExcludedSuppliers []int64 // 冻结排除（§10-6，写 calc_snapshot.excluded_suppliers）
	All               []SupplierCalcResult
	OfficialVerNo     *int // 当前官方价版本（calc_snapshot.official_price_version_no）
}

// RecalcOutcome 是单个 SKU 重算的最终结果。
type RecalcOutcome struct {
	SKUID     int64
	SKUCode   string
	Status    string // "CREATED" / "UNCHANGED" / "NO_QUOTE"
	Version   int
	Unchanged bool
	// BaselineID 新版本 cost_baseline.id（6d-3：锁定审计的 target_id 用——
	// 锁 handler 不应二次反查，产生方直接回填最可靠）。未产新版本时为 0。
	BaselineID int64
}

// ---- Store 接口（GORM 实现见 internal/repo/cost_*.go） ----

// Store 是成本域的仓储接口。实现方必须用 txOf(ctx)（幂等中间件事务不被绕过——红线 5）。
type Store interface {
	// ListRecalcTargets 把 COST_RECALC payload 展开成 SKU 集合。
	// 不依赖 payload 里 quote_sheet_id 当前是否仍 EFFECTIVE（交接 §6-3）：
	// 该 sheet 的 sku_id 全集 ∪ 该供应商当前 EFFECTIVE 报价的 sku_id，去重升序。
	ListRecalcTargets(ctx context.Context, supplierID, quoteSheetID int64) ([]int64, error)
	// LoadRecalcInput 装配一个 SKU 的重算输入（只读，多供应商全集）。
	LoadRecalcInput(ctx context.Context, skuID int64) (*RecalcInput, error)
	// LoadCurrentBaselineForUpdate 读当前版本（含组件），并 SELECT ... FOR UPDATE 串行化并发重算。
	// 无当前版本 → (nil, nil)——此时并发兜底交给 uk_cost_current(23505)。
	LoadCurrentBaselineForUpdate(ctx context.Context, skuID int64) (*Baseline, error)
	// ApplyNewVersion 单事务：旧版本 is_current=false + valid_to=eventTime；
	// 新版本 INSERT（version=prev+1）+ cost_component 批量 INSERT +
	// audit_log(COST_BASELINE_RECALC) + event_outbox(cost.baseline.changed)。
	// 撞约束（23505/23P01）→ ErrVersionConflict。
	ApplyNewVersion(ctx context.Context, p ApplyParams) (newID int64, newVersion int, err error)
}

// ApplyParams 是 ApplyNewVersion 的参数。
type ApplyParams struct {
	SKUID           int64
	HasPrevious     bool
	PreviousVersion int
	EventTime       time.Time
	Baseline        *Baseline // 版本号由 repo 置 PreviousVersion+1
	BackupSequence  []int64
	CalcSnapshot    map[string]any
	Identity        JobIdentity
	RequestID       string
}

// JobIdentity 是任务触发身份（audit source_type，红线 10：系统触发必须写来源）。
type JobIdentity struct {
	OperatorID   int64
	OperatorRole string
	SourceType   string // WORKER / CRON / AGENT
	RequestID    string
}

// ---- 任务队列（6b-3 消费者） ----

// MaxRetry 是 COST_RECALC 的最大重试次数（含首次执行）：
// retry_count 达到 MaxRetry 即置 DEAD 并写告警，不再重试（06-cost §9）。
const MaxRetry = 3

// TaskPayload 是 task_job.payload 的解析结果（§9 四种触发 + 5d 的 effective_time）。
type TaskPayload struct {
	SupplierID    int64      `json:"supplier_id"`
	QuoteSheetID  int64      `json:"quote_sheet_id"`
	Reason        string     `json:"reason"`
	EffectiveTime *time.Time `json:"effective_time,omitempty"`
	// SKUID 6d-2 新增：参数变更触发重算时**直接指 SKU**（不需要 supplier/sheet 维度）。
	// 指针区分「缺省=走 supplier/sheet 展开」与「有值=直算该 SKU」——零值 int64 在 JSON 里
	// 无法表达「未传」语义（sku_id=0 也是合法但不该出现的值，用 nil 防呆）。
	SKUID *int64 `json:"sku_id,omitempty"`
}

// TaskJob 是 task_job 行的领域视图。
type TaskJob struct {
	ID         int64
	JobType    string
	Payload    TaskPayload
	Status     string
	RetryCount int
}

// ErrTaskJobNotFound 任务行不存在（并发已被复位/清理）。
var ErrTaskJobNotFound = errors.New("task_job 不存在")

// TaskStore 是任务队列的仓储接口（GORM 实现见 internal/repo/cost_task.go）。
// 纪律：认领/推进只动 task_job 自己的行；业务写（基线版本切换）在 domain 层
// 另行开事务——两者必须分离，业务回滚一拖，RUNNING 就成孤儿（6b-3 复核注意点 1）。
type TaskStore interface {
	// ResetStaleRunning 启动复位：COST_RECALC 的 RUNNING 残留（强杀/超时强退）放回
	// PENDING 且 retry_count+1（复位本身算一次失败，计入退避）。返回复位数。
	ResetStaleRunning(ctx context.Context, jobType string) (int64, error)
	// Claim 条件更新认领 PENDING→RUNNING（§9 第一道防线）；ok=false = 已被别的消费者拿走。
	Claim(ctx context.Context, taskID int64, now time.Time) (ok bool, err error)
	// LoadTask 读任务行 + 解 payload。不存在 → ErrTaskJobNotFound。
	LoadTask(ctx context.Context, taskID int64) (*TaskJob, error)
	// MarkDone 任务完成（RUNNING→DONE，清 last_error）。
	MarkDone(ctx context.Context, taskID int64, now time.Time) error
	// MarkFailed 置 FAILED + retry_count+1 + 退避 next_run_at；
	// 超限置 DEAD 并原子写 alert(COST_RECALC_DEAD, CRITICAL)。
	MarkFailed(ctx context.Context, taskID int64, cause string, now time.Time) error
}

// Backoff 指数退避：retry_count=1 → 1min，=2 → 5min（1min × 5^(n-1)，6b-3 裁决）。
// n<=0 按 1 计（防御）。
func Backoff(retryCount int) time.Duration {
	if retryCount <= 1 {
		return time.Minute
	}
	d := time.Minute
	for i := 1; i < retryCount; i++ {
		d *= 5
	}
	return d
}

// ---- Service ----

// Service 是成本域服务（无状态，可并发使用）。
type Service struct {
	store     Store
	taskStore TaskStore
	log       *zap.Logger
}

// NewService 构造成本服务。taskStore 为 6b-3 消费者依赖（可为 nil——不用 ConsumeTask 时）；
// log 为 nil 时用 zap.NewNop()。
func NewService(store Store, taskStore TaskStore, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{store: store, taskStore: taskStore, log: log}
}

// CalcSKU 计算一个 SKU 的成本（纯计算，不落库）。
// 冻结供应商在排序前剔除并记录 excluded_suppliers。无任何参与报价 → ErrNoQuoteSKU。
func (s *Service) CalcSKU(input *RecalcInput, now time.Time) (*SkuCalcResult, error) {
	if input == nil || len(input.Quotes) == 0 {
		return nil, ErrNoQuoteSKU
	}
	frozen := make(map[int64]bool, len(input.Statuses))
	for _, st := range input.Statuses {
		if st.QualStatus == "FROZEN" || st.SettleStatus == "FROZEN" || st.Status == "INACTIVE" {
			frozen[st.SupplierID] = true
		}
	}

	results := make([]SupplierCalcResult, 0, len(input.Quotes))
	excluded := make([]int64, 0)
	for _, q := range input.Quotes {
		if frozen[q.SupplierID] {
			excluded = append(excluded, q.SupplierID)
			continue
		}
		rp, ok := ResolveParams(input.Params, input.SKUID, q.SupplierID)
		if !ok {
			// 三级全缺 = 编程错误/迁移缺失（000016 已种 GLOBAL），panic 不静默兜底（交接 §6-4）
			panic(fmt.Sprintf("cost_param 三级参数全缺：sku_id=%d supplier_id=%d", input.SKUID, q.SupplierID))
		}
		if q.Currency != input.Currency {
			return nil, fmt.Errorf("报价币种 %s 与模型币种 %s 不一致（supplier=%d sheet=%d）",
				q.Currency, input.Currency, q.SupplierID, q.QuoteSheetID)
		}
		comps := make([]Component, 0, len(q.Components))
		for _, qc := range q.Components {
			comps = append(comps, Component{
				ComponentType: qc.ComponentType,
				UnitCost:      UnitCost(qc.UnitPrice, rp.LossRate, rp.ChannelRate),
				SupplierCost:  qc.UnitPrice,
			})
		}
		basis, ok := RepresentativeComponent(comps)
		if !ok {
			continue // 空组件行（上游校验漏网）——跳过该供应商，不 panic
		}
		var repCost, repRaw decimal.Decimal
		for _, c := range comps {
			if c.ComponentType == basis {
				repCost, repRaw = c.UnitCost, c.SupplierCost
				break
			}
		}
		results = append(results, SupplierCalcResult{
			SupplierID: q.SupplierID, QuoteSheetID: q.QuoteSheetID, QuoteVersion: q.QuoteVersion,
			ValidFrom: q.ValidFrom, Components: comps,
			RepresentBasis: basis, RepresentCost: repCost, RepresentRaw: repRaw,
			StabilitySince: StabilitySince(q.EffectiveRanges),
			RPM:            constraintsInt64(q.Constraints, "rpm"),
			TPM:            constraintsInt64(q.Constraints, "tpm"),
			Compatibility:  constraintsCompat(q.Constraints, "compatibility"),
			ParamsScope:    rp.ParamsScope, LossRate: rp.LossRate, ChannelRate: rp.ChannelRate,
			TaxInclusive: rp.TaxInclusive, WithholdingTax: rp.WithholdingTax,
		})
	}
	if len(results) == 0 {
		return nil, ErrNoQuoteSKU
	}

	minCost := results[0].RepresentCost
	for _, r := range results[1:] {
		if r.RepresentCost.LessThan(minCost) {
			minCost = r.RepresentCost
		}
	}
	inputs := make([]FactorInput, 0, len(results))
	for _, r := range results {
		inputs = append(inputs, FactorInput{
			SupplierID: r.SupplierID, UnitCost: r.RepresentCost,
			FirstEffectiveFrom: r.StabilitySince,
			RPM:                r.RPM, TPM: r.TPM, Compatibility: r.Compatibility,
		})
	}
	// now 显式透传：补录/回溯任务（RETRO effective_time）不能用函数内 time.Now()。
	scores := ScoreSuppliers(inputs, minCost, now)
	scoreBySupplier := make(map[int64]FactorScores, len(scores))
	for _, sc := range scores {
		scoreBySupplier[sc.SupplierID] = sc
	}
	for i := range results {
		results[i].Scores = scoreBySupplier[results[i].SupplierID]
	}

	// 6d：主供应商 = 四因子总分最高者。平手三级规则（已裁决，保证确定性）：
	// Total 降序（decimal.GreaterThan，不转 float）→ RepresentCost 升序 → SupplierID 升序。
	sortSupplierResults(results)
	backup := make([]int64, 0, len(results)-1)
	for _, r := range results[1:] {
		backup = append(backup, r.SupplierID)
	}
	sortInt64s(excluded)
	return &SkuCalcResult{
		SKUID: input.SKUID, SKUCode: input.SKUCode, Currency: input.Currency,
		Primary: results[0], BackupSequence: backup, ExcludedSuppliers: excluded,
		All: results, OfficialVerNo: input.OfficialVerNo,
	}, nil
}

// LoadRecalcInputForCompare 是 6d-4 比价/议价服务专用的装配入口。
// CompareService 持 *Service 复用 CalcSKU 纯函数，但 s.store 是私有字段——
// 通过本方法暴露只读装配，绝不导出 store 字段本身（破坏封装）。
// 内部与 RecalcSKU 同一条 LoadRecalcInput 调用，口径唯一。
func (s *Service) LoadRecalcInputForCompare(ctx context.Context, skuID int64) (*RecalcInput, error) {
	return s.store.LoadRecalcInput(ctx, skuID)
}

// RecalcSKU 一个 SKU 的完整重算：装配 → 计算 → 值未变判定 → 必要时不可变版本切换。
// 并发安全：LoadCurrentBaselineForUpdate 行锁 + uk_cost_current/ex_cost_no_overlap 双兜底。
//
// 参数 manualLockSupplierID（6d-3）：
//   - nil  → 无本次锁定 override（正常任务消费都传 nil）；
//   - 非 nil → 本条由 MANUAL_LOCK 入口触发，next.primary 强制为该值。
//     LockService.LockPrimary 在调用前已完成陷阱 4 的四项校验（存在 / 该 SKU 有 EFFECTIVE 报价 /
//     未冻结 / 供应商品质正常），这里不再重复查库，直接用。
//
// 手动锁定的单向 sticky 语义（6d-3 提示词陷阱 1/3 裁决）：
//
//	任何一次**非** MANUAL_LOCK 的触发：
//	  - 前一个版本 locked_manual=true 且被锁供应商仍有效 → next.primary 维持 prev.PrimarySupplierID、
//	    next.LockedManual=true（不动摇），calc_snapshot.primary_selection_rule="manual-lock"；
//	  - 前一个版本 locked_manual=true 且被锁供应商已失效 → 返回 ErrLockedSupplierInvalid，
//	    不产新版本（绝不静默降级/自动解锁）；
//	  - 前一个版本未锁 → next.LockedManual=false、selection_rule="four-factor"。
//	MANUAL_LOCK 触发：
//	  - next.primary = *manualLockSupplierID、next.LockedManual=true、selection_rule="manual-lock"。
func (s *Service) RecalcSKU(ctx context.Context, skuID int64, reason, requestID string, eventTime time.Time, ident JobIdentity, manualLockSupplierID *int64) (*RecalcOutcome, error) {
	if !ValidChangeReason(reason) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidChangeReason, reason)
	}
	if reason != ReasonManualLock && manualLockSupplierID != nil {
		return nil, fmt.Errorf("%w: manualLockSupplierID 仅在 MANUAL_LOCK 触发时允许传入", ErrInvalidChangeReason)
	}
	input, err := s.store.LoadRecalcInput(ctx, skuID)
	if err != nil {
		return nil, fmt.Errorf("load recalc input sku=%d: %w", skuID, err)
	}
	outcome := &RecalcOutcome{SKUID: skuID, SKUCode: input.SKUCode}

	// 锁先取：NO_QUOTE 分支需要知道「上一版是否锁定」才能决定返回 NO_QUOTE 还是
	// ErrLockedSupplierInvalid（锁定状态下报价断档绝不是静默跳过——那是"悄悄丢锁"）。
	// FOR UPDATE 行锁提前到计算前持有（秒级纯计算，无外部等待，无死锁新增面）。
	prev, err := s.store.LoadCurrentBaselineForUpdate(ctx, skuID)
	if err != nil {
		return nil, fmt.Errorf("load current baseline sku=%d: %w", skuID, err)
	}

	calc, err := s.CalcSKU(input, eventTime)
	if errors.Is(err, ErrNoQuoteSKU) {
		// 锁定状态下报价断档 = 被锁供应商失效（陷阱 3 无效分支）——明确报错，不产新版本。
		if prev != nil && prev.LockedManual {
			return nil, fmt.Errorf("%w：sku=%d 已无 EFFECTIVE 报价（locked supplier=%d）",
				ErrLockedSupplierInvalid, skuID, prev.PrimarySupplierID)
		}
		outcome.Status = "NO_QUOTE"
		return outcome, nil
	}
	if err != nil {
		return nil, err
	}

	// ---- 主供应商与锁定状态裁决（sticky 语义的总开关） ----
	// primaryResult = 新版本实际采用的供应商计算结果（锁定场景下不一定是 calc.Primary 的算法冠军）。
	primaryResult := calc.Primary
	selectionRule := "four-factor"
	manualLock := false
	switch {
	case reason == ReasonManualLock && manualLockSupplierID != nil:
		// 显式换锁：LockService 已完成陷阱 4 的四项校验（存在 / 该 SKU 有 EFFECTIVE 报价 /
		// 未冻结），被锁供应商必在 calc.All 里——找不到是编程错误，不静默兜底。
		idx := -1
		for i := range calc.All {
			if calc.All[i].SupplierID == *manualLockSupplierID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, fmt.Errorf("%w：sku=%d 锁定目标 supplier=%d 不在参与计算集合（校验漏网，属编程错误）",
				ErrLockedSupplierInvalid, skuID, *manualLockSupplierID)
		}
		primaryResult = calc.All[idx]
		selectionRule = "manual-lock"
		manualLock = true
	case prev != nil && prev.LockedManual:
		// 非锁定触发的后续重算：锁必须跨触发延续（陷阱 1：不能悄悄解锁）。
		idx := -1
		for i := range calc.All {
			if calc.All[i].SupplierID == prev.PrimarySupplierID {
				idx = i
				break
			}
		}
		if idx < 0 {
			// 不在参与评分集合 = 失效（冻结/INACTIVE/结算冻结/行已删除）。
			// 陷阱 3 的「无效 → 报错」分支：不自动降级、不自动解锁、不产新版本。
			return nil, fmt.Errorf("%w：sku=%d 被锁定供应商 supplier=%d 当前已失效（冻结或该 SKU 无 EFFECTIVE 报价）",
				ErrLockedSupplierInvalid, skuID, prev.PrimarySupplierID)
		}
		// 有效 → 维持锁定：primary 不动。组件取被锁供应商的逐组件口径——参数变更等场景
		// 下成本值可跟随该供应商重算（锁的是供应商选择，不是冻结成本数字本身）。
		primaryResult = calc.All[idx]
		selectionRule = "manual-lock"
		manualLock = true
	}
	next := &Baseline{
		Currency:          calc.Currency,
		PrimarySupplierID: primaryResult.SupplierID,
		LossRate:          primaryResult.LossRate,
		ChannelRate:       primaryResult.ChannelRate,
		// 陷阱 1 修复：绝不硬编码 false——未锁保持未锁、锁过保持锁过、显式换锁置真。
		LockedManual:   manualLock,
		ChangeReason:   reason,
		Components:     primaryResult.Components,
		FormulaVersion: FormulaVersion,
	}
	if prev != nil && Unchanged(prev, next) {
		outcome.Status = "UNCHANGED"
		outcome.Unchanged = true
		outcome.Version = prev.Version
		return outcome, nil
	}

	snapshot := buildSnapshot(calc, reason, selectionRule, primaryResult)
	hasPrev := prev != nil
	prevVer := 0
	if hasPrev {
		prevVer = prev.Version
	}
	newBaselineID, newVer, err := s.store.ApplyNewVersion(ctx, ApplyParams{
		SKUID: skuID, HasPrevious: hasPrev, PreviousVersion: prevVer, EventTime: eventTime,
		Baseline: next, BackupSequence: calc.BackupSequence, CalcSnapshot: snapshot,
		Identity: ident, RequestID: requestID,
	})
	if err != nil {
		return nil, err // ErrVersionConflict 原样上抛
	}
	outcome.Status = "CREATED"
	outcome.Version = newVer
	outcome.BaselineID = newBaselineID
	return outcome, nil
}

// RecalcFromTask 消费一条 COST_RECALC 的编排入口（6b-3 消费者调用）：
// 展开 SKU 集合 → 逐个 RecalcSKU → 汇总。单 SKU 失败不中断其他 SKU（各自独立版本事务），
// 返回首个错误供消费者决定任务级重试。
func (s *Service) RecalcFromTask(ctx context.Context, supplierID, quoteSheetID int64, reason, requestID string, eventTime time.Time, ident JobIdentity) ([]RecalcOutcome, error) {
	skuIDs, err := s.store.ListRecalcTargets(ctx, supplierID, quoteSheetID)
	if err != nil {
		return nil, fmt.Errorf("list recalc targets supplier=%d sheet=%d: %w", supplierID, quoteSheetID, err)
	}
	outcomes := make([]RecalcOutcome, 0, len(skuIDs))
	var firstErr error
	for _, skuID := range skuIDs {
		// 任务消费路径不存在「本次锁定 override」——nil 即正常 sticky 语义（锁过维持、未锁走四因子）。
		o, err := s.RecalcSKU(ctx, skuID, reason, requestID, eventTime, ident, nil)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		outcomes = append(outcomes, *o)
	}
	return outcomes, firstErr
}

// ResetStaleRunning 启动复位：COST_RECALC 的 RUNNING 残留放回 PENDING 且 retry_count+1。
// 幂等，可反复调用；供 6b-3 worker 每个 tick 前执行。
func (s *Service) ResetStaleRunning(ctx context.Context) (int64, error) {
	if s.taskStore == nil {
		return 0, errors.New("cost.Service 未接 TaskStore")
	}
	return s.taskStore.ResetStaleRunning(ctx, "COST_RECALC")
}

// ConsumeTask 消费一条 COST_RECALC（6b-3 worker 入口）。
//
// 流程（每步的状态推进与业务写严格分离——业务回滚不能把 RUNNING 拖成孤儿）：
//  1. Claim PENDING→RUNNING；ok=false（被别的消费者拿走/已推进）→ 返回 (false, nil) 不报错。
//  2. RecalcFromTask 业务重算（内部每 SKU 独立版本事务；effective_time 作 event_time，
//     绝不向更早回灌——§9）。单 SKU 失败记首个错误，不中断其他 SKU。
//  3. 无错误 → MarkDone；NO_QUOTE 视为 DONE（6b-2 复核裁决：报价链断档是业务事实，
//     重试不改变结果——warn 日志保留最后已知基线，符合"历史时点成本可追溯"）。
//  4. 有错误 → MarkFailed（库内 retry_count+1 + 退避 next_run_at，超限置 DEAD 并写
//     COST_RECALC_DEAD/CRITICAL 告警）。last_error 必须带约束名（uk_cost_current /
//     ex_cost_no_overlap 等），供运维一眼分流预期冲突与真异常。
//
// eventTime 由消费者传入（通常是 now；RETRO 任务的 payload.effective_time 优先）。
func (s *Service) ConsumeTask(ctx context.Context, taskID int64, eventTime time.Time, ident JobIdentity) (claimed bool, err error) {
	if s.taskStore == nil {
		return false, errors.New("cost.Service 未接 TaskStore，无法消费任务")
	}
	ok, err := s.taskStore.Claim(ctx, taskID, eventTime)
	if err != nil {
		return false, fmt.Errorf("claim task=%d: %w", taskID, err)
	}
	if !ok {
		return false, nil
	}
	task, err := s.taskStore.LoadTask(ctx, taskID)
	if err != nil {
		return true, fmt.Errorf("load task=%d: %w", taskID, err)
	}
	if task.Payload.Reason == "" {
		task.Payload.Reason = ReasonQuoteEffective
	}
	effTime := eventTime
	if task.Payload.EffectiveTime != nil {
		effTime = *task.Payload.EffectiveTime
	}

	var outcomes []RecalcOutcome
	var firstErr error
	if task.Payload.SKUID != nil {
		// 6d-2 直算分支：参数变更重算只指定 SKU，不依赖 supplier/sheet 维度。
		// 独立一个 SKU 就是一个独立版本事务（RecalcSKU 内部已做并发防护）。
		// 6d-3：任务消费不传 manualLockSupplierID（nil）——锁定状态靠 prev.LockedManual 维持。
		o, err := s.RecalcSKU(ctx, *task.Payload.SKUID, task.Payload.Reason, ident.RequestID, effTime, ident, nil)
		if err == nil && o != nil {
			outcomes = []RecalcOutcome{*o}
		}
		firstErr = err
	} else {
		// 旧路径保持原样（QUOTES/RETRO/EXPIRE_REMOVE 三种场景仍按 supplier+sheet 展开）。
		outcomes, firstErr = s.RecalcFromTask(ctx, task.Payload.SupplierID, task.Payload.QuoteSheetID,
			task.Payload.Reason, ident.RequestID, effTime, ident)
	}
	if firstErr == nil {
		for _, o := range outcomes {
			if o.Status == "NO_QUOTE" {
				s.log.Warn("cost recalc NO_QUOTE, keep last known baseline",
					zap.Int64("task_id", taskID), zap.Int64("sku_id", o.SKUID),
					zap.Int64("supplier_id", task.Payload.SupplierID),
					zap.String("reason", task.Payload.Reason))
			}
		}
		if err := s.taskStore.MarkDone(ctx, taskID, eventTime); err != nil {
			return true, fmt.Errorf("mark done task=%d: %w", taskID, err)
		}
		return true, nil
	}
	if ferr := s.taskStore.MarkFailed(ctx, taskID, firstErr.Error(), eventTime); ferr != nil {
		return true, fmt.Errorf("mark failed task=%d: %w (business error was: %s)", taskID, ferr, firstErr.Error())
	}
	return true, nil
}

// sortSupplierResults 主/备选统一的排序（6d §6.5 平手三级规则）：
// Total 降序（decimal.GreaterThan，不转 float 比）→ RepresentCost 升序 → SupplierID 升序。
func sortSupplierResults(rs []SupplierCalcResult) {
	sort.Slice(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if a.Scores.Total.GreaterThan(b.Scores.Total) {
			return true
		}
		if b.Scores.Total.GreaterThan(a.Scores.Total) {
			return false
		}
		if !a.RepresentCost.Equal(b.RepresentCost) {
			return a.RepresentCost.LessThan(b.RepresentCost)
		}
		return a.SupplierID < b.SupplierID
	})
}

// sortInt64s 升序排序（excluded_suppliers 等确定序输出用）。
func sortInt64s(v []int64) { sort.Slice(v, func(i, j int) bool { return v[i] < v[j] }) }

// ---- constraints_ 取值（JSONB → 评分输入） ----
//
// Constraints 是 map[string]any：encoding/json 默认把数字解成 float64，
// GORM jsonb 驱动可能给 json.Number。纪律：无法解析就当缺失（走 0.5 兜底），
// 绝不 panic、绝不静默传 0（0 在配额公式里会真的参与归一，把分压成 0）。

// constraintsInt64 取整数配额（rpm/tpm）。支持 float64 / json.Number / 字符串数字
// （含 "3e3" 这类浮点写法）；非整数值（如 3000.5）判缺失——配额语义不允许小数。
func constraintsInt64(c map[string]any, key string) *int64 {
	if c == nil {
		return nil
	}
	v, ok := c[key]
	if !ok || v == nil {
		return nil
	}
	var f float64
	switch t := v.(type) {
	case float64:
		f = t
	case json.Number:
		parsed, err := t.Float64()
		if err != nil {
			return nil
		}
		f = parsed
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil
		}
		parsed, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil
		}
		f = parsed
	default:
		return nil // bool / 数组 / 对象等：无法解析，走缺失兜底
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f {
		return nil
	}
	n := int64(f)
	return &n
}

// constraintsCompat 取兼容字段并归一成字符串语义：
//   - 字符串 → 原样返回（三级判定在 CompatibilityScore，空串兜底 0.5）；
//   - bool → "true" / "false"（"false" 在显式不兼容集合内，判 0）；
//   - 其他类型（数字 / 对象 / 数组 / nil）→ ""（无法判定，走 0.5 未声明兜底，不报错）。
func constraintsCompat(c map[string]any, key string) string {
	if c == nil {
		return ""
	}
	v, ok := c[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// buildSnapshot 组装 calc_snapshot（§10-9 字段全集：formula_version /
// excluded_suppliers / params_scope / 报价与官方价版本号 / 四因子原始值+归一化值）。
//
// 6d-3 起 primary 与 rule 由调用方裁决后显式传入（陷阱 5）：
//   - 四因子正常路径：primary=calc.Primary（算法冠军）、rule="four-factor"；
//   - 手动锁定/维持锁定：primary=被锁供应商的 SupplierCalcResult、rule="manual-lock"——
//     算法在此刻根本没有参与 primary 的产生，绝不能把 rule 写成 "four-factor"
//     让查询者误以为这是算法结论。
//
// 注意：scores[] 仍完整记录**全部参与计算**供应商的四因子得分（含被覆盖的算法冠军）——
// 快照的价值正在于「算法当时怎么想 + 人最后怎么定」两可对照，删了反而丢可追溯性。
func buildSnapshot(calc *SkuCalcResult, reason, selectionRule string, primary SupplierCalcResult) map[string]any {
	scores := make([]map[string]any, 0, len(calc.All))
	for _, r := range calc.All {
		entry := map[string]any{
			"supplier_id":      r.SupplierID,
			"quote_sheet_id":   r.QuoteSheetID,
			"quote_version_no": r.QuoteVersion,
			"quote_valid_from": r.ValidFrom.UTC().Format(time.RFC3339),
			"supplier_cost":    r.RepresentRaw.String(), // 供应商原始报价（quote_component.unit_price）——只记录不参与计算
			"price":            r.Scores.PriceNormalized.Round(6).String(),
			"stability":        r.Scores.StabilityNormalized.Round(6).String(),
			"quota":            r.Scores.QuotaNormalized.Round(6).String(),
			"compatibility":    r.Scores.CompatNormalized.Round(6).String(),
			"total":            r.Scores.Total.Round(6).String(),
			"params_scope":     r.ParamsScope,
			"loss_rate":        r.LossRate.String(),
			"channel_rate":     r.ChannelRate.String(),
		}
		// 稳定性起算日：nil 必须写 JSON null，不要零值时间戳 "0001-01-01T00:00:00Z"。
		// 6d 真实化后 stability/quota/compatibility 统一 Round(6) 字符串（与 price/total 同口径）。
		if r.Scores.StabilitySince != nil {
			entry["stability_since"] = r.Scores.StabilitySince.UTC().Format(time.RFC3339)
		} else {
			entry["stability_since"] = nil
		}
		scores = append(scores, entry)
	}
	snapshot := map[string]any{
		"formula_version":     FormulaVersion,
		"change_reason":       reason,
		"primary_supplier_id": primary.SupplierID,
		"quote_sheet_id":      primary.QuoteSheetID,
		"quote_version_no":    primary.QuoteVersion,
		"quote_valid_from":    primary.ValidFrom.UTC().Format(time.RFC3339),
		"params":              map[string]any{"loss_rate": primary.LossRate.String(), "channel_rate": primary.ChannelRate.String(), "tax_inclusive": primary.TaxInclusive, "withholding_tax": primary.WithholdingTax.String()},
		"params_scope":        primary.ParamsScope,
		"scores":              scores,
		"excluded_suppliers":  calc.ExcludedSuppliers,
		// 6d-3：rule 由调用方裁决（"four-factor" / "manual-lock"），绝不硬编码——
		// 锁定场景下算法没有参与 primary 产生，写成 four-factor 会污染读侧认知。
		"primary_selection_rule": selectionRule,
	}
	if calc.OfficialVerNo != nil {
		snapshot["official_price_version_no"] = *calc.OfficialVerNo
	}
	return snapshot
}
