// Package api 的 quotes.go：报价审批与激活（内部门户 M4，05-quotes.md §7/§8/§9/§10/§14）。
// 行级过滤：报价单随供应商归属（设计 §3.2）；market_best 等比价数据不过滤（放开比价决议）。
package api

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/supplier"
	infra_middleware "model_bss/internal/infra/middleware"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// QuoteHandler 是内部报价审批接口的处理器。
type QuoteHandler struct {
	approveSvc *supplier.ApproveService
}

// NewQuoteHandler 构造报价审批处理器。
func NewQuoteHandler(approveSvc *supplier.ApproveService) *QuoteHandler {
	return &QuoteHandler{approveSvc: approveSvc}
}

// ownerScopeOf 从登录态提取数据域视图（设计 §3.2 图 D-3 的查询期输入）。
func ownerScopeOf(op *middleware.Operator) supplier.OwnerScope {
	return supplier.OwnerScope{
		DataScope:  string(op.DataScope),
		StaffID:    op.StaffID,
		MyOrgID:    op.MyOrgID,
		ScopePaths: op.ScopePaths,
	}
}

// operatorRoleOf 取操作员主角色 code（审计 operator_role 用）；无角色时兜底 STAFF。
func operatorRoleOf(op *middleware.Operator) string {
	if op != nil && len(op.Roles) > 0 {
		return op.Roles[0]
	}
	return "STAFF"
}

// approveErrToAppErr 把审批领域错误映射为 HTTP 错误码（message 透传，可直接展示）。
func approveErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, supplier.ErrQuoteNotFound):
		return apperr.ErrNotFound
	case errors.Is(err, supplier.ErrQuoteScopeForbidden):
		return apperr.New(apperr.ErrForbidden.Code, err.Error(), apperr.ErrForbidden.Status)
	case errors.Is(err, supplier.ErrQuoteNotApprovable), errors.Is(err, supplier.ErrQuoteConflict):
		return apperr.New(apperr.ErrIdempotency.Code, err.Error(), apperr.ErrIdempotency.Status) // 409
	case errors.Is(err, supplier.ErrQuoteRejectReasonInvalid):
		return apperr.New(apperr.ErrInvalidParams.Code, err.Error(), apperr.ErrInvalidParams.Status)
	default:
		return apperr.ErrSystem
	}
}

