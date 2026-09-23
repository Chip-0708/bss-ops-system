// Package api 的 change_request.go：变更单与审批进度的只读查询接口（联调 P1-4 / P1-5）。
//
// 背景：官方价变更（PRICE_UP / PRICE_DOWN）与价目表发布（PRICE_BOOK_PUBLISH / ROLLBACK）
// 此前只有建单与审批动作两个入口，页面刷新或换人后无法追踪审批进度（前端只能依赖
// 浏览器缓存的本次会话结果）。本文件补齐服务端权威查询。
//
// 权限：不挂模块权限点——与 /approvals/:id/decision 同口径（审批权由
// approval_step.required_role 决定，见 router.go 注释），查询只要求已认证。
package api

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"model_bss/internal/domain/model"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// ChangeRequestHandler 变更单查询 handler。
type ChangeRequestHandler struct {
	svc *model.Service
}

// NewChangeRequestHandler 构造 handler。
func NewChangeRequestHandler(svc *model.Service) *ChangeRequestHandler {
	return &ChangeRequestHandler{svc: svc}
}

// ListChangeRequests 变更单列表（支持类型 / 状态 / SKU 筛选 + 分页）。
//
// @Summary 变更单列表（含审批进度摘要）
// @Description 官方价变更单与价目表发布进度统一查询。type 取值：DEPRECATE / PRICE_UP / PRICE_DOWN / PRICE_BOOK_PUBLISH / PRICE_BOOK_ROLLBACK / SPECIAL_PRICE；status 取值：PENDING / APPROVED / REJECTED。返回每单的 steps_total / steps_approved / pending_step_no / pending_role。
// @Tags 变更单
// @Produce json
// @Security BearerAuth
// @Param type query string false "变更类型（空=全部）"
// @Param status query string false "状态（空=全部）"
// @Param sku_id query int false "SKU ID（空=全部）"
// @Param page query int false "页码（默认 1）"
// @Param size query int false "每页大小（默认 20，上限 200）"
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Router /api/internal/change-requests [get]
func (h *ChangeRequestHandler) ListChangeRequests(c *gin.Context) {
	q := model.ChangeRequestQuery{
		ChangeType: c.Query("type"),
		Status:     c.Query("status"),
	}
	if v := c.Query("sku_id"); v != "" {
		sku, err := strconv.ParseInt(v, 10, 64)
		if err != nil || sku <= 0 {
			response.Error(c, apperr.ErrInvalidParams)
			return
		}
		q.SKUID = &sku
	}
	q.Page, q.Size = parsePageQuery(c)

	res, err := h.svc.ListChangeRequests(c.Request.Context(), q)
	if err != nil {
		response.Error(c, modelErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// GetChangeRequest 变更单详情（含完整审批步骤链）。
//
// @Summary 变更单详情（含审批步骤链）
// @Description 前端按 change_request_id 恢复审批状态（价目表发布、官方价变更均适用），不再依赖浏览器缓存。
// @Tags 变更单
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "change_request.id"
// @Success 200 {object} api.APIResponse "code=0；data=ChangeRequestDetail（含 steps[]）"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 404 {object} api.APIResponse "code=10004 变更单不存在"
// @Router /api/internal/change-requests/{id} [get]
func (h *ChangeRequestHandler) GetChangeRequest(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	res, err := h.svc.GetChangeRequest(c.Request.Context(), id)
	if err != nil {
		response.Error(c, modelErrToAppErr(err))
		return
	}
	response.Success(c, res)
}
