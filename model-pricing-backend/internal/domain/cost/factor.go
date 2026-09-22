// Package cost 的 factor.go：四因子评分的纯函数（06-cost §1 表）。
// 6d 起主供应商 = 四因子总分最高者（6b 的「完全成本最低者」占位已退役——
// 见 §6.5 平手规则：Total desc → RepresentCost asc → SupplierID asc）。
// 权重常量为正式口径：价格 0.55 / 稳定性 0.25 / 配额 0.12 / 兼容 0.08（总和=1）。
package cost

import (
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// 四因子权重（06-cost §1：0.55/0.25/0.12/0.08，总和=1）。
var (
	weightPrice         = decimal.RequireFromString("0.55")
	weightStability     = decimal.RequireFromString("0.25")
	weightQuota         = decimal.RequireFromString("0.12")
	weightCompatibility = decimal.RequireFromString("0.08")
)

// neutralScore 缺失数据兜底中性分（新供应商稳定性 / 缺 rpm+tpm / 未声明兼容）。
var neutralScore = decimal.RequireFromString("0.5")

// FactorInput 是单家供应商评分的原始输入。
//
// 归一化与排序的输入是**完全成本**（UnitCost），不是原始报价——
// 用原始报价会算出 min/raw > 1 的荒谬归一化值（6b-2 复核踩过）。
// 原始报价（quote_component.unit_price）如需追溯，请走 calc_snapshot.scores[].supplier_cost，
// 那里只记录、不参与任何计算。
type FactorInput struct {
	SupplierID int64
	// UnitCost 该供应商代表组件的**完全成本**（归一化与排序的唯一输入）。
	UnitCost decimal.Decimal
	// FirstEffectiveFrom 该供应商在该 SKU 首次生效的 valid_from（稳定性起算，
	// 由 StabilitySince 对 EFFECTIVE/EXPIRED 区间断档聚合得出）；nil = 无历史 → 0.5。
	FirstEffectiveFrom *time.Time
	// RPM / TPM 配额原始值（constraints_.rpm / constraints_.tpm，可空）。
	RPM *int64
	TPM *int64
	// Compatibility constraints_.compatibility 原文（三级判定见 CompatibilityScore）。
	Compatibility string
}

// FactorScores 是单家供应商的四因子得分（归一化值写 calc_snapshot.scores）。
type FactorScores struct {
	SupplierID int64
	// 归一化值 [0,1]
	PriceNormalized     decimal.Decimal
	StabilityNormalized decimal.Decimal
	QuotaNormalized     decimal.Decimal
	CompatNormalized    decimal.Decimal
	// Total = 0.55×price + 0.25×stability + 0.12×quota + 0.08×compat
	Total decimal.Decimal
	// StabilitySince 本次评分所用的稳定性起算日（StabilityScore 的输入）。
	// 只用于写 calc_snapshot.scores[].stability_since 做可追溯，不参与任何计算。
	StabilitySince *time.Time
}

// PriceScore 价格归一化：min_cost / supplier_cost（最低价为 1）。
// minCost 为全体参与供应商的最小代表成本。minCost 为 0 或负 → 全 0（保护，不除零）。
func PriceScore(supplierCost, minCost decimal.Decimal) decimal.Decimal {
	if !minCost.IsPositive() || !supplierCost.IsPositive() {
		return decimal.Zero
	}
	return minCost.Div(supplierCost)
}

// StabilityScore 稳定性口径：min(1, days_effective/90)，从首次生效 valid_from 起算。
// first=nil（新供应商无历史）→ 0.5 中性，不惩罚 newcomers。
func StabilityScore(first *time.Time, now time.Time) decimal.Decimal {
	if first == nil {
		return neutralScore
	}
	days := now.Sub(*first).Hours() / 24
	if days < 0 {
		days = 0
	}
	v := decimal.NewFromFloat(days).Div(decimal.NewFromInt(90))
	if v.GreaterThan(decimal.NewFromInt(1)) {
		return decimal.NewFromInt(1)
	}
	return v
}

// QuotaScore 配额归一化：rpm/maxRpm，缺 rpm 用 tpm/maxTpm；两者都缺 → 0.5。
// max 为全体参与供应商的最大值；max<=0 → 0.5（无从归一，中性）。
func QuotaScore(rpm, tpm, maxRPM, maxTPM *int64) decimal.Decimal {
	if rpm != nil && maxRPM != nil && *maxRPM > 0 {
		return decimal.NewFromInt(*rpm).Div(decimal.NewFromInt(*maxRPM))
	}
	if tpm != nil && maxTPM != nil && *maxTPM > 0 {
		return decimal.NewFromInt(*tpm).Div(decimal.NewFromInt(*maxTPM))
	}
	return neutralScore
}

// compatIncompatible 是「显式不兼容」判词集合（06-cost §1 ⼗三级兼容口径）。
// trim + 小写化后命中任一值 → 0；空串 → 0.5（未声明）；其余非空 → 1.0。
// 含中文判词与机器可写判词（bool false / 数字 0 已在上游归一成字符串 "false" / "0"）。
var compatIncompatible = map[string]bool{
	"none": true, "false": true, "incompatible": true, "0": true,
	"不兼容": true, "不支持": true,
}

// CompatibilityScore 兼容判定（三级，06-cost §1）：
// 缺失或空串 → 0.5（未声明）；显式不兼容集合 → 0；其余非空 → 1.0。
func CompatibilityScore(compatibility string) decimal.Decimal {
	s := strings.ToLower(strings.TrimSpace(compatibility))
	if s == "" {
		return neutralScore
	}
	if compatIncompatible[s] {
		return decimal.Zero
	}
	return decimal.NewFromInt(1)
}

// TotalScore 加权总分：0.55×price + 0.25×stability + 0.12×quota + 0.08×compat。
func TotalScore(price, stability, quota, compat decimal.Decimal) decimal.Decimal {
	return price.Mul(weightPrice).
		Add(stability.Mul(weightStability)).
		Add(quota.Mul(weightQuota)).
		Add(compat.Mul(weightCompatibility))
}

// EffectiveRange 是供应商在某 SKU 上的一条生效区间（quote_sheet.valid_from/valid_to）。
// 包含 EFFECTIVE 与 EXPIRED（稳定性看的是历史连续性，与当前状态无关）。
type EffectiveRange struct {
	From time.Time
	To   time.Time
}

// StabilitySince 在生效区间集合上聚出「当前连续供货窗口」的起点（06-cost §1 稳定性口径）。
//
// 算法（全部为边界已锁的实现细节，改动请先读单测）：
//  1. 丢弃 To <= From 的脏区间（种子数据里实有 3 条，混入会把起点带偏）；
//  2. 剩余区间按 From 升序；From 相同 → To 大者在前（确定性，真库未出现并列）；
//  3. 从首个区间起连续延伸：start=R[0].From，curEnd=R[0].To；
//     后续区间若 From > curEnd（严格大于）判为断档，start 重置为该区间 From；
//     To == curEnd 属「首尾相接」不算断档（与半开区间 [from, to) 语义一致）。
//     延伸必须 curEnd = max(curEnd, R[i].To)——真库存在覆盖后续区间的长窗口
//     （sheet 22 To=09-18 覆盖 26/27），直接 curEnd=R[i].To 会把窗口往回缩，
//     让后面本应相接的区间被误判成断档。这是本函数最容易写错的一行，单测已锁。
//  4. 无有效区间 → nil（调用方走 0.5 兜底）；否则返回最后的 start。
func StabilitySince(ranges []EffectiveRange) *time.Time {
	valid := make([]EffectiveRange, 0, len(ranges))
	for _, r := range ranges {
		if !r.To.After(r.From) {
			continue // 脏区间（含 To==From 的零长度区间）
		}
		valid = append(valid, r)
	}
	if len(valid) == 0 {
		return nil
	}
	sort.Slice(valid, func(i, j int) bool {
		if valid[i].From.Equal(valid[j].From) {
			return valid[i].To.After(valid[j].To)
		}
		return valid[i].From.Before(valid[j].From)
	})
	start := valid[0].From
	curEnd := valid[0].To
	for _, r := range valid[1:] {
		if r.From.After(curEnd) {
			// 断档：供货连续性中断，从这条区间重新起算。
			// 注意不要用 >=：To == 下一区间 From 算首尾相接（半开区间语义，不算断档）。
			start = r.From
		}
		if r.To.After(curEnd) {
			curEnd = r.To
		}
	}
	s := start
	return &s
}

// ScoreSuppliers 对一组供应商算归一化得分（6d：四因子全部真实计算）。
// inputs 为全体参与评分的供应商（冻结排除在调用方完成），minCost 已算好传入防重复扫描；
// maxRPM/maxTPM 在函数内取同一集合的最大值（语义与 minCost 一致：只看能参与的）。
// now 由 CalcSKU 显式透传（补录/回溯场景不能用函数内 time.Now()）。
func ScoreSuppliers(inputs []FactorInput, minCost decimal.Decimal, now time.Time) []FactorScores {
	var maxRPM, maxTPM *int64
	for i := range inputs {
		if inputs[i].RPM != nil && (maxRPM == nil || *inputs[i].RPM > *maxRPM) {
			v := *inputs[i].RPM
			maxRPM = &v
		}
		if inputs[i].TPM != nil && (maxTPM == nil || *inputs[i].TPM > *maxTPM) {
			v := *inputs[i].TPM
			maxTPM = &v
		}
	}
	out := make([]FactorScores, 0, len(inputs))
	for _, in := range inputs {
		s := FactorScores{
			SupplierID:          in.SupplierID,
			PriceNormalized:     PriceScore(in.UnitCost, minCost),
			StabilityNormalized: StabilityScore(in.FirstEffectiveFrom, now),
			QuotaNormalized:     QuotaScore(in.RPM, in.TPM, maxRPM, maxTPM),
			CompatNormalized:    CompatibilityScore(in.Compatibility),
			StabilitySince:      in.FirstEffectiveFrom,
		}
		s.Total = TotalScore(s.PriceNormalized, s.StabilityNormalized, s.QuotaNormalized, s.CompatNormalized)
		out = append(out, s)
	}
	return out
}
