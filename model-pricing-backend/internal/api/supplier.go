// Package api 的 supplier.go：供应商门户接口（05-quotes.md §2 可报价 SKU 列表 / §3 提交报价 / §4 历史 / §5 详情）。
// 供应商身份只从登录态解析，绝不接受请求参数指定 supplier_id。
package api

import (
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/supplier"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// SupplierHandler 是供应商门户接口的处理器。
// quoteSvc 为可选依赖：nil 表示依赖未注入（单测场景），报价相关路由不注册。
type SupplierHandler struct {
	supplierSvc *supplier.Service
	quoteSvc    *supplier.QuoteService
}

// NewSupplierHandler 构造供应商处理器。
func NewSupplierHandler(supplierSvc *supplier.Service, quoteSvc *supplier.QuoteService) *SupplierHandler {
	return &SupplierHandler{supplierSvc: supplierSvc, quoteSvc: quoteSvc}
}

// resolveSupplier 从登录态解析供应商身份；失败时已写响应，返回 (nil, false)。
func (h *SupplierHandler) resolveSupplier(c *gin.Context) (*supplier.Supplier, *middleware.Operator, bool) {
	op := middleware.OperatorFrom(c)
	if op == nil || op.OperatorType != "SUPPLIER" {
		response.Error(c, apperr.ErrUnauthorized)
		return nil, nil, false
	}
	sup, err := h.supplierSvc.ResolveByOperator(c.Request.Context(), op.OperatorID)
	if err != nil {
		if errors.Is(err, supplier.ErrNotFound) {
			response.Error(c, apperr.ErrNotFound)
			return nil, nil, false
		}
		response.Error(c, apperr.ErrSystem)
		return nil, nil, false
	}
	return sup, op, true
}

// quoteErrToAppErr 把报价领域错误映射为 HTTP 错误码。
// 领域错误已携带可直接展示的中文 message，这里取 err.Error() 覆盖默认提示。
func quoteErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, supplier.ErrQuoteConflict):
		return apperr.New(apperr.ErrIdempotency.Code, err.Error(), apperr.ErrIdempotency.Status) // 409
	case errors.Is(err, supplier.ErrQuoteNotFound):
		return apperr.ErrNotFound
	case errors.Is(err, supplier.ErrQuoteItemsEmpty),
		errors.Is(err, supplier.ErrQuoteItemsTooMany),
		errors.Is(err, supplier.ErrQuoteDuplicateSku),
		errors.Is(err, supplier.ErrQuoteSkuNotQuotable),
		errors.Is(err, supplier.ErrQuoteValidToInvalid),
		errors.Is(err, supplier.ErrQuoteFxTierRequired),
		errors.Is(err, supplier.ErrQuoteFxTierInvalid),
		errors.Is(err, supplier.ErrQuoteMultiplierForbidden),
		errors.Is(err, supplier.ErrQuotePriceInconsistent),
		errors.Is(err, supplier.ErrQuotePriceInvalid),
		errors.Is(err, supplier.ErrQuoteComponentEmpty),
		errors.Is(err, supplier.ErrQuoteComponentTypeUnknown),
		errors.Is(err, supplier.ErrQuoteConstraintKeyUnknown):
		return apperr.New(apperr.ErrInvalidParams.Code, err.Error(), apperr.ErrInvalidParams.Status) // 400
	default:
		return apperr.ErrSystem
	}
}

// ListSupplierSKUs 供应商可报价 SKU 列表。
//
// @Summary 供应商可报价 SKU 列表
// @Description 返回 lifecycle_status ∈ (PUBLISHED, PURCHASABLE, PENDING_VERIFY) 的 SKU，含官方价基准只读列。不含他人报价/平台成本/毛利。
// @Tags 供应商门户
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param size query int false "每页" default(20)
// @Param keyword query string false "关键字"
// @Param vendor_id query int64 false "厂商筛选"
// @Param family_id query int64 false "系列筛选"
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 404 {object} api.APIResponse "code=10004 供应商档案不存在"
// @Router /api/supplier/skus [get]
func (h *SupplierHandler) ListSupplierSKUs(c *gin.Context) {
	if _, _, ok := h.resolveSupplier(c); !ok {
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	res, err := h.supplierSvc.ListSupplierSKUs(c.Request.Context(), supplier.ListSupplierSKUQuery{
		Keyword:  c.Query("keyword"),
		Page:     page,
		Size:     size,
		VendorID: parseInt64Query(c, "vendor_id"),
		FamilyID: parseInt64Query(c, "family_id"),
	})
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}

	response.Success(c, res)
}

// parseInt64Query 解析 int64 query 参数。
func parseInt64Query(c *gin.Context, key string) *int64 {
	v := c.Query(key)
	if v == "" {
		return nil
	}
	if id, err := strconv.ParseInt(v, 10, 64); err == nil {
		return &id
	}
	return nil
}

