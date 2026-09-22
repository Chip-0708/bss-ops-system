package cost

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// fakeStore 为单测的内存成本仓储（项目惯例：单测全内存 fake，PG 语义靠真库 E2E）。
type fakeStore struct {
	targets    []int64
	targetsErr error
	input      *RecalcInput
	inputErr   error
	prev       *Baseline
	prevErr    error
	applied    []ApplyParams
	applyID    int64
	applyVer   int
	applyErr   error
	forUpdateN int // LoadCurrentBaselineForUpdate 调用次数
}

func (f *fakeStore) ListRecalcTargets(_ context.Context, _, _ int64) ([]int64, error) {
	return f.targets, f.targetsErr
}

func (f *fakeStore) LoadRecalcInput(_ context.Context, _ int64) (*RecalcInput, error) {
	return f.input, f.inputErr
}

func (f *fakeStore) LoadCurrentBaselineForUpdate(_ context.Context, _ int64) (*Baseline, error) {
	f.forUpdateN++
	return f.prev, f.prevErr
}

func (f *fakeStore) ApplyNewVersion(_ context.Context, p ApplyParams) (int64, int, error) {
	f.applied = append(f.applied, p)
	return f.applyID, f.applyVer, f.applyErr
}

var testNow = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
var testIdent = JobIdentity{OperatorID: 0, OperatorRole: "WORKER", SourceType: "WORKER", RequestID: "req-1"}

// 基准输入：sheet 27 / sku 40（交接 §6-1：只有 input，unit_price=2.5，GLOBAL 参数 0.03/0.01）。
func baseInput() *RecalcInput {
	return &RecalcInput{
		SKUID: 40, SKUCode: "gpt-5-chat", Currency: "USD",
		Quotes: []QuoteInput{{
			SupplierID: 1, QuoteSheetID: 27, QuoteVersion: 12, ValidFrom: testNow.Add(-time.Hour),
			Currency: "USD",
			Components: []QuoteComponentInput{{
				ComponentType: "input", UnitPrice: d("2.50000000"),
				Multiplier: decPtr(d("1.000000")),
			}},
		}},
		Params: []Param{
			{ScopeType: ScopeGlobal, ScopeID: 0, LossRate: d("0.0300"), ChannelRate: d("0.0100"), TaxInclusive: true},
		},
		Statuses: []SupplierStatus{{SupplierID: 1, QualStatus: "VALID", SettleStatus: "NORMAL", Status: "ACTIVE"}},
	}
}

func decPtr(v decimal.Decimal) *decimal.Decimal { return &v }

func TestCalcSKU_AcceptanceBaseline(t *testing.T) {
	svc := NewService(&fakeStore{}, nil, nil)
	got, err := svc.CalcSKU(baseInput(), testNow)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.Primary.SupplierID)
	require.Equal(t, "input", got.Primary.RepresentBasis)
	// unit_cost = 2.5×1.03×1.01 = 2.60075，入库 8 位 = 2.60075000（交接 §8）
	require.Equal(t, "2.60075000", got.Primary.RepresentCost.Round(8).StringFixed(8))
	require.Equal(t, "2.50000000", got.Primary.Components[0].SupplierCost.StringFixed(8))
	require.Equal(t, ScopeGlobal, got.Primary.ParamsScope)
	require.Empty(t, got.BackupSequence)
	require.Empty(t, got.ExcludedSuppliers)
	// floor = 2.60075 / 0.85 = 3.05970588（交接 §8）
	fl, err := Floor(got.Primary.RepresentCost.Round(8), d("0.15"))
	require.NoError(t, err)
	require.Equal(t, "3.05970588", fl.StringFixed(8))
}

