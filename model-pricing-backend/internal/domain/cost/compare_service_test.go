// Package cost 的 compare_service_test.go：§4 比价 + §5 议价机会的单测。
//
// fake 说明：
//   - fakeCompareStore 只承载 BaselineReadStore 的读侧数据（trend / skuIDs / 名称 / 阈值）；
//   - CalcSKU 用真·Service（calc 是纯函数），但 LoadRecalcInputForCompare 会走 store——
//     所以把 Service 也接 fakeStore，让装配路径可控（Service.store 是私有字段，
//     同包内测试可直接构造 Service{store: ...}——与 service_test.go 同款做法）。
//   - 所有金额用手算锚点断言（绝不只测"非零"——6d-2/6d-3 同款纪律）。
package cost

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCompareStore 同时实现 cost.Store（CalcSKU 的 LoadRecalcInput 来源）与
// BaselineReadStore（CompareService 的读侧数据）。只填测试触碰到的字段。
type fakeCompareStore struct {
	inputs map[int64]*RecalcInput // skuID → 装配好的输入（LoadRecalcInput 透传）

	// BaselineReadStore 读侧
	resolveID    int64
	resolveOK    bool
	trendRows    []TrendPoint
	trendErr     error
	skuIDs       []int64
	skuIDsErr    error
	threshold    decimal.Decimal
	thresholdErr error
	names        map[int64]string
	namesErr     error
	listItems    []BaselineListItem
	listTotal    int64
	listErr      error
	primaryID    map[int64]int64 // skuID → 锁定的 primary_supplier_id（锁定场景）

	// 记录调用，供断言
	trendGotDays []int
	trendGotSKU  []int64
}

// ---- cost.Store 方法（CalcSKU 装配用） ----

func (f *fakeCompareStore) ListRecalcTargets(context.Context, int64, int64) ([]int64, error) {
	return nil, errors.New("fakeCompareStore.ListRecalcTargets: not implemented")
}
func (f *fakeCompareStore) LoadRecalcInput(_ context.Context, skuID int64) (*RecalcInput, error) {
	if in, ok := f.inputs[skuID]; ok {
		return in, nil
	}
	return nil, errors.New("fakeCompareStore: no such sku input")
}
func (f *fakeCompareStore) LoadCurrentBaselineForUpdate(context.Context, int64) (*Baseline, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeCompareStore) ApplyNewVersion(context.Context, ApplyParams) (int64, int, error) {
	return 0, 0, errors.New("not implemented")
}

// 编译期断言：fakeCompareStore 同时实现读/写两个接口
// （cost.Store 借给 Service 装配 CalcSKU 输入；BaselineReadStore 借给 CompareService）。
var _ Store = (*fakeCompareStore)(nil)
var _ BaselineReadStore = (*fakeCompareStore)(nil)

// ---- BaselineReadStore 方法 ----

func (f *fakeCompareStore) ListBaselines(context.Context, BaselineListQuery) ([]BaselineListItem, int64, error) {
	return f.listItems, f.listTotal, f.listErr
}
func (f *fakeCompareStore) ResolveSKUID(context.Context, string) (int64, bool, error) {
	return f.resolveID, f.resolveOK, nil
}
func (f *fakeCompareStore) ListBaselineHistory(context.Context, int64, *time.Time) ([]BaselineHistoryItem, error) {
	return nil, nil
}
func (f *fakeCompareStore) LoadMinGrossMargin(context.Context) (decimal.Decimal, error) {
	return decimal.Zero, nil
}
func (f *fakeCompareStore) ListBaselineTrend(_ context.Context, skuID int64, days int) ([]TrendPoint, error) {
	f.trendGotSKU = append(f.trendGotSKU, skuID)
	f.trendGotDays = append(f.trendGotDays, days)
	return f.trendRows, f.trendErr
}
func (f *fakeCompareStore) ListSKUIDs(context.Context) ([]int64, error) {
	return f.skuIDs, f.skuIDsErr
}
func (f *fakeCompareStore) LoadSysConfigDecimal(context.Context, string) (decimal.Decimal, error) {
	return f.threshold, f.thresholdErr
}
func (f *fakeCompareStore) ListPrimarySupplierNames(context.Context, []int64) (map[int64]string, error) {
	return f.names, f.namesErr
}
func (f *fakeCompareStore) ListPrimarySupplierIDs(_ context.Context, _ []int64) (map[int64]int64, error) {
	return f.primaryID, nil
}

