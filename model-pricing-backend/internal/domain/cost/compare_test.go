package cost

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestComputeRange_Weighted(t *testing.T) {
	// 两家：1.0(total=0.6) / 3.0(total=0.4) → weighted = (0.6+1.2)/1.0 = 1.8
	r := ComputeRange([]SupplierCost{
		{SupplierID: 1, UnitCost: d("1.0"), Total: d("0.6")},
		{SupplierID: 2, UnitCost: d("3.0"), Total: d("0.4")},
	})
	require.True(t, d("1.0").Equal(r.CostMin))
	require.True(t, d("3.0").Equal(r.CostMax))
	require.True(t, d("1.8").Equal(r.CostWeighted.Round(6)), "weighted=%s", r.CostWeighted.String())
}

func TestComputeRange_ZeroTotalFallsBackToArithmeticMean(t *testing.T) {
	// total 全 0 → 算术平均 (1+3)/2 = 2，不除零
	r := ComputeRange([]SupplierCost{
		{SupplierID: 1, UnitCost: d("1.0"), Total: decimal.Zero},
		{SupplierID: 2, UnitCost: d("3.0"), Total: decimal.Zero},
	})
	require.True(t, d("2.0").Equal(r.CostWeighted))
}

func TestComputeRange_SingleSupplier(t *testing.T) {
	r := ComputeRange([]SupplierCost{{SupplierID: 1, UnitCost: d("2.60075000"), Total: d("0.775")}})
	require.True(t, d("2.60075000").Equal(r.CostMin))
	require.True(t, d("2.60075000").Equal(r.CostMax))
	require.True(t, d("2.60075000").Equal(r.CostWeighted))
}

func TestComputeRange_Empty(t *testing.T) {
	r := ComputeRange(nil)
	require.True(t, r.CostMin.IsZero() && r.CostMax.IsZero() && r.CostWeighted.IsZero())
}

func TestComputeTrend_DailyLatestWins(t *testing.T) {
	loc := shanghaiLoc
	at := func(day, hour int) time.Time {
		return time.Date(2026, 9, day, hour, 0, 0, 0, loc)
	}
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, loc)
	versions := []VersionPoint{
		{ValidFrom: at(10, 9), UnitCost: d("2.60"), Version: 1},
		{ValidFrom: at(10, 15), UnitCost: d("2.55"), Version: 2}, // 同日更新 → 取 version 2
		{ValidFrom: at(12, 8), UnitCost: d("2.70"), Version: 3},
		{ValidFrom: at(1, 8), UnitCost: d("9.99"), Version: 9}, // 窗口外（days=7）
	}
	got := ComputeTrend(versions, 7, now)
	require.Len(t, got, 2)
	require.Equal(t, "2026-09-10", got[0].Date)
	require.True(t, d("2.55").Equal(got[0].UnitCost))
	require.Equal(t, 2, got[0].Version)
	require.Equal(t, "2026-09-12", got[1].Date)
	require.Equal(t, 3, got[1].Version)
}

func TestComputeTrend_NoInterpolation(t *testing.T) {
	loc := shanghaiLoc
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, loc)
	versions := []VersionPoint{
		{ValidFrom: time.Date(2026, 9, 10, 8, 0, 0, 0, loc), UnitCost: d("2.60"), Version: 1},
		// 9/11、9/12、9/13 没重算 → 不插值，不应出现
	}
	got := ComputeTrend(versions, 7, now)
	require.Len(t, got, 1)
	require.Equal(t, "2026-09-10", got[0].Date)
}

func TestComputeTrend_TimezoneDayBoundary(t *testing.T) {
	// UTC 2026-09-13T17:30 = 上海 9/14 01:30 → 归 9/14 这一天
	utc := time.Date(2026, 9, 13, 17, 30, 0, 0, time.UTC)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, shanghaiLoc)
	got := ComputeTrend([]VersionPoint{
		{ValidFrom: utc, UnitCost: d("2.60"), Version: 5},
	}, 7, now)
	require.Len(t, got, 1)
	require.Equal(t, "2026-09-14", got[0].Date)
}

func TestComputeTrend_Empty(t *testing.T) {
	require.Empty(t, ComputeTrend(nil, 7, time.Now()))
	require.Empty(t, ComputeTrend([]VersionPoint{{ValidFrom: time.Now()}}, 0, time.Now()))
}

func TestMarketBest(t *testing.T) {
	require.True(t, d("2.5").Equal(MarketBest([]decimal.Decimal{d("3.0"), d("2.5"), d("2.8")})))
	require.True(t, decimal.Zero.Equal(MarketBest(nil)))
}

func TestRepresentativeComponent(t *testing.T) {
	// 有 input → input
	comp, ok := RepresentativeComponent([]Component{
		{ComponentType: "output"}, {ComponentType: "input"}, {ComponentType: "cached_input"},
	})
	require.True(t, ok)
	require.Equal(t, "input", comp)
	// 无 input → 字母序第一个
	comp, ok = RepresentativeComponent([]Component{
		{ComponentType: "output"}, {ComponentType: "cached_input"},
	})
	require.True(t, ok)
	require.Equal(t, "cached_input", comp)
	// 空 → false
	_, ok = RepresentativeComponent(nil)
	require.False(t, ok)
}