// twoSuppliersInput 构造「A 贵但有配额有历史 / B 便宜无约束无历史」的对照输入
// （与迁移 000020 演示装置同构）：A=supplier1 单价 2.50 / B=supplier2 单价 2.30。
// now 选择 2026-09-15 12:00+08，与交接 §8.3 参考值同一时点口径。
func twoSuppliersInput() (*RecalcInput, time.Time) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, loc)
	rng := func(f, t time.Time) EffectiveRange { return EffectiveRange{From: f, To: t} }
	tm := func(month time.Month, day, h, m, s int) time.Time {
		return time.Date(2026, month, day, h, m, s, 0, loc)
	}
	in := &RecalcInput{
		SKUID: 40, SKUCode: "gpt-5-chat", Currency: "USD",
		Quotes: []QuoteInput{
			{
				SupplierID: 1, QuoteSheetID: 27, QuoteVersion: 12,
				ValidFrom: tm(9, 13, 12, 0, 0), Currency: "USD",
				Constraints: map[string]any{"rpm": float64(3000), "tpm": float64(2000000)},
				// 与真库同构：脏区间（19/24/25）+ 干净连续段（20~22/26/27），期望起点 09-10
				EffectiveRanges: []EffectiveRange{
					rng(tm(9, 13, 22, 22, 44), tm(9, 10, 0, 0, 0)), // sheet19 脏（To<From）
					rng(tm(9, 10, 0, 0, 0), tm(9, 11, 0, 0, 0)),    // sheet20
					rng(tm(9, 11, 0, 0, 0), tm(9, 12, 0, 0, 0)),    // sheet21
					rng(tm(9, 12, 0, 0, 0), tm(9, 18, 3, 21, 38)),  // sheet22 长窗口
					rng(tm(9, 13, 10, 0, 0), tm(9, 9, 0, 0, 0)),    // sheet24 脏
					rng(tm(9, 13, 12, 0, 0), tm(9, 13, 0, 0, 0)),   // sheet25 脏
					rng(tm(9, 13, 0, 0, 0), tm(9, 13, 12, 0, 0)),   // sheet26（被 22 覆盖）
					rng(tm(9, 13, 12, 0, 0), tm(12, 31, 0, 0, 0)),  // sheet27 当前
				},
				Components: []QuoteComponentInput{{ComponentType: "input", UnitPrice: d("2.50")}},
			},
			{
				SupplierID: 2, QuoteSheetID: 29, QuoteVersion: 3,
				ValidFrom:       tm(9, 14, 9, 26, 6),
				Currency:        "USD",
				Constraints:     nil,
				EffectiveRanges: []EffectiveRange{rng(tm(9, 14, 9, 26, 6), tm(12, 31, 0, 0, 0))},
				Components:      []QuoteComponentInput{{ComponentType: "input", UnitPrice: d("2.30")}},
			},
		},
		Params: []Param{
			{ScopeType: ScopeGlobal, ScopeID: 0, LossRate: d("0.0300"), ChannelRate: d("0.0100"), TaxInclusive: true},
		},
		Statuses: []SupplierStatus{
			{SupplierID: 1, QualStatus: "VALID", SettleStatus: "NORMAL", Status: "ACTIVE"},
			{SupplierID: 2, QualStatus: "VALID", SettleStatus: "NORMAL", Status: "ACTIVE"},
		},
	}
	return in, now
}

// TestCalcSKU_PrimaryByTotalScore 是本批的逆直觉验收核心（交接 §8.3/§8.4）：
// B 的完全成本更低（2.39269 < 2.60075），但 A 的四因子总分更高（配额 1.0 + 稳定更早），
// 6d 口径下更贵的 A 当选主供应商。与 6b 「完全成本最低」占位口径结论不同才算接上。
func TestCalcSKU_PrimaryByTotalScore(t *testing.T) {
	in, now := twoSuppliersInput()
	svc := NewService(&fakeStore{}, nil, nil)
	got, err := svc.CalcSKU(in, now)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.Primary.SupplierID, "更贵但总分更高的 A 必须当选（6d 四因子）")
	require.Equal(t, []int64{2}, got.BackupSequence)
	// 成本常量未被评分改写（主供应商还是 2.60075000、B 还是 2.39269000）
	require.Equal(t, "2.60075000", got.Primary.RepresentCost.Round(8).StringFixed(8))
	require.Equal(t, "2.39269000", got.All[1].RepresentCost.Round(8).StringFixed(8))
	a, b := got.All[0], got.All[1]
	// 价格归一：A=0.92 整除、B=1（交接 §8.1）
	require.True(t, d("0.92").Equal(a.Scores.PriceNormalized), "a.price=%s", a.Scores.PriceNormalized)
	require.True(t, decimal.NewFromInt(1).Equal(b.Scores.PriceNormalized))
	// 稳定性起算点：历史扫描真的跑了（09-10，不是当前单 sheet27 的 09-13 12:00）
	require.NotNil(t, a.StabilitySince)
	require.Equal(t, time.Date(2026, 9, 10, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)), *a.StabilitySince)
	require.NotNil(t, b.StabilitySince)
	// 配额：A rpm=3000 最大 → 1；B 无约束 → 0.5
	require.True(t, decimal.NewFromInt(1).Equal(a.Scores.QuotaNormalized))
	require.True(t, d("0.5").Equal(b.Scores.QuotaNormalized))
	// 兼容：两家都未声明 → 0.5
	require.True(t, d("0.5").Equal(a.Scores.CompatNormalized))
	require.True(t, d("0.5").Equal(b.Scores.CompatNormalized))
	// 总分差恒定 ≈ 0.028203 = 0.016 + 0.25×(daysA-daysB)/90（交接 §8.3）
	require.True(t, a.Scores.Total.GreaterThan(b.Scores.Total),
		"a.total=%s 应 > b.total=%s", a.Scores.Total, b.Scores.Total)
	diff := a.Scores.Total.Sub(b.Scores.Total)
	require.InDelta(t, 0.028203, diff.InexactFloat64(), 1e-5, "totalA-totalB=%s", diff)
	// 参考值（now=09-15 12:00+08）：totalA≈0.681278 / totalB≈0.653075
	require.InDelta(t, 0.681278, a.Scores.Total.InexactFloat64(), 1e-4, "totalA=%s", a.Scores.Total)
	require.InDelta(t, 0.653075, b.Scores.Total.InexactFloat64(), 1e-4, "totalB=%s", b.Scores.Total)
}

