// Package api 的 cost_compare.go：§4 比价与 §5 议价机会接口（内部门户 M5:V）。
// 两个接口都是只读——handler 只规整参数与路由 404/400，业务编排全部在 cost.CompareService。
// 与 §2/§3 列表/历史同一挂接点 maskAndSuccess（按端点，不做全局钩子——6b-4 裁决 2）。
package api

import (
	"github.com/gin-gonic/gin"

	"model_bss/internal/domain/cost"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// CostCompareHandler 是 §4/§5 接口的处理器。
type CostCompareHandler struct {
	svc *cost.CompareService
}

// NewCostCompareHandler 构造处理器。
func NewCostCompareHandler(svc *cost.CompareService) *CostCompareHandler {
	return &CostCompareHandler{svc: svc}
}

// Compare 多供应商比价 + 成本区间 + 趋势。
//
// @Summary 多供应商比价 + 成本区间 + 趋势（M5:V）
// @Description 对当前 EFFECTIVE 报价**实时计算**（不走 calc_snapshot——快照受任务消费延迟影响最多 1 分钟，且 scores 里无 constraints/逐组件成本）。suppliers[] 含逐组件完全成本、constraints 原始值、四因子得分、is_primary/is_backup；range 实时算（cost_weighted 按四因子 total 加权，Σtotal=0 退化为算术平均）；trend 按天聚合取每日最新版本（Asia/Shanghai 日历日），不插值；market_best = 参与供应商中完全成本最低值。days 容错规整：<=0→90，>365→365。
// @Tags 成本管理
// @Produce json
// @Security BearerAuth
// @Param sku path string true "sku_id（纯数字）或 sku_code"
// @Param days query int false "趋势窗口天数（默认 90，上限 365）" default(90)
// @Success 200 {object} api.APIResponse "code=0；data={sku_id,sku_code,currency,suppliers,range,trend,market_best,market_best_basis}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M5:V"
// @Failure 404 {object} api.APIResponse "code=10004 SKU 不存在或当前无 EFFECTIVE 报价"
// @Router /api/internal/cost/baselines/{sku}/compare [get]
func (h *CostCompareHandler) Compare(c *gin.Context) {
	sku := c.Param("sku")
	if sku == "" {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	// days 容错（陷阱 7：查询参数不打断前端）——规整规则在 service 层（normalizeDays），
	// handler 只做解析：无法解析当缺省（Atoi 失败 → 0 → 90）。
	days := mustAtoi(c.DefaultQuery("days", "90"))
	res, ok, err := h.svc.Compare(c.Request.Context(), sku, days)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	if !ok {
		response.Error(c, apperr.ErrNotFound)
		return
	}
	maskAndSuccess(c, res)
}

// Opportunities 议价机会看板 / 单点依赖告警。
//
// @Summary 议价机会看板 / 单点依赖告警（M5:V）
// @Description type=bargain：主供应商价格显著高于市场最低价的清单（阈值 quote_anomaly_mkt，默认 0.30，严格大于才入选；deviation=(primary−market_best)/market_best）。type=single_point：只有 1 家有效报价的已上架 SKU（复用 §2 列表的 single_point 语义，不另写查询）。type 缺失或非法 → 400。list 永远是非 null 数组（空清单是合法事实，前端按 [] 渲染）。
// @Tags 成本管理
// @Produce json
// @Security BearerAuth
// @Param type query string true "bargain | single_point"
// @Success 200 {object} api.APIResponse "code=0；data={type,list,total}"
// @Failure 400 {object} api.APIResponse "code=10001 type 缺失或非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M5:V"
// @Router /api/internal/cost/opportunities [get]
func (h *CostCompareHandler) Opportunities(c *gin.Context) {
	typ := c.Query("type")
	if typ == "" {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code,
			"缺少必填查询参数 type（可选值：bargain / single_point）", apperr.ErrInvalidParams.Status))
		return
	}
	if !cost.ValidOpportunityType(typ) {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code,
			"非法的 type（可选值：bargain / single_point）", apperr.ErrInvalidParams.Status))
		return
	}
	res, err := h.svc.Opportunities(c.Request.Context(), typ)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	maskAndSuccess(c, res)
}
