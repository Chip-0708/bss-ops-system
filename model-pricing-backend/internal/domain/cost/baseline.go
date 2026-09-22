// Package cost 的 baseline.go：成本基线不可变版本的领域模型与值未变判定。
// 契约 06-cost.md §0.1/§0.2/§0.4 与交接 §4：
//   - 列名是 version（不是 version_no），cost_component 列名是 supplier_cost（不是 source_unit_price）。
//   - 值未变判定：逐组件 unit_cost decimal.Equal 全精度比较（不用哈希，尾零敏感），
//     再加 loss_rate + channel_rate + primary_supplier_id + formula_version。
//     formula_version 参与判定——6c 切换算法时必须强制全量重算一次（交接 §4）。
//
// 公式常量锁死（交接 §6-1，单测验证）：
//
//	unit_cost = supplier_unit_price × (1+loss_rate) × (1+channel_rate)
//	floor     = unit_cost / (1 - min_gross_margin)
package cost

import (
	"errors"

	"github.com/shopspring/decimal"
)

// FormulaVersion 是完全成本公式的版本号（写 calc_snapshot.formula_version，
// 并参与值未变判定——改它等于强制全量重算一次，这是换公式时的预期行为）。
// 6d-v1：四因子主供应商选择（Total desc → RepresentCost asc → SupplierID asc）+
// 稳定性/配额/兼容三因子真实化（6b-v1 为「完全成本最低」占位口径）。
const FormulaVersion = "6d-v1"

// change_reason 枚举（06-cost §0.4，禁止自创——红线 4）。
const (
	ReasonQuoteEffective      = "QUOTE_EFFECTIVE"
	ReasonOfficialPriceChange = "OFFICIAL_PRICE_CHANGE"
	ReasonRetro               = "RETRO"
	ReasonParamChange         = "PARAM_CHANGE"
	ReasonManualLock          = "MANUAL_LOCK"
	ReasonDailyRecalc         = "DAILY_RECALC"
	ReasonSupplierSwitch      = "SUPPLIER_SWITCH"
	ReasonExpireRemove        = "EXPIRE_REMOVE"
)

// validChangeReasons 供请求侧校验（6b-4 + 后续批次用）。
var validChangeReasons = map[string]bool{
	ReasonQuoteEffective: true, ReasonOfficialPriceChange: true, ReasonRetro: true,
	ReasonParamChange: true, ReasonManualLock: true, ReasonDailyRecalc: true,
	ReasonSupplierSwitch: true, ReasonExpireRemove: true,
}

// ValidChangeReason 报告 change_reason 是否在 §0.4 枚举内。
func ValidChangeReason(s string) bool { return validChangeReasons[s] }

// ErrNoEffectiveQuote 该供应商在该 SKU 上当前没有任何 EFFECTIVE 报价，算不出成本。
// 6b-3 消费者把它当业务失败（置 FAILED 重试），不算系统错误。
var ErrNoEffectiveQuote = errors.New("该 SKU 当前无有效供应商报价，无法计算成本")

// Component 是成本基线的一个组件（对应 cost_component 行）。
type Component struct {
	ComponentType string
	// UnitCost 完全成本口径（unit_cost 列）。
	UnitCost decimal.Decimal
	// SupplierCost 供应商原始成本列（supplier_cost 列），溯源用。
	SupplierCost decimal.Decimal
}

// Baseline 是成本基线版本的核心字段（值未变判定与逐组件比较的全部输入）。
type Baseline struct {
	Version           int
	Currency          string
	PrimarySupplierID int64
	LossRate          decimal.Decimal
	ChannelRate       decimal.Decimal
	LockedManual      bool
	ChangeReason      string
	Components        []Component
	// FormulaVersion 该版本计算所用公式（calc_snapshot.formula_version 的同值冗余，参与判定）。
	FormulaVersion string
}

// Unchanged 值未变判定（06-cost §1/§10-4 + 交接 §4 + 6d-3 锁定状态判定）：
// 逐组件（component_type 对齐）unit_cost decimal.Equal 全精度比较，
// 再加 loss_rate / channel_rate / primary_supplier_id / formula_version / locked_manual 全同才跳过。
// 不用整体哈希——decimal 哈希对尾零敏感，而 decimal.Equal 是数值比较。
func Unchanged(prev *Baseline, next *Baseline) bool {
	if prev == nil || next == nil {
		return false
	}
	if prev.PrimarySupplierID != next.PrimarySupplierID {
		return false
	}
	// 6d-3：锁定状态参与值未变判定——「未锁→锁」或「锁→未锁」都必产新版本。
	// 没有这条，锁上了也会被判"值未变"静默跳过（6d-3 提示词陷阱 2）。
	if prev.LockedManual != next.LockedManual {
		return false
	}
	if !prev.LossRate.Equal(next.LossRate) || !prev.ChannelRate.Equal(next.ChannelRate) {
		return false
	}
	if prev.FormulaVersion != next.FormulaVersion {
		return false
	}
	if len(prev.Components) != len(next.Components) {
		return false
	}
	prevByType := make(map[string]decimal.Decimal, len(prev.Components))
	for _, c := range prev.Components {
		prevByType[c.ComponentType] = c.UnitCost
	}
	for _, c := range next.Components {
		old, ok := prevByType[c.ComponentType]
		if !ok || !old.Equal(c.UnitCost) {
			return false
		}
	}
	return true
}

// UnitCost 完全成本公式（6b 口径，不含税项、汇率不参与、不乘倍率——unit_price 已是折算后单价）：
//
//	unit_cost = unitPrice × (1+lossRate) × (1+channelRate)
//
// 注意乘数是 (1+loss)×(1+channel)，不是 (1+loss+channel)（交接 §6-1）。
func UnitCost(unitPrice, lossRate, channelRate decimal.Decimal) decimal.Decimal {
	return unitPrice.Mul(decimal.NewFromInt(1).Add(lossRate)).Mul(decimal.NewFromInt(1).Add(channelRate))
}

// Floor 销售侧唯一下限：floor = unitCost / (1 - minGrossMargin)。
// minGrossMargin 必须在 [0,1) 内；≥1 时除零，返回错误（调用方视为配置错误）。
func Floor(unitCost, minGrossMargin decimal.Decimal) (decimal.Decimal, error) {
	denom := decimal.NewFromInt(1).Sub(minGrossMargin)
	if denom.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, errors.New("min_gross_margin 必须小于 1")
	}
	return unitCost.Div(denom), nil
}

// FloorRate 计算 floor 时使用的除数 (1 - minGrossMargin)，供 floor 的同口径复核用。
func FloorRate(minGrossMargin decimal.Decimal) decimal.Decimal {
	return decimal.NewFromInt(1).Sub(minGrossMargin)
}
