// Package api 的 price_sync.go：官方价采集批次 + 暂存区接口（内部门户 M2，
// 07-supplier-and-price-change.md §5/§6，阶段 7a）。
// 写接口 POST /staging-prices 必挂幂等（红线 5）；读接口无归属过滤
// （官方价是全局主数据，与 M1 模型管理同款口径）。
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

// PriceSyncHandler 是采集/暂存接口的处理器。
type PriceSyncHandler struct {
	syncSvc *price.SyncService
}

// NewPriceSyncHandler 构造处理器。
func NewPriceSyncHandler(syncSvc *price.SyncService) *PriceSyncHandler {
	return &PriceSyncHandler{syncSvc: syncSvc}
}

// priceSyncPageSize 规整分页参数（与 costPageSize 同款：page<1→1；size<1→20；size>100→100）。
func priceSyncPageSize(c *gin.Context) (page, size int) {
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

// syncErrToAppErr 把采集/暂存领域错误映射为 HTTP 错误码（message 透传）。
func syncErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, price.ErrSyncJobNotFound):
		return apperr.New(apperr.ErrNotFound.Code, err.Error(), apperr.ErrNotFound.Status)
	case errors.Is(err, price.ErrJobTypeInvalid),
		errors.Is(err, price.ErrJobSourceEmpty),
		errors.Is(err, price.ErrJobSKUInvalid),
		errors.Is(err, price.ErrStagingItemsEmpty),
		errors.Is(err, price.ErrStagingTooMany),
		errors.Is(err, price.ErrStagingItemNoSKU),
		errors.Is(err, price.ErrStagingSKUNotFound),
		errors.Is(err, price.ErrStagingCurrencyInvalid),
		errors.Is(err, price.ErrStagingPayloadEmpty),
		errors.Is(err, price.ErrStagingComponentInvalid),
		errors.Is(err, price.ErrStagingPriceInvalid):
		return apperr.New(apperr.ErrInvalidParams.Code, err.Error(), apperr.ErrInvalidParams.Status)
	default:
		return apperr.ErrSystem
	}
}

// CreateSyncJob 创建采集批次。
//
// @Summary 创建采集批次（M2:E，创建即 SUCCESS）
// @Description MVP 人工录入模式：sync_job 只是「一批暂存录入的容器」，创建即完成（status=SUCCESS、started_at=finished_at=now、error_msg=null）。sku_ids 仅留 item_count 痕迹（表无 payload 列）。P1 自动采集时补 RUNNING/FAILED 状态机（CLAUDE.md 遗留 7a-①）。
// @Tags 官方价变更
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body price.CreateJobInput true "job_type（SYNC_PRICES 等）+ source + sku_ids"
// @Success 200 {object} api.APIResponse "code=0；data={id,job_type,source,status,started_at,finished_at,error_msg,item_count}"
// @Failure 400 {object} api.APIResponse "code=10001 job_type 非法 / source 空白 / sku_ids 含非正整数"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M2:E"
// @Router /api/internal/price-sync/jobs [post]
func (h *PriceSyncHandler) CreateSyncJob(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	var body price.CreateJobInput
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("JSON 解析失败：%s", err.Error()), apperr.ErrInvalidParams.Status))
		return
	}
	job, err := h.syncSvc.CreateJob(c.Request.Context(), body, price.Operator{
		OperatorID:   op.OperatorID,
		OperatorRole: operatorRoleOf(op), // 只用于审计落库，绝不参与权限判定
	}, requestIDOf(c))
	if err != nil {
		response.Error(c, syncErrToAppErr(err))
		return
	}
	response.Success(c, job)
}

// ListSyncJobs 采集批次列表。
//
// @Summary 采集批次列表（M2:V，id DESC 分页）
// @Tags 官方价变更
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param size query int false "每页（上限 100）" default(20)
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M2:V"
// @Router /api/internal/price-sync/jobs [get]
func (h *PriceSyncHandler) ListSyncJobs(c *gin.Context) {
	page, size := priceSyncPageSize(c)
	res, err := h.syncSvc.ListJobs(c.Request.Context(), page, size)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	response.Success(c, res)
}

