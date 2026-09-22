// Package perm 定义全部功能权限点常量（M1–M12 × V/E/A/C/P）。
//
// 权限点字符串与设计文档 §3.4 一一对应。AuthZ 中间件与各处 const 引用必须使用
// 本包的常量而非裸字符串，以保证拼写错误在服务启动时被一次性发现。
package perm

import (
	"fmt"
	"slices"
)

// 权限点常量格式：M{模块}.{操作}，操作码 V=查看 / E=编辑 / A=审批 / C=配置 / P=特权。
const (
	M1View      = "M1:V" // 模型管理·查看
	M1Edit      = "M1:E" // 模型管理·编辑
	M1Approve   = "M1:A" // 模型管理·审批
	M1Config    = "M1:C" // 模型管理·配置
	M1Privilege = "M1:P" // 模型管理·特权

	M2View      = "M2:V" // 官方价格同步·查看
	M2Edit      = "M2:E" // 官方价格同步·编辑
	M2Approve   = "M2:A" // 官方价格同步·审批
	M2Config    = "M2:C" // 官方价格同步·配置
	M2Privilege = "M2:P" // 官方价格同步·特权

	M3View      = "M3:V" // 供应商管理·查看
	M3Edit      = "M3:E" // 供应商管理·编辑
	M3Approve   = "M3:A" // 供应商管理·审批
	M3Config    = "M3:C" // 供应商管理·配置
	M3Privilege = "M3:P" // 供应商管理·特权

	M4View      = "M4:V" // 报价管理·查看
	M4Edit      = "M4:E" // 报价管理·编辑
	M4Approve   = "M4:A" // 报价管理·审批
	M4Config    = "M4:C" // 报价管理·配置
	M4Privilege = "M4:P" // 报价管理·特权

	M5View      = "M5:V" // 成本管理·查看
	M5Edit      = "M5:E" // 成本管理·编辑
	M5Approve   = "M5:A" // 成本管理·审批
	M5Config    = "M5:C" // 成本管理·配置
	M5Privilege = "M5:P" // 成本管理·特权

	M6View      = "M6:V" // 定价策略·查看
	M6Edit      = "M6:E" // 定价策略·编辑
	M6Approve   = "M6:A" // 定价策略·审批
	M6Config    = "M6:C" // 定价策略·配置
	M6Privilege = "M6:P" // 定价策略·特权

	M7View      = "M7:V" // 价目表·查看
	M7Edit      = "M7:E" // 价目表·编辑
	M7Approve   = "M7:A" // 价目表·审批
	M7Config    = "M7:C" // 价目表·配置
	M7Privilege = "M7:P" // 价目表·特权

	M8View      = "M8:V" // 客户管理·查看
	M8Edit      = "M8:E" // 客户管理·编辑
	M8Approve   = "M8:A" // 客户管理·审批
	M8Config    = "M8:C" // 客户管理·配置
	M8Privilege = "M8:P" // 客户管理·特权

	M9View      = "M9:V" // 客户报价·查看
	M9Edit      = "M9:E" // 客户报价·编辑
	M9Approve   = "M9:A" // 客户报价·审批
	M9Config    = "M9:C" // 客户报价·配置
	M9Privilege = "M9:P" // 客户报价·特权

	M10View      = "M10:V" // 财务结算·查看
	M10Edit      = "M10:E" // 财务结算·编辑
	M10Approve   = "M10:A" // 财务结算·审批
	M10Config    = "M10:C" // 财务结算·配置
	M10Privilege = "M10:P" // 财务结算·特权

	M11View      = "M11:V" // 组织与权限·查看
	M11Edit      = "M11:E" // 组织与权限·编辑
	M11Approve   = "M11:A" // 组织与权限·审批
	M11Config    = "M11:C" // 组织与权限·配置
	M11Privilege = "M11:P" // 组织与权限·特权

	M12View      = "M12:V" // 审计·查看
	M12Edit      = "M12:E" // 审计·编辑
	M12Approve   = "M12:A" // 审计·审批
	M12Config    = "M12:C" // 审计·配置
	M12Privilege = "M12:P" // 审计·特权
)

// All 返回全部 60 个权限点，供启动校验使用。
func All() []string {
	return append([]string{}, allPoints...)
}

