// Package cost 的 compare_service.go：§4 比价（多供应商对照 + 成本区间 + 趋势 + 市场最低）
// 与 §5 议价机会（bargain / single_point）的只读编排。
//
// 两个关键裁决（6d-4 提示词陷阱 1/2/5）：
//
// 【陷阱 1：compare 绝不从 calc_snapshot 读】
// buildSnapshot（service.go）的 scores[] 只有四因子归一化值与 supplier_cost，
// 没有 constraints 原始值 / 逐组件完全成本 / 代表组件 unit_cost。
// 契约 §4:144 明说「range：实时计算，不落库」——字面含义就是对**当前 EFFECTIVE 报价**
// 实时算一遍，而不是回放最近一次重算的快照（快照受 task_job 消费延迟影响，最多落后 1 分钟）。
// 因此本服务走 LoadRecalcInputForCompare + CalcSKU 实时计算——报价生效后立刻可见，
// 且不需要动 buildSnapshot（不影响历史版本兼容与存量测试）。
//
// 【陷阱 2：复用 CalcSKU 纯函数，禁止复制一份】
// CalcSKU 挂在 *Service 上但内部是纯计算（只用 input，不触 s.store 写路径）。
// CompareService 直接持有 *Service：通过 LoadRecalcInputForCompare 装配输入（读），
// 再调 CalcSKU（纯函数）——"读侧"定位由行为决定，不由依赖关系决定。
// 绝不允许把 CalcSKU 的逻辑复制到本文件（双份实现 = 漂移炸弹，6d-3 同款纪律）。
//
// 【陷阱 5：bargain 的 N 次重算是有意为之】
// market_best 只能从 CalcSKU 实时算出（快照里没有 unit_cost）。
// 当前 SKU 数量极小（真库个位数），遍历所有有基线的 SKU 逐算不是瓶颈；
// 数据总是最新的（不受任务消费延迟影响）。SKU 量上到性能成为问题再加缓存/批处理——
// 已登记 CLAUDE.md 遗留 6d-4-②，现在不过度设计。
package cost

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

// ---- §4 比价 ----

// CompareComponentDTO 是比价视图的逐组件成本（§0.2 对偶：unit_cost 完全成本 / supplier_cost 原始价）。
type CompareComponentDTO struct {
	ComponentType string `json:"component_type"`
	UnitCost      string `json:"unit_cost"`     // 完全成本，StringFixed(8)
	SupplierCost  string `json:"supplier_cost"` // 供应商原始报价，StringFixed(8)
}

// CompareConstraintsDTO 是供应商 constraints_ 的原始值（快照不入这里——实时计算才有）。
type CompareConstraintsDTO struct {
	RPM           *int64 `json:"rpm"`
	TPM           *int64 `json:"tpm"`
	Compatibility string `json:"compatibility"` // 缺失时为 ""（与评分口径一致，未声明走 0.5）
}

// CompareScoresDTO 是四因子归一化得分（Round(6)，与 calc_snapshot.scores 同一精度口径）。
type CompareScoresDTO struct {
	Price          decimal.Decimal `json:"price"`
	Stability      decimal.Decimal `json:"stability"`
	Quota          decimal.Decimal `json:"quota"`
	Compatibility  decimal.Decimal `json:"compatibility"`
	Total          decimal.Decimal `json:"total"`
	StabilitySince *string         `json:"stability_since"` // nil → JSON null，绝不写零值时间戳
}

// CompareSupplierDTO 是 §4 suppliers[] 的一家供应商。
type CompareSupplierDTO struct {
	SupplierID    int64                 `json:"supplier_id"`
	QuoteSheetID  int64                 `json:"quote_sheet_id"`
	QuoteVersion  int                   `json:"quote_version"`
	ValidFrom     string                `json:"valid_from"` // RFC3339 UTC
	Components    []CompareComponentDTO `json:"components"`
	Constraints   CompareConstraintsDTO `json:"constraints"`
	Scores        CompareScoresDTO      `json:"scores"`
	IsPrimary     bool                  `json:"is_primary"`
	IsBackup      bool                  `json:"is_backup"`
	UnitCost      string                `json:"unit_cost"`       // 代表组件完全成本，StringFixed(8)
	UnitCostBasis string                `json:"unit_cost_basis"` // 代表组件 component_type
}

