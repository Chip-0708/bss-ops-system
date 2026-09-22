// Package api 的 price_confirm.go：官方价确认入正式版本 + 审批 + 生效连锁（内部门户 M2，
// 07-supplier-and-price-change.md §7/§8/§9，阶段 7b）。
// POST /staging-prices/confirm 必挂幂等（红线 5）；审批动作复用 M4 退役的 DecideApproval
// （7b 已把 biz_type 从硬编码 "DEPRECATE" 改为从 change_request.change_type 读，并注入
// onApproved 回调执行生效连锁——PRICE_UP/PRICE_DOWN 走 price.ApplyOfficialPriceChange）。
package api

import (
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/price"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// PriceConfirmHandler 是官方价确认接口的处理器。
type PriceConfirmHandler struct {
	confirmSvc *price.ConfirmService
}

// NewPriceConfirmHandler 构造处理器。
func NewPriceConfirmHandler(confirmSvc *price.ConfirmService) *PriceConfirmHandler {
	return &PriceConfirmHandler{confirmSvc: confirmSvc}
}

// confirmErrToAppErr 把确认领域错误映射为 HTTP 错误码（message 透传）。
func confirmErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, price.ErrConfirmStagingNotFound):
		return apperr.New(apperr.ErrNotFound.Code, err.Error(), apperr.ErrNotFound.Status)
	case errors.Is(err, price.ErrConfirmStagingProcessed):
		return apperr.New(apperr.ErrIdempotency.Code, err.Error(), apperr.ErrIdempotency.Status)
	case errors.Is(err, price.ErrConfirmStagingsEmpty),
		errors.Is(err, price.ErrConfirmEffectiveFuture),
		errors.Is(err, price.ErrConfirmStagingJobMismatch),
		errors.Is(err, price.ErrConfirmNoComponent):
		return apperr.New(apperr.ErrInvalidParams.Code, err.Error(), apperr.ErrInvalidParams.Status)
	default:
		return apperr.ErrSystem
	}
}

// ConfirmStaging 确认入正式版本（创建 change_request + 审批步）。
//
// @Summary 确认入正式版本（M2:E，幂等必填 Idempotency-Key）
// @Description 校验 staging_ids 非空 / 每条存在且属于该 sync_job 且 processed=false / effective_time≤now（>now→400，预约生效登记为遗留 7b-①）；同 SKU 多条以 max(staging.id) 胜出；自动判涨跌方向（任一组件 new>old→PRICE_UP 2 步审批 MODEL_OPS→PRICING_OP，否则 PRICE_DOWN 1 步 MODEL_OPS）；涨价才生成 margin_preview（floor=price×(1+loss)×(1+channel)/(1−min_gross_margin)）。单事务：change_request + approval_steps + staging.processed=true。生效连锁（price_version 新版本 / 报价静默跟随 / COST_RECALC 入队 / official_price.changed 出站 / cache_version+1）在审批全部通过时由 DecideApproval 回调执行。
// @Tags 官方价变更
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body price.ConfirmInput true "sync_job_id + staging_ids[] + effective_time(RFC3339，≤now)"
// @Success 200 {object} api.APIResponse "code=0；data={change_request_id,step_count}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法（staging_ids 空 / effective_time 未来或非法 / 批次不匹配）"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M2:E"
// @Failure 404 {object} api.APIResponse "code=10004 staging_id 不存在"
// @Failure 409 {object} api.APIResponse "code=10005 staging 已确认 / 幂等冲突"
// @Router /api/internal/staging-prices/confirm [post]
func (h *PriceConfirmHandler) ConfirmStaging(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	var body price.ConfirmInput
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("JSON 解析失败：%s", err.Error()), apperr.ErrInvalidParams.Status))
		return
	}
	res, err := h.confirmSvc.Confirm(c.Request.Context(), body, op.OperatorID, operatorRoleOf(op), requestIDOf(c))
	if err != nil {
		response.Error(c, confirmErrToAppErr(err))
		return
	}
	data := gin.H{"change_request_id": res.ChangeRequestID, "step_count": res.StepCount}
	middleware.SetIdempotencyResult(c, data) // 重放返回首次结果（6d-3 遗留③的同款防线）
	response.Success(c, data)
}
