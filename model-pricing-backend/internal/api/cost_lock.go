// Package api 的 cost_lock.go：手动锁定主供应商接口（内部门户 M5，06-cost.md §6，6d-3）。
// 服务层二次鉴权用 perm.AnyRoleCan(op.Roles, perm.CanLockPrimary)——全角色 OR，
// 绝不只看 Roles[0]（smoke_admin 的真值形状 [PLATFORM_ADMIN, MODEL_OPS, PRICING_OP] 不含
// PROCUREMENT，必须被拒绝；buyer_a 的 [PROCUREMENT] 必须放行）。
package api

import (
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/cost"
	"model_bss/pkg/apperr"
	"model_bss/pkg/perm"
	"model_bss/pkg/response"
)

// CostLockHandler 是手动锁定主供应商的处理器。
type CostLockHandler struct {
	svc     *cost.LockService
	skuRead *cost.ReadService // 复用其 store 的 ResolveSKUID——{sku} 数字/code 解析口径与 §3 历史完全一致
}

// NewCostLockHandler 构造处理器（skuRead 为 nil 时 handler 内部会报错——
// 契约要求 {sku} 解析与 history 完全同口径，自己再写一份就是双倍漂移源）。
func NewCostLockHandler(svc *cost.LockService, skuRead *cost.ReadService) *CostLockHandler {
	return &CostLockHandler{svc: svc, skuRead: skuRead}
}

// lockPrimaryBody 是 POST body（reason 必填；supplier_id 必填且 >0）。
type lockPrimaryBody struct {
	SupplierID int64  `json:"supplier_id"`
	Reason     string `json:"reason"`
}

// LockPrimary 手动锁定主供应商。
//
// @Summary 手动锁定主供应商（M5:E + 服务层角色收敛：仅 PROCUREMENT）
// @Description 覆盖四因子算法结论，新版本 change_reason=MANUAL_LOCK、locked_manual=true。锁定是单向 sticky：后续任何触发（QUOTE_EFFECTIVE/PARAM_CHANGE/EXPIRE_REMOVE 等）都维持该锁定；若被锁供应商失效（冻结/该 SKU 无 EFFECTIVE 报价），后续重算明确报错、绝不自动降级或自动解锁。重复锁定同一家供应商 = 合法 no-op（不产新版本、不重复写审计）。本批不含解锁接口（见 CLAUDE.md 遗留 6d-3-①）。
// @Tags 成本管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param sku path string true "sku_id（纯数字）或 sku_code"
// @Param body body lockPrimaryBody true "锁定请求（supplier_id 必填，reason 必填）"
// @Success 200 {object} api.APIResponse "code=0；data={sku_id,version,primary_supplier_id,change_reason,locked:true,unchanged}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法（reason 空白/supplier_id<=0/sku 段为空）"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M5:E（中间件）或角色不含 PROCUREMENT（服务层）"
// @Failure 404 {object} api.APIResponse "code=10004 SKU 或 supplier 不存在"
// @Failure 409 {object} api.APIResponse "code=10005 该供应商在此 SKU 上无 EFFECTIVE 报价，或供应商处于冻结/失效状态"
// @Router /api/internal/cost/baselines/{sku}/lock-primary [post]
func (h *CostLockHandler) LockPrimary(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	// 服务层二次鉴权（CLAUDE.md 红线 + 06-cost §11-1）：M5:E 中间件只看权限点，
	// 6 角色都持 M5:E，必须再按角色 code 收敛到 PROCUREMENT。AnyRoleCan 扫**全角色**——
	// PLATFORM_ADMIN 即便手持 M5:E 也会被拒（它不持 PROCUREMENT）。
	if !perm.AnyRoleCan(op.Roles, perm.CanLockPrimary) {
		response.Error(c, apperr.New(
			apperr.ErrForbidden.Code,
			fmt.Sprintf("角色 %v 不含 PROCUREMENT，不允许锁定主供应商（M5:E 服务层角色收敛）", op.Roles),
			apperr.ErrForbidden.Status,
		))
		return
	}

	// {sku} 解析与 §3 历史完全同口径（纯数字按 id、否则按 sku_code；不存在 → 404）——
	// 走 ReadService 的 store 就是走同一段代码，绝不再写一份。
	sku := c.Param("sku")
	if sku == "" {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	// ReadService.History 已封装 sku→skuID 解析 + 404 语义，直接复用。
	// 只取解析（不调历史本身），避免无谓的查询开销。
	skuID, ok, err := h.skuReadResolveSKUID(c, sku)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	if !ok {
		response.Error(c, apperr.ErrNotFound)
		return
	}

	var body lockPrimaryBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("JSON 解析失败：%s", err.Error()), apperr.ErrInvalidParams.Status))
		return
	}
	if body.SupplierID <= 0 {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "supplier_id 必须 >0", apperr.ErrInvalidParams.Status))
		return
	}

	res, err := h.svc.LockPrimary(c.Request.Context(), cost.LockPrimaryInput{
		SkuID:      skuID,
		SupplierID: body.SupplierID,
		Reason:     body.Reason,
	}, cost.PutOperator{
		OperatorID:   op.OperatorID,
		OperatorRole: operatorRoleOf(op), // 只用于审计落库，绝不参与权限判定
	}, requestIDOf(c))
	if err != nil {
		response.Error(c, costLockErrToAppErr(err))
		return
	}
	response.Success(c, gin.H{
		"sku_id":              res.SkuID,
		"version":             res.Version,
		"primary_supplier_id": res.PrimarySupplierID,
		"change_reason":       res.ChangeReason,
		"locked":              res.Locked,
		"unchanged":           res.Unchanged,
	})
}

// skuReadResolveSKUID 透传 ReadService 的 {sku} 解析（handler 侧唯一入口）。
// 把它做成方法而不是字段访问，是为了在 NewCostLockHandler 没接 ReadService 时能
// 返回明确错误（编程错误）而不是 nil 指针 panic。
func (h *CostLockHandler) skuReadResolveSKUID(c *gin.Context, sku string) (int64, bool, error) {
	if h.skuRead == nil {
		return 0, false, fmt.Errorf("cost: NewCostLockHandler 未接 ReadService（无法解析 {sku}）")
	}
	return h.skuRead.ResolveSKUID(c.Request.Context(), sku)
}

// costLockErrToAppErr 把领域错误映射为 HTTP 错误码（契约 §6 明确列了 400/404/409 三类）。
func costLockErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, cost.ErrLockReasonEmpty):
		return apperr.New(apperr.ErrInvalidParams.Code, err.Error(), apperr.ErrInvalidParams.Status) // 400
	case errors.Is(err, cost.ErrLockSupplierNotFound):
		return apperr.New(apperr.ErrNotFound.Code, err.Error(), apperr.ErrNotFound.Status) // 404
	case errors.Is(err, cost.ErrLockNoEffectiveQuote),
		errors.Is(err, cost.ErrLockSupplierFrozen),
		errors.Is(err, cost.ErrLockedSupplierInvalid):
		return apperr.New(apperr.ErrIdempotency.Code, err.Error(), apperr.ErrIdempotency.Status) // 409
	default:
		return apperr.ErrSystem
	}
}