// CompareRangeDTO 是 §4 range（实时计算，不落库）。
type CompareRangeDTO struct {
	CostMin      string `json:"cost_min"`
	CostMax      string `json:"cost_max"`
	CostWeighted string `json:"cost_weighted"` // 四因子 total 加权；Σtotal=0 退化为算术平均
}

// CompareTrendPointDTO 是 §4 trend[] 的一点（SQL 按天聚合已在 repo 收口，
// 这里序列化脱 decimal——buildSnapshot/列表同款 StringFixed(8) 纪律）。
type CompareTrendPointDTO struct {
	Date     string `json:"date"` // YYYY-MM-DD（Asia/Shanghai 日历日）
	UnitCost string `json:"unit_cost"`
	Version  int    `json:"version"`
}

// CompareResult 是 §4 接口 data 段。
type CompareResult struct {
	SKUID           int64                  `json:"sku_id"`
	SKUCode         string                 `json:"sku_code"`
	Currency        string                 `json:"currency"`
	Suppliers       []CompareSupplierDTO   `json:"suppliers"`
	Range           CompareRangeDTO        `json:"range"`
	Trend           []CompareTrendPointDTO `json:"trend"`
	MarketBest      string                 `json:"market_best"`       // 全市场代表组件完全成本最低价
	MarketBestBasis string                 `json:"market_best_basis"` // market_best 的代表组件口径标注
}

// ---- §5 议价机会 ----

// BargainItem 是 type=bargain 的一行：主供应商价格显著高于市场最低价。
type BargainItem struct {
	SKUID               int64  `json:"sku_id"`
	SKUCode             string `json:"sku_code"`
	PrimarySupplierID   int64  `json:"primary_supplier_id"`
	PrimarySupplierName string `json:"primary_supplier_name"`
	UnitCost            string `json:"unit_cost"`   // 主供应商代表组件完全成本
	MarketBest          string `json:"market_best"` // 全市场最低
	Deviation           string `json:"deviation"`   // (unit_cost − market_best) / market_best，Round(6)
	Threshold           string `json:"threshold"`   // quote_anomaly_mkt（透传便于前端展示与排障）
}

// SinglePointItem 是 type=single_point 的一行：只有 1 家有效报价。
type SinglePointItem struct {
	SKUID               int64  `json:"sku_id"`
	SKUCode             string `json:"sku_code"`
	PrimarySupplierID   int64  `json:"primary_supplier_id"`
	PrimarySupplierName string `json:"primary_supplier_name"`
	UnitCost            string `json:"unit_cost"`
	UnitCostBasis       string `json:"unit_cost_basis"`
	SupplierCount       int    `json:"supplier_count"`
}

// OpportunitiesResult 是 §5 接口 data 段（type 决定 list 元素形状；
// list 永不返回 null——空清单是合法事实，前端按 [] 渲染）。
type OpportunitiesResult struct {
	Type  string `json:"type"`
	List  any    `json:"list"`
	Total int    `json:"total"`
}

// Bargain / SinglePoint opportunity 类型常量（handler 校验同一来源）。
const (
	OpportunityTypeBargain     = "bargain"
	OpportunityTypeSinglePoint = "single_point"
)

// ValidOpportunityType 报告 type 参数是否合法（§5 只允许两个值，其他一律 400）。
func ValidOpportunityType(t string) bool {
	return t == OpportunityTypeBargain || t == OpportunityTypeSinglePoint
}

// normalizeDays 规整 days 查询参数（陷阱 7：容错不 400）：
// <=0 → 默认 90；>365 → 上限 365。查询参数容错，不打断前端。
func normalizeDays(days int) int {
	if days <= 0 {
		return 90
	}
	if days > 365 {
		return 365
	}
	return days
}

// ---- CompareService ----

// CompareService 是 §4/§5 的只读服务（无状态，可并发）。
// svc 仅用于：LoadRecalcInputForCompare（读装配）+ CalcSKU（纯函数）——绝不在此落库。
type CompareService struct {
	svc   *Service
	store BaselineReadStore
	now   func() time.Time // 测试可注入；nil → time.Now().UTC
}