// SubmitQuote 提交报价。
//
// @Summary 提交报价（幂等、双向自洽复核、钳制生效时间）
// @Description 必须携带 Idempotency-Key。校验顺序按 05-quotes.md §3 九步：items 非空/≤500 行/SKU 不重复且在可报价集合内 → 过去时间静默钳制（响应 clamped:true）→ valid_to>valid_from → 币种锁定 → 汇率档位（USD 必填 8 档，CNY 传了置 null）→ 倍率-价格自洽复核（容差 1e-4，无官方价强制绝对价）→ 非终态互斥（409）→ 未知 component_type/constraints key 拒绝。落库单事务：quote_sheet(APPROVING, version_no=max+1) + items + components + todo_task + audit_log。
// @Tags 供应商门户
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param Idempotency-Key header string true "幂等键（客户端生成唯一串）"
// @Param body body supplier.SubmitQuoteInput true "报价单（valid_from/valid_to/items 必填）"
// @Success 200 {object} api.APIResponse "code=0；data={id,supplier_id,version_no,status,valid_from,valid_to,source,retroactive,clamped,item_count,submitted_at}"
// @Failure 400 {object} api.APIResponse "code=10001 校验失败（message 可直接展示）/ 同幂等键异 body"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 404 {object} api.APIResponse "code=10004 供应商档案不存在"
// @Failure 409 {object} api.APIResponse "code=10005 已存在审批中/待生效报价（非终态互斥）或幂等冲突"
// @Router /api/supplier/quotes [post]
func (h *SupplierHandler) SubmitQuote(c *gin.Context) {
	sup, op, ok := h.resolveSupplier(c)
	if !ok {
		return
	}

	var in supplier.SubmitQuoteInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}

	res, err := h.quoteSvc.SubmitQuote(c.Request.Context(), sup, in, op.OperatorID, requestIDOf(c))
	if err != nil {
		response.Error(c, quoteErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// ListQuoteHistory 报价历史。
//
// @Summary 报价历史（含审批结论）
// @Description 只返回登录主体自己的报价（服务层按 supplier_id 硬过滤，不接受入参指定）；含被覆盖/过期/驳回的全部历史版本。不含任何他人报价/平台售价/毛利。decision.result 取值 APPROVED/REJECTED，未审批为 null；驳回原因供应商可见。
// @Tags 供应商门户
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param size query int false "每页" default(20)
// @Param status query string false "状态筛选（§1 状态字典）"
// @Param sku_id query int64 false "SKU 筛选"
// @Param from query string false "生效时间下限（ISO 8601）"
// @Param to query string false "生效时间上限（ISO 8601）"
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}"
// @Failure 400 {object} api.APIResponse "code=10001 时间参数非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 404 {object} api.APIResponse "code=10004 供应商档案不存在"
// @Router /api/supplier/quotes/history [get]
func (h *SupplierHandler) ListQuoteHistory(c *gin.Context) {
	sup, _, ok := h.resolveSupplier(c)
	if !ok {
		return
	}

	q := supplier.QuoteHistoryQuery{
		Status: c.Query("status"),
		SkuID:  parseInt64Query(c, "sku_id"),
		Page:   mustAtoi(c.DefaultQuery("page", "1")),
		Size:   mustAtoi(c.DefaultQuery("size", "20")),
	}
	if v := c.Query("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "from 时间格式非法（需 ISO 8601 带时区）", 400))
			return
		}
		q.From = &t
	}
	if v := c.Query("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "to 时间格式非法（需 ISO 8601 带时区）", 400))
			return
		}
		q.To = &t
	}

	res, err := h.quoteSvc.ListQuoteHistory(c.Request.Context(), sup.ID, q)
	if err != nil {
		response.Error(c, quoteErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// GetQuote 报价详情。
//
// @Summary 报价详情
// @Description 越权（不是自己的报价单）返回 404（不返回 403，避免探测他人 ID）。响应含报价单头 + items[]（sku_code/model_name/currency/fx_tier/constraints/components[]）+ decision。不含官方价以外任何成本信息。
// @Tags 供应商门户
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "报价单 ID"
// @Success 200 {object} api.APIResponse "code=0；data=报价详情"
// @Failure 400 {object} api.APIResponse "code=10001 id 非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 404 {object} api.APIResponse "code=10004 报价单不存在或不属于当前供应商"
// @Router /api/supplier/quotes/{id} [get]
func (h *SupplierHandler) GetQuote(c *gin.Context) {
	sup, _, ok := h.resolveSupplier(c)
	if !ok {
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}

	detail, err := h.quoteSvc.GetQuoteDetail(c.Request.Context(), sup.ID, id)
	if err != nil {
		response.Error(c, quoteErrToAppErr(err))
		return
	}
	response.Success(c, detail)
}

// mustAtoi 解析 int query 参数；失败返回 0（由 service 层规整为默认值）。
func mustAtoi(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}