// CreateStagingPrices 手工录入采集结果。
//
// @Summary 手工录入采集结果（M2:E，幂等必填 Idempotency-Key）
// @Description 逐行校验（sku_id 存在 / raw_sku_code 精确匹配 / currency 3 位 / payload 非空且值合法非负 decimal），任一行非法整批 400 不落库。sku_id 与 raw_sku_code 二选一（sku_id 优先，都给了忽略 raw_sku_code）；raw_sku_code 匹配不到 → sku_id=null、match_status=UNMATCHED（只标记，处理策略待产品裁决——遗留 7a-③）。processed 恒 false（7b confirm 置 true）。diff 不在写入时落库——GET 列表实时计算。
// @Tags 官方价变更
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body price.CreateStagingInput true "sync_job_id + items[]（sku_id/raw_sku_code 二选一 + currency + payload）"
// @Success 200 {object} api.APIResponse "code=0；data={created_count,staging_ids}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法（含幂等键缺失）"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M2:E"
// @Failure 404 {object} api.APIResponse "code=10004 sync_job_id 不存在"
// @Failure 409 {object} api.APIResponse "code=10005 幂等冲突"
// @Router /api/internal/staging-prices [post]
func (h *PriceSyncHandler) CreateStagingPrices(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	if op == nil {
		response.Error(c, apperr.ErrUnauthorized)
		return
	}
	var body price.CreateStagingInput
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, fmt.Sprintf("JSON 解析失败：%s", err.Error()), apperr.ErrInvalidParams.Status))
		return
	}
	res, err := h.syncSvc.CreateStaging(c.Request.Context(), body, price.Operator{
		OperatorID:   op.OperatorID,
		OperatorRole: operatorRoleOf(op),
	}, requestIDOf(c))
	if err != nil {
		response.Error(c, syncErrToAppErr(err))
		return
	}
	data := gin.H{"created_count": res.CreatedCount, "staging_ids": res.StagingIDs}
	middleware.SetIdempotencyResult(c, data) // 重放返回首次结果（6d-3 遗留③的同款防线）
	response.Success(c, data)
}

// ListStagingPrices 暂存区列表（含实时差异比对）。
//
// @Summary 暂存区列表（M2:V，diff 实时计算）
// @Description diff_status：NEW（无同币种当前官方价）/ CHANGED / UNCHANGED / UNMATCHED（sku_id=null）。diff_detail 只含 CHANGED 组件：old_price 为 DB numeric(20,8) 原文（如 "2.50000000"，与录入原文 "2.50" 数值等价——前端按数值比较，勿按字符串相等）；delta_pct=(new−old)/old 6 位小数，old=0 或 NEW 时为 null。payload 多出的组件不参与比对。按 id ASC（录入序）分页。
// @Tags 官方价变更
// @Produce json
// @Security BearerAuth
// @Param sync_job_id query int64 false "按采集批次过滤"
// @Param page query int false "页码" default(1)
// @Param size query int false "每页（上限 100）" default(20)
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M2:V"
// @Router /api/internal/staging-prices [get]
func (h *PriceSyncHandler) ListStagingPrices(c *gin.Context) {
	page, size := priceSyncPageSize(c)
	q := price.StagingQuery{Page: page, Size: size}
	if raw := c.Query("sync_job_id"); raw != "" {
		id := mustAtoi(raw)
		if id <= 0 {
			response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "sync_job_id 必须是正整数", apperr.ErrInvalidParams.Status))
			return
		}
		id64 := int64(id)
		q.SyncJobID = &id64
	}
	res, err := h.syncSvc.ListStaging(c.Request.Context(), q)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	response.Success(c, res)
}