// ListPendingQuotes 待审批报价列表。
//
// @Summary 待审批报价（行级过滤）
// @Description status=APPROVING 的报价单，按数据域过滤（SELF=我引入的供应商；DEPT/DEPT_SUB/ALL 按设计 §3.2）。按提交时间 FIFO。
// @Tags 报价审批
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param size query int false "每页" default(20)
// @Param supplier_id query int64 false "供应商筛选"
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}（含 wait_hours/has_previous）"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M4:V"
// @Router /api/internal/quotes/pending [get]
func (h *QuoteHandler) ListPendingQuotes(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	q := supplier.PendingQuoteQuery{
		SupplierID: parseInt64Query(c, "supplier_id"),
		Page:       mustAtoi(c.DefaultQuery("page", "1")),
		Size:       mustAtoi(c.DefaultQuery("size", "20")),
	}
	res, err := h.approveSvc.ListPending(c.Request.Context(), ownerScopeOf(op), q)
	if err != nil {
		response.Error(c, approveErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// ApproveQuote 审批通过。
//
// @Summary 审批通过（幂等）
// @Description 前置 status=APPROVING（否则 409）；SELF 域只能批自己引入的供应商（越权 403）。事务内：状态推进 + todo DONE + task_job(ACTIVATE_QUOTE) 入队 + 审计。immediate（valid_from≤now）时事务提交后同步激活。
// @Tags 报价审批
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "报价单 ID"
// @Param Idempotency-Key header string true "幂等键"
// @Success 200 {object} api.APIResponse "code=0；data={id,status,approved_at,activate_at,immediate}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M4:A / 非归属供应商"
// @Failure 404 {object} api.APIResponse "code=10004 报价单不存在"
// @Failure 409 {object} api.APIResponse "code=10005 状态冲突（非 APPROVING）/ 幂等冲突"
// @Router /api/internal/quotes/{id}/approve [post]
func (h *QuoteHandler) ApproveQuote(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	op := middleware.OperatorFrom(c)
	res, err := h.approveSvc.Approve(c.Request.Context(), id, ownerScopeOf(op),
		op.StaffID, operatorRoleOf(op), requestIDOf(c))
	if err != nil {
		response.Error(c, approveErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// RejectQuote 审批驳回。
//
// @Summary 审批驳回（幂等）
// @Description reason 必填 10~500 字（按 rune 计），写入 reject_reason（供应商可见）+ rejected_at。前置 status=APPROVING（否则 409）。REJECTED 是终态，修订必须提交新版本。
// @Tags 报价审批
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "报价单 ID"
// @Param Idempotency-Key header string true "幂等键"
// @Param body body supplier.RejectInput true "驳回原因"
// @Success 200 {object} api.APIResponse "code=0；data={id,status,rejected_at}"
// @Failure 400 {object} api.APIResponse "code=10001 reason 长度非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M4:A / 非归属供应商"
// @Failure 404 {object} api.APIResponse "code=10004 报价单不存在"
// @Failure 409 {object} api.APIResponse "code=10005 状态冲突（非 APPROVING）/ 幂等冲突"
// @Router /api/internal/quotes/{id}/reject [post]
func (h *QuoteHandler) RejectQuote(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	var in supplier.RejectInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	op := middleware.OperatorFrom(c)
	res, err := h.approveSvc.Reject(c.Request.Context(), id, ownerScopeOf(op), in,
		op.StaffID, operatorRoleOf(op), requestIDOf(c))
	if err != nil {
		response.Error(c, approveErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// ActivateDueQuotes 手动触发到期激活。
//
// @Summary 激活到期报价（运维 / E2E 手动端点）
// @Description 扫描 status=APPROVED_PENDING 且 valid_from≤now 的报价单，逐条独立事务激活：旧 EFFECTIVE→EXPIRED → 本单 EFFECTIVE → PENDING_VERIFY SKU→PURCHASABLE → COST_RECALC 入队 → event_outbox。单条失败记 last_error 跳过，不中断整批。
// @Tags 报价审批
// @Produce json
// @Security BearerAuth
// @Success 200 {object} api.APIResponse "code=0；data={scanned,activated,items:[{id,supplier_id,version_no,closed_previous_id}]}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M4:E"
// @Router /api/internal/quotes/activate-due [post]
func (h *QuoteHandler) ActivateDueQuotes(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	res, err := h.approveSvc.ActivateDueQuotes(c.Request.Context(), supplier.ActivateParams{
		OperatorID:   op.StaffID,
		OperatorRole: operatorRoleOf(op),
		SourceType:   "HUMAN",
		RequestID:    infra_middleware.FromContext(c), // 本次请求的 request_id（审计追溯链）
	})
	if err != nil {
		response.Error(c, approveErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// GetQuoteDiff 差异对比与毛利预演。
//
// @Summary 差异对比（本次 vs 上一版 vs 官方价 vs 市场最低）+ 毛利预演 + 失真提示
// @Description 以 sku_id 为键三方对照；market_best 为全市场 EFFECTIVE 最低价（不做归属过滤，放开比价决议）。distortion=true 表示全单倍率完全相同（缓存维度标红）。margin_preview 为简化预演（floor=unit_price/(1−min_gross_margin)，真正预演待 Stage 6）。
// @Tags 报价审批
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "报价单 ID"
// @Success 200 {object} api.APIResponse "code=0；data=QuoteDiff"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M4:V / 非归属供应商"
// @Failure 404 {object} api.APIResponse "code=10004 报价单不存在"
// @Router /api/internal/quotes/{id}/diff [get]
func (h *QuoteHandler) GetQuoteDiff(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	op := middleware.OperatorFrom(c)
	res, err := h.approveSvc.Diff(c.Request.Context(), id, ownerScopeOf(op))
	if err != nil {
		response.Error(c, approveErrToAppErr(err))
		return
	}
	response.Success(c, res)
}