// TestCalcSKU_StabilityCapped flips 封顶场景：90 天后两家稳定性都撞 1，
// 分差退化为 0.016（=0.55×(1−0.92)）——证明封顶逻辑真的在（交接 §8.3 第四种情况）。
func TestCalcSKU_StabilityCapped(t *testing.T) {
	in, _ := twoSuppliersInput()
	cappedNow := time.Date(2026, 12, 20, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	svc := NewService(&fakeStore{}, nil, nil)
	got, err := svc.CalcSKU(in, cappedNow)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.Primary.SupplierID)
	a, b := got.All[0], got.All[1]
	require.True(t, decimal.NewFromInt(1).Equal(a.Scores.StabilityNormalized),
		"a.stab=%s 应封顶 1", a.Scores.StabilityNormalized)
	require.True(t, decimal.NewFromInt(1).Equal(b.Scores.StabilityNormalized),
		"b.stab=%s 应封顶 1", b.Scores.StabilityNormalized)
	diff := a.Scores.Total.Sub(b.Scores.Total)
	require.InDelta(t, 0.016, diff.InexactFloat64(), 1e-6, "封顶后 totalA-totalB=%s 应≈0.016", diff)
}

func TestCalcSKU_TieBreakByCost(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, loc)
	mkParams := func() []Param {
		return []Param{{ScopeType: ScopeGlobal, ScopeID: 0, LossRate: d("0.03"), ChannelRate: d("0.01"), TaxInclusive: true}}
	}
	mkStatuses := func(ids ...int64) []SupplierStatus {
		out := make([]SupplierStatus, 0, len(ids))
		for _, id := range ids {
			out = append(out, SupplierStatus{SupplierID: id, QualStatus: "VALID", SettleStatus: "NORMAL", Status: "ACTIVE"})
		}
		return out
	}
	hist := []EffectiveRange{{From: now.Add(-48 * time.Hour), To: now.Add(24 * time.Hour)}}

	// 场景 1：同史同配额（stab/quota/compat 三项打平）→ total 只在价格上分胜负 → 便宜的 B 胜。
	in1 := &RecalcInput{
		SKUID: 40, SKUCode: "x", Currency: "USD", Params: mkParams(), Statuses: mkStatuses(1, 2),
		Quotes: []QuoteInput{
			{SupplierID: 1, QuoteSheetID: 27, QuoteVersion: 1, ValidFrom: now.Add(-time.Hour), Currency: "USD",
				Constraints: map[string]any{"rpm": float64(3000)}, EffectiveRanges: hist,
				Components: []QuoteComponentInput{{ComponentType: "input", UnitPrice: d("2.50")}}},
			{SupplierID: 2, QuoteSheetID: 29, QuoteVersion: 1, ValidFrom: now.Add(-time.Hour), Currency: "USD",
				Constraints: map[string]any{"rpm": float64(3000)}, EffectiveRanges: hist,
				Components: []QuoteComponentInput{{ComponentType: "input", UnitPrice: d("2.40")}}},
		},
	}
	svc := NewService(&fakeStore{}, nil, nil)
	got1, err := svc.CalcSKU(in1, now)
	require.NoError(t, err)
	require.Equal(t, int64(2), got1.Primary.SupplierID, "其他因子打平时，便宜的必须胜（total 第一级）")
	require.Equal(t, []int64{1}, got1.BackupSequence)

	// 场景 2：total 精确相等（同价同史同配额）→ RepresentCost 也相等 → supplier_id 小者胜（第三级兜底）。
	in2 := &RecalcInput{
		SKUID: 40, SKUCode: "x", Currency: "USD", Params: mkParams(), Statuses: mkStatuses(1, 3),
		Quotes: []QuoteInput{
			{SupplierID: 1, QuoteSheetID: 27, QuoteVersion: 1, ValidFrom: now.Add(-time.Hour), Currency: "USD",
				Constraints: map[string]any{"rpm": float64(3000)}, EffectiveRanges: hist,
				Components: []QuoteComponentInput{{ComponentType: "input", UnitPrice: d("2.50000000")}}},
			{SupplierID: 3, QuoteSheetID: 30, QuoteVersion: 1, ValidFrom: now.Add(-2 * time.Hour), Currency: "USD",
				Constraints: map[string]any{"rpm": float64(3000)}, EffectiveRanges: hist,
				Components: []QuoteComponentInput{{ComponentType: "input", UnitPrice: d("2.50000000")}}},
		},
	}
	got2, err := svc.CalcSKU(in2, now)
	require.NoError(t, err)
	require.True(t, got2.All[0].Scores.Total.Equal(got2.All[1].Scores.Total), "total 必须精确相等才能测第二/三级")
	require.Equal(t, int64(1), got2.Primary.SupplierID, "total/cost 全等 → supplier_id 小者")
}

