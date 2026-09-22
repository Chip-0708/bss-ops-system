package cost

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestPriceScore_LowestIsOne(t *testing.T) {
	// min=2.5, 自身 2.5 → 1
	require.True(t, decimal.NewFromInt(1).Equal(PriceScore(d("2.5"), d("2.5"))))
	// min=2.5, 自身 5.0 → 0.5
	require.True(t, d("0.5").Equal(PriceScore(d("5.0"), d("2.5"))))
}

func TestPriceScore_ZeroGuards(t *testing.T) {
	require.True(t, decimal.Zero.Equal(PriceScore(d("2.5"), decimal.Zero)))
	require.True(t, decimal.Zero.Equal(PriceScore(decimal.Zero, d("2.5"))))
	require.True(t, decimal.Zero.Equal(PriceScore(d("2.5"), d("-1"))))
}

func TestStabilityScore(t *testing.T) {
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	// 新供应商（无历史）→ 0.5
	require.True(t, d("0.5").Equal(StabilityScore(nil, now)))
	// 45 天 → 0.5
	first := now.AddDate(0, 0, -45)
	require.True(t, d("0.5").Equal(StabilityScore(&first, now)))
	// 90 天 → 1（封顶）
	first = now.AddDate(0, 0, -90)
	require.True(t, decimal.NewFromInt(1).Equal(StabilityScore(&first, now)))
	// 180 天 → 仍 1
	first = now.AddDate(0, 0, -180)
	require.True(t, decimal.NewFromInt(1).Equal(StabilityScore(&first, now)))
	// 9 天 → 0.1
	first = now.AddDate(0, 0, -9)
	got := StabilityScore(&first, now)
	require.True(t, d("0.1").Equal(got.Round(4)), "9/90 应为 0.1，得到 %s", got.String())
	// 未来时间（脏数据）→ 0，不取负
	future := now.AddDate(0, 0, 3)
	require.True(t, decimal.Zero.Equal(StabilityScore(&future, now)))
}

func TestQuotaScore(t *testing.T) {
	i64 := func(v int64) *int64 { return &v }
	// rpm 归一
	require.True(t, d("0.5").Equal(QuotaScore(i64(300), nil, i64(600), nil)))
	// rpm 缺 → tpm 归一
	require.True(t, d("0.25").Equal(QuotaScore(nil, i64(1000), nil, i64(4000))))
	// 两者都缺 → 0.5
	require.True(t, d("0.5").Equal(QuotaScore(nil, nil, i64(600), i64(4000))))
	// max<=0 → 0.5
	require.True(t, d("0.5").Equal(QuotaScore(i64(300), nil, i64(0), nil)))
}

func TestCompatibilityScore(t *testing.T) {
	require.True(t, d("0.5").Equal(CompatibilityScore("")))
	require.True(t, decimal.NewFromInt(1).Equal(CompatibilityScore("OpenAI 兼容")))
	// 三级判定：trim + 小写化后命中显式不兼容集合 → 0（六个值逐条锁，6d-1 交接 §6.2）
	componentScoreZero := []string{"none", "false", "incompatible", "0", "不兼容", "不支持"}
	for _, v := range componentScoreZero {
		require.True(t, decimal.Zero.Equal(CompatibilityScore(v)),
			"%q 应判显式不兼容=0", v)
	}
	// trim + 大小写不敏感
	require.True(t, decimal.Zero.Equal(CompatibilityScore("  NONE ")))
	require.True(t, decimal.Zero.Equal(CompatibilityScore("False")))
	// 命中集合前缀但不是完整词 → 1.0（严格匹配，防止 "none-of-the-above" 被误杀）
	require.True(t, decimal.NewFromInt(1).Equal(CompatibilityScore("none-of-the-above")))
	// 空白只有空格 → 未声明 0.5
	require.True(t, d("0.5").Equal(CompatibilityScore("   ")))
}

