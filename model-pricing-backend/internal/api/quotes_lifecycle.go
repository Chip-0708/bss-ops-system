// Package api 的 quotes_lifecycle.go：到期闭环/异常检测/特权补录接口
// （05-quotes.md §11/§12/§13，内部门户 M4）。
// 行级过滤：expiring 按 ownerSupplierScope 注入；anomalies 不过滤（§3.2 放开比价）。
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

// QuoteLifecycleHandler 是到期闭环/异常/补录的处理器。
type QuoteLifecycleHandler struct {
	lifecycleSvc *supplier.LifecycleService
	approveSvc   *supplier.ApproveService // confirm-remove 归属校验复用 checkScope
}

// NewQuoteLifecycleHandler 构造生命周期处理器。
func NewQuoteLifecycleHandler(lifecycleSvc *supplier.LifecycleService, approveSvc *supplier.ApproveService) *QuoteLifecycleHandler {
	return &QuoteLifecycleHandler{lifecycleSvc: lifecycleSvc, approveSvc: approveSvc}
}

// lifecycleErrToAppErr 把领域错误映射为 HTTP 错误码（message 透传）。
func lifecycleErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, supplier.ErrQuoteNotFound):
		return apperr.ErrNotFound
	case errors.Is(err, supplier.ErrQuoteScopeForbidden):
		return apperr.New(apperr.ErrForbidden.Code, err.Error(), apperr.ErrForbidden.Status)
	case errors.Is(err, supplier.ErrQuoteNotEffective),
		errors.Is(err, supplier.ErrQuoteInGracePeriod),
		errors.Is(err, supplier.ErrQuoteAlreadyRemoved),
		errors.Is(err, supplier.ErrQuoteConflict):
		return apperr.New(apperr.ErrIdempotency.Code, err.Error(), apperr.ErrIdempotency.Status) // 409
	case errors.Is(err, supplier.ErrRetroTimeNotPast),
		errors.Is(err, supplier.ErrRetroReasonInvalid),
		errors.Is(err, supplier.ErrRetroSupplierNotFound),
		errors.Is(err, supplier.ErrQuoteItemsEmpty),
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