func TestCalcSKU_FrozenSupplierExcluded(t *testing.T) {
	in := baseInput()
	in.Statuses[0].QualStatus = "FROZEN"
	svc := NewService(&fakeStore{}, nil, nil)
	_, err := svc.CalcSKU(in, testNow)
	require.ErrorIs(t, err, ErrNoQuoteSKU) // 唯一一家被冻结 → 无参与报价
}

func TestCalcSKU_FrozenExcludedFromBackup(t *testing.T) {
	in := baseInput()
	in.Quotes = append(in.Quotes, QuoteInput{
		SupplierID: 2, QuoteSheetID: 29, QuoteVersion: 3, ValidFrom: testNow.Add(-time.Hour),
		Currency:   "USD",
		Components: []QuoteComponentInput{{ComponentType: "input", UnitPrice: d("2.40")}},
	})
	in.Statuses = append(in.Statuses,
		SupplierStatus{SupplierID: 2, QualStatus: "VALID", SettleStatus: "FROZEN", Status: "ACTIVE"})
	svc := NewService(&fakeStore{}, nil, nil)
	got, err := svc.CalcSKU(in, testNow)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.Primary.SupplierID)  // 2 被冻结，不能当主
	require.Empty(t, got.BackupSequence)                // 也不进备选
	require.Equal(t, []int64{2}, got.ExcludedSuppliers) // 记入排除
}

func TestCalcSKU_SupplierParamOverride(t *testing.T) {
	in := baseInput()
	in.Params = append(in.Params,
		Param{ScopeType: ScopeSupplier, ScopeID: 1, LossRate: d("0.0500"), ChannelRate: d("0.0100")})
	svc := NewService(&fakeStore{}, nil, nil)
	got, err := svc.CalcSKU(in, testNow)
	require.NoError(t, err)
	// 2.5×1.05×1.01 = 2.65125000（交接 §6-1 第二常量）
	require.Equal(t, "2.65125000", got.Primary.Components[0].UnitCost.Round(8).StringFixed(8))
	require.Equal(t, ScopeSupplier, got.Primary.ParamsScope)
}

func TestCalcSKU_CurrencyMismatchFails(t *testing.T) {
	in := baseInput()
	in.Quotes[0].Currency = "CNY"
	svc := NewService(&fakeStore{}, nil, nil)
	_, err := svc.CalcSKU(in, testNow)
	require.Error(t, err)
	require.Contains(t, err.Error(), "币种")
}

func TestCalcSKU_ParamMissingPanics(t *testing.T) {
	in := baseInput()
	in.Params = nil
	svc := NewService(&fakeStore{}, nil, nil)
	require.Panics(t, func() { _, _ = svc.CalcSKU(in, testNow) })
}

