// Package api 的 customer_special.go：9b §3/§4/§5 特价审批 + 刷新 + 导出。
//
// 权限：
//   - POST /customer-quotes/:id/special-price  M9:E + 幂等
//   - POST /customer-quotes/:id/refresh         M9:E + 幂等
//   - GET  /customer-quotes/:id/export          M9:V（直返文件流，不走统一响应信封）
//
// 导出例外说明已合入 docs/api/README.md §3（与 5b 模板下载同款直返）。
package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/customer"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// CustomerSpecialHandler 特价/刷新/导出。
type CustomerSpecialHandler struct {
	special *customer.SpecialPriceService
	refresh *customer.RefreshService
	export  *customer.ExportService
}

// NewCustomerSpecialHandler 构造。
func NewCustomerSpecialHandler(special *customer.SpecialPriceService, refresh *customer.RefreshService, export *customer.ExportService) *CustomerSpecialHandler {
	return &CustomerSpecialHandler{special: special, refresh: refresh, export: export}
}

// specialErrToAppErr 本 handler 的领域错误 → HTTP 错误码。
func specialErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, customer.ErrQuoteNotFound),
		errors.Is(err, customer.ErrQuoteForExportNotFound),
		errors.Is(err, customer.ErrQuoteForRefreshNotFound):
		return apperr.New(apperr.ErrNotFound.Code, err.Error(), apperr.ErrNotFound.Status) // 404
	case errors.Is(err, customer.ErrQuoteNotDraft),
		errors.Is(err, customer.ErrQuoteAlreadyPending),
		errors.Is(err, customer.ErrQuoteTerminal),
		errors.Is(err, customer.ErrQuoteExpired):
		return apperr.New(apperr.ErrIdempotency.Code, err.Error(), apperr.ErrIdempotency.Status) // 409 业务冲突
	case errors.Is(err, customer.ErrReasonRequired),
		errors.Is(err, customer.ErrExpectedMarginInvalid),
		errors.Is(err, customer.ErrExportFormatUnsupported):
		return apperr.New(apperr.ErrInvalidParams.Code, err.Error(), apperr.ErrInvalidParams.Status) // 400
	default:
		return apperr.ErrSystem
	}
}

// specialPriceBody 申请特价请求体。
type specialPriceBody struct {
	Reason         string `json:"reason" binding:"required"`
	ExpectedMargin string `json:"expected_margin" binding:"required"`
}

