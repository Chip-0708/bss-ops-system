// Package api 的 pricing_publish.go：价目表发布 + 回滚（内部门户 M7，
// 08-pricing.md §3/§4，阶段 8b-1）。
// 写接口（POST publish/rollback）必挂幂等（红线 5）。
package api

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/pricing"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// PricingPublishHandler 是价目表发布/回滚的处理器。
type PricingPublishHandler struct {
	publishSvc *pricing.PublishService
}

// NewPricingPublishHandler 构造处理器。
func NewPricingPublishHandler(publishSvc *pricing.PublishService) *PricingPublishHandler {
	return &PricingPublishHandler{publishSvc: publishSvc}
}

// pricingPublishErrToAppErr 把发布/回滚领域错误映射为 HTTP 错误码（message 透传）。
func pricingPublishErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, pricing.ErrPriceBookNotFound), errors.Is(err, pricing.ErrRollbackTargetNotFound):
		return apperr.New(apperr.ErrNotFound.Code, err.Error(), apperr.ErrNotFound.Status)
	case errors.Is(err, pricing.ErrPriceBookNotDraft), errors.Is(err, pricing.ErrPriceBookNotEffective),
		errors.Is(err, pricing.ErrRollbackToSelf):
		return apperr.New(apperr.ErrIdempotency.Code, err.Error(), apperr.ErrIdempotency.Status) // 409 状态冲突
	case errors.Is(err, pricing.ErrFloorViolation):
		return apperr.New(apperr.ErrIdempotency.Code, err.Error(), apperr.ErrIdempotency.Status) // 409 红线冲突
	case errors.Is(err, pricing.ErrInvalidMode), errors.Is(err, pricing.ErrScheduledNotSupported),
		errors.Is(err, pricing.ErrGrayNotSupported):
		return apperr.New(apperr.ErrInvalidParams.Code, err.Error(), apperr.ErrInvalidParams.Status) // 400
	default:
		return apperr.ErrSystem
	}
}

// ---- 请求体 ----

// publishBody 是发布的请求体。
type publishBody struct {
	EffectiveTime string `json:"effective_time" binding:"required"`
	Mode          string `json:"mode" binding:"required"`
	GrayPercent   int    `json:"gray_percent"`
}

// rollbackBody 是回滚的请求体。
type rollbackBody struct {
	TargetVersionNo int    `json:"target_version_no" binding:"required"`
	Reason          string `json:"reason" binding:"required"`
}

// Publish 发布价目表。
//
// @Summary 发布价目表（M7:E，双人审批，幂等必填 Idempotency-Key）
// @Description 草稿 DRAFT → APPROVING（原地升格，version_no 不变）；创建 change_request（sku_id=NULL）+ 2 步审批（PRICING_OP → FINANCE）。floor_violation=true → 409；SCHEDULED/GRAY → 400。
// @Tags 定价与价目表
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "price_book ID"
// @Param Idempotency-Key header string true "幂等键"
// @Param body body api.publishBody true "effective_time/mode 必填"
// @Success 200 {object} api.APIResponse "code=0；data={price_book_id,version_no,change_request_id,step_count,effective_time,status}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法 / SCHEDULED / GRAY"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M7:E"
// @Failure 404 {object} api.APIResponse "code=10004 价目表不存在"
// @Failure 409 {object} api.APIResponse "code=10005 非草稿状态 / floor_violation"
// @Router /api/internal/price-books/{id}/publish [post]
func (h *PricingPublishHandler) Publish(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	id, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil || id <= 0 {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "id 非法", apperr.ErrInvalidParams.Status))
		return
	}
	var body publishBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("JSON 解析失败：%s", err.Error()), apperr.ErrInvalidParams.Status))
		return
	}
	effectiveTime, terr := time.Parse(time.RFC3339, body.EffectiveTime)
	if terr != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("effective_time 非法：%s", terr.Error()), apperr.ErrInvalidParams.Status))
		return
	}
	res, err := h.publishSvc.Publish(c.Request.Context(), pricing.PublishInput{
		PriceBookID:   id,
		EffectiveTime: effectiveTime,
		Mode:          body.Mode,
		GrayPercent:   body.GrayPercent,
	}, op.OperatorID, requestIDOf(c))
	if err != nil {
		response.Error(c, pricingPublishErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// Rollback 回滚价目表。
//
// @Summary 回滚价目表（M7:E，双人审批，幂等必填 Idempotency-Key）
// @Description 复制历史版本的 items+components 成新版本（rollback_of=target_version_no），走与发布完全相同的流程。用当前成本基线重算的 floor 校验目标版本售价，低于当前红线 → 409。
// @Tags 定价与价目表
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "price_book ID（当前生效版）"
// @Param Idempotency-Key header string true "幂等键"
// @Param body body api.rollbackBody true "target_version_no/reason 必填"
// @Success 200 {object} api.APIResponse "code=0；data={price_book_id,version_no,change_request_id,step_count,effective_time,status}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M7:E"
// @Failure 404 {object} api.APIResponse "code=10004 价目表/目标版本不存在"
// @Failure 409 {object} api.APIResponse "code=10005 非生效版 / 回滚到自己 / floor_violation"
// @Router /api/internal/price-books/{id}/rollback [post]
func (h *PricingPublishHandler) Rollback(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	id, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil || id <= 0 {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "id 非法", apperr.ErrInvalidParams.Status))
		return
	}
	var body rollbackBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("JSON 解析失败：%s", err.Error()), apperr.ErrInvalidParams.Status))
		return
	}
	res, err := h.publishSvc.Rollback(c.Request.Context(), pricing.RollbackInput{
		PriceBookID:     id,
		TargetVersionNo: body.TargetVersionNo,
		Reason:          body.Reason,
	}, op.OperatorID, requestIDOf(c))
	if err != nil {
		response.Error(c, pricingPublishErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}
