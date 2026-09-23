package api

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/supplier"
	"model_bss/pkg/apperr"
	"model_bss/pkg/fieldmask"
	"model_bss/pkg/perm"
	"model_bss/pkg/response"
)

var supplierCommercialFields = []string{
	"settle_type", "billing_cycle", "min_recharge", "credit_line", "credit_used", "deposit_amount",
}

func visibleSupplierProfile(detail *supplier.ProfileDetail, op *middleware.Operator) interface{} {
	mask := append([]string(nil), op.FieldMask...)
	if !perm.CanReadSupplierCommercial(op.Roles, op.StaffID, detail.OwnerProcurementOperatorID) {
		mask = append(mask, supplierCommercialFields...)
	}
	return fieldmask.Apply(detail, mask)
}

// SupplierProfileHandler 提供内部门户 M3 供应商档案只读查询。
type SupplierProfileHandler struct{ svc *supplier.ProfileService }

func NewSupplierProfileHandler(svc *supplier.ProfileService) *SupplierProfileHandler {
	return &SupplierProfileHandler{svc: svc}
}

// ListProfiles 返回数据域内的供应商列表。
// @Summary 供应商列表（M3:V，按归属采购过滤）
// @Tags 供应商管理
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param size query int false "每页（上限 100）" default(20)
// @Param keyword query string false "法人名称关键字"
// @Param qual_status query string false "VALID/EXPIRING/FROZEN"
// @Param status query string false "ACTIVE/INACTIVE"
// @Success 200 {object} api.APIResponse "data={list,total,page,size}"
// @Router /api/internal/suppliers [get]
func (h *SupplierProfileHandler) ListProfiles(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	q := supplier.ProfileQuery{
		Keyword: c.Query("keyword"), QualStatus: c.Query("qual_status"), Status: c.Query("status"),
		Page: mustAtoi(c.DefaultQuery("page", "1")), Size: mustAtoi(c.DefaultQuery("size", "20")),
	}
	res, err := h.svc.List(c.Request.Context(), ownerScopeOf(op), q)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	maskAndSuccess(c, res)
}

// GetProfile 返回数据域内的供应商详情；不存在 404，超出数据域 403。
// @Summary 供应商档案详情（M3:V，按归属采购过滤）
// @Tags 供应商管理
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "供应商 ID"
// @Success 200 {object} api.APIResponse "data=供应商档案"
// @Failure 403 {object} api.APIResponse "超出数据域"
// @Failure 404 {object} api.APIResponse "供应商不存在"
// @Router /api/internal/suppliers/{id} [get]
func (h *SupplierProfileHandler) GetProfile(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	res, err := h.svc.Get(c.Request.Context(), ownerScopeOf(op), id)
	if errors.Is(err, supplier.ErrNotFound) {
		response.Error(c, apperr.ErrNotFound)
		return
	}
	if errors.Is(err, supplier.ErrProfileOutOfScope) {
		response.Error(c, apperr.ErrForbidden)
		return
	}
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	response.Success(c, visibleSupplierProfile(res, op))
}