// ListExpiringQuotes 到期清单。
//
// @Summary 到期清单（行级过滤，含单点依赖与级别）
// @Description 返回 status=EFFECTIVE 且 valid_to ≤ now+days 的报价单。grace_until 实时算（valid_to+quote_grace_days，不写列）。single_point=该单内是否有 SKU 只有这一家有效报价（跨供应商计数）。alert_level：days>7→NORMAL；0~7→HIGH；过期未过宽限→URGENT；过宽限未确认→URGENT+pending_remove；单点依赖阈值提前至 14/7 且级别升一级。
// @Tags 报价审批
// @Produce json
// @Security BearerAuth
// @Param days query int false "到期窗口（天）" default(7)
// @Param only_single_point query bool false "只看单点依赖"
// @Param page query int false "页码" default(1)
// @Param size query int false "每页" default(20)
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M4:V"
// @Router /api/internal/quotes/expiring [get]
func (h *QuoteLifecycleHandler) ListExpiringQuotes(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	q := supplier.ExpiringQuoteQuery{
		Days:            mustAtoi(c.DefaultQuery("days", "7")),
		OnlySinglePoint: c.Query("only_single_point") == "true",
		Page:            mustAtoi(c.DefaultQuery("page", "1")),
		Size:            mustAtoi(c.DefaultQuery("size", "20")),
	}
	res, err := h.lifecycleSvc.ListExpiring(c.Request.Context(), ownerScopeOf(op), q)
	if err != nil {
		response.Error(c, lifecycleErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// ConfirmRemoveQuote 人工确认剔除。
//
// @Summary 人工确认剔除（幂等，立即执行）
// @Description 已过宽限期才可执行（valid_to+quote_grace_days < now，实时算），未过→409。confirm=true 同事务置 EXPIRED+remove_confirmed+COST_RECALC(EXPIRE_REMOVE)+event_outbox；confirm=false 撤销确认（仅当尚未执行剔除）。
// @Tags 报价审批
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "报价单 ID"
// @Param Idempotency-Key header string true "幂等键"
// @Param body body supplier.ConfirmRemoveInput true "{confirm, reason}"
// @Success 200 {object} api.APIResponse "code=0；data={id,status,remove_confirmed,executed_at}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M4:E / 非归属供应商"
// @Failure 404 {object} api.APIResponse "code=10004 报价单不存在"
// @Failure 409 {object} api.APIResponse "code=10005 仍在宽限期 / 非生效状态 / 幂等冲突"
// @Router /api/internal/quotes/{id}/confirm-remove [post]
func (h *QuoteLifecycleHandler) ConfirmRemoveQuote(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	op := middleware.OperatorFrom(c)

	// 归属校验复用 ApproveService.checkScope（确认剔除也要求归属）
	if err := h.approveSvc.CheckScope(c.Request.Context(), id, ownerScopeOf(op)); err != nil {
		response.Error(c, lifecycleErrToAppErr(err))
		return
	}

	var in supplier.ConfirmRemoveInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}

	res, err := h.lifecycleSvc.ConfirmRemove(c.Request.Context(), id, in,
		op.StaffID, operatorRoleOf(op), requestIDOf(c))
	if err != nil {
		response.Error(c, lifecycleErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// ScanQuotes 手动触发定时扫描。
//
// @Summary 手动触发到期/异常扫描（领域方法 + 手动端点，不装 ticker）
// @Description type=expire：到期前 T-7/T-3 生成 todo+alert（去重）；type=expire-final：兜底处理 remove_confirmed=true 仍未剔除的历史遗留（正常查不到行）；type=anomaly：异常检测写 alert。均不挂幂等（扫描语义，靠去重保证可重复执行）。
// @Tags 报价审批
// @Produce json
// @Security BearerAuth
// @Param type query string true "expire / expire-final / anomaly"
// @Success 200 {object} api.APIResponse "code=0；data={type,scanned,todo_created,alert_created,processed}"
// @Failure 400 {object} api.APIResponse "code=10001 type 非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M4:E"
// @Router /api/internal/quotes/scan [post]
func (h *QuoteLifecycleHandler) ScanQuotes(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	typ := c.Query("type")
	// 手动端点触发：HUMAN 身份（operator=当前操作员），request_id 取本次请求
	ident := supplier.JobIdentity{
		OperatorID:   op.StaffID,
		OperatorRole: operatorRoleOf(op),
		SourceType:   "HUMAN",
		RequestID:    requestIDOf(c),
	}
	var res *supplier.ScanResult
	var err error
	switch typ {
	case "expire":
		res, err = h.lifecycleSvc.RunExpireScan(c.Request.Context(), ident)
	case "expire-final":
		res, err = h.lifecycleSvc.RunExpireFinal(c.Request.Context(), ident)
	case "anomaly":
		res, err = h.lifecycleSvc.RunAnomalyScan(c.Request.Context(), 30, ident)
	default:
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "type 必须是 expire / expire-final / anomaly", 400))
		return
	}
	if err != nil {
		response.Error(c, lifecycleErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// ListQuoteAnomalies 报价异常检测。
//
// @Summary 报价异常清单（D4，不做归属过滤——比价属平台级市场信息）
// @Description 实时计算不落表。取近 days 天 status∈(APPROVING,APPROVED_PENDING,EFFECTIVE) 的报价。判定：偏离上一版 |p−prev|/prev > quote_anomaly_pct(0.20) 或偏离市场最低 (p−best)/best > quote_anomaly_mkt(0.30)（严格大于）。reason：PREV_DEVIATION / MARKET_DEVIATION / BOTH。
// @Tags 报价审批
// @Produce json
// @Security BearerAuth
// @Param days query int false "回溯窗口（天）" default(30)
// @Param page query int false "页码" default(1)
// @Param size query int false "每页" default(20)
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M4:V"
// @Router /api/internal/quotes/anomalies [get]
func (h *QuoteLifecycleHandler) ListQuoteAnomalies(c *gin.Context) {
	q := supplier.AnomalyQuery{
		Days: mustAtoi(c.DefaultQuery("days", "30")),
		Page: mustAtoi(c.DefaultQuery("page", "1")),
		Size: mustAtoi(c.DefaultQuery("size", "20")),
	}
	res, err := h.lifecycleSvc.ListAnomalies(c.Request.Context(), q)
	if err != nil {
		response.Error(c, lifecycleErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// RetroEffectiveQuote 特权补录。
//
// @Summary 特权补录（RETRO_OP，幂等，免审批）
// @Description 集合路径 + supplier_id 入体（5d 裁决 1）。effective_time 必须过去；audit_reason ≥10 字（按 rune）；创建即 APPROVED_PENDING 并立即激活（复用 §10 ActivateDueQuotes，同事务关闭旧 EFFECTIVE）；COST_RECALC payload 带 effective_time（绝不向更早回灌）。激活失败写 alert(QUOTE_ACTIVATE_FAILED) 并返回明确错误。当月补录超 retro_monthly_limit(5) 写 RETRO_LIMIT alert。撞 uk_quote_pending → 409 并带出阻塞单号与版本号。
// @Tags 报价审批
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param Idempotency-Key header string true "幂等键"
// @Param body body supplier.RetroEffectiveInput true "{supplier_id, effective_time, valid_to, audit_reason, items}"
// @Success 200 {object} api.APIResponse "code=0；data={id,status,retroactive,closed_previous_id,cost_recalc_queued,retro_count_this_month,alert_created}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法 / 未来时间 / 理由不足"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M4:P"
// @Failure 404 {object} api.APIResponse "code=10004 供应商不存在"
// @Failure 409 {object} api.APIResponse "code=10005 已有待生效报价（含阻塞单号）/ 幂等冲突"
// @Router /api/internal/quotes/retro-effective [post]
func (h *QuoteLifecycleHandler) RetroEffectiveQuote(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	var in supplier.RetroEffectiveInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	res, err := h.lifecycleSvc.RetroEffective(c.Request.Context(), in,
		op.StaffID, operatorRoleOf(op), requestIDOf(c))
	if err != nil {
		response.Error(c, lifecycleErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}
