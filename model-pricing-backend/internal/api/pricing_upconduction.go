// Package api 的 pricing_upconduction.go：涨价传导决策队列（内部门户 M7，
// 08-pricing.md §5，阶段 8b-2）。
// 写接口（POST generate / decide）必挂幂等（红线 5）。
//
// 权限剔除（裁决 4）：
//   - margin_before / margin_after 通过 field_mask 剔除——
//     000022 迁移已把这两个字段并入 SALES.field_mask.hide；
//     maskAndSuccess 沿用 cost 家族同一挂接点。
package api

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/pricing"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// PricingUpconductionHandler 涨价传导队列处理器。
type PricingUpconductionHandler struct {
	svc *pricing.UpconductionService
}

// NewPricingUpconductionHandler 构造。
func NewPricingUpconductionHandler(svc *pricing.UpconductionService) *PricingUpconductionHandler {
	return &PricingUpconductionHandler{svc: svc}
}

// pricingUpconductionErrToAppErr 领域错误 → HTTP 错误码。
func pricingUpconductionErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, pricing.ErrUpconductionNotFound):
		return apperr.New(apperr.ErrNotFound.Code, err.Error(), apperr.ErrNotFound.Status) // 404
	case errors.Is(err, pricing.ErrUpconductionNotPending):
		return apperr.New(apperr.ErrIdempotency.Code, err.Error(), apperr.ErrIdempotency.Status) // 409 状态冲突
	case errors.Is(err, pricing.ErrUpconductionBelowFloor):
		return apperr.New(apperr.ErrIdempotency.Code, err.Error(), apperr.ErrIdempotency.Status) // 409 红线冲突
	case errors.Is(err, pricing.ErrUpconductionInvalidAction):
		return apperr.New(apperr.ErrInvalidParams.Code, err.Error(), apperr.ErrInvalidParams.Status) // 400
	default:
		return apperr.ErrSystem
	}
}

// decideBody 决策请求体。
type decideBody struct {
	Decision      string `json:"decision" binding:"required"`
	OverridePrice string `json:"override_price"`
	Reason        string `json:"reason"`
}

// ListQueue 涨价传导队列列表（M7:V）。
//
// @Summary 涨价传导队列列表（M7:V；margin_* 字段按 field_mask 剔除）
// @Description 每行 = 一个 (sku_id, level_code) 在"成本上涨"时生成的待决策项。cost_before/cost_after 为成本基线前/后版本 unit_cost；price_current 为该 level 当前 EFFECTIVE 价目表代表组件售价；price_suggested = price_current × (1 + cost_delta_pct)；floor_price = cost_after / (1 - min_gross_margin)；margin_before/margin_after 按 field_mask 剔除（SALES 不可得）；status ∈ {PENDING, FOLLOWED, NOT_FOLLOWED}；frozen_until 为冻结期，超时 worker 自动 NOT_FOLLOWED。
// @Tags 定价与价目表
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param size query int false "每页（上限 200）" default(20)
// @Param status query string false "按状态过滤（PENDING / FOLLOWED / NOT_FOLLOWED）"
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M7:V"
// @Router /api/internal/price-upconduction [get]
func (h *PricingUpconductionHandler) ListQueue(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	status := c.Query("status")
	items, total, err := h.svc.ListQueue(c.Request.Context(), status, page, size)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 200 {
		size = 20
	}
	maskAndSuccess(c, gin.H{
		"list":  items,
		"total": total,
		"page":  page,
		"size":  size,
	})
}

// GenerateQueue 手动触发涨价传导队列生成（M7:E + 幂等）。
//
// @Summary 生成涨价传导队列（M7:E + 幂等必填 Idempotency-Key；手工触发）
// @Description 扫描"当前版 unit_cost > 上一版 unit_cost"的 SKU，对每个 (sku_id, level_code) 有 EFFECTIVE 价目表的生成一行 PENDING。同 (sku_id, level_code, cost_after) 已有 PENDING/FOLLOWED 行 → 跳过（幂等）。裁决 7：本批只生成队列，不自动改价；FOLLOW 由人工决策。
// @Tags 定价与价目表
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param Idempotency-Key header string true "幂等键"
// @Success 200 {object} api.APIResponse "code=0；data={generated_count,queue_ids,skipped_sku_ids,generated_at}"
// @Failure 400 {object} api.APIResponse "code=10001 缺幂等键"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M7:E"
// @Router /api/internal/price-upconduction/generate [post]
func (h *PricingUpconductionHandler) GenerateQueue(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	res, err := h.svc.GenerateQueue(c.Request.Context(), op.OperatorID, requestIDOf(c))
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// Decide 涨价传导决策（M7:E + 幂等）。
//
// @Summary 涨价传导决策（M7:E + 幂等必填 Idempotency-Key；双人通过 apply）
// @Description decision ∈ {FOLLOW, NOT_FOLLOW}。FOLLOW：仅置 status=FOLLOWED（不自动发布价目表——裁决 7 待 8b-3）；NOT_FOLLOW 且当前售价 < floor → 409（须走特价审批）；非 PENDING 行 → 409；不存在 → 404。
// @Tags 定价与价目表
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "队列行 id"
// @Param Idempotency-Key header string true "幂等键"
// @Param body body decideBody true "decision 必填；override_price / reason 可选"
// @Success 200 {object} api.APIResponse "code=0；data={queue_id,status,decided_by,decided_at,override_price?}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法 / decision 非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M7:E"
// @Failure 404 {object} api.APIResponse "code=10004 队列行不存在"
// @Failure 409 {object} api.APIResponse "code=10005 非 PENDING / NOT_FOLLOW 低于 floor"
// @Router /api/internal/price-upconduction/{id}/decide [post]
func (h *PricingUpconductionHandler) Decide(c *gin.Context) {
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
	var body decideBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("JSON 解析失败：%s", err.Error()), apperr.ErrInvalidParams.Status))
		return
	}

	// override_price 可空——空字符串视为"未传"，与"传 0"区分（裁决 7 当前不用 override_price 真正改价）。
	var override decimal.Decimal
	var hasOverride bool
	if body.OverridePrice != "" {
		d, err := decimal.NewFromString(body.OverridePrice)
		if err != nil {
			response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("override_price 非法：%s", err.Error()), apperr.ErrInvalidParams.Status))
			return
		}
		override = d
		hasOverride = true
	}

	// 取操作员角色（与 7b ConfirmService CreatedBy=OperatorRole 同款）。
	operatorRole := ""
	if len(op.Roles) > 0 {
		operatorRole = op.Roles[0]
	}

	res, err := h.svc.Decide(c.Request.Context(), pricing.DecideInput{
		QueueID:       id,
		OperatorID:    op.OperatorID,
		Decision:      body.Decision,
		OverridePrice: override,
		HasOverride:   hasOverride,
		Reason:        body.Reason,
		RequestID:     requestIDOf(c),
	}, operatorRole)
	if err != nil {
		response.Error(c, pricingUpconductionErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}