func TestCalcSKU_EmptyQuotes(t *testing.T) {
	svc := NewService(&fakeStore{}, nil, nil)
	_, err := svc.CalcSKU(&RecalcInput{SKUID: 40}, testNow)
	require.ErrorIs(t, err, ErrNoQuoteSKU)
}

// TestCalcSKU_ScoresFourFactorReal 单家场景：6d 后三因子不再是 0.5 恒等——
// 无历史 nil → stab 0.5、无约束 → quota/compat 0.5，但 price 必须为 1（唯一一家即最低），
// total = 0.55 + 0.25×0.5 + 0.12×0.5 + 0.08×0.5 = 0.775（本场景数值上恰好与 6b 同，
// 但来源是真实计算：把 Constraints 填上 rpm 后 total 必须变——下一条断言）。
func TestCalcSKU_ScoresFourFactorReal(t *testing.T) {
	in := baseInput()
	svc := NewService(&fakeStore{}, nil, nil)
	got, err := svc.CalcSKU(in, testNow)
	require.NoError(t, err)
	require.True(t, decimal.NewFromInt(1).Equal(got.Primary.Scores.PriceNormalized))
	require.True(t, d("0.5").Equal(got.Primary.Scores.StabilityNormalized))
	require.True(t, d("0.5").Equal(got.Primary.Scores.QuotaNormalized))
	require.True(t, d("0.5").Equal(got.Primary.Scores.CompatNormalized))
	require.True(t, d("0.775").Equal(got.Primary.Scores.Total.Round(4)),
		"total=%s 应为 0.775", got.Primary.Scores.Total)

	// 填上 rpm + 13 天前生效历史：quota→1、stab=13/90≈0.144、total 必须严变（非占位）。
	in.Quotes[0].Constraints = map[string]any{"rpm": float64(3000)}
	in.Quotes[0].EffectiveRanges = []EffectiveRange{
		{From: testNow.AddDate(0, 0, -13), To: testNow.AddDate(0, 1, 0)},
	}
	got2, err := svc.CalcSKU(in, testNow)
	require.NoError(t, err)
	require.True(t, decimal.NewFromInt(1).Equal(got2.Primary.Scores.QuotaNormalized), "quota 必须为 1（真实归一）")
	require.True(t, got2.Primary.Scores.StabilityNormalized.LessThan(d("0.5")) &&
		got2.Primary.Scores.StabilityNormalized.GreaterThan(decimal.Zero),
		"stab=%s 必须为 (0,0.5) 的真实天数归一", got2.Primary.Scores.StabilityNormalized)
	require.NotNil(t, got2.Primary.Scores.StabilitySince)
}

// ---- constraints_ 取值（6.4 类型容错：不 panic、不静默传 0，无法解析走兜底） ----

func TestConstraintsInt64(t *testing.T) {
	c := map[string]any{"rpm": float64(3000)} // encoding/json 默认形态
	got := constraintsInt64(c, "rpm")
	require.NotNil(t, got)
	require.Equal(t, int64(3000), *got)

	// json.Number（GORM jsonb 驱动的另一形态）
	c = map[string]any{"rpm": json.Number("2000000")}
	got = constraintsInt64(c, "rpm")
	require.NotNil(t, got)
	require.Equal(t, int64(2000000), *got)

	// 字符串数字（含科学计数法写法）
	c = map[string]any{"rpm": "3000"}
	require.Equal(t, int64(3000), *constraintsInt64(c, "rpm"))
	c = map[string]any{"rpm": "3e3"}
	require.Equal(t, int64(3000), *constraintsInt64(c, "rpm"))

	// 缺失路径全部 → nil（走 0.5 兜底，绝不静默传 0——0 会真的参与归一把分压成 0）
	require.Nil(t, constraintsInt64(nil, "rpm"))
	require.Nil(t, constraintsInt64(map[string]any{}, "rpm"))
	require.Nil(t, constraintsInt64(map[string]any{"rpm": nil}, "rpm"))
	require.Nil(t, constraintsInt64(map[string]any{"rpm": "abc"}, "rpm"))
	require.Nil(t, constraintsInt64(map[string]any{"rpm": true}, "rpm"))
	require.Nil(t, constraintsInt64(map[string]any{"rpm": float64(3000.5)}, "rpm"), "非整数配额不合法")
	require.Nil(t, constraintsInt64(map[string]any{"rpm": math.Inf(1)}, "rpm"))
	require.Nil(t, constraintsInt64(map[string]any{"rpm": json.Number("xx")}, "rpm"))
}

