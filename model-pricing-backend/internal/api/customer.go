// Package api 的 customer.go：9a 客户域（内部门户 M8/M9，
// 09-customer-quote.md §1 客户列表 + §2 生成报价）。
//
// 权限：
//   - GET  /customers           M8:V + 行级过滤（SALES 只看自己的客户）
//   - POST /customers/:id/transfer  M8:E + 仅主管（OPS_ADMIN/PLATFORM_ADMIN）+ 幂等
//   - POST /customer-quotes     M9:E + 幂等
//
// 红线：
//   - 写接口必挂 Idempotency（红线 5）。
//   - 金额字段字符串化（红线 1）——unit_price / floor_price / credit_limit 等都走 decimal。
//   - 字段剔除（红线 6）——credit_* / deposit_* 对销售角色可保留（自己的客户），
//     本批不剔除；如需对供应商/客户门户剔除，在 9c 再挂。
package api

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/auth"
	"model_bss/internal/domain/customer"
	"model_bss/pkg/apperr"
	"model_bss/pkg/perm"
	"model_bss/pkg/response"
)

// CustomerHandler 客户域处理器。
type CustomerHandler struct {
	svc *customer.Service
}

// NewCustomerHandler 构造。
func NewCustomerHandler(svc *customer.Service) *CustomerHandler {
	return &CustomerHandler{svc: svc}
}

// customerErrToAppErr 领域错误 → HTTP 错误码。
func customerErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, customer.ErrCustomerNotFound):
		return apperr.New(apperr.ErrNotFound.Code, err.Error(), apperr.ErrNotFound.Status) // 404
	case errors.Is(err, customer.ErrSourceQuoteNotFound):
		return apperr.New(apperr.ErrNotFound.Code, err.Error(), apperr.ErrNotFound.Status) // 404
	case errors.Is(err, customer.ErrCustomerOutOfScope):
		return apperr.New(apperr.ErrForbidden.Code, err.Error(), apperr.ErrForbidden.Status) // 403
	case errors.Is(err, customer.ErrTransferNotAllowed):
		return apperr.New(apperr.ErrForbidden.Code, err.Error(), apperr.ErrForbidden.Status) // 403
	case errors.Is(err, customer.ErrNoEffectivePriceBook),
		errors.Is(err, customer.ErrSourceQuoteCrossCustomer),
		errors.Is(err, customer.ErrBelowFloor),
		errors.Is(err, customer.ErrTransferToSelf):
		return apperr.New(apperr.ErrIdempotency.Code, err.Error(), apperr.ErrIdempotency.Status) // 409 业务冲突
	case errors.Is(err, customer.ErrInvalidQuoteType),
		errors.Is(err, customer.ErrTempValidToRequired),
		errors.Is(err, customer.ErrInvalidTransferTarget),
		errors.Is(err, customer.ErrConfirmRequired):
		return apperr.New(apperr.ErrInvalidParams.Code, err.Error(), apperr.ErrInvalidParams.Status) // 400
	default:
		return apperr.ErrSystem
	}
}

// scopeOf 把 middleware.Operator 投影到 customer.OwnerScope（行级过滤）。
func scopeOf(op *middleware.Operator) customer.OwnerScope {
	return customer.OwnerScope{
		DataScope:  op.DataScope,
		StaffID:    op.StaffID,
		MyOrgID:    op.MyOrgID,
		ScopePaths: op.ScopePaths,
	}
}

// ListCustomers 客户列表（M8:V + 行级过滤）。
//
// @Summary 客户列表（M8:V；行级过滤：SALES 只看自己的客户，OPS_ADMIN/PLATFORM_ADMIN 看部门及下级）
// @Description 返回 customer_profile + legal_subject.legal_name + internal_staff.name 联表。keyword 按 legal_name ILIKE 搜索。行级过滤按 owner_sales_operator_id（销售归属），与 supplier.applyOwnerScope 同形态。
// @Tags 客户报价
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param size query int false "每页（上限 200）" default(20)
// @Param keyword query string false "按 legal_name ILIKE 搜索"
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M8:V"
// @Router /api/internal/customers [get]
func (h *CustomerHandler) ListCustomers(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	keyword := c.Query("keyword")
	res, err := h.svc.ListCustomers(c.Request.Context(), scopeOf(op), keyword, page, size)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	response.Success(c, res)
}

