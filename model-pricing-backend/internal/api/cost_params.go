// Package api 的 cost_params.go：成本参数读写接口（内部门户 M5，06-cost.md §7）。
// GET 输出「defaults + overrides[]」；PUT 全量替换 overrides（defaults 本批次只读）。
// 服务层二次鉴权用 perm.AnyRoleCan（全角色 OR）——operatorRoleOf 只用于审计，
// 绝不用于权限判定（CLAUDE.md 项目红线）。
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/cost"
	"model_bss/pkg/apperr"
	"model_bss/pkg/perm"
	"model_bss/pkg/response"
)

// CostParamHandler 是成本参数读写的处理器。
type CostParamHandler struct {
	svc *cost.ParamService
}

// NewCostParamHandler 构造处理器。
func NewCostParamHandler(svc *cost.ParamService) *CostParamHandler {
	return &CostParamHandler{svc: svc}
}

// ListCostParams 成本参数读取（全局默认 + 全部覆盖）。
//
// @Summary 成本参数读取（M5:V，无归属过滤）
// @Description 返回 defaults（GLOBAL 行）+ overrides[]（MODEL/SUPPLIER 行，按 scope_type, scope_id 升序）。所有比率字段都是**数值字符串**，透传 DB numeric(8,4) 入库原字符（含尾零，如 "0.0300"）——前端必须按数值比较（parseFloat/decimal 比较），**不要按字符串相等比较**（"0.0300" 与 "0.03" 数值同但字符串异）。本批次 defaults 只读（6d-2 遗留项①）。
// @Tags 成本管理
// @Produce json
// @Security BearerAuth
// @Success 200 {object} api.APIResponse "code=0；data={defaults:{loss_rate,channel_rate,tax_inclusive,withholding_tax},overrides:[…]}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M5:V"
// @Router /api/internal/cost/params [get]
func (h *CostParamHandler) ListCostParams(c *gin.Context) {
	view, err := h.svc.ListParams(c.Request.Context())
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	// 成本参数对内部 M5:V 持有者全可见（无字段剔除需求；000017 只剔 unit_cost 系列）。
	// 仍走 maskAndSuccess 以与只读接口同一挂接点保持一致（成本接口家族惯例）。
	maskAndSuccess(c, view)
}

// UpdateCostParams 成本参数全量替换（只写 overrides）。
//
// @Summary 成本参数全量替换（M5:E + 服务层角色收敛：仅 PRICING_OP）
// @Description 全量替换语义：body.overrides 是替换后的完整覆盖集（[] 合法清空）；事务内 DELETE MODEL/SUPPLIER 全量 + 逐行 INSERT + 审计 + 逐受影响 SKU 入队 COST_RECALC。body.defaults 本批次只读——传入即 400（绝不静默忽略）；body 必须带 overrides 键（缺失即 400）。费率必须 0~1、小数位 ≤4、能解析为十进制；scope_id 必须在对应表真实存在；同一 (scope_type,scope_id) 重复即 400。所有变更**同步触发**受影响 SKU 的成本重算（PARAM_CHANGE）。
// @Tags 成本管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body cost.PutParamsInput true "全量替换请求体（overrides 必填）"
// @Success 200 {object} api.APIResponse "code=0；data={overrides_count,submitted_tasks,task_ids,audit_log_id}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法（含 defaults 非空 / overrides 缺失 / scope_type 非 MODEL/SUPPLIER / scope_id 不存在 / 重复 / 费率非法）"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M5:E（中间件）或角色不含 PRICING_OP（服务层）"
// @Router /api/internal/cost/params [put]
func (h *CostParamHandler) UpdateCostParams(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}

	// 服务层二次鉴权（CLAUDE.md 红线：M5:E 中间件只看权限点，6 角色都持 M5:E，
	// 必须再按角色 code 收敛到 PRICING_OP）。AnyRoleCan 扫**全角色**——
	// smoke_admin 的真值形状 [PLATFORM_ADMIN MODEL_OPS PRICING_OP] 正是靠这个放行。
	// 注意：如果中间件层已 403，这里根本到不了——此分支仅当「中间件放行但角色不含
	// PRICING_OP」（如 FINANCE / MODEL_OPS 单角色账号）时触发。
	if !perm.AnyRoleCan(op.Roles, perm.CanEditCostParam) {
		response.Error(c, apperr.New(
			apperr.ErrForbidden.Code,
			fmt.Sprintf("角色 %v 不含 PRICING_OP，不允许修改成本参数（M5:E 服务层角色收敛）", op.Roles),
			apperr.ErrForbidden.Status,
		))
		return
	}

	// 用 json.RawMessage 先收一层，区分「缺 overrides 键」与「overrides=[]」。
	// gin 的 ShouldBindJSON 会把缺失键和显式 null 都编成 nil 切片，无法区分——
	// 裁决 4 要求区分，所以先取 raw body 再手工解码。
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	var raw struct {
		Defaults  *cost.ParamViewCost  `json:"defaults"`
		Overrides *[]cost.OverrideItem `json:"overrides"`
	}
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("JSON 解析失败：%s", err.Error()), apperr.ErrInvalidParams.Status))
		return
	}

	res, err := h.svc.ReplaceOverrides(c.Request.Context(), cost.PutParamsInput{
		Defaults:  raw.Defaults,
		Overrides: raw.Overrides,
	}, cost.PutOperator{
		OperatorID:   op.OperatorID,
		OperatorRole: operatorRoleOf(op), // 只用于审计落库，绝不参与权限判定
	}, requestIDOf(c))
	if err != nil {
		response.Error(c, costParamErrToAppErr(err))
		return
	}
	response.Success(c, gin.H{
		"overrides_count": res.OverridesCount,
		"submitted_tasks": res.SubmittedTasks,
		"task_ids":        res.TaskIDs,
		"audit_log_id":    res.AuditLogID,
	})
}

// costParamErrToAppErr 把领域错误映射为 HTTP 错误码（全部 400，消息透传）。
func costParamErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, cost.ErrDefaultsReadOnly),
		errors.Is(err, cost.ErrOverridesKeyMissing),
		errors.Is(err, cost.ErrScopeTypeInvalid),
		errors.Is(err, cost.ErrScopeNotFound),
		errors.Is(err, cost.ErrDuplicateScope),
		errors.Is(err, cost.ErrRateInvalid):
		return apperr.New(apperr.ErrInvalidParams.Code, err.Error(), apperr.ErrInvalidParams.Status)
	default:
		return apperr.ErrSystem
	}
}
