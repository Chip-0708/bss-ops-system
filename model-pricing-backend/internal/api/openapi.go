// Package api 的 openapi.go：11a 开放接口处理器（§1 鉴权 + §2 七个只读接口）。
//
// 权限模型（stage11a 裁决 2）：
//   - 不挂 AuthN / RequirePerm / Idempotency；
//   - POST /auth/token 公开（client_id+secret 换 token）；
//   - 其余 6 个 GET 挂 OpenAuthN（校验 open_api_token）。
//
// 字段剔除（裁决 6/7）：
//   - SQL 层不 SELECT cost/margin/floor_price/calc_snapshot/supplier_cost；
//   - DTO 物理不含内部字段（unit_cost_basis 仅用于内部排序，不返回）。
package api

import (
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"model_bss/internal/domain/openapi"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// OpenApiHandler 开放接口处理器。
type OpenApiHandler struct {
	svc *openapi.Service
}

// NewOpenApiHandler 构造。
func NewOpenApiHandler(svc *openapi.Service) *OpenApiHandler {
	return &OpenApiHandler{svc: svc}
}

// openErrToAppErr 领域错误 → HTTP。
func openErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, openapi.ErrInvalidClient),
		errors.Is(err, openapi.ErrTokenExpired):
		return apperr.New(apperr.ErrUnauthorized.Code, err.Error(), apperr.ErrUnauthorized.Status) // 401
	case errors.Is(err, openapi.ErrSKUNotFound),
		errors.Is(err, openapi.ErrNoBaseline),
		errors.Is(err, openapi.ErrNoPriceBook):
		return apperr.New(apperr.ErrNotFound.Code, err.Error(), apperr.ErrNotFound.Status) // 404
	default:
		return apperr.ErrSystem
	}
}

