// Package cost 的 param.go：成本参数（cost_param 表）的领域模型与三级解析。
// 契约 06-cost.md §7/§10-3：scope_type ∈ GLOBAL/MODEL/SUPPLIER，解析优先级
// SUPPLIER > MODEL > GLOBAL（最具体者优先）；GLOBAL 用 scope_id=0 哨兵（000016）。
// 三级都缺属于编程错误（GLOBAL 种子行在 000016 已落库），解析方 panic，不静默兜底。
package cost

import "github.com/shopspring/decimal"

// scope_type 枚举（chk_cost_param_scope 同集合）。
const (
	ScopeGlobal   = "GLOBAL"
	ScopeModel    = "MODEL"
	ScopeSupplier = "SUPPLIER"
)

// GlobalScopeID 是 GLOBAL 行的 scope_id 哨兵值（000016：UNIQUE(scope_type, scope_id)
// 对 NULL 失效，所以用 0 + chk_cost_param_global_zero 兜底）。
const GlobalScopeID int64 = 0

// Param 是一条成本参数（对应 cost_param 行）。
// 金额/比率一律 decimal，禁止 float64（红线 1）。
type Param struct {
	ScopeType string
	ScopeID   int64
	// LossRate 损耗系数（默认 0.0300）。
	LossRate decimal.Decimal
	// ChannelRate 通道费率（默认 0.0100）。
	ChannelRate decimal.Decimal
	// TaxInclusive / WithholdingTax 本阶段（6b）不参与计算（完全成本不含税项，
	// 交接 §4 决策），仅随 calc_snapshot 留痕。
	TaxInclusive   bool
	WithholdingTax decimal.Decimal
}

// ResolvedParam 是解析后的生效参数，ParamsScope 记录命中的层级（写 calc_snapshot.params_scope）。
type ResolvedParam struct {
	Param
	// ParamsScope 命中的层级：SUPPLIER / MODEL / GLOBAL。
	ParamsScope string
}

// StoredParam 是 cost_param 行的**原样**视图（Task 2 Option A 落地载体）。
// 比率三字段是**原始字符串**（不是 decimal）——直接透传 numeric(8,4) 入库的
// 字符（含尾零），读路径绝不二次规整。
//
// 为什么不用 decimal：
//   - decimal.String() 会 trim 尾零——"0.0300" → "0.03"，必然把 GET 打回原 P2 形态；
//   - 重算引擎仍需 decimal 数值，所以 Param 保持 decimal（计算用），StoredParam 是
//     **读侧专用**的字符串载体，二者并行存在，不互相替代。
//
// 写侧仍过 formatRate（StringFixed(4)）规整后落库——即库存值与 StoredParam 读出的
// 字符串在 numeric(8,4) 约束下天然恒等（库里恒存 4 位小数）。
type StoredParam struct {
	ScopeType      string
	ScopeID        int64
	LossRate       string // numeric(8,4) 原始入库字符串（含尾零，如 "0.0300"）
	ChannelRate    string
	TaxInclusive   bool
	WithholdingTax string
}

// StoredParamOf 把 Param（decimal 形态）转为 StoredParam——**仅供测试伪造器**模拟
// 「从库存取到的字符串」；生产代码绝不从这里构造读侧响应（生产路径用
// ParamStore.ListStoredParams 直接返回原生字符串）。
// 实现用 formatRate（StringFixed(4)）：与 numeric(8,4) 对齐——这正是库存恒定的形态。
func StoredParamOf(p Param) StoredParam {
	return StoredParam{
		ScopeType: p.ScopeType, ScopeID: p.ScopeID,
		LossRate:       formatRate(p.LossRate),
		ChannelRate:    formatRate(p.ChannelRate),
		TaxInclusive:   p.TaxInclusive,
		WithholdingTax: formatRate(p.WithholdingTax),
	}
}

// ResolveParams 按 SUPPLIER > MODEL > GLOBAL 解析生效参数（06-cost §10-3）。
// rows 是某 (modelID, supplierID) 下全部三级参数行（调用方一次性取出）。
// 返回 (命中行, true)；三级都缺 → (零值, false)，由调用方 panic（不静默兜底）。
func ResolveParams(rows []Param, modelID, supplierID int64) (ResolvedParam, bool) {
	var modelRow, globalRow *Param
	for i := range rows {
		r := &rows[i]
		switch r.ScopeType {
		case ScopeSupplier:
			if r.ScopeID == supplierID {
				return ResolvedParam{Param: *r, ParamsScope: ScopeSupplier}, true
			}
		case ScopeModel:
			if r.ScopeID == modelID {
				modelRow = r
			}
		case ScopeGlobal:
			globalRow = r
		}
	}
	if modelRow != nil {
		return ResolvedParam{Param: *modelRow, ParamsScope: ScopeModel}, true
	}
	if globalRow != nil {
		return ResolvedParam{Param: *globalRow, ParamsScope: ScopeGlobal}, true
	}
	return ResolvedParam{}, false
}