// newCompareSvc 构造 CompareService + 它依赖的 *Service（用同一个 fake store 双向接）。
func newCompareSvc(store *fakeCompareStore) (*CompareService, *Service) {
	svc := NewService(store, nil, nil) // 重算 Service（store 走 fake，CalcSKU 纯计算）
	cs := NewCompareService(svc, store)
	// 固定 now = 2026-09-16 UTC：与 twoSupplierInput 的 ValidFrom=2026-09-01 配对，
	// 稳定性分数精确 = 15/90 = 1/6（让手算锚点精确，不靠四舍五入近似）。
	cs.now = func() time.Time { return time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC) }
	return cs, svc
}

// ---- 测试数据装配 ----

// compareNow 是本测试文件固定的 now（2026-09-16 00:00:00 UTC）。
// 用它让稳定性分数精确（effective 天数 = 15，stability = 15/90 = 1/6 精确）。
// 不命名为 fixedNow——param_service_test.go 已经用了那个名字（同包冲突）。
var compareNow = time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)

// twoSupplierInput 装配一个两供应商的输入（默认 CNY）。
// ValidFrom 相对 compareNow 配 15 天（= 2026-09-01）；EffectiveRanges 用 [ValidFrom, compareNow]
// （闭合区间，保证 StabilitySince 返回 ValidFrom 而不是 nil）。
// loss=0.03 channel=0.01 固定（与真库 GLOBAL 一致），供应商单价/配额/兼容可调。
func twoSupplierInput(skuID int64, p1, p2 string) *RecalcInput {
	price1 := decimal.RequireFromString(p1)
	price2 := decimal.RequireFromString(p2)
	validFrom := compareNow.AddDate(0, 0, -15) // 2026-09-01 UTC
	effective := []EffectiveRange{{From: validFrom, To: compareNow}}
	return &RecalcInput{
		SKUID:    skuID,
		SKUCode:  "model-a",
		Currency: "CNY",
		Quotes: []QuoteInput{
			{
				SupplierID: 1, QuoteSheetID: 100, QuoteVersion: 1,
				ValidFrom: validFrom, Currency: "CNY",
				Components: []QuoteComponentInput{
					{ComponentType: "input", UnitPrice: price1},
				},
				Constraints:     map[string]any{"rpm": float64(1000)},
				EffectiveRanges: effective,
			},
			{
				SupplierID: 2, QuoteSheetID: 200, QuoteVersion: 1,
				ValidFrom: validFrom, Currency: "CNY",
				Components: []QuoteComponentInput{
					{ComponentType: "input", UnitPrice: price2},
				},
				Constraints:     map[string]any{"rpm": float64(2000), "compatibility": "openai"},
				EffectiveRanges: effective,
			},
		},
		Params: []Param{{
			ScopeType: "GLOBAL", ScopeID: 0,
			LossRate: decimal.RequireFromString("0.03"), ChannelRate: decimal.RequireFromString("0.01"),
			TaxInclusive: true, WithholdingTax: decimal.Zero,
		}},
		Statuses: []SupplierStatus{}, // 无冻结
	}
}

// ---- §4 Compare 单测 ----