// IssueToken 签发开放接口 token。
//
// @Summary 开放接口 token 签发
// @Description client_id + client_secret（sys_config 配置）换 1h 短期 token。与三大门户 login_session 完全隔离。
// @Tags 开放接口
// @Accept json
// @Produce json
// @Param body body object{client_id=string,client_secret=string} true "凭证"
// @Success 200 {object} api.APIResponse "code=0；data=TokenResult{token,expires_at}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法"
// @Failure 401 {object} api.APIResponse "code=10002 client_id 或 client_secret 错误"
// @Router /api/open/auth/token [post]
func (h *OpenApiHandler) IssueToken(c *gin.Context) {
	var body struct {
		ClientID     string `json:"client_id" binding:"required"`
		ClientSecret string `json:"client_secret" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "client_id / client_secret 必填", apperr.ErrInvalidParams.Status))
		return
	}
	res, err := h.svc.IssueToken(c.Request.Context(), body.ClientID, body.ClientSecret, requestIDOf(c))
	if err != nil {
		response.Error(c, openErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// GetAliases 模型别名全量列表。
//
// @Summary 模型别名列表（开放接口）
// @Description since 为客户端已见版本号；since >= version 返回空 items，否则返回全量别名（联 model_sku 拿 sku_code）。不含 cost/margin/floor_price。
// @Tags 开放接口
// @Produce json
// @Security BearerAuth
// @Param since query int64 false "客户端已见版本号" default(0)
// @Success 200 {object} api.APIResponse "code=0；data=AliasesResult{version,items[]}"
// @Failure 401 {object} api.APIResponse "code=10002 token 已过期或不存在"
// @Router /api/open/aliases [get]
func (h *OpenApiHandler) GetAliases(c *gin.Context) {
	since, _ := strconv.ParseInt(c.DefaultQuery("since", "0"), 10, 64)
	res, err := h.svc.GetAliases(c.Request.Context(), since)
	if err != nil {
		response.Error(c, openErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// GetSellableModels 上架可售模型。
//
// @Summary 上架可售模型（开放接口）
// @Description 返回 lifecycle_status IN ('PUBLISHED','PURCHASABLE') 的 SKU 列表。level_tags 从 model_sku.tags 透传（NULL→[]）。不含 cost/margin/floor_price。
// @Tags 开放接口
// @Produce json
// @Security BearerAuth
// @Success 200 {object} api.APIResponse "code=0；data=SellableModelsResult{items[]}"
// @Failure 401 {object} api.APIResponse "code=10002 token 已过期或不存在"
// @Router /api/open/sellable-models [get]
func (h *OpenApiHandler) GetSellableModels(c *gin.Context) {
	res, err := h.svc.GetSellableModels(c.Request.Context())
	if err != nil {
		response.Error(c, openErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// GetRouting 主备路由。
//
// @Summary 主备路由（开放接口）
// @Description primary=当前成本基线主供应商（weight=100），backups=按四因子评分排序的其余供应商（weight=0）。无成本基线或无 EFFECTIVE 报价 → 404。不返回评分明细。
// @Tags 开放接口
// @Produce json
// @Security BearerAuth
// @Param sku path string true "sku_id 或 sku_code"
// @Success 200 {object} api.APIResponse "code=0；data=RoutingResult{sku_id,primary,backups[],currency}"
// @Failure 401 {object} api.APIResponse "code=10002 token 已过期或不存在"
// @Failure 404 {object} api.APIResponse "code=10004 SKU 不存在 / 暂无成本基线"
// @Router /api/open/routing/{sku} [get]
func (h *OpenApiHandler) GetRouting(c *gin.Context) {
	sku := c.Param("sku")
	if sku == "" {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	res, err := h.svc.GetRouting(c.Request.Context(), sku)
	if err != nil {
		response.Error(c, openErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// GetPriceBook 当前生效价目表。
//
// @Summary 当前生效价目表（开放接口）
// @Description 按 level_code 返回当前 EFFECTIVE 价目表（联 model_sku + 代表组件 unit_price）。不含 floor_price/unit_cost/margin/baseline。
// @Tags 开放接口
// @Produce json
// @Security BearerAuth
// @Param level query string true "价目表等级（如 GLOBAL）"
// @Success 200 {object} api.APIResponse "code=0；data=PriceBookResult{level_code,version_no,currency,items[]}"
// @Failure 400 {object} api.APIResponse "code=10001 level 必填"
// @Failure 401 {object} api.APIResponse "code=10002 token 已过期或不存在"
// @Failure 404 {object} api.APIResponse "code=10004 该等级暂无生效价目表"
// @Router /api/open/price-book [get]
func (h *OpenApiHandler) GetPriceBook(c *gin.Context) {
	level := c.Query("level")
	if level == "" {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "level 必填", apperr.ErrInvalidParams.Status))
		return
	}
	res, err := h.svc.GetPriceBook(c.Request.Context(), level)
	if err != nil {
		response.Error(c, openErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// GetCostSnapshot 成本快照。
//
// @Summary 成本快照（开放接口）
// @Description 返回聚合 unit_cost（代表组件完全成本），无组件明细。asOf 为空=当前版本，非空=该时刻生效版本（valid_from<=asOf AND (valid_to IS NULL OR valid_to>asOf)）。
// @Tags 开放接口
// @Produce json
// @Security BearerAuth
// @Param sku query string true "sku_id 或 sku_code"
// @Param asOf query string false "RFC3339 时刻"
// @Success 200 {object} api.APIResponse "code=0；data=CostSnapshotResult{sku_id,unit_cost,currency,as_of,baseline_version}"
// @Failure 400 {object} api.APIResponse "code=10001 sku 必填 / asOf 非法"
// @Failure 401 {object} api.APIResponse "code=10002 token 已过期或不存在"
// @Failure 404 {object} api.APIResponse "code=10004 SKU 不存在 / 暂无成本基线"
// @Router /api/open/cost-snapshot [get]
func (h *OpenApiHandler) GetCostSnapshot(c *gin.Context) {
	sku := c.Query("sku")
	if sku == "" {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "sku 必填", apperr.ErrInvalidParams.Status))
		return
	}
	var asOf *time.Time
	if raw := c.Query("asOf"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "asOf 必须是 RFC3339（如 2026-08-01T00:00:00+08:00）", apperr.ErrInvalidParams.Status))
			return
		}
		asOf = &t
	}
	res, err := h.svc.GetCostSnapshot(c.Request.Context(), sku, asOf)
	if err != nil {
		response.Error(c, openErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// PullEvents 长轮询事件。
//
// @Summary 长轮询事件（开放接口）
// @Description 30s 超时，每秒 tick 一次。since 过滤 id > since；limit 默认 100 上限 100。超时返回空列表（非错误）。payload 为 jsonb 全量透传。
// @Tags 开放接口
// @Produce json
// @Security BearerAuth
// @Param since query int64 false "客户端已见最大事件 id" default(0)
// @Param limit query int false "返回上限" default(100)
// @Success 200 {object} api.APIResponse "code=0；data=EventsResult{last_id,events[]}"
// @Failure 401 {object} api.APIResponse "code=10002 token 已过期或不存在"
// @Router /api/open/events [get]
func (h *OpenApiHandler) PullEvents(c *gin.Context) {
	since, _ := strconv.ParseInt(c.DefaultQuery("since", "0"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	res, err := h.svc.PullEvents(c.Request.Context(), since, limit)
	if err != nil {
		response.Error(c, openErrToAppErr(err))
		return
	}
	response.Success(c, res)
}
