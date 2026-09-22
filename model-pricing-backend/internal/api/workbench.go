// Package api 的 workbench.go：10a §2 待办聚合 + §3 指标卡。
//
// 权限模型（裁决 10）：已认证即可，**不挂模块权限点**——按角色自动裁剪。
// 数据域：中间件 DataScope 注入；服务层根据角色决定是否应用行级过滤（PLATFORM_ADMIN 看全部）。
package api

import (
	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/workbench"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// WorkbenchHandler 工作台处理器。
type WorkbenchHandler struct {
	workSvc *workbench.Service
}

// NewWorkbenchHandler 构造。
func NewWorkbenchHandler(workSvc *workbench.Service) *WorkbenchHandler {
	return &WorkbenchHandler{workSvc: workSvc}
}

// ListTodos 待办聚合。
//
// @Summary 工作台待办聚合（按角色裁剪）
// @Description 已认证即可；采购/销售/定价/财务看自己的待办（assignee_id=operator_id），PLATFORM_ADMIN 看全部。按 (biz_type,biz_id,priority) 去重。deeplink 按 biz_type 生成。
// @Tags 工作台
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param size query int false "每页" default(20)
// @Param biz_type query string false "业务类型（QUOTE / QUOTE_EXPIRE）"
// @Param status query string false "状态（OPEN / DONE）"
// @Success 200 {object} api.APIResponse "code=0；data=TodoListResult{list,total,page,size}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Router /api/internal/workbench/todos [get]
func (h *WorkbenchHandler) ListTodos(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	page, size := parsePageQuery(c)
	q := workbench.TodoQuery{
		BizType: c.Query("biz_type"),
		Status:  c.Query("status"),
		Page:    page,
		Size:    size,
	}
	res, err := h.workSvc.ListTodos(c.Request.Context(), op.Roles, op.OperatorID, q)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	response.Success(c, res)
}

// ListMetrics 指标卡。
//
// @Summary 工作台指标卡（按角色裁剪）
// @Description 已认证即可；按的角色裁剪：PROCUREMENT 4 卡（待审批报价/本月生效/30天内到期/单点依赖）、PRICING_OP 3 卡（待发布价目表/破 floor/成本上涨待决策）、SALES 3 卡（客户/待确认报价/季度成交额占位）、FINANCE 2 卡（汇率锁定占位/押金未付）。多角色取并集。**本接口不返回 unit_cost/floor_price/margin 等字段**（成本数据只在 service 内部用于破 floor 判定）。
// @Tags 工作台
// @Produce json
// @Security BearerAuth
// @Success 200 {object} api.APIResponse "code=0；data=MetricsResult{cards[]}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Router /api/internal/workbench/metrics [get]
func (h *WorkbenchHandler) ListMetrics(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	res, err := h.workSvc.ListMetrics(c.Request.Context(), op.Roles, op.OperatorID)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	response.Success(c, res)
}

// file ends here