// TestCompare_SuppliersExpanded 断言 suppliers[] 的每个字段都正确展开（组件/constraints/scores/is_primary）。
func TestCompare_SuppliersExpanded(t *testing.T) {
	store := &fakeCompareStore{
		resolveID: 40, resolveOK: true,
		inputs: map[int64]*RecalcInput{
			40: twoSupplierInput(40, "2.5", "2.3"),
		},
		trendRows: []TrendPoint{{Date: "2026-09-15", UnitCost: decimal.RequireFromString("2.60075000"), Version: 12}},
		names:     map[int64]string{2: "供应商乙"},
	}
	cs, _ := newCompareSvc(store)
	res, ok, err := cs.Compare(context.Background(), "40", 90)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, res)
	require.Len(t, res.Suppliers, 2)

	// 手算锚点：unit_cost = price × 1.03 × 1.01
	// 供应商1: 2.5 × 1.03 × 1.01 = 2.60075000
	// 供应商2: 2.3 × 1.03 × 1.01 = 2.39269000
	var s1, s2 *CompareSupplierDTO
	for i := range res.Suppliers {
		switch res.Suppliers[i].SupplierID {
		case 1:
			s1 = &res.Suppliers[i]
		case 2:
			s2 = &res.Suppliers[i]
		}
	}
	require.NotNil(t, s1)
	require.NotNil(t, s2)

	assert.Equal(t, "2.60075000", s1.UnitCost)
	assert.Equal(t, "input", s1.UnitCostBasis)
	assert.False(t, s1.IsPrimary, "供应商 1 更贵（2.6 > 2.39），price_normalized 低，总分低")
	assert.True(t, s1.IsBackup)
	require.Len(t, s1.Components, 1)
	assert.Equal(t, "input", s1.Components[0].ComponentType)
	assert.Equal(t, "2.60075000", s1.Components[0].UnitCost)
	assert.Equal(t, "2.50000000", s1.Components[0].SupplierCost)
	require.NotNil(t, s1.Constraints.RPM)
	assert.Equal(t, int64(1000), *s1.Constraints.RPM)

	assert.Equal(t, "2.39269000", s2.UnitCost)
	assert.True(t, s2.IsPrimary)
	assert.False(t, s2.IsBackup)
	require.NotNil(t, s2.Constraints.RPM)
	assert.Equal(t, int64(2000), *s2.Constraints.RPM)
	assert.Equal(t, "openai", s2.Constraints.Compatibility)

	// scores 数值锚点：now 固定 2026-09-16 → 稳定性 = 15/90 = 1/6 精确。
	// price: s1 = 2.39269/2.60075 ≈ 0.920；s2 = 1
	// stability: 两家都 1/6
	// quota: s1 = 1000/2000 = 0.5；s2 = 1
	// compat: s1 = 0.5（"" 未声明）；s2 = 1（"openai"）
	costA := decimal.RequireFromString("2.60075")
	costB := decimal.RequireFromString("2.39269")
	stab := decimal.RequireFromString("1").Div(decimal.RequireFromString("6"))
	expS1Total := costB.Div(costA).Mul(decimal.RequireFromString("0.55")).
		Add(stab.Mul(decimal.RequireFromString("0.25"))).
		Add(decimal.RequireFromString("0.12").Mul(decimal.RequireFromString("0.5"))).
		Add(decimal.RequireFromString("0.08").Mul(decimal.RequireFromString("0.5")))
	assert.True(t, s1.Scores.Total.Sub(expS1Total.Round(6)).Abs().LessThan(decimal.RequireFromString("0.000001")),
		"s1 total 期望≈%s 实得 %s", expS1Total.String(), s1.Scores.Total.String())
}

// TestCompare_RangeAnchors 断言 range 三值与市场最低价（手算锚点）。
func TestCompare_RangeAnchors(t *testing.T) {
	store := &fakeCompareStore{
		resolveID: 40, resolveOK: true,
		inputs: map[int64]*RecalcInput{
			40: twoSupplierInput(40, "2.5", "2.3"),
		},
		names: map[int64]string{},
	}
	cs, _ := newCompareSvc(store)
	res, ok, err := cs.Compare(context.Background(), "40", 90)
	require.NoError(t, err)
	require.True(t, ok)

	assert.Equal(t, "2.39269000", res.Range.CostMin, "min = 供应商2 的 2.39269")
	assert.Equal(t, "2.60075000", res.Range.CostMax, "max = 供应商1 的 2.60075")
	assert.Equal(t, "2.39269000", res.MarketBest, "market_best = min")
	assert.Equal(t, "input", res.MarketBestBasis)

	// cost_weighted = Σ(cost×total)/Σ(total)（total>0 时）
	// 手工算一次精确值做锚点（stability=1/6 精确）
	costA := decimal.RequireFromString("2.60075")
	costB := decimal.RequireFromString("2.39269")
	stab := decimal.RequireFromString("1").Div(decimal.RequireFromString("6"))
	s1Total := costB.Div(costA).Mul(decimal.RequireFromString("0.55")).
		Add(stab.Mul(decimal.RequireFromString("0.25"))).
		Add(decimal.RequireFromString("0.12").Mul(decimal.RequireFromString("0.5"))).
		Add(decimal.RequireFromString("0.08").Mul(decimal.RequireFromString("0.5")))
	s2Total := decimal.RequireFromString("0.55").
		Add(stab.Mul(decimal.RequireFromString("0.25"))).
		Add(decimal.RequireFromString("0.12")).
		Add(decimal.RequireFromString("0.08"))
	expWeighted := costA.Mul(s1Total).Add(costB.Mul(s2Total)).Div(s1Total.Add(s2Total))
	gotWeighted, err := decimal.NewFromString(res.Range.CostWeighted)
	require.NoError(t, err)
	assert.True(t, gotWeighted.Sub(expWeighted).Abs().LessThan(decimal.RequireFromString("0.00000001")),
		"weighted 期望 %s 实得 %s", expWeighted.String(), res.Range.CostWeighted)
}