func TestTotalScore_Weights(t *testing.T) {
	// 全 1 → 1（权重和=1 的恒等校验）
	require.True(t, decimal.NewFromInt(1).Equal(
		TotalScore(decimal.NewFromInt(1), decimal.NewFromInt(1), decimal.NewFromInt(1), decimal.NewFromInt(1))))
	// 全 0.5 → 0.5
	require.True(t, d("0.5").Equal(
		TotalScore(d("0.5"), d("0.5"), d("0.5"), d("0.5"))))
	// 只有 price=1，其余 0 → 0.55
	require.True(t, d("0.55").Equal(
		TotalScore(decimal.NewFromInt(1), decimal.Zero, decimal.Zero, decimal.Zero)))
}

// TestScoreSuppliers_FourFactors 锁 6d 真实三因子接线（不再是 0.5 占位）。
// 场景：A 贵但有配额、B 便宜无配额——A 的 quota=1、B 的 quota=0.5 必须由真实 rpm 归一得出；
// stability 用真实起算点；compat 显式不兼容必须压到 0。
func TestScoreSuppliers_FourFactors(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	rpm := func(v int64) *int64 { return &v }
	firstA := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC) // 5.5 天前
	inputs := []FactorInput{
		{SupplierID: 1, UnitCost: d("2.60075000"), FirstEffectiveFrom: &firstA, RPM: rpm(3000), Compatibility: "OpenAI 兼容"},
		{SupplierID: 2, UnitCost: d("2.39269000"), Compatibility: "incompatible"},
	}
	scores := ScoreSuppliers(inputs, d("2.39269000"), now)
	require.Len(t, scores, 2)
	byID := map[int64]FactorScores{}
	for _, s := range scores {
		byID[s.SupplierID] = s
	}
	a, b := byID[1], byID[2]
	// 价格：A=2.39269/2.60075=0.92（交接 §8.1：正好整除），B=1
	require.True(t, d("0.92").Equal(a.PriceNormalized), "a.price=%s", a.PriceNormalized)
	require.True(t, decimal.NewFromInt(1).Equal(b.PriceNormalized))
	// 稳定性：A 有真实起算点（不是 nil 的 0.5 兜底）：5.5/90 = 0.061111…
	require.True(t, d("0.061111").Equal(a.StabilityNormalized.Round(6)), "a.stab=%s", a.StabilityNormalized)
	require.Equal(t, &firstA, a.StabilitySince, "StabilitySince 必须透传供快照追溯")
	// B 无历史 → 0.5；StabilitySince 为 nil
	require.True(t, d("0.5").Equal(b.StabilityNormalized))
	require.Nil(t, b.StabilitySince)
	// 配额：A rpm=3000=maxRPM → 1；B 无 rpm/tpm → 0.5（max 只在参与供应商内取）
	require.True(t, decimal.NewFromInt(1).Equal(a.QuotaNormalized))
	require.True(t, d("0.5").Equal(b.QuotaNormalized))
	// 兼容：A 非空 → 1；B 显式不兼容 → 0
	require.True(t, decimal.NewFromInt(1).Equal(a.CompatNormalized))
	require.True(t, decimal.Zero.Equal(b.CompatNormalized))
	// total 不再是 6b 的 0.775 恒等（6b 三因子全 0.5 的场景已被本测试替代）
	require.True(t, a.Total.GreaterThan(decimal.Zero))
}

// TestScoreSuppliers_MaxQuotaWithinParticipants maxRPM/maxTPM 只在传入的 inputs 内取——
// 语义与 minCost 一致（被冻结/排除的供应商在调用方就已拿掉）。
func TestScoreSuppliers_MaxQuotaWithinParticipants(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	rpm := func(v int64) *int64 { return &v }
	inputs := []FactorInput{
		{SupplierID: 1, UnitCost: d("2.5"), RPM: rpm(3000)},
		{SupplierID: 2, UnitCost: d("2.5"), RPM: rpm(6000)},
	}
	scores := ScoreSuppliers(inputs, d("2.5"), now)
	byID := map[int64]FactorScores{}
	for _, s := range scores {
		byID[s.SupplierID] = s
	}
	// 若 max 取错（比如取成 3000 或只看自己），3000/6000=0.5 这条就会挂
	require.True(t, d("0.5").Equal(byID[1].QuotaNormalized), "quota=%s", byID[1].QuotaNormalized)
	require.True(t, decimal.NewFromInt(1).Equal(byID[2].QuotaNormalized))
}

func TestScoreSuppliers_Empty(t *testing.T) {
	require.Empty(t, ScoreSuppliers(nil, d("2.5"), time.Now()))
}

