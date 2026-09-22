// Package api 的 cost.go：成本基线只读接口（内部门户 M5，06-cost.md §2 列表 / §3 历史）。
//
// 字段剔除首次真正挂接（红线 6 + 000017）：
// handler 在 service 出 DTO 之后、response.Success 之前，用 op.FieldMask +
// fieldmask.Apply 物理删除命中键（SALES 剔 unit_cost 等；SUPPLIER 再剔 floor_price / calc_snapshot）。
// 逐接口挂接，不做全局钩子（裁决 2：全局会波及阶段 0–5 已上线接口，未逐接口审过之前不动）。
// mask 来自登录快照（snap.FieldMask()）——000017 改库后，旧 token 必须重新登录才带新 mask。
package api

import (
	"time"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/cost"
	"model_bss/pkg/apperr"
	"model_bss/pkg/fieldmask"
	"model_bss/pkg/response"
)

// CostHandler 是成本只读接口的处理器。
type CostHandler struct {
	readSvc *cost.ReadService
}

// NewCostHandler 构造处理器。
func NewCostHandler(readSvc *cost.ReadService) *CostHandler {
	return &CostHandler{readSvc: readSvc}
}

// costPageSize 规整分页参数：page<1→1；size<1→20；size>100→100。
// 与 models/quotes 现有约定一致（README 默认 20，上限 100 防爆查）。
func costPageSize(c *gin.Context) (page, size int) {
	page = mustAtoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	size = mustAtoi(c.DefaultQuery("size", "20"))
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

// maskAndSuccess 把 service 结果按当前操作员 field_mask 物理剔除后返回。
// op 为 nil（不可能——受保护路由必有）时保守回原始数据，不静默泄露。
func maskAndSuccess(c *gin.Context, data interface{}) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Success(c, data)
		return
	}
	response.Success(c, fieldmask.Apply(data, op.FieldMask))
}

// ListCostBaselines 成本基线列表（当前版本）。
//
// @Summary 成本基线列表（当前版本，M5:V，无归属过滤）
// @Description 每行 = 某 SKU 的当前成本基线版本。unit_cost 为代表组件完全成本（input 优先，否则组件字母序）；unit_cost_basis 标注该组件；floor_price = unit_cost/(1−min_gross_margin)，读 sys_config 失败时该批为 null（读路径降级）。supplier_count 为当前 EFFECTIVE 报价的去重供应商数，single_point=(count==1)。按 field_mask 剔除敏感键：SALES 剔 unit_cost/unit_cost_basis/supplier_cost/calc_snapshot，SUPPLIER 另剔 floor_price；buyer（PROCUREMENT）可见全字段。
// @Tags 成本管理
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param size query int false "每页（上限 100）" default(20)
// @Param keyword query string false "sku_code 模糊匹配（大小写不敏感）"
// @Param only_single_point query bool false "只返回 single_point=true 的行"
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M5:V"
// @Router /api/internal/cost/baselines [get]
func (h *CostHandler) ListCostBaselines(c *gin.Context) {
	page, size := costPageSize(c)
	q := cost.BaselineListQuery{
		Keyword:         c.Query("keyword"),
		OnlySinglePoint: c.Query("only_single_point") == "true",
		Page:            page,
		Size:            size,
	}
	res, err := h.readSvc.ListBaselines(c.Request.Context(), q)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	maskAndSuccess(c, res)
}

// GetCostBaselineHistory 成本版本历史（支持历史时点）。
//
// @Summary 成本版本历史（M5:V，{sku} 为 sku_id 或 sku_code）
// @Description asOf 为空 = 全量时间线（version DESC，返回 version / valid_from / valid_to / change_reason / primary_supplier_id / primary_supplier_name / unit_cost / unit_cost_basis / calc_snapshot）；asOf 非空 = 该时刻生效的那一个版本（valid_from<=asOf AND (valid_to IS NULL OR valid_to>asOf)，查不到返回 200+空 list）。{sku} 纯数字按 id，否则按 sku_code；都匹配不到 → 404。calc_snapshot 整体返回（含 formula_version/params/scores/excluded_suppliers），SUPPLIER 角色整块剔除。
// @Tags 成本管理
// @Produce json
// @Security BearerAuth
// @Param sku path string true "sku_id（纯数字）或 sku_code"
// @Param asOf query string false "RFC3339 时刻（如 2026-08-01T00:00:00+08:00）"
// @Success 200 {object} api.APIResponse "code=0；data={list:[…]}"
// @Failure 400 {object} api.APIResponse "code=10001 asOf 非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M5:V"
// @Failure 404 {object} api.APIResponse "code=10004 SKU 不存在"
// @Router /api/internal/cost/baselines/{sku}/history [get]
func (h *CostHandler) GetCostBaselineHistory(c *gin.Context) {
	sku := c.Param("sku")
	if sku == "" {
		response.Error(c, apperr.ErrInvalidParams)
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
	items, ok, err := h.readSvc.History(c.Request.Context(), sku, asOf)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	if !ok {
		response.Error(c, apperr.ErrNotFound)
		return
	}
	maskAndSuccess(c, gin.H{"list": items})
}