// TestCompare_RangeArithmeticFallback 断言 total 之和为 0 时退化为算术平均（陷阱 3）。
// 构造：两家非零 unit_cost（2.0 / 3.0），四因子全 0——
//
//	price = 0？不行，min>0 时必有一家 price=1。要 total 全 0 必须 min_cost=0 →
//	但那样就有一家 unit_cost=0 + price=1（它自己 / min=0 → PriceScore 保护性给 0），
//	另一家（min>0 / supplier>0）也可能非 0。
//
// 换成更直接的构造：让 stability/quota/compat 三因子全 0、price 唯一那家有值 → 但 total = 0.55×1 ≠ 0。
// 真实可构造的 sumW=0：两家都 unit_cost=0（price 保护 0）+ stability=0（first=now）+
// quota=0（其他家 maxRPM=0/都没有 rpm，兜底 0.5——不行，0.5>0）——
// quota 兜底 0.5 就永远不是 0。
//
// 换思路： ComputeRange 是独立纯函数，直接对它做单元测试更干净——构造 total 全 0 的输入。
// 本测试走 handler 路径（CalcSKU 输出）验证 fallback 真的被打到：让两家 total 真的为 0
// 的唯一现实路径是 unit_cost 全 0，那时 min=max=weighted=0，任何算法结果相同——
// 所以「验证 fallback 被打到且结果是算术平均」这条**只能靠 ComputeRange 直测**，
// handler 路径上 Σtotal=0 且 unit_cost 非零的组合物理不可达（price_normalized 至少一家是 1）。
// 这里改为 ComputeRange 直测 + handler 路径用非零 total 场景验证加权路径（防回归）。
// ComputeRange 直测的重要性：它是 6d-4 提示词变异 1 的唯一抓手。
func TestCompare_RangeArithmeticFallback(t *testing.T) {
	// 构造 sum_total=0 且 unit_cost 非零——ComputeRange 直测（handler 路径物理不可达此组合）。
	sups := []SupplierCost{
		{SupplierID: 1, UnitCost: decimal.RequireFromString("2.0"), Total: decimal.Zero},
		{SupplierID: 2, UnitCost: decimal.RequireFromString("3.0"), Total: decimal.Zero},
	}
	r := ComputeRange(sups)
	// 算术平均：(2.0 + 3.0) / 2 = 2.5（decimal.Equal 是数值比较，尾零不敏感——与 Unchanged 同款纪律）
	assert.True(t, decimal.RequireFromString("2.5").Equal(r.CostWeighted), "Σtotal=0 时必须退化为算术平均，实得 %s", r.CostWeighted.String())
	assert.True(t, decimal.RequireFromString("2.0").Equal(r.CostMin))
	assert.True(t, decimal.RequireFromString("3.0").Equal(r.CostMax))
}