// GetQuoteContext 返回生成报价前的当前价目表与本客户历史报价。
//
// @Summary 客户报价上下文（M9:V；当前价目表 + 可克隆历史）
// @Description 返回客户 level_code、当前 EFFECTIVE 价目表售价项，以及该客户历史报价及售价项。只含售价，不返回成本、floor 或毛利；执行与客户列表相同的数据域校验。
// @Tags 客户报价
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "customer_profile.id"
// @Param page query int false "历史报价页码" default(1)
// @Param size query int false "历史报价每页（上限 200）" default(20)
// @Success 200 {object} api.APIResponse "code=0；data=QuoteContext{customer_id,level_code,price_book,history}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M9:V 或客户超出数据域"
// @Failure 404 {object} api.APIResponse "code=10004 客户不存在"
// @Router /api/internal/customers/{id}/quote-context [get]
func (h *CustomerHandler) GetQuoteContext(c *gin.Context) {
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
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	res, err := h.svc.GetQuoteContext(c.Request.Context(), id, scopeOf(op), page, size)
	if err != nil {
		response.Error(c, customerErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// transferBody 移交请求体。
type transferBody struct {
	ToOperatorID int64  `json:"to_operator_id" binding:"required"`
	Reason       string `json:"reason"`
	Confirm      bool   `json:"confirm"`
}

// TransferCustomer 客户移交（M8:E + 仅主管 + 双确认 + 幂等）。
//
// @Summary 客户移交（M8:E + 仅主管；双确认；历史报价随迁 + 原归属留档）
// @Description 首次 confirm=false 返回影响面（from/to 销售名、随迁报价数、专属价目表行数）；二次 confirm=true 才执行：单事务 UPDATE customer_profile.owner_sales_operator_id + UPDATE customer_quote.owner_sales_operator_id（随迁）+ UPDATE customer_quote.origin_owner_id（留档，NULL 才填）+ audit_log。目标必须是 ACTIVE + 持 SALES 角色的 staff；移交目标是当前归属 → 409 no-op；非主管 → 403。
// @Tags 客户报价
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "customer_profile.id"
// @Param Idempotency-Key header string true "幂等键"
// @Param body body transferBody true "to_operator_id 必填；reason 可选；confirm=true 才执行"
// @Success 200 {object} api.APIResponse "confirm=false：data=TransferImpact；confirm=true：data=TransferResult"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法 / 目标销售非法 / 缺 confirm"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M8:E 或非主管"
// @Failure 404 {object} api.APIResponse "code=10004 客户不存在"
// @Failure 409 {object} api.APIResponse "code=10005 移交目标是当前归属"
// @Router /api/internal/customers/{id}/transfer [post]
func (h *CustomerHandler) TransferCustomer(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	// 主管校验（红线：鉴权不能只看主角色）——OPS_ADMIN / PLATFORM_ADMIN 任一命中。
	if !perm.AnyRoleCan(op.Roles, func(code string) bool {
		return code == perm.RoleOpsAdmin || code == perm.RolePlatformAdmin
	}) {
		response.Error(c, customerErrToAppErr(customer.ErrTransferNotAllowed))
		return
	}
	id, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil || id <= 0 {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "id 非法", apperr.ErrInvalidParams.Status))
		return
	}
	var body transferBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("JSON 解析失败：%s", err.Error()), apperr.ErrInvalidParams.Status))
		return
	}
	if body.ToOperatorID <= 0 {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "to_operator_id 必须 > 0", apperr.ErrInvalidParams.Status))
		return
	}

	in := customer.TransferInput{
		CustomerID:   id,
		ToOperatorID: body.ToOperatorID,
		Reason:       body.Reason,
		Confirm:      body.Confirm,
	}
	if !body.Confirm {
		// 首次：返回影响面。
		imp, err := h.svc.TransferPreview(c.Request.Context(), in)
		if err != nil {
			response.Error(c, customerErrToAppErr(err))
			return
		}
		response.Success(c, imp)
		return
	}
	// 二次：执行。
	res, err := h.svc.Transfer(c.Request.Context(), in, op.OperatorID, requestIDOf(c))
	if err != nil {
		response.Error(c, customerErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// quoteItemBody 报价明细请求体。
type quoteItemBody struct {
	SKUID     int64  `json:"sku_id" binding:"required"`
	UnitPrice string `json:"unit_price"`
}

// generateQuoteBody 生成报价请求体。
type generateQuoteBody struct {
	CustomerID    int64           `json:"customer_id" binding:"required"`
	QuoteType     string          `json:"quote_type" binding:"required"`
	SourceQuoteID *int64          `json:"source_quote_id,omitempty"`
	ValidTo       *time.Time      `json:"valid_to,omitempty"`
	Items         []quoteItemBody `json:"items,omitempty"`
}

// GenerateQuote 生成客户报价（M9:E + 幂等）。
//
// @Summary 生成客户报价（M9:E + 幂等必填 Idempotency-Key；quote_type=APPLY/CLONE/TEMP）
// @Description APPLY：套用客户 level_code 的当前生效 price_book，items 可省略（全量带出）→ 无生效价目表 400；CLONE：克隆 source_quote_id 的价格（须同 customer_id，跨客户 409）；TEMP：手工填 items，valid_to 必填。三类型都做 floor 硬性校验——任一 unit_price < Floor(cost_baseline.unit_cost, min_gross_margin) → 409（须走特价审批 §3）。落库 status=DRAFT，version_no=MAX+1（uk_cq_ver 兜底）。
// @Tags 客户报价
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param Idempotency-Key header string true "幂等键"
// @Param body body generateQuoteBody true "customer_id / quote_type 必填；CLONE 需 source_quote_id；TEMP 需 valid_to + items"
// @Success 200 {object} api.APIResponse "code=0；data={id,customer_id,version_no,status,quote_type,item_count,below_floor_count,price_book_version,valid_until,owner_sales_operator_id,created_at}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法 / 无生效价目表 / TEMP 缺 valid_to / quote_type 非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M9:E 或客户出数据域"
// @Failure 404 {object} api.APIResponse "code=10004 客户不存在 / 源报价不存在"
// @Failure 409 {object} api.APIResponse "code=10005 低于 floor（含 floor_violations[]） / 克隆跨客户"
// @Router /api/internal/customer-quotes [post]
func (h *CustomerHandler) GenerateQuote(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	var body generateQuoteBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("JSON 解析失败：%s", err.Error()), apperr.ErrInvalidParams.Status))
		return
	}
	items := make([]customer.QuoteItemInput, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, customer.QuoteItemInput{SKUID: it.SKUID, UnitPrice: it.UnitPrice})
	}
	res, err := h.svc.GenerateQuote(c.Request.Context(), customer.GenerateQuoteInput{
		CustomerID:    body.CustomerID,
		QuoteType:     body.QuoteType,
		SourceQuoteID: body.SourceQuoteID,
		ValidTo:       body.ValidTo,
		Items:         items,
	}, scopeOf(op), op.OperatorID, requestIDOf(c))
	if err != nil {
		// floor 违规要返回 violations 明细（不吞字段——红线 5）。
		var floorErr *customer.QuoteFloorError
		if errors.As(err, &floorErr) {
			response.ErrorData(c, apperr.New(apperr.ErrIdempotency.Code,
				fmt.Sprintf("%s（违规 %d 项）", customer.ErrBelowFloor.Error(), len(floorErr.Violations)),
				apperr.ErrIdempotency.Status), map[string]interface{}{
				"floor_violations": floorErr.Violations,
			})
			return
		}
		response.Error(c, customerErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// 引用避免 unused——实际不动 auth 包（scopeOf 用了 op.DataScope 已是 auth.DataScope）。
var _ = auth.ScopeSELF
