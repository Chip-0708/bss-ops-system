// Package api 的 customer_portal.go：9c §6 客户门户 6 接口。
//
// 权限模型（裁决 7）：
//   - **不挂模块权限点**——靠 AuthN（会话有效）+ owner_id 行级过滤
//     （OperatorID == customer_id）。
//   - CUSTOMER 门户账号没有内部角色，RequirePerm 会一律 403。
//
// 字段剔除（裁决 6）：
//   - 客户门户所有 DTO 在领域层就**不返回**成本/毛利字段
//     （unit_cost / floor_price / margin / baseline / cost_before / cost_after / cost_delta_pct）；
//   - 本 handler 不调用 fieldmask.Apply——因为 DTO 物理上不存在这些 key，
//     无需运行时剔除。详情见 docs/api/09-customer-quote.md §6 末尾。
package api

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/customer"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// CustomerPortalHandler 客户门户处理器。
type CustomerPortalHandler struct {
	portalSvc *customer.PortalService
}

// NewCustomerPortalHandler 构造。
func NewCustomerPortalHandler(portalSvc *customer.PortalService) *CustomerPortalHandler {
	return &CustomerPortalHandler{portalSvc: portalSvc}
}

// resolveCustomer 从登录态解析客户身份（CUSTOMER portal + OperatorID 即 customer_id）。
// 失败时已写响应，返回 (0, nil, false)。
func (h *CustomerPortalHandler) resolveCustomer(c *gin.Context) (int64, *middleware.Operator, bool) {
	op := middleware.OperatorFrom(c)
	if op == nil || op.OperatorType != "CUSTOMER" {
		response.Error(c, apperr.ErrUnauthorized)
		return 0, nil, false
	}
	return op.OperatorID, op, true
}

// portalErrToAppErr 领域错误 → HTTP。
func portalErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, customer.ErrPortalQuoteNotFound),
		errors.Is(err, customer.ErrPortalNoEffectivePriceBook),
		errors.Is(err, customer.ErrPortalCustomerNotFound):
		return apperr.New(apperr.ErrNotFound.Code, err.Error(), apperr.ErrNotFound.Status) // 404
	case errors.Is(err, customer.ErrPortalQuoteNotApprovable),
		errors.Is(err, customer.ErrPortalQuoteExpired):
		return apperr.New(apperr.ErrIdempotency.Code, err.Error(), apperr.ErrIdempotency.Status) // 409 业务冲突
	default:
		return apperr.ErrSystem
	}
}

