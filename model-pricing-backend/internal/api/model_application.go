// Package api 的 model_application.go：新模型申请的供应商侧与内部侧接口。
//
// 契约（设计文档 §8.5 接口清单 + §3.3 状态总表）：
//
//	供应商侧  POST /api/supplier/model-applications        提交申请（幂等）
//	          GET  /api/supplier/model-applications        申请进度与驳回原因（行级过滤）
//	内部侧    GET  /api/internal/model-applications        列表（M1:V，支持状态/供应商筛选）
//	          POST /api/internal/model-applications/:id/decision   审核（M1:E，幂等）
//
// 状态机：SUBMITTED → APPROVED（已入库）/ MERGED（已合并）/ REJECTED（已关闭）。
// 终态不可再审核（由 repo 的条件更新保证，并发双审只有一个成功）。
package api

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/supplier"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// applicationErrToAppErr 申请域错误 → 统一错误码。
func applicationErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, supplier.ErrApplicationInvalid):
		return apperr.ErrInvalidParams
	case errors.Is(err, supplier.ErrApplicationNotFound), errors.Is(err, supplier.ErrTargetSKUNotFound):
		return apperr.ErrNotFound
	case errors.Is(err, supplier.ErrApplicationConflict):
		// 终态已决 / 并发双审：语义是"状态冲突，禁止重复提交"。
		return apperr.ErrIdempotency
	case errors.Is(err, supplier.ErrNotFound):
		return apperr.ErrNotFound
	}
	return apperr.ErrSystem
}

// SupplierApplicationHandler 供应商侧新模型申请。
type SupplierApplicationHandler struct {
	supplierSvc *supplier.Service
}

// NewSupplierApplicationHandler 构造供应商侧 handler。
func NewSupplierApplicationHandler(svc *supplier.Service) *SupplierApplicationHandler {
	return &SupplierApplicationHandler{supplierSvc: svc}
}

// SubmitApplication 提交新模型申请。
//
// @Summary 提交新模型申请
// @Description 供应商提交拟引入的新模型。payload 为自由结构（能力/上下文/模态/期望定价等）。提交时按 model_name 与已有 SKU 做相似度查重，结果落在 dup_top3 供内部审核参考（只提示不拦截）。
// @Tags 供应商-新模型申请
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param Idempotency-Key header string true "幂等键"
// @Param body body supplier.SubmitApplicationInput true "{model_name, vendor_id?, payload}"
// @Success 200 {object} api.APIResponse "code=0；data=ModelApplication（status=SUBMITTED）"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法（model_name/payload 缺失或非 JSON 对象）"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 非供应商主体"
// @Router /api/supplier/model-applications [post]
func (h *SupplierApplicationHandler) SubmitApplication(c *gin.Context) {
	sup, op, ok := resolveSupplierFrom(c, h.supplierSvc)
	if !ok {
		return
	}
	var in supplier.SubmitApplicationInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	res, err := h.supplierSvc.SubmitModelApplication(
		c.Request.Context(), sup.ID, in, op.OperatorID, requestIDOf(c))
	if err != nil {
		response.Error(c, applicationErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// ListApplications 我的申请列表（申请进度与驳回原因）。
//
// @Summary 我的模型申请列表
// @Description 行级过滤：仅返回本供应商主体的申请。支持 status 筛选（SUBMITTED/MERGED/APPROVED/REJECTED）。
// @Tags 供应商-新模型申请
// @Produce json
// @Security BearerAuth
// @Param status query string false "状态筛选（空=全部）"
// @Param page query int false "页码（默认 1）"
// @Param size query int false "每页大小（默认 20，上限 200）"
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Router /api/supplier/model-applications [get]
func (h *SupplierApplicationHandler) ListApplications(c *gin.Context) {
	sup, _, ok := resolveSupplierFrom(c, h.supplierSvc)
	if !ok {
		return
	}
	page, size := parsePageQuery(c)
	res, err := h.supplierSvc.ListModelApplications(c.Request.Context(), sup.ID, supplier.ApplicationQuery{
		Status: c.Query("status"),
		Page:   page,
		Size:   size,
	})
	if err != nil {
		response.Error(c, applicationErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// ModelApplicationHandler 内部侧申请审核。
type ModelApplicationHandler struct {
	supplierSvc *supplier.Service
}

// NewModelApplicationHandler 构造内部侧 handler。
func NewModelApplicationHandler(svc *supplier.Service) *ModelApplicationHandler {
	return &ModelApplicationHandler{supplierSvc: svc}
}

// ListApplications 内部列表（跨供应商，支持状态/供应商筛选）。
//
// @Summary 模型申请列表（内部）
// @Description 模型运营查看全部供应商的申请，支持按状态与供应商筛选。dup_top3 为提交时的查重候选，供审核参考。
// @Tags 模型申请
// @Produce json
// @Security BearerAuth
// @Param status query string false "状态筛选（空=全部）"
// @Param supplier_id query int false "供应商 ID（空=全部）"
// @Param page query int false "页码（默认 1）"
// @Param size query int false "每页大小（默认 20，上限 200）"
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M1:V 权限"
// @Router /api/internal/model-applications [get]
func (h *ModelApplicationHandler) ListApplications(c *gin.Context) {
	q := supplier.ApplicationQuery{Status: c.Query("status")}
	if v := c.Query("supplier_id"); v != "" {
		sid, err := strconv.ParseInt(v, 10, 64)
		if err != nil || sid <= 0 {
			response.Error(c, apperr.ErrInvalidParams)
			return
		}
		q.SupplierID = &sid
	}
	q.Page, q.Size = parsePageQuery(c)

	res, err := h.supplierSvc.ListModelApplicationsInternal(c.Request.Context(), q)
	if err != nil {
		response.Error(c, applicationErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// DecideApplication 审核申请（APPROVE 入库 / MERGE 合并 / REJECT 驳回）。
//
// @Summary 审核模型申请
// @Description APPROVE / MERGE 必须指定 target_sku_id（指向已存在的 model_sku）；REJECT 必须填 reason。仅 SUBMITTED 可审核，终态再次审核返回 10005。
// @Tags 模型申请
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "model_application.id"
// @Param Idempotency-Key header string true "幂等键"
// @Param body body supplier.ApplicationDecisionInput true "{action, target_sku_id?, reason?}"
// @Success 200 {object} api.APIResponse "code=0；data=ModelApplication（status 已推进）"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法（缺 target_sku_id / reason）"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M1:E 权限"
// @Failure 404 {object} api.APIResponse "code=10004 申请或目标 SKU 不存在"
// @Failure 409 {object} api.APIResponse "code=10005 申请已终结（不可重复审核）"
// @Router /api/internal/model-applications/{id}/decision [post]
func (h *ModelApplicationHandler) DecideApplication(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	var in supplier.ApplicationDecisionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	op := middleware.OperatorFrom(c)
	operatorID := int64(0)
	if op != nil {
		operatorID = op.OperatorID
	}
	res, err := h.supplierSvc.DecideModelApplication(
		c.Request.Context(), id, in, operatorID, requestIDOf(c))
	if err != nil {
		response.Error(c, applicationErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}