// TestCompare_DaysNormalization 断言 days 参数边界规整（陷阱 7：<=0→90，>365→365）。
func TestCompare_DaysNormalization(t *testing.T) {
	cases := []struct {
		name    string
		in      int
		wantDay int
	}{
		{"days=0 → 90", 0, 90},
		{"days=-5 → 90", -5, 90},
		{"days=30 → 30", 30, 30},
		{"days=365 → 365", 365, 365},
		{"days=999 → 365", 999, 365},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeCompareStore{
				resolveID: 40, resolveOK: true,
				inputs: map[int64]*RecalcInput{40: twoSupplierInput(40, "2.5", "2.3")},
				names:  map[int64]string{},
			}
			cs, _ := newCompareSvc(store)
			_, _, err := cs.Compare(context.Background(), "40", tc.in)
			require.NoError(t, err)
			require.NotEmpty(t, store.trendGotDays)
			assert.Equal(t, tc.wantDay, store.trendGotDays[0], "规整后传给 repo 的 days")
		})
	}
}

// TestCompare_SKUNotFound 断言 {sku} 解析不到 → found=false → handler 回 404。
func TestCompare_SKUNotFound(t *testing.T) {
	store := &fakeCompareStore{resolveOK: false}
	cs, _ := newCompareSvc(store)
	res, ok, err := cs.Compare(context.Background(), "model-no-such", 90)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, res)
}

// TestCompare_NoQuoteSKU 断言 SKU 存在但无 EFFECTIVE 报价 → 同样 404（不自创 200-空壳）。
func TestCompare_NoQuoteSKU(t *testing.T) {
	store := &fakeCompareStore{
		resolveID: 40, resolveOK: true,
		inputs: map[int64]*RecalcInput{
			40: {SKUID: 40, SKUCode: "model-a", Currency: "CNY", Quotes: []QuoteInput{}},
		},
	}
	cs, _ := newCompareSvc(store)
	res, ok, err := cs.Compare(context.Background(), "40", 90)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, res)
}

// TestCompare_TrendSerialization 断言 trend 序列化为 StringFixed(8) 金额串。
func TestCompare_TrendSerialization(t *testing.T) {
	store := &fakeCompareStore{
		resolveID: 40, resolveOK: true,
		inputs: map[int64]*RecalcInput{40: twoSupplierInput(40, "2.5", "2.3")},
		names:  map[int64]string{},
		trendRows: []TrendPoint{
			{Date: "2026-09-15", UnitCost: decimal.RequireFromString("2.6"), Version: 12},
			{Date: "2026-09-16", UnitCost: decimal.RequireFromString("2.39269"), Version: 13},
		},
	}
	cs, _ := newCompareSvc(store)
	res, ok, err := cs.Compare(context.Background(), "40", 90)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, res.Trend, 2)
	assert.Equal(t, "2026-09-15", res.Trend[0].Date)
	assert.Equal(t, "2.60000000", res.Trend[0].UnitCost)
	assert.Equal(t, 12, res.Trend[0].Version)
	assert.Equal(t, "2.39269000", res.Trend[1].UnitCost)
}

// ---- §5 Opportunities 单测 ----