// NewCompareService 构造比价/议价服务。svc 与 store 均必传（nil 属编程错误，调用即炸——
// 不静默兜底成"空结果"，那是把断线包装成业务事实）。
func NewCompareService(svc *Service, store BaselineReadStore) *CompareService {
	return &CompareService{svc: svc, store: store, now: func() time.Time { return time.Now().UTC() }}
}

// calcForSKU 装配输入并跑纯计算（Compare 与 bargain 共用同一入口）。
// SKU 无 EFFECTIVE 报价 → ErrNoQuoteSKU 原样上抛（调用方按"该 SKU 不参与"处理，不静默吞）。
func (s *CompareService) calcForSKU(ctx context.Context, skuID int64) (*SkuCalcResult, error) {
	input, err := s.svc.LoadRecalcInputForCompare(ctx, skuID)
	if err != nil {
		return nil, err
	}
	return s.svc.CalcSKU(input, s.now())
}

// Compare 实现 §4 比价。
// 返回 (result, found, err)：found=false = {sku} 解析不到，由 handler 回 404。
// 该 SKU 存在但无 EFFECTIVE 报价 → 同样 (nil, false, nil)：契约上"对比对象不存在"
// 与"SKU 不存在"对调用方是同一句"没有比价内容"（§4 没有为 NO_QUOTE 定专门语义，
// 404 与 §3 history 的 ResolveSKUID 失败语义一致，不自创 200-空壳）。
func (s *CompareService) Compare(ctx context.Context, sku string, days int) (*CompareResult, bool, error) {
	skuID, ok, err := s.store.ResolveSKUID(ctx, sku)
	if err != nil || !ok {
		return nil, ok, err
	}
	calc, err := s.calcForSKU(ctx, skuID)
	if errors.Is(err, ErrNoQuoteSKU) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}

	// suppliers[] 展开（顺序 = calc.All 的四因子排序序——主供应商在前，确定性输出）。
	suppliers := make([]CompareSupplierDTO, 0, len(calc.All))
	backupSet := make(map[int64]bool, len(calc.BackupSequence))
	for _, id := range calc.BackupSequence {
		backupSet[id] = true
	}
	rangeInput := make([]SupplierCost, 0, len(calc.All))
	for _, r := range calc.All {
		comps := make([]CompareComponentDTO, 0, len(r.Components))
		for _, c := range r.Components {
			comps = append(comps, CompareComponentDTO{
				ComponentType: c.ComponentType,
				UnitCost:      c.UnitCost.StringFixed(8),
				SupplierCost:  c.SupplierCost.StringFixed(8),
			})
		}
		var stabSince *string
		if r.Scores.StabilitySince != nil {
			str := r.Scores.StabilitySince.UTC().Format(time.RFC3339)
			stabSince = &str
		}
		suppliers = append(suppliers, CompareSupplierDTO{
			SupplierID: r.SupplierID, QuoteSheetID: r.QuoteSheetID,
			QuoteVersion: r.QuoteVersion, ValidFrom: r.ValidFrom.UTC().Format(time.RFC3339),
			Components: comps,
			Constraints: CompareConstraintsDTO{
				RPM: r.RPM, TPM: r.TPM, Compatibility: r.Compatibility,
			},
			Scores: CompareScoresDTO{
				Price: r.Scores.PriceNormalized.Round(6), Stability: r.Scores.StabilityNormalized.Round(6),
				Quota: r.Scores.QuotaNormalized.Round(6), Compatibility: r.Scores.CompatNormalized.Round(6),
				Total: r.Scores.Total.Round(6), StabilitySince: stabSince,
			},
			IsPrimary: r.SupplierID == calc.Primary.SupplierID,
			IsBackup:  backupSet[r.SupplierID],
			UnitCost:  r.RepresentCost.StringFixed(8), UnitCostBasis: r.RepresentBasis,
		})
		rangeInput = append(rangeInput, SupplierCost{
			SupplierID: r.SupplierID, UnitCost: r.RepresentCost, Total: r.Scores.Total,
		})
	}

	// range（陷阱 3：加权除零保护在 ComputeRange 内部，无需在此重复）。
	rg := ComputeRange(rangeInput)

	// market_best = 全市场最低价（与 cost_min 同值——当前"市场"就是这些 EFFECTIVE 报价全集）。
	// 调用 MarketBest 纯函数（不是复用 rg.CostMin）——让 market_best 的语义独立于 range
	// （变异验证 2 的挂接点：改 MarketBest 取 max → 这里的市场价立即错，测试抓回）。
	costs := make([]decimal.Decimal, 0, len(calc.All))
	for _, r := range calc.All {
		costs = append(costs, r.RepresentCost)
	}
	marketBest := MarketBest(costs)
	marketBestBasis := ""
	for _, r := range calc.All {
		if r.RepresentCost.Equal(marketBest) {
			marketBestBasis = r.RepresentBasis
			break
		}
	}

	// trend（SQL 按天聚合，Asia/Shanghai 日历日已由 repo 收口；不插值——那天没重算就没点）。
	days = normalizeDays(days)
	trendRows, err := s.store.ListBaselineTrend(ctx, skuID, days)
	if err != nil {
		return nil, true, err
	}
	trend := make([]CompareTrendPointDTO, 0, len(trendRows))
	for _, p := range trendRows {
		trend = append(trend, CompareTrendPointDTO{
			Date:     p.Date,
			UnitCost: p.UnitCost.StringFixed(8),
			Version:  p.Version,
		})
	}

	return &CompareResult{
		SKUID: calc.SKUID, SKUCode: calc.SKUCode, Currency: calc.Currency,
		Suppliers: suppliers,
		Range: CompareRangeDTO{
			CostMin:      rg.CostMin.StringFixed(8),
			CostMax:      rg.CostMax.StringFixed(8),
			CostWeighted: rg.CostWeighted.StringFixed(8),
		},
		Trend:           trend,
		MarketBest:      marketBest.StringFixed(8),
		MarketBestBasis: marketBestBasis,
	}, true, nil
}