// RequestSpecialPrice 申请特价（M9:E + 幂等）。
//
// @Summary 申请特价（M9:E + 幂等必填 Idempotency-Key）
// @Description 把 DRAFT 客户报价标记为「特价审批中」：change_request[SPECIAL_PRICE] + 2 步 approval_step（PRICING_OP → FINANCE，biz_type=change_type）+ customer_quote.special_price_status='PENDING'。margin_impact={current_price,current_margin,target_price,target_margin,delta_gap_distance} 预演（裁决 2）。重复申请（special_price_status=PENDING）→ 409。
// @Tags 客户报价
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "customer_quote.id"
// @Param Idempotency-Key header string true "幂等键"
// @Param body body specialPriceBody true "reason / expected_margin 必填"
// @Success 200 {object} api.APIResponse "code=0；data=SpecialPriceResult{quote_id, change_request_id, step_count, status, margin_impact}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法（缺 reason/expected_margin、expected_margin 非 decimal）"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M9:E"
// @Failure 404 {object} api.APIResponse "code=10004 报价不存在"
// @Failure 409 {object} api.APIResponse "code=10005 报价非 DRAFT / 已在特价审批中（special_price_status=PENDING）"
// @Router /api/internal/customer-quotes/{id}/special-price [post]
func (h *CustomerSpecialHandler) RequestSpecialPrice(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "id 非法", apperr.ErrInvalidParams.Status))
		return
	}
	var body specialPriceBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("JSON 解析失败：%s", err.Error()), apperr.ErrInvalidParams.Status))
		return
	}
	res, err := h.special.Request(c.Request.Context(), customer.SpecialPriceInput{
		QuoteID:        id,
		Reason:         body.Reason,
		ExpectedMargin: body.ExpectedMargin,
	}, op.OperatorID, requestIDOf(c))
	if err != nil {
		response.Error(c, specialErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// refreshBody 刷新请求体。
type refreshBody struct {
	Reason string `json:"reason"`
}

// RefreshQuote 刷新客户报价（M9:E + 幂等）。
//
// @Summary 刷新客户报价（M9:E + 幂等必填 Idempotency-Key）
// @Description 按最新成本基线重算 floor_price；unit_price 不动。floor_price 变了才生成新版本（version_no+1，旧版本 EXPIRED）；值未变则 unchanged=true 且不产生新版本（changed_items=[]）。状态可刷新仅限 DRAFT/PENDING。
// @Tags 客户报价
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "customer_quote.id"
// @Param Idempotency-Key header string true "幂等键"
// @Param body body refreshBody false "reason 可选"
// @Success 200 {object} api.APIResponse "code=0；data=RefreshResult{quote_id, old_version_no, new_version_no, changed_items[], unchanged, status}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M9:E"
// @Failure 404 {object} api.APIResponse "code=10004 报价不存在"
// @Failure 409 {object} api.APIResponse "code=10005 报价态不可刷新（APPROVED/EFFECTIVE/EXPIRED/REJECTED）"
// @Router /api/internal/customer-quotes/{id}/refresh [post]
func (h *CustomerSpecialHandler) RefreshQuote(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "id 非法", apperr.ErrInvalidParams.Status))
		return
	}
	var body refreshBody
	// reason 可选：空 body 也允许（9b §4 没写 reason 必填）。
	if err := c.ShouldBindJSON(&body); err != nil {
		// 解析失败不是致命——可能没传 body；用空 reason 继续。
		body = refreshBody{}
	}
	res, err := h.refresh.Refresh(c.Request.Context(), customer.RefreshInput{
		QuoteID: id,
		Reason:  body.Reason,
	}, op.OperatorID, requestIDOf(c))
	if err != nil {
		response.Error(c, specialErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// ExportQuote 导出客户报价（M9:V，直返文件流）。
//
// @Summary 导出客户报价（M9:V；直返 XLSX 文件流，不是统一响应信封；本批仅支持 format=xlsx，PDF 登记 9b-② 遗留）
// @Description TEMP 报价且未过期（valid_until >= now）才能导出，文件含水印「临时报价，有效期至 X，仅限 Y 使用」。其余类型（APPLY/CLONE/SPECIAL/CONTRACT）不带水印。过期 → 409。
// @Tags 客户报价
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "customer_quote.id"
// @Param format query string false "格式：xlsx（本批仅支持）" default(xlsx)
// @Success 200 {file} binary "XLSX 文件流；Content-Type: application/vnd.openxmlformats-officedocument.spreadsheetml.sheet；Content-Disposition: attachment; filename=\"customer_quote_<id>.xlsx\""
// @Failure 400 {object} api.APIResponse "code=10001 参数非法 / format 不支持"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M9:V"
// @Failure 404 {object} api.APIResponse "code=10004 报价不存在"
// @Failure 409 {object} api.APIResponse "code=10005 TEMP 报价已过期"
// @Router /api/internal/customer-quotes/{id}/export [get]
func (h *CustomerSpecialHandler) ExportQuote(c *gin.Context) {
	if middleware.OperatorFrom(c) == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "id 非法", apperr.ErrInvalidParams.Status))
		return
	}
	format := c.DefaultQuery("format", "xlsx")

	data, err := h.export.GenerateXLSX(c.Request.Context(), id, format)
	if err != nil {
		response.Error(c, specialErrToAppErr(err))
		return
	}
	c.Data(http.StatusOK,
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		data)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="customer_quote_%d.%s"`, id, format))
}