func TestConstraintsCompat(t *testing.T) {
	// 字符串原样返回（空串在 CompatibilityScore 兜底 0.5）
	require.Equal(t, "OpenAI 兼容", constraintsCompat(map[string]any{"compatibility": "OpenAI 兼容"}, "compatibility"))
	require.Equal(t, "", constraintsCompat(map[string]any{"compatibility": ""}, "compatibility"))
	// bool false → "false"（显式不兼容集合，判 0）；true → "true"（判 1.0）
	require.Equal(t, "false", constraintsCompat(map[string]any{"compatibility": false}, "compatibility"))
	require.Equal(t, "true", constraintsCompat(map[string]any{"compatibility": true}, "compatibility"))
	// 其他类型（数字 / 对象 / 数组 / nil）→ ""（无法判定，走 0.5 未声明兜底，不报错不 panic）
	require.Equal(t, "", constraintsCompat(map[string]any{"compatibility": float64(1)}, "compatibility"))
	require.Equal(t, "", constraintsCompat(map[string]any{"compatibility": map[string]any{"x": 1}}, "compatibility"))
	require.Equal(t, "", constraintsCompat(map[string]any{"compatibility": []any{"a"}}, "compatibility"))
	require.Equal(t, "", constraintsCompat(map[string]any{"compatibility": nil}, "compatibility"))
	require.Equal(t, "", constraintsCompat(nil, "compatibility"))
	require.Equal(t, "", constraintsCompat(map[string]any{}, "compatibility"))
	// 与 CompatibilityScore 的接线闭环（评分入口只认字符串，本函数是来源）
	require.True(t, decimal.Zero.Equal(CompatibilityScore(constraintsCompat(map[string]any{"compatibility": false}, "compatibility"))))
	require.True(t, decimal.NewFromInt(1).Equal(CompatibilityScore(constraintsCompat(map[string]any{"compatibility": true}, "compatibility"))))
	require.True(t, d("0.5").Equal(CompatibilityScore(constraintsCompat(map[string]any{"compatibility": float64(1)}, "compatibility"))))
}

// ---- RecalcSKU 编排 ----

func TestRecalcSKU_CreatesFirstVersion(t *testing.T) {
	store := &fakeStore{input: baseInput(), applyID: 101, applyVer: 1}
	svc := NewService(store, nil, nil)
	out, err := svc.RecalcSKU(context.Background(), 40, ReasonQuoteEffective, "req-1", testNow, testIdent, nil)
	require.NoError(t, err)
	require.Equal(t, "CREATED", out.Status)
	require.Equal(t, 1, out.Version)
	require.Len(t, store.applied, 1)
	p := store.applied[0]
	require.False(t, p.HasPrevious)
	require.Equal(t, "6d-v1", p.CalcSnapshot["formula_version"])
	require.Equal(t, "four-factor", p.CalcSnapshot["primary_selection_rule"])
	require.Equal(t, int64(1), p.CalcSnapshot["primary_supplier_id"])
	require.Equal(t, ScopeGlobal, p.CalcSnapshot["params_scope"])
	require.Contains(t, p.CalcSnapshot, "excluded_suppliers")
	require.Contains(t, p.CalcSnapshot, "scores")
	// 单家无历史：scores[0].stability_since 必须是显式 null（key 在、值为 nil），
	// 不是零值时间戳 "0001-01-01T00:00:00Z"（6d-1 交接 §6.6）
	scores, ok := p.CalcSnapshot["scores"].([]map[string]any)
	require.True(t, ok && len(scores) == 1)
	require.Contains(t, scores[0], "stability_since")
	require.Nil(t, scores[0]["stability_since"])
}

func TestRecalcSKU_SnapshotStabilitySince(t *testing.T) {
	// 有历史：stability_since 必须带真实起算点（RFC3339 字符串）
	in, now := twoSuppliersInput()
	store := &fakeStore{input: in, applyID: 201, applyVer: 4}
	svc := NewService(store, nil, nil)
	out, err := svc.RecalcSKU(context.Background(), 40, ReasonQuoteEffective, "req-1b", now, testIdent, nil)
	require.NoError(t, err)
	require.Equal(t, "CREATED", out.Status)
	scores := store.applied[0].CalcSnapshot["scores"].([]map[string]any)
	require.Len(t, scores, 2)
	byID := map[int64]map[string]any{}
	for _, s := range scores {
		byID[s["supplier_id"].(int64)] = s
	}
	// 快照统一 UTC（RFC3339）：+08 的 09-10 00:00 / 09-14 09:26:06 → UTC 前一日 16:00 / 01:26:06
	require.Equal(t, "2026-09-09T16:00:00Z", byID[1]["stability_since"])
	require.Equal(t, "2026-09-14T01:26:06Z", byID[2]["stability_since"])
	require.Equal(t, "four-factor", store.applied[0].CalcSnapshot["primary_selection_rule"])
}

