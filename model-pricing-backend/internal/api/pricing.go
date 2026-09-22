// Package api 的 pricing.go：定价策略配置 + 生成价目表草稿（内部门户 M6/M7，
// 08-pricing.md §1/§2，阶段 8a）。
// 写接口（POST/PUT policies、POST price-books）必挂幂等（红线 5）。
// 字段名以真实 DDL 为准（price_method/param_value/rounding_rule），契约 0.1 的
// markup_type/markup_value/floor_rule 与 DDL 不符——契约待更新（遗留 8a-⑦）。
package api

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/pricing"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// PricingHandler 是定价接口的处理器。
type PricingHandler struct {
	pricingSvc *pricing.PricingService
}

// NewPricingHandler 构造处理器。
func NewPricingHandler(pricingSvc *pricing.PricingService) *PricingHandler {
	return &PricingHandler{pricingSvc: pricingSvc}
}

// pricingErrToAppErr 把定价领域错误映射为 HTTP 错误码（message 透传）。
func pricingErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, pricing.ErrPolicyNotFound):
		return apperr.New(apperr.ErrNotFound.Code, err.Error(), apperr.ErrNotFound.Status)
	case errors.Is(err, pricing.ErrInvalidScopeType),
		errors.Is(err, pricing.ErrInvalidPriceMethod),
		errors.Is(err, pricing.ErrInvalidParamValue),
		errors.Is(err, pricing.ErrModelTypeUnsupported):
		return apperr.New(apperr.ErrInvalidParams.Code, err.Error(), apperr.ErrInvalidParams.Status)
	default:
		return apperr.ErrSystem
	}
}

// ---- 请求体 ----

// upsertPolicyBody 是创建/更新策略的请求体（字段名以 DDL 为准）。
type upsertPolicyBody struct {
	Code        string  `json:"code" binding:"required"`
	Name        string  `json:"name" binding:"required"`
	ScopeType   string  `json:"scope_type" binding:"required"`
	ScopeID     *int64  `json:"scope_id"`
	LevelCode   *string `json:"level_code"`
	PriceMethod string  `json:"price_method" binding:"required"`
	ParamValue  string  `json:"param_value" binding:"required"`
	Priority    int     `json:"priority"`
	Status      string  `json:"status" binding:"required"`
}

// generateDraftBody 是生成草稿的请求体。
type generateDraftBody struct {
	LevelCode string  `json:"level_code" binding:"required"`
	Currency  string  `json:"currency" binding:"required"`
	PolicyIDs []int64 `json:"policy_ids"`
	SKUIDs    []int64 `json:"sku_ids"`
}

// ListPolicies 策略列表。
//
// @Summary 定价策略列表（M6:V）
// @Description 按 priority 升序、id 升序分页返回。字段名以 DDL 为准（price_method/param_value）。
// @Tags 定价与价目表
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param size query int false "每页（上限 100）" default(20)
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M6:V"
// @Router /api/internal/pricing/policies [get]
func (h *PricingHandler) ListPolicies(c *gin.Context) {
	page, size := costPageSize(c)
	list, total, err := h.pricingSvc.ListPolicies(c.Request.Context(), page, size)
	if err != nil {
		response.Error(c, pricingErrToAppErr(err))
		return
	}
	response.Success(c, gin.H{"list": list, "total": total, "page": page, "size": size})
}