// parsePageQuery 通用 page/size 解析。
func parsePageQuery(c *gin.Context) (page, size int) {
	page = 1
	size = 20
	if p := c.Query("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}
	if s := c.Query("size"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 200 {
			size = n
		}
	}
	return
}

// GetPriceBook 当前生效价目表。
//
// @Summary 当前生效价目表（本等级，分币种）
// @Description 按当前登录客户的 level_code 返回当前 EFFECTIVE 价目表（联 model_sku + 代表组件 unit_price）。**不含 floor_price / unit_cost / margin / baseline**。
// @Tags 客户门户
// @Produce json
// @Security BearerAuth
// @Success 200 {object} api.APIResponse "code=0；data=PortalPriceBookView{level_code, version_no, currency, items[]}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 404 {object} api.APIResponse "code=10004 当前等级暂无生效价目表 / 客户不存在"
// @Router /api/customer/price-book [get]
func (h *CustomerPortalHandler) GetPriceBook(c *gin.Context) {
	customerID, _, ok := h.resolveCustomer(c)
	if !ok {
		return
	}
	view, err := h.portalSvc.GetPriceBook(c.Request.Context(), customerID)
	if err != nil {
		response.Error(c, portalErrToAppErr(err))
		return
	}
	response.Success(c, view)
}

// ListQuotes 我的报价与合同。
//
// @Summary 我的报价与合同
// @Description 返回当前客户的 customer_quote（按 id DESC 分页）+ customer_price_book（合同价，SourceKind=CONTRACT）联合列表。只含自己的报价（行级过滤）。
// @Tags 客户门户
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param size query int false "每页" default(20)
// @Success 200 {object} api.APIResponse "code=0；data=PortalQuoteListResult{list, total, page, size}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Router /api/customer/quotes [get]
func (h *CustomerPortalHandler) ListQuotes(c *gin.Context) {
	customerID, _, ok := h.resolveCustomer(c)
	if !ok {
		return
	}
	page, size := parsePageQuery(c)
	res, err := h.portalSvc.ListQuotes(c.Request.Context(), customerID, customer.PortalQuoteQuery{Page: page, Size: size})
	if err != nil {
		response.Error(c, portalErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// AcceptQuote 接受报价 → 转合同价（幂等）。
//
// @Summary 接受报价 → 转合同价（幂等）
// @Description 报价必须是 APPROVED 状态（或 special_price_status='APPROVED' 的特价单）且未过 valid_until；行级过滤 customer_id=登录客户。单事务：customer_quote.status→EFFECTIVE + INSERT customer_price_book + audit CUSTOMER_QUOTE_ACCEPTED。
// @Tags 客户门户
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "customer_quote.id"
// @Param Idempotency-Key header string true "幂等键"
// @Success 200 {object} api.APIResponse "code=0；data=PortalAcceptResult{quote_id, new_status, contract_cnt}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 404 {object} api.APIResponse "code=10004 报价不存在（或不是自己的报价）"
// @Failure 409 {object} api.APIResponse "code=10005 报价非 APPROVED / 报价已过期"
// @Router /api/customer/quotes/{id}/accept [post]
func (h *CustomerPortalHandler) AcceptQuote(c *gin.Context) {
	customerID, op, ok := h.resolveCustomer(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "id 非法", apperr.ErrInvalidParams.Status))
		return
	}
	res, err := h.portalSvc.AcceptQuote(c.Request.Context(), id, customerID, op.OperatorID, requestIDOf(c))
	if err != nil {
		response.Error(c, portalErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// GetBilling 余额 / 授信占用 / 押金状态。
//
// @Summary 余额 / 授信占用 / 押金状态
// @Description 从 customer_profile 读 credit_limit / credit_used / deposit_amount / deposit_status / billing_cycle。账单明细 bills=[] 占位（9c-① 遗留：待计费系统接入）。
// @Tags 客户门户
// @Produce json
// @Security BearerAuth
// @Success 200 {object} api.APIResponse "code=0；data=PortalBillingView{credit_limit, credit_used, deposit_amount, deposit_status, billing_cycle, bills}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 404 {object} api.APIResponse "code=10004 客户不存在"
// @Router /api/customer/billing [get]
func (h *CustomerPortalHandler) GetBilling(c *gin.Context) {
	customerID, _, ok := h.resolveCustomer(c)
	if !ok {
		return
	}
	view, err := h.portalSvc.GetBilling(c.Request.Context(), customerID)
	if err != nil {
		response.Error(c, portalErrToAppErr(err))
		return
	}
	response.Success(c, view)
}

// ListNotifications 通知列表。
//
// @Summary 通知列表
// @Description 当前客户的通知（created_at DESC 分页）。标记已读接口 POST /notifications/{id}/read 登记 9c-② 遗留。
// @Tags 客户门户
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param size query int false "每页" default(20)
// @Success 200 {object} api.APIResponse "code=0；data=PortalNotificationListResult{list, total, page, size}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Router /api/customer/notifications [get]
func (h *CustomerPortalHandler) ListNotifications(c *gin.Context) {
	customerID, _, ok := h.resolveCustomer(c)
	if !ok {
		return
	}
	page, size := parsePageQuery(c)
	res, err := h.portalSvc.ListNotifications(c.Request.Context(), customerID, customer.PortalPageQuery{Page: page, Size: size})
	if err != nil {
		response.Error(c, portalErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// GetHome H5 首页聚合。
//
// @Summary H5 首页聚合：余额 + 待处理 + 未读通知 + 常用模型
// @Description balance（与 /billing 同源）+ pending_count（DRAFT/PENDING 报价数）+ unread_notifications + common_models（本等级生效价目表的 SKU 列表，**不含价格明细以外的成本/毛利字段**）。本批单接口聚合，9c-④ 登记是否拆分为并行请求。
// @Tags 客户门户
// @Produce json
// @Security BearerAuth
// @Success 200 {object} api.APIResponse "code=0；data=PortalHomeView{balance, pending_count, unread_notifications, common_models}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 404 {object} api.APIResponse "code=10004 客户不存在"
// @Router /api/customer/home [get]
func (h *CustomerPortalHandler) GetHome(c *gin.Context) {
	customerID, _, ok := h.resolveCustomer(c)
	if !ok {
		return
	}
	view, err := h.portalSvc.GetHome(c.Request.Context(), customerID)
	if err != nil {
		response.Error(c, portalErrToAppErr(err))
		return
	}
	response.Success(c, view)
}

var _ = fmt.Sprintf // suppress unused import (fmt may be needed for future errors)
