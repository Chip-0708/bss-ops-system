// Package cost 的 compare.go：比价/成本区间/趋势/市场最低价的纯计算（不落库）。
// 契约 06-cost.md §4：
//   - range.cost_weighted 按四因子 total 加权，total 之和为 0 时退化为算术平均（不许除零）。
//   - trend 按天聚合取每日最新版本，不插值（§10-8）。
//   - market_best = 全市场该组件完全成本最低价。
//
// 注意：§4 接口本身是 6b 之后的批次实现（交接 §3「6b 不做 §4」），
// 本文件先把纯函数与单测锁住，供后续 handler 直接复用。
package cost

import (
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// SupplierCost 是比价视图里一家供应商的代表组件成本（计算输入）。
type SupplierCost struct {
	SupplierID int64
	// UnitCost 该供应商代表组件完全成本。
	UnitCost decimal.Decimal
	// Total 四因子加权总分（weight 用）。
	Total decimal.Decimal
}

// Range 是成本区间（§4 range；不落库）。
type Range struct {
	CostMin      decimal.Decimal
	CostMax      decimal.Decimal
	CostWeighted decimal.Decimal
}

// ComputeRange 计算成本区间。
// 加权平均 = Σ(unitCost×total) / Σ(total)；Σ(total)=0 或全零 → 退化为算术平均（不除零）。
// 空输入返回零值（调用方按"无报价"处理，不上接口）。
func ComputeRange(sups []SupplierCost) Range {
	if len(sups) == 0 {
		return Range{}
	}
	minV := sups[0].UnitCost
	maxV := sups[0].UnitCost
	sumW := decimal.Zero
	sumWX := decimal.Zero
	sumX := decimal.Zero
	for _, s := range sups {
		if s.UnitCost.LessThan(minV) {
			minV = s.UnitCost
		}
		if s.UnitCost.GreaterThan(maxV) {
			maxV = s.UnitCost
		}
		sumW = sumW.Add(s.Total)
		sumWX = sumWX.Add(s.UnitCost.Mul(s.Total))
		sumX = sumX.Add(s.UnitCost)
	}
	var weighted decimal.Decimal
	if sumW.IsPositive() {
		weighted = sumWX.Div(sumW)
	} else {
		weighted = sumX.Div(decimal.NewFromInt(int64(len(sups))))
	}
	return Range{CostMin: minV, CostMax: maxV, CostWeighted: weighted}
}

// TrendPoint 是成本趋势的一个点（§4 trend[]）。
type TrendPoint struct {
	Date     string // YYYY-MM-DD（Asia/Shanghai 日历日，由调用方决定时区后传入）
	UnitCost decimal.Decimal
	Version  int
}

// VersionPoint 是一个基线版本的时间戳输入。
type VersionPoint struct {
	ValidFrom time.Time
	UnitCost  decimal.Decimal
	Version   int
}

// shanghaiLoc 生效判定统一 Asia/Shanghai（红线 2）。
var shanghaiLoc = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		// 容器镜像缺 tzdata 时按固定 +8 兜底（与 PG 会话时区一致，确定性）。
		return time.FixedZone("Asia/Shanghai", 8*3600)
	}
	return loc
}()

// ComputeTrend 按天聚合取每日最新版本（§10-8：不插值）。
// versions 为任意顺序的历史版本（含已关闭的），now 决定窗口右端；
// 返回最近 days 天内、每天（Asia/Shanghai 日历日）最后生效的那个版本，按日期升序。
// 「那天没重算」则那天不出现（不插值）。
func ComputeTrend(versions []VersionPoint, days int, now time.Time) []TrendPoint {
	if days <= 0 || len(versions) == 0 {
		return nil
	}
	windowStart := now.In(shanghaiLoc).AddDate(0, 0, -days)
	byDay := make(map[string]VersionPoint)
	for _, v := range versions {
		t := v.ValidFrom.In(shanghaiLoc)
		if t.Before(windowStart) || t.After(now.In(shanghaiLoc)) {
			continue
		}
		day := t.Format("2006-01-02")
		cur, ok := byDay[day]
		if !ok || v.ValidFrom.After(cur.ValidFrom) ||
			(v.ValidFrom.Equal(cur.ValidFrom) && v.Version > cur.Version) {
			byDay[day] = v
		}
	}
	daysList := make([]string, 0, len(byDay))
	for day := range byDay {
		daysList = append(daysList, day)
	}
	sort.Strings(daysList)
	out := make([]TrendPoint, 0, len(daysList))
	for _, day := range daysList {
		v := byDay[day]
		out = append(out, TrendPoint{Date: day, UnitCost: v.UnitCost, Version: v.Version})
	}
	return out
}

// MarketBest 全市场该组件完全成本最低价（§4 market_best）。
// 空输入返回零值。isCurrent 过滤由调用方在装配输入时完成。
func MarketBest(unitCosts []decimal.Decimal) decimal.Decimal {
	if len(unitCosts) == 0 {
		return decimal.Zero
	}
	best := unitCosts[0]
	for _, c := range unitCosts[1:] {
		if c.LessThan(best) {
			best = c
		}
	}
	return best
}

// RepresentativeComponent 选代表组件（§10-2）：优先 input，无则按 component_type 字母序第一个。
// 空输入返回 ("", false)。
func RepresentativeComponent(components []Component) (string, bool) {
	if len(components) == 0 {
		return "", false
	}
	for _, c := range components {
		if c.ComponentType == "input" {
			return "input", true
		}
	}
	best := components[0].ComponentType
	for _, c := range components[1:] {
		if c.ComponentType < best {
			best = c.ComponentType
		}
	}
	return best, true
}