// TestOpportunities_BargainThreshold 断言 deviation > threshold 严格大于才入选（变异 3 防线）。
// 构造真实锁定场景：供应商 2 贵（p=2.6）但被锁定（primary），供应商 1 便宜（p=2.0）。
// deviation = (cost2 − cost1)/cost1 = (2.70556−2.0812)/2.0812 = 0.3 恰好——要 >0.30 才入选，
// 所以把 p2 再调高一点让它严格大于：p2=2.62 → cost2=2.72637220, dev≈0.31。
func TestOpportunities_BargainThreshold(t *testing.T) {
	store := &fakeCompareStore{
		resolveID: 40, resolveOK: true,
		inputs: map[int64]*RecalcInput{
			40: twoSupplierInput(40, "2.0", "2.62"),
		},
		skuIDs:    []int64{40},
		threshold: decimal.RequireFromString("0.30"),
		names:     map[int64]string{2: "供应商乙"},
		primaryID: map[int64]int64{40: 2}, // 供应商 2 被锁定（算法上它更贵，但人工锁定）
	}
	cs, svc := newCompareSvc(store)
	// 锁定场景：CompareService 不调 LockService，bargain 直接复用 CalcSKU——
	// 但 CalcSKU 的 primary 是四因子冠军（便宜的供应商 1），不是被锁的供应商 2。
	// **这里暴露一个 6d-4 实现陷阱**：bargain 的"主供应商"必须来自 cost_baseline 当前行
	// （被锁定的供应商），不是 CalcSKU 的 Primary。修正方案：bargain 不读 CalcSKU.Primary，
	// 而从 ListPrimarySupplierNames 同款数据源拿 primary_supplier_id 再在 calc.All 里定位。
	// 见 compare_service.go opportunitiesBargain 的 primaryID 查找逻辑。
	_ = svc
	res, err := cs.Opportunities(context.Background(), OpportunityTypeBargain)
	require.NoError(t, err)
	list, ok := res.List.([]BargainItem)
	require.True(t, ok)
	require.Len(t, list, 1, "被锁定的贵供应商（2.62 vs 2.0）应该入选")
	assert.Equal(t, int64(2), list[0].PrimarySupplierID)
	assert.Equal(t, "供应商乙", list[0].PrimarySupplierName)

	// 手算锚点
	cost1 := decimal.RequireFromString("2.0").Mul(decimal.RequireFromString("1.03")).Mul(decimal.RequireFromString("1.01"))
	cost2 := decimal.RequireFromString("2.62").Mul(decimal.RequireFromString("1.03")).Mul(decimal.RequireFromString("1.01"))
	expDev := cost2.Sub(cost1).Div(cost1).Round(6).StringFixed(6)
	assert.Equal(t, expDev, list[0].Deviation)
	assert.Equal(t, cost2.StringFixed(8), list[0].UnitCost)
	assert.Equal(t, cost1.StringFixed(8), list[0].MarketBest)
	assert.Equal(t, "0.300000", list[0].Threshold)
}

// TestOpportunities_BargainBoundaryNotIncluded 断言 deviation == threshold 时**不入选**（严格大于）。
// 这是变异验证 3 的防线：若代码改成 >=，这条测试会红。
// 构造：p2 = p1 × 1.3 精确 → cost2 = cost1 × 1.3 精确 → deviation = 0.3 精确（不入选）。
func TestOpportunities_BargainBoundaryNotIncluded(t *testing.T) {
	store := &fakeCompareStore{
		resolveID: 40, resolveOK: true,
		inputs: map[int64]*RecalcInput{
			40: twoSupplierInput(40, "2.0", "2.6"), // cost2 = 2.70556, cost1 = 2.0812, dev = 0.3 精确
		},
		skuIDs:    []int64{40},
		threshold: decimal.RequireFromString("0.30"),
		names:     map[int64]string{},
		primaryID: map[int64]int64{40: 2}, // 贵供应商被锁定（让 deviation 真正计算出来）
	}
	cs, _ := newCompareSvc(store)
	res, err := cs.Opportunities(context.Background(), OpportunityTypeBargain)
	require.NoError(t, err)
	list := res.List.([]BargainItem)
	assert.Empty(t, list, "deviation 恰好 == threshold 时不入选（严格大于才入选）")
}

// TestOpportunities_BargainEmptyList 断言无议价机会时返回空 list 而不是 null。
// 便宜供应商被锁定（primary）→ primary 就是市场最低 → deviation=0 < 0.30 → 空。
func TestOpportunities_BargainEmptyList(t *testing.T) {
	store := &fakeCompareStore{
		resolveID: 40, resolveOK: true,
		inputs: map[int64]*RecalcInput{
			40: twoSupplierInput(40, "2.5", "2.3"),
		},
		skuIDs:    []int64{40},
		threshold: decimal.RequireFromString("0.30"),
		names:     map[int64]string{},
		// primaryID 缺省 → fakeCompareStore.primaryID 返回 0 → 走 CalcSKU.Primary
		// CalcSKU.Primary 是算法冠军（便宜的 2.3 那家）→ deviation=0
	}
	cs, _ := newCompareSvc(store)
	res, err := cs.Opportunities(context.Background(), OpportunityTypeBargain)
	require.NoError(t, err)
	list := res.List.([]BargainItem)
	assert.NotNil(t, list, "空清单必须是非 nil 空切片——前端按 [] 渲染")
	assert.Len(t, list, 0)
	assert.Equal(t, 0, res.Total)
}