// Opportunities 实现 §5。typ 已经 handler 校验（ValidOpportunityType），此处不再重复判。
//
// bargain（阈值 quote_anomaly_mkt，严格大于）：
//
//	遍历所有有基线的 SKU → 实时计算主供应商与市场最低的偏离度。
//	该 SKU 无 EFFECTIVE 报价（ErrNoQuoteSKU）→ 跳过（无市场无从议价），不报错拖垮整单。
//	阈值读 sys_config 失败 → 上抛错误（比价阈值的兜底是把"无阈值"当"全入选"或"全不选"，
//	两者都在静默吞事实——按 ReadService.LoadMinGrossMargin 同款纪律：读侧错误上抛 500）。
//
// 主供应商名从 ListBaselines 一次取回（避免逐 SKU 再查 suppliers）。
func (s *CompareService) Opportunities(ctx context.Context, typ string) (*OpportunitiesResult, error) {
	switch typ {
	case OpportunityTypeBargain:
		return s.opportunitiesBargain(ctx)
	case OpportunityTypeSinglePoint:
		return s.opportunitiesSinglePoint(ctx)
	default:
		// handler 已校验；走到这里是绕过 handler 的直接调用（编程错误），显式拒绝。
		return nil, ErrInvalidChangeReason // 复用"非法枚举"类错误，不自创新错误类型
	}
}