// ---- StabilitySince 生效区间聚合（6d-1 交接 §6.3，全用例已锁） ----

func er(from, to string) EffectiveRange {
	layout := "2006-01-02"
	f, _ := time.Parse(layout, from)
	t, _ := time.Parse(layout, to)
	return EffectiveRange{From: f, To: t}
}

func requireSince(t *testing.T, want string, ranges []EffectiveRange) {
	t.Helper()
	got := StabilitySince(ranges)
	require.NotNil(t, got)
	wantT, _ := time.Parse("2006-01-02", want)
	require.True(t, wantT.Equal(*got), "StabilitySince=%s，应为 %s", got.Format("2006-01-02"), want)
}

func TestStabilitySince_Continuous(t *testing.T) {
	// 连续无断档（To == 下一 From 算首尾相接）：起点=最早 From
	requireSince(t, "2026-09-10", []EffectiveRange{
		er("2026-09-10", "2026-09-11"),
		er("2026-09-11", "2026-09-12"),
		er("2026-09-12", "2026-09-18"),
	})
}

func TestStabilitySince_OneGap(t *testing.T) {
	// 中间断档一次：起点=断档后最早区间 From
	requireSince(t, "2026-09-05", []EffectiveRange{
		er("2026-09-01", "2026-09-03"),
		er("2026-09-05", "2026-09-08"),
	})
}

func TestStabilitySince_TwoGaps(t *testing.T) {
	// 断档两次：起点=最后一段的 From
	requireSince(t, "2026-09-10", []EffectiveRange{
		er("2026-09-01", "2026-09-02"),
		er("2026-09-04", "2026-09-05"),
		er("2026-09-10", "2026-09-12"),
	})
}

func TestStabilitySince_AllDirty(t *testing.T) {
	// 全脏数据（To<=From）→ nil（走 0.5 兜底）
	require.Nil(t, StabilitySince([]EffectiveRange{
		er("2026-09-10", "2026-09-01"),
		er("2026-09-05", "2026-09-05"), // 零长度也脏
	}))
}

func TestStabilitySince_Empty(t *testing.T) {
	require.Nil(t, StabilitySince(nil))
}

func TestStabilitySince_SingleRange(t *testing.T) {
	requireSince(t, "2026-09-10", []EffectiveRange{er("2026-09-10", "2026-09-11")})
}

func TestStabilitySince_OverlappingRanges(t *testing.T) {
	// 【最易写错的一条】重叠区间：长窗口 To=09-18 完全覆盖中间的 09-13→09-13.5，
	// curEnd 必须 max 延伸而不是直接覆盖赋值——直接赋值会把窗口缩回 09-13.5，
	// 最后一个区间 (09-14→09-20) 的 From(09-14) > 09-13.5 被误判成断档（错得 09-14）。
	// 正确结果：全程不断档，起点=09-10。（对应真库 sheet 22 vs 26/27 的形态）
	requireSince(t, "2026-09-10", []EffectiveRange{
		er("2026-09-10", "2026-09-11"),
		er("2026-09-11", "2026-09-12"),
		er("2026-09-12", "2026-09-18"), // 长窗口
		er("2026-09-13", "2026-09-14"), // 被长窗口覆盖起点、To 又落回窗口内的短区间
		er("2026-09-15", "2026-09-20"), // From=09-15 < curEnd=09-18：若 curEnd 被缩回 09-14 这里必断档
	})
}

func TestStabilitySince_DirtyMixKeepsCleanStart(t *testing.T) {
	// 脏区间混入：丢弃后取最早干净 From（对应真库 sheet 19/24/25 三条脏数据的形态）
	requireSince(t, "2026-09-10", []EffectiveRange{
		er("2026-09-13", "2026-09-10"), // 脏
		er("2026-09-10", "2026-09-11"),
		er("2026-09-11", "2026-09-12"),
	})
}

func TestStabilitySince_TouchingIsNotGap(t *testing.T) {
	// To == From 首尾相接不算断档（半开区间语义）：两段应合成一条
	requireSince(t, "2026-09-01", []EffectiveRange{
		er("2026-09-01", "2026-09-05"),
		er("2026-09-05", "2026-09-08"),
	})
}