// allPoints 是 All() 的实际数据源，仅在本包内使用。
var allPoints = []string{
	M1View, M1Edit, M1Approve, M1Config, M1Privilege,
	M2View, M2Edit, M2Approve, M2Config, M2Privilege,
	M3View, M3Edit, M3Approve, M3Config, M3Privilege,
	M4View, M4Edit, M4Approve, M4Config, M4Privilege,
	M5View, M5Edit, M5Approve, M5Config, M5Privilege,
	M6View, M6Edit, M6Approve, M6Config, M6Privilege,
	M7View, M7Edit, M7Approve, M7Config, M7Privilege,
	M8View, M8Edit, M8Approve, M8Config, M8Privilege,
	M9View, M9Edit, M9Approve, M9Config, M9Privilege,
	M10View, M10Edit, M10Approve, M10Config, M10Privilege,
	M11View, M11Edit, M11Approve, M11Config, M11Privilege,
	M12View, M12Edit, M12Approve, M12Config, M12Privilege,
}

// permStore 供 ValidateInDB 使用的数据访问抽象。
type permStore interface {
	AllPoints() ([]string, error)
}

// ValidateInDB 从存储加载权限点并与代码常量比对，防止拼写错误造成静默放行。
// 集合不一致（代码存在但 DB 缺失 / DB 存在但代码未声明）都会返回错误。
func ValidateInDB(store permStore) error {
	dbList, err := store.AllPoints()
	if err != nil {
		return fmt.Errorf("load permission points: %w", err)
	}

	codeSet := make(map[string]struct{}, len(allPoints))
	for _, p := range allPoints {
		codeSet[p] = struct{}{}
	}
	dbSet := make(map[string]struct{}, len(dbList))
	for _, p := range dbList {
		dbSet[p] = struct{}{}
	}

	var missing, extra []string
	for p := range codeSet {
		if _, ok := dbSet[p]; !ok {
			missing = append(missing, p)
		}
	}
	for p := range dbSet {
		if _, ok := codeSet[p]; !ok {
			extra = append(extra, p)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		return fmt.Errorf("permission point mismatch: code-but-not-in-db=%v, in-db-but-not-code=%v", missing, extra)
	}
	return nil
}

// ---- 角色 code 常量（服务层"按角色 code 收敛"用，pkg/perm 单点） ----
//
// 背景（06-cost.md §11-1）：M5:E 权限点在 000007_seed 里被 6 个角色持有
// （PRICING_OP / PROCUREMENT / FINANCE 应有，MODEL_OPS / RETRO_OP 是历史遗留越权）。
// 中间件 RequirePerm(M5:E) 只看权限点、无法区分这 6 个角色，服务层必须再按角色 code
// 收敛一层。如果散落在 service 里逐处写 `roleCode=="PRICING_OP"` 之类的字面值，
// 很容易漏写或多写——所以集中在本包，并用单测锁住允许/禁止集合。
const (
	RolePlatformAdmin = "PLATFORM_ADMIN"
	RoleModelOps      = "MODEL_OPS"
	RoleProcurement   = "PROCUREMENT"
	RolePricingOp     = "PRICING_OP"
	RoleSales         = "SALES"
	RoleFinance       = "FINANCE"
	RoleRetroOp       = "RETRO_OP"
	RoleSupplier      = "SUPPLIER"
	RoleCustomer      = "CUSTOMER"
	RoleOpsAdmin      = "OPS_ADMIN"
)

// CanEditCostParam 成本参数（cost_param 表）只允许 PRICING_OP 编辑。
// 中间件 RequirePerm(M5:E) 保第一层；本函数保第二层（越权角色即使持 M5:E 也拒绝）。
func CanEditCostParam(roleCode string) bool { return roleCode == RolePricingOp }

// CanLockPrimary 主供应商锁定（cost_baseline.locked_manual）只允许 PROCUREMENT 调用。
func CanLockPrimary(roleCode string) bool { return roleCode == RoleProcurement }

// CanLockFx 汇率锁定（fx_rate_lock）只允许 FINANCE 调用。
func CanLockFx(roleCode string) bool { return roleCode == RoleFinance }

// AnyRoleCan 对操作员的**全部角色**做 OR 判定：任一角色满足 single 即放行。
// 服务层二次鉴权必须用它而不是 Roles[0]——smoke_admin 的真值形状就是
// [PLATFORM_ADMIN MODEL_OPS PRICING_OP]，只看首个角色会把合法操作员 403 掉
// （operatorRoleOf 仅用于审计落库，绝不参与权限判定）。
func AnyRoleCan(roleCodes []string, single func(string) bool) bool {
	if single == nil {
		return false // 没有判定器绝不能放行
	}
	return slices.ContainsFunc(roleCodes, single)
}
