package cost

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

var minMargin = decimal.RequireFromString("0.15")

// TestUnitCost_FormulaConstants 锁死交接 §6-1 的验收常量。
// 乘数是 (1+loss)×(1+channel)，不是 (1+loss+channel)。
func TestUnitCost_FormulaConstants(t *testing.T) {
	cases := []struct {
		name         string
		price        string
		loss         string
		channel      string
		wantUnitCost string // numeric(20,8) 入库值（Round(8)）
		wantFloor    string // floor=unit_cost(8位)/0.85，StringFixed(8) 表示
	}{
		// 验收基准（交接 §8）：sku 40 / sheet 27，GLOBAL 参数
		{"2.50/0.0300/0.0100", "2.50", "0.0300", "0.0100", "2.60075000", "3.05970588"},
		// 损耗系数覆盖到 5%（验收容错区间上沿）
		{"2.50/0.0500/0.0100", "2.50", "0.0500", "0.0100", "2.65125000", "3.11911765"},
		// sku 41 input 12.00
		{"12.00/0.0300/0.0100", "12.00", "0.0300", "0.0100", "12.48360000", "14.68658824"},
		// sku 41 output 60.00
		{"60.00/0.0300/0.0100", "60.00", "0.0300", "0.0100", "62.41800000", "73.43294118"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := UnitCost(d(tc.price), d(tc.loss), d(tc.channel))
			// 入库值（numeric(20,8) 等价）
			uc8 := uc.Round(8)
			require.Equal(t, tc.wantUnitCost, uc8.StringFixed(8), "unit_cost 不匹配")
			// 反证：若误用 (1+loss+channel) 则必然不等
			wrong := d(tc.price).Mul(decimal.NewFromInt(1).Add(d(tc.loss)).Add(d(tc.channel))).Round(8)
			if tc.name == "2.50/0.0300/0.0100" {
				require.NotEqual(t, uc8.StringFixed(8), wrong.StringFixed(8),
					"两种公式在该用例应产生不同结果（2.60075000 vs 2.60000000）")
			}
			// floor 用入库后的 8 位值再除（交接 §8 常量即按此口径）
			fl, err := Floor(uc8, minMargin)
			require.NoError(t, err)
			require.Equal(t, tc.wantFloor, fl.StringFixed(8), "floor 不匹配")
		})
	}
}

func TestFloor_MarginMustBeLessThanOne(t *testing.T) {
	_, err := Floor(d("2.60075000"), decimal.NewFromInt(1))
	require.Error(t, err)
	_, err = Floor(d("2.60075000"), d("1.2"))
	require.Error(t, err)
}

func baselineForTest() *Baseline {
	return &Baseline{
		Version:           3,
		Currency:          "USD",
		PrimarySupplierID: 1,
		LossRate:          d("0.0300"),
		ChannelRate:       d("0.0100"),
		ChangeReason:      ReasonQuoteEffective,
		FormulaVersion:    FormulaVersion,
		Components: []Component{
			{ComponentType: "input", UnitCost: d("2.60075000"), SupplierCost: d("2.50000000")},
			{ComponentType: "output", UnitCost: d("5.20150000"), SupplierCost: d("5.00000000")},
		},
	}
}

func TestUnchanged_AllSame(t *testing.T) {
	prev := baselineForTest()
	next := baselineForTest()
	next.Version = 4
	require.True(t, Unchanged(prev, next))
}

func TestUnchanged_DecimalEqualTrailingZeroInsensitive(t *testing.T) {
	// decimal.Equal 是数值比较："2.60075" 与 "2.60075000" 必须判同（交接 §4：不用哈希就是因为尾零敏感）
	prev := baselineForTest()
	next := baselineForTest()
	next.Components[0].UnitCost = d("2.60075") // 数值相等、字面值不同
	require.True(t, Unchanged(prev, next))
}

func TestUnchanged_FormulaVersionDiffForcesNewVersion(t *testing.T) {
	// 交接 §6-2 点名：除 formula_version 外全同 → Unchanged=false（6c 切换强制全量重算）
	prev := baselineForTest()
	next := baselineForTest()
	next.FormulaVersion = "6c-v1"
	require.False(t, Unchanged(prev, next))
}

func TestUnchanged_LossRateDiff(t *testing.T) {
	prev := baselineForTest()
	next := baselineForTest()
	next.LossRate = d("0.0500")
	require.False(t, Unchanged(prev, next))
}

func TestUnchanged_ChannelRateDiff(t *testing.T) {
	prev := baselineForTest()
	next := baselineForTest()
	next.ChannelRate = d("0.0200")
	require.False(t, Unchanged(prev, next))
}

func TestUnchanged_PrimarySupplierDiff(t *testing.T) {
	prev := baselineForTest()
	next := baselineForTest()
	next.PrimarySupplierID = 2
	require.False(t, Unchanged(prev, next))
}

func TestUnchanged_ComponentValueDiff(t *testing.T) {
	prev := baselineForTest()
	next := baselineForTest()
	next.Components[1].UnitCost = d("5.20150001")
	require.False(t, Unchanged(prev, next))
}

func TestUnchanged_ComponentAdded(t *testing.T) {
	prev := baselineForTest()
	next := baselineForTest()
	next.Components = append(next.Components,
		Component{ComponentType: "reasoning", UnitCost: d("1.0"), SupplierCost: d("0.9")})
	require.False(t, Unchanged(prev, next))
}

func TestUnchanged_ComponentRemoved(t *testing.T) {
	prev := baselineForTest()
	next := baselineForTest()
	next.Components = next.Components[:1]
	require.False(t, Unchanged(prev, next))
}

func TestUnchanged_ComponentTypeSwapped(t *testing.T) {
	// 组件集合不同（reasoning 替换 output）→ 不同
	prev := baselineForTest()
	next := baselineForTest()
	next.Components[1] = Component{ComponentType: "reasoning", UnitCost: d("5.20150000"), SupplierCost: d("5.0")}
	require.False(t, Unchanged(prev, next))
}

func TestUnchanged_NilPrevIsChanged(t *testing.T) {
	require.False(t, Unchanged(nil, baselineForTest()))
	require.False(t, Unchanged(baselineForTest(), nil))
}

func TestValidChangeReason(t *testing.T) {
	require.True(t, ValidChangeReason(ReasonQuoteEffective))
	require.True(t, ValidChangeReason(ReasonExpireRemove))
	require.False(t, ValidChangeReason("SOME_RANDOM_REASON"))
}