// opportunitiesBargain 实现 type=bargain。
//
// 【主供应商来源裁决（6d-4 实现陷阱）】
// bargain 的"主供应商"必须来自 cost_baseline 当前行（ListPrimarySupplierIDs），
// **不是** CalcSKU.Primary——若该 SKU 被人工锁定了主供应商（locked_manual=true，
// 见 6d-3），算法冠军是便宜那家，但被锁的贵那家才是真实主供应商。用 CalcSKU.Primary
// 会读到算法冠军，把"贵供应商被锁定"的场景漏掉（这正是 bargain 接口存在的核心场景）。
// 决不在 calc.Primary 上偷工——主供应商身份的唯一权威是 cost_baseline 当前行。
func (s *CompareService) opportunitiesBargain(ctx context.Context) (*OpportunitiesResult, error) {
	threshold, err := s.store.LoadSysConfigDecimal(ctx, "quote_anomaly_mkt")
	if err != nil {
		return nil, err
	}
	skuIDs, err := s.store.ListSKUIDs(ctx)
	if err != nil {
		return nil, err
	}

	// 主供应商名与 ID 一次查全（单条 SQL 拿两个维度，避免逐 SKU 再查——不 N+1）。
	names, err := s.store.ListPrimarySupplierNames(ctx, skuIDs)
	if err != nil {
		return nil, err
	}
	primaryIDs, err := s.store.ListPrimarySupplierIDs(ctx, skuIDs)
	if err != nil {
		return nil, err
	}

	list := make([]BargainItem, 0)
	for _, skuID := range skuIDs {
		primaryID, hasPrimary := primaryIDs[skuID]
		if !hasPrimary || primaryID == 0 {
			continue // 该 SKU 基线数据残缺（无 primary）——跳过，不静默吞事实
		}
		calc, err := s.calcForSKU(ctx, skuID)
		if errors.Is(err, ErrNoQuoteSKU) {
			continue // 无 EFFECTIVE 报价 = 无市场可言，跳过（不是错误）
		}
		if err != nil {
			return nil, err
		}
		// 用 baseline 的 primary_supplier_id 在 calc.All 里定位该供应商的当前完全成本。
		// 锁定供应商失效（冻结/无 EFFECTIVE 报价）→ 它不在 calc.All → 跳过该 SKU
		// （失效供应商无市场成本可比较，留给锁定解锁或下次激活流程处理）。
		var primaryCost decimal.Decimal
		found := false
		marketBest := decimal.Zero
		for _, r := range calc.All {
			if r.SupplierID == primaryID {
				primaryCost = r.RepresentCost
				found = true
			}
			if !marketBest.IsPositive() || r.RepresentCost.LessThan(marketBest) {
				marketBest = r.RepresentCost
			}
		}
		if !found {
			continue // 锁定供应商失效（它不在参与评分的 EFFECTIVE 集合里）
		}
		if !marketBest.IsPositive() {
			continue // 市场最低价为 0/负 = 数据事故场景，不参与议价判断（不除零）
		}
		deviation := primaryCost.Sub(marketBest).Div(marketBest)
		if !deviation.GreaterThan(threshold) {
			continue // 严格大于（契约 §5 + 提示词变异验证 3：>= 是错的）
		}
		list = append(list, BargainItem{
			SKUID: calc.SKUID, SKUCode: calc.SKUCode,
			PrimarySupplierID:   primaryID,
			PrimarySupplierName: names[primaryID],
			UnitCost:            primaryCost.StringFixed(8),
			MarketBest:          marketBest.StringFixed(8),
			Deviation:           deviation.Round(6).StringFixed(6),
			Threshold:           threshold.Round(6).StringFixed(6),
		})
	}
	return &OpportunitiesResult{Type: OpportunityTypeBargain, List: list, Total: len(list)}, nil
}

// opportunitiesSinglePoint 实现 type=single_point。
// 复用 ListBaselines（OnlySinglePoint=true）——列表已有 supplier_count/single_point 语义，
// 绝不另写一份查询（陷阱 6：同一语义两份实现必漂移）。
// Size 上限对齐列表接口分页上限——单点 SKU 数在可见期内不会超过该上限；
// 超出时本接口返回的是前 1000 行（已登记 6d-4-②，与 bargain 的 N 次重算同属规模问题）。
func (s *CompareService) opportunitiesSinglePoint(ctx context.Context) (*OpportunitiesResult, error) {
	items, _, err := s.store.ListBaselines(ctx, BaselineListQuery{
		OnlySinglePoint: true, Page: 1, Size: 1000,
	})
	if err != nil {
		return nil, err
	}
	list := make([]SinglePointItem, 0, len(items))
	for _, it := range items {
		list = append(list, SinglePointItem{
			SKUID: it.SKUID, SKUCode: it.SKUCode,
			PrimarySupplierID: it.PrimarySupplierID, PrimarySupplierName: it.PrimarySupplierName,
			UnitCost: it.UnitCost, UnitCostBasis: it.UnitCostBasis,
			SupplierCount: it.SupplierCount,
		})
	}
	return &OpportunitiesResult{Type: OpportunityTypeSinglePoint, List: list, Total: len(list)}, nil
}