func TestRecalcSKU_UnchangedSkips(t *testing.T) {
	// 旧版本与新计算全同 → 不产生新版本（日终重算不撑爆表）
	prev := &Baseline{
		Version: 7, Currency: "USD", PrimarySupplierID: 1,
		LossRate: d("0.0300"), ChannelRate: d("0.0100"),
		ChangeReason: ReasonQuoteEffective, FormulaVersion: FormulaVersion,
		Components: []Component{
			{ComponentType: "input", UnitCost: UnitCost(d("2.5"), d("0.03"), d("0.01")), SupplierCost: d("2.5")},
		},
	}
	store := &fakeStore{input: baseInput(), prev: prev}
	svc := NewService(store, nil, nil)
	out, err := svc.RecalcSKU(context.Background(), 40, ReasonDailyRecalc, "req-2", testNow, testIdent, nil)
	require.NoError(t, err)
	require.Equal(t, "UNCHANGED", out.Status)
	require.Equal(t, 7, out.Version)
	require.Empty(t, store.applied)
	require.Equal(t, 1, store.forUpdateN) // FOR UPDATE 行锁必须被调用
}

func TestRecalcSKU_ChangeReasonNotPartOfUnchanged(t *testing.T) {
	// change_reason 不同但值全同 → 仍跳过（值未变判定不含 reason）
	prev := &Baseline{
		Version: 7, Currency: "USD", PrimarySupplierID: 1,
		LossRate: d("0.0300"), ChannelRate: d("0.0100"),
		ChangeReason: ReasonManualLock, FormulaVersion: FormulaVersion,
		Components: []Component{
			{ComponentType: "input", UnitCost: UnitCost(d("2.5"), d("0.03"), d("0.01")), SupplierCost: d("2.5")},
		},
	}
	store := &fakeStore{input: baseInput(), prev: prev}
	svc := NewService(store, nil, nil)
	out, err := svc.RecalcSKU(context.Background(), 40, ReasonQuoteEffective, "req-3", testNow, testIdent, nil)
	require.NoError(t, err)
	require.Equal(t, "UNCHANGED", out.Status)
}

func TestRecalcSKU_ValueChangedCreatesNewVersion(t *testing.T) {
	prev := &Baseline{
		Version: 7, Currency: "USD", PrimarySupplierID: 1,
		LossRate: d("0.0300"), ChannelRate: d("0.0100"),
		ChangeReason: ReasonQuoteEffective, FormulaVersion: FormulaVersion,
		Components: []Component{
			{ComponentType: "input", UnitCost: d("2.70"), SupplierCost: d("2.6")},
		},
	}
	store := &fakeStore{input: baseInput(), prev: prev, applyID: 102, applyVer: 8}
	svc := NewService(store, nil, nil)
	out, err := svc.RecalcSKU(context.Background(), 40, ReasonQuoteEffective, "req-4", testNow, testIdent, nil)
	require.NoError(t, err)
	require.Equal(t, "CREATED", out.Status)
	require.Equal(t, 8, out.Version)
	require.True(t, store.applied[0].HasPrevious)
	require.Equal(t, 7, store.applied[0].PreviousVersion)
}

func TestRecalcSKU_FormulaVersionChangeForcesNewVersion(t *testing.T) {
	// 除 formula_version 外全同 → 新版本（6c 切换强制全量重算的保障）
	prev := &Baseline{
		Version: 7, Currency: "USD", PrimarySupplierID: 1,
		LossRate: d("0.0300"), ChannelRate: d("0.0100"),
		ChangeReason: ReasonQuoteEffective, FormulaVersion: "6a-v0",
		Components: []Component{
			{ComponentType: "input", UnitCost: UnitCost(d("2.5"), d("0.03"), d("0.01")), SupplierCost: d("2.5")},
		},
	}
	store := &fakeStore{input: baseInput(), prev: prev, applyID: 103, applyVer: 8}
	svc := NewService(store, nil, nil)
	out, err := svc.RecalcSKU(context.Background(), 40, ReasonDailyRecalc, "req-5", testNow, testIdent, nil)
	require.NoError(t, err)
	require.Equal(t, "CREATED", out.Status)
	require.Equal(t, 8, out.Version)
}