// TestOpportunities_BargainNoQuoteSKUSkipped 断言 NO_QUOTE 的 SKU 被跳过而不是拖垮整单。
func TestOpportunities_BargainNoQuoteSKUSkipped(t *testing.T) {
	store := &fakeCompareStore{
		resolveID: 40, resolveOK: true,
		inputs: map[int64]*RecalcInput{
			40: twoSupplierInput(40, "2.0", "2.62"),
			41: {SKUID: 41, SKUCode: "model-b", Currency: "CNY", Quotes: []QuoteInput{}},
		},
		skuIDs:    []int64{40, 41},
		threshold: decimal.RequireFromString("0.30"),
		names:     map[int64]string{2: "供应商乙"},
		primaryID: map[int64]int64{40: 2},
	}
	cs, _ := newCompareSvc(store)
	res, err := cs.Opportunities(context.Background(), OpportunityTypeBargain)
	require.NoError(t, err, "单 SKU NO_QUOTE 不应拖垮整单")
	list := res.List.([]BargainItem)
	require.Len(t, list, 1)
	assert.Equal(t, int64(40), list[0].SKUID, "只有 sku40 入选，sku41 被跳过")
}

// TestOpportunities_SinglePoint 断言 supplier_count==1 的 SKU 入选、>=2 不入选。
func TestOpportunities_SinglePoint(t *testing.T) {
	store := &fakeCompareStore{
		resolveID: 40, resolveOK: true,
		inputs: map[int64]*RecalcInput{},
		listItems: []BaselineListItem{
			{SKUID: 41, SKUCode: "model-b", PrimarySupplierID: 2, PrimarySupplierName: "供应商乙",
				UnitCost: "62.41800000", UnitCostBasis: "output", SupplierCount: 1, SinglePoint: true},
			// sku40 supplier_count=2 不应出现——repo 侧已按 OnlySinglePoint 过滤
		},
		listTotal: 1,
		names:     map[int64]string{},
	}
	cs, _ := newCompareSvc(store)
	res, err := cs.Opportunities(context.Background(), OpportunityTypeSinglePoint)
	require.NoError(t, err)
	assert.Equal(t, OpportunityTypeSinglePoint, res.Type)
	list := res.List.([]SinglePointItem)
	require.Len(t, list, 1)
	assert.Equal(t, int64(41), list[0].SKUID)
	assert.Equal(t, 1, list[0].SupplierCount)
	assert.Equal(t, "62.41800000", list[0].UnitCost)
	assert.Equal(t, "供应商乙", list[0].PrimarySupplierName)
}

// TestOpportunities_InvalidType 断言非法 type 走 default 分支报错（handler 已校验，这里是双保险）。
func TestOpportunities_InvalidType(t *testing.T) {
	store := &fakeCompareStore{}
	cs, _ := newCompareSvc(store)
	_, err := cs.Opportunities(context.Background(), "foo")
	require.Error(t, err)
}

// TestOpportunities_BargainThresholdLoadErr 断言阈值读 sys_config 失败 → 错误上抛不兜底。
func TestOpportunities_BargainThresholdLoadErr(t *testing.T) {
	store := &fakeCompareStore{
		thresholdErr: errors.New("sys_config quote_anomaly_mkt not found"),
	}
	cs, _ := newCompareSvc(store)
	_, err := cs.Opportunities(context.Background(), OpportunityTypeBargain)
	require.Error(t, err, "阈值读取失败必须上抛——兜底等于静默吞事实")
}

// TestValidOpportunityType 锁定合法值集合（防手写漂移）。
func TestValidOpportunityType(t *testing.T) {
	assert.True(t, ValidOpportunityType("bargain"))
	assert.True(t, ValidOpportunityType("single_point"))
	assert.False(t, ValidOpportunityType(""))
	assert.False(t, ValidOpportunityType("foo"))
	assert.False(t, ValidOpportunityType("Bargain"), "大小写不兼容——契约用小写")
}

// TestNormalizeDays 锁定规整规则。
func TestNormalizeDays(t *testing.T) {
	assert.Equal(t, 90, normalizeDays(0))
	assert.Equal(t, 90, normalizeDays(-5))
	assert.Equal(t, 30, normalizeDays(30))
	assert.Equal(t, 365, normalizeDays(365))
	assert.Equal(t, 365, normalizeDays(999))
}