// CreatePolicy 创建策略。
//
// @Summary 创建定价策略（M6:E，幂等必填 Idempotency-Key）
// @Description scope_type 支持 ALL/VENDOR/FAMILY/SKU（MODEL_TYPE 本批不支持→400，遗留 8a-⑤）；price_method 支持 MARGIN/COST_UP/OFFICIAL_ANCHOR/FIXED（TIERED 不在 DDL→400，遗留 8a-②）；param_value 金额字符串（numeric(12,6)）；rounding_rule 自动 'CEIL'（DDL 默认值）。
// @Tags 定价与价目表
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body api.upsertPolicyBody true "code/name/scope_type/price_method/param_value/status 必填"
// @Success 200 {object} api.APIResponse "code=0；data=策略（含 id）"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法（枚举非法 / param_value 非数字）"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M6:E"
// @Router /api/internal/pricing/policies [post]
func (h *PricingHandler) CreatePolicy(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	var body upsertPolicyBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("JSON 解析失败：%s", err.Error()), apperr.ErrInvalidParams.Status))
		return
	}
	p := &pricing.PricingPolicy{
		Code: body.Code, Name: body.Name, ScopeType: body.ScopeType, ScopeID: body.ScopeID,
		LevelCode: body.LevelCode, PriceMethod: body.PriceMethod, ParamValue: body.ParamValue,
		Priority: body.Priority, Status: body.Status,
	}
	if err := h.pricingSvc.CreatePolicy(c.Request.Context(), p, op.OperatorID, requestIDOf(c)); err != nil {
		response.Error(c, pricingErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, p)
	response.Success(c, p)
}

// UpdatePolicy 更新策略。
//
// @Summary 更新定价策略（M6:E，幂等必填 Idempotency-Key）
// @Description 按 id 全量字段更新；校验同创建。策略不存在→404。
// @Tags 定价与价目表
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "策略 id"
// @Param body body api.upsertPolicyBody true "全量字段"
// @Success 200 {object} api.APIResponse "code=0；data=策略"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M6:E"
// @Failure 404 {object} api.APIResponse "code=10004 策略不存在"
// @Router /api/internal/pricing/policies/{id} [put]
func (h *PricingHandler) UpdatePolicy(c *gin.Context) {
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
	var body upsertPolicyBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("JSON 解析失败：%s", err.Error()), apperr.ErrInvalidParams.Status))
		return
	}
	p := &pricing.PricingPolicy{
		ID: id, Code: body.Code, Name: body.Name, ScopeType: body.ScopeType, ScopeID: body.ScopeID,
		LevelCode: body.LevelCode, PriceMethod: body.PriceMethod, ParamValue: body.ParamValue,
		Priority: body.Priority, Status: body.Status,
	}
	if err := h.pricingSvc.UpdatePolicy(c.Request.Context(), p, op.OperatorID, requestIDOf(c)); err != nil {
		response.Error(c, pricingErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, p)
	response.Success(c, p)
}

// GenerateDraft 生成价目表草稿。
//
// @Summary 生成价目表草稿（M7:E，幂等必填 Idempotency-Key）
// @Description 不落正式版本（status='DRAFT'，version_no=MAX(version_no)+1 避免撞 uk_pb_ver）。对每个 SKU：取当前 cost_baseline 代表组件 unit_cost → 套用优先级最高命中策略（不叠加）→ 按 price_method 算售价（CEIL 8 位）→ 算 floor=cost/(1-min_gross_margin) → 售价<floor 则 floor_violation=true。sku_ids 空=全量在架 SKU；无命中策略/无成本基线的 SKU 跳过不进 diff_report。blocked_count 只是统计，不阻止生成（发布校验在 8b）。
// @Tags 定价与价目表
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body api.generateDraftBody true "level_code/currency 必填；policy_ids/sku_ids 可选"
// @Success 200 {object} api.APIResponse "code=0；data={draft_id,level_code,currency,item_count,diff_report,blocked_count}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M7:E"
// @Router /api/internal/price-books [post]
func (h *PricingHandler) GenerateDraft(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	var body generateDraftBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("JSON 解析失败：%s", err.Error()), apperr.ErrInvalidParams.Status))
		return
	}
	draft, err := h.pricingSvc.GenerateDraft(c.Request.Context(), pricing.GenerateDraftInput{
		LevelCode: body.LevelCode, Currency: body.Currency,
		PolicyIDs: body.PolicyIDs, SKUIDs: body.SKUIDs,
	}, op.OperatorID, requestIDOf(c))
	if err != nil {
		response.Error(c, pricingErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, draft)
	response.Success(c, draft)
}