func TestRecalcSKU_NoQuoteOutcome(t *testing.T) {
	store := &fakeStore{input: &RecalcInput{SKUID: 40, SKUCode: "x", Currency: "USD"}}
	svc := NewService(store, nil, nil)
	out, err := svc.RecalcSKU(context.Background(), 40, ReasonExpireRemove, "req-6", testNow, testIdent, nil)
	require.NoError(t, err)
	require.Equal(t, "NO_QUOTE", out.Status)
	require.Empty(t, store.applied)
}

func TestRecalcSKU_InvalidReason(t *testing.T) {
	store := &fakeStore{input: baseInput()}
	svc := NewService(store, nil, nil)
	_, err := svc.RecalcSKU(context.Background(), 40, "BOGUS_REASON", "req-7", testNow, testIdent, nil)
	require.ErrorIs(t, err, ErrInvalidChangeReason)
}

func TestRecalcSKU_ConflictPropagates(t *testing.T) {
	store := &fakeStore{input: baseInput(), applyErr: ErrVersionConflict}
	svc := NewService(store, nil, nil)
	_, err := svc.RecalcSKU(context.Background(), 40, ReasonQuoteEffective, "req-8", testNow, testIdent, nil)
	require.ErrorIs(t, err, ErrVersionConflict)
}

// ---- RecalcFromTask 编排 ----

func TestRecalcFromTask_MultiSKU(t *testing.T) {
	in41 := baseInput()
	in41.SKUID, in41.SKUCode = 41, "claude-opus"
	in41.Quotes[0].Components = []QuoteComponentInput{
		{ComponentType: "input", UnitPrice: d("12.00")},
		{ComponentType: "output", UnitPrice: d("60.00")},
	}
	svc := NewService(&multiStore{
		inputs:  map[int64]*RecalcInput{40: baseInput(), 41: in41},
		targets: []int64{40, 41},
	}, nil, nil)
	outs, err := svc.RecalcFromTask(context.Background(), 1, 27, ReasonQuoteEffective, "req-9", testNow, testIdent)
	require.NoError(t, err)
	require.Len(t, outs, 2)
	// sku 41 验收常量：12×1.0403=12.4836 / 60×1.0403=62.418（交接 §6-1）
	var out41 *RecalcOutcome
	for i := range outs {
		if outs[i].SKUID == 41 {
			out41 = &outs[i]
		}
	}
	require.NotNil(t, out41)
	require.Equal(t, "CREATED", out41.Status)
}

func TestRecalcFromTask_OneFailsOthersContinue(t *testing.T) {
	ms := &multiStore{
		inputs:   map[int64]*RecalcInput{40: baseInput(), 41: baseInput()},
		applyErr: map[int64]error{40: errors.New("boom")},
		targets:  []int64{40, 41},
	}
	svc := NewService(ms, nil, nil)
	outs, err := svc.RecalcFromTask(context.Background(), 1, 27, ReasonQuoteEffective, "req-10", testNow, testIdent)
	require.Error(t, err)
	require.Len(t, outs, 1) // 41 成功被收集
	require.Equal(t, int64(41), outs[0].SKUID)
}

// multiStore 按 SKU 区分输入/错误的 fake。
type multiStore struct {
	inputs   map[int64]*RecalcInput
	applyErr map[int64]error
	targets  []int64
}

func (m *multiStore) ListRecalcTargets(_ context.Context, _, _ int64) ([]int64, error) {
	return m.targets, nil
}
func (m *multiStore) LoadRecalcInput(_ context.Context, skuID int64) (*RecalcInput, error) {
	in := m.inputs[skuID]
	if in == nil {
		return &RecalcInput{SKUID: skuID, Currency: "USD"}, nil
	}
	cp := *in
	cp.SKUID = skuID
	return &cp, nil
}
func (m *multiStore) LoadCurrentBaselineForUpdate(_ context.Context, _ int64) (*Baseline, error) {
	return nil, nil
}
func (m *multiStore) ApplyNewVersion(_ context.Context, p ApplyParams) (int64, int, error) {
	if err := m.applyErr[p.SKUID]; err != nil {
		return 0, 0, err
	}
	return 100 + p.SKUID, p.PreviousVersion + 1, nil
}
