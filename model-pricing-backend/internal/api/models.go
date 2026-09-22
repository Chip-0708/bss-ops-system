// Package api 的 models.go：模型管理 M1 接口（内部门户）。
// 契约源头：docs/api/04-models.md（§1 列表 / §2 创建 / §3 维护）。
package api

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/model"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// ModelHandler 是模型管理接口的处理器。
type ModelHandler struct {
	svc *model.Service
}

// NewModelHandler 构造模型处理器。
func NewModelHandler(svc *model.Service) *ModelHandler {
	return &ModelHandler{svc: svc}
}

// ListModels 模型库列表。
//
// @Summary 模型库列表（view=family 按系列折叠 / view=sku 展开）
// @Description 全局主数据，不注入数据域行级过滤（§3.2 归属表不含 model_sku）。
// @Tags 模型管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param view query string false "视图：family（默认）/ sku"
// @Param keyword query string false "名称 / 编码 / SKU 模糊匹配"
// @Param vendor_id query int64 false "厂商筛选"
// @Param family_id query int64 false "系列筛选"
// @Param model_type query string false "模型类型"
// @Param lifecycle_status query string false "生命周期状态"
// @Param tier_tag query string false "档位筛选"
// @Param page query int false "页码" default(1)
// @Param size query int false "每页" default(20)
// @Success 200 {object} api.APIResponse "code=0；data={list,total,page,size}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M1:V"
// @Router /api/internal/models [get]
func (h *ModelHandler) ListModels(c *gin.Context) {
	var q model.ListQuery
	q.View = c.DefaultQuery("view", "family")
	if q.View != "family" && q.View != "sku" {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	q.Keyword = c.Query("keyword")
	q.ModelType = c.Query("model_type")
	q.LifecycleStatus = c.Query("lifecycle_status")
	q.TierTag = c.Query("tier_tag")
	q.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	q.Size, _ = strconv.Atoi(c.DefaultQuery("size", "20"))

	if v := c.Query("vendor_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			q.VendorID = &id
		}
	}
	if v := c.Query("family_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			q.FamilyID = &id
		}
	}

	if q.View == "sku" {
		res, err := h.svc.List(c.Request.Context(), q)
		if err != nil {
			response.Error(c, modelErrToAppErr(err))
			return
		}
		response.Success(c, res)
		return
	}

	res, err := h.svc.ListFamilies(c.Request.Context(), q)
	if err != nil {
		response.Error(c, modelErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// ModelOptions 返回厂商与系列下拉选项。
//
// @Summary 模型厂商与系列选项
// @Tags 模型管理
// @Produce json
// @Security BearerAuth
// @Success 200 {object} api.APIResponse "code=0；data={vendors,families}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M1:V"
// @Router /api/internal/models/options [get]
func (h *ModelHandler) ModelOptions(c *gin.Context) {
	res, err := h.svc.Options(c.Request.Context())
	if err != nil {
		response.Error(c, modelErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// CreateModel 创建 SKU。
//
// @Summary 创建 SKU（lifecycle_status 固定为 DRAFT）
// @Description 必须携带 Idempotency-Key；capability 未知 key 拒绝写入。
// @Tags 模型管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param Idempotency-Key header string true "幂等键（客户端生成唯一串）"
// @Param body body model.CreateSKUInput true "创建参数"
// @Success 200 {object} api.APIResponse "code=0；data=新建 SKU 完整对象"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法 / sku_code 重复 / capability 未知 key"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M1:E"
// @Failure 409 {object} api.APIResponse "code=10005 幂等冲突"
// @Router /api/internal/models [post]
func (h *ModelHandler) CreateModel(c *gin.Context) {
	var in model.CreateSKUInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}

	op := middleware.OperatorFrom(c)
	requestID := requestIDOf(c)

	sku, err := h.svc.Create(c.Request.Context(), in, op.OperatorID, requestID)
	if err != nil {
		response.Error(c, modelErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, sku)
	response.Success(c, sku)
}

// UpdateModel 维护 SKU。
//
// @Summary 维护 SKU（lifecycle_status 不可直接改）
// @Description 别名全量覆盖语义；lifecycle_status 须走 publish / deprecate / batch 流程。
// @Tags 模型管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "SKU ID"
// @Param body body model.UpdateSKUInput true "维护参数"
// @Success 200 {object} api.APIResponse "code=0；data=更新后完整对象"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M1:E"
// @Failure 404 {object} api.APIResponse "code=10004 SKU 不存在"
// @Router /api/internal/models/{id} [put]
func (h *ModelHandler) UpdateModel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}

	var in model.UpdateSKUInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}

	op := middleware.OperatorFrom(c)
	requestID := requestIDOf(c)

	sku, err := h.svc.Update(c.Request.Context(), id, in, op.OperatorID, requestID)
	if err != nil {
		response.Error(c, modelErrToAppErr(err))
		return
	}
	response.Success(c, sku)
}

// modelErrToAppErr 把模型领域错误映射为 HTTP 错误码。
func modelErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, model.ErrUnknownCapabilityKey):
		return apperr.ErrInvalidParams
	case errors.Is(err, model.ErrDuplicateSkuCode), errors.Is(err, model.ErrDuplicateAlias):
		return apperr.ErrInvalidParams
	case errors.Is(err, model.ErrFamilyVendorMismatch):
		return apperr.ErrInvalidParams
	case errors.Is(err, model.ErrMergeSameSKU), errors.Is(err, model.ErrSourceReferenced):
		return apperr.ErrInvalidParams
	case errors.Is(err, model.ErrBatchTooLarge), errors.Is(err, model.ErrBatchActionInvalid):
		return apperr.ErrInvalidParams
	case errors.Is(err, model.ErrPublishStateInvalid):
		return apperr.ErrInvalidParams
	case errors.Is(err, model.ErrSnapshotInvalid), errors.Is(err, model.ErrDeprecateStateInvalid):
		return apperr.ErrInvalidParams
	case errors.Is(err, model.ErrSelfApproval):
		return apperr.ErrInvalidParams
	case errors.Is(err, model.ErrApproverRoleMismatch):
		return apperr.ErrForbidden
	case errors.Is(err, model.ErrPublishConflict), errors.Is(err, model.ErrDeprecateConflict):
		return apperr.ErrIdempotency // 409 状态冲突
	case errors.Is(err, model.ErrNotFound):
		return apperr.ErrNotFound
	default:
		return apperr.ErrSystem
	}
}

// ReplaceAliases 别名维护（全量覆盖语义）。
//
// @Summary 别名维护（全量覆盖）
// @Description 前端提交该 SKU 当前全部别名，服务端做差集增删；传空数组 = 清空。
// @Tags 模型管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "SKU ID"
// @Param body body model.AliasInput true "别名列表（全量覆盖）"
// @Success 200 {object} api.APIResponse "code=0；data=更新后 SKU"
// @Failure 400 {object} api.APIResponse "code=10001 别名指向其他 SKU / 非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M1:E"
// @Failure 404 {object} api.APIResponse "code=10004 SKU 不存在"
// @Router /api/internal/models/{id}/aliases [post]
func (h *ModelHandler) ReplaceAliases(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	var in model.AliasInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	op := middleware.OperatorFrom(c)
	sku, err := h.svc.ReplaceAliases(c.Request.Context(), id, in, op.OperatorID)
	if err != nil {
		response.Error(c, modelErrToAppErr(err))
		return
	}
	response.Success(c, sku)
}

// SuggestAliases 查重建议（Top3，pg_trgm 模糊匹配）。
//
// @Summary 别名查重建议（Top3）
// @Description 基于 pg_trgm 相似度的 SKU 编码模糊匹配；读操作。
// @Tags 模型管理
// @Produce json
// @Security BearerAuth
// @Param keyword query string true "查重关键字（1~64）"
// @Success 200 {object} api.APIResponse "code=0；data={suggestions:[{sku_id,sku_code,score}]}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M1:V"
// @Router /api/internal/models/aliases/suggest [get]
func (h *ModelHandler) SuggestAliases(c *gin.Context) {
	keyword := c.Query("keyword")
	res, err := h.svc.SuggestAliases(c.Request.Context(), keyword)
	if err != nil {
		response.Error(c, modelErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// MergeAliases 一键合并为别名。
//
// @Summary 一键合并为别名
// @Description 被合并方名称写入 model_alias（source='MERGE'）；强制幂等。不做物理删除。
// @Tags 模型管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param Idempotency-Key header string true "幂等键"
// @Param body body model.MergeInput true "合并参数"
// @Success 200 {object} api.APIResponse "code=0；data={target_sku_id,merged_sku_id,alias_id,alias}"
// @Failure 400 {object} api.APIResponse "code=10001 target=source / source 被引用 / 别名已存在"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M1:E"
// @Failure 404 {object} api.APIResponse "code=10004 SKU 不存在"
// @Failure 409 {object} api.APIResponse "code=10005 幂等冲突"
// @Router /api/internal/models/aliases/merge [post]
func (h *ModelHandler) MergeAliases(c *gin.Context) {
	var in model.MergeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	op := middleware.OperatorFrom(c)
	res, err := h.svc.Merge(c.Request.Context(), in, op.OperatorID)
	if err != nil {
		response.Error(c, modelErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// BatchModels 批量改状态/打标签（部分成功语义）。
//
// @Summary 批量改状态/打标签
// @Description 单次 ≤200；禁止上架/退役动作；单个失败不影响其他项。
// @Tags 模型管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body model.BatchInput true "批量参数"
// @Success 200 {object} api.APIResponse "code=0；data={total,succeeded,failed:[{sku_id,code,message}]}"
// @Failure 400 {object} api.APIResponse "code=10001 超过 200 / 动作非法（含上架/退役）"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M1:E"
// @Router /api/internal/models/batch [post]
func (h *ModelHandler) BatchModels(c *gin.Context) {
	var in model.BatchInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	op := middleware.OperatorFrom(c)
	res, err := h.svc.Batch(c.Request.Context(), in, op.OperatorID)
	if err != nil {
		response.Error(c, modelErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// PublishModel 上架。
//
// @Summary 上架
// @Description 前置 lifecycle_status=PURCHASABLE；条件更新做并发占位（0 行 → 409）。
// @Tags 模型管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "SKU ID"
// @Param Idempotency-Key header string false "幂等键（建议携带）"
// @Param body body object false "{ \"remark\": \"可选备注\" }"
// @Success 200 {object} api.APIResponse "code=0；data={sku_id,lifecycle_status,published_at}"
// @Failure 400 {object} api.APIResponse "code=10001 状态不允许"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M1:E"
// @Failure 404 {object} api.APIResponse "code=10004 SKU 不存在"
// @Failure 409 {object} api.APIResponse "code=10005 并发冲突"
// @Router /api/internal/models/{id}/publish [post]
func (h *ModelHandler) PublishModel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	op := middleware.OperatorFrom(c)
	res, err := h.svc.Publish(c.Request.Context(), id, op.OperatorID)
	if err != nil {
		response.Error(c, modelErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// DeprecationImpact 退役影响分析。
//
// @Summary 退役影响分析
// @Description 统计 price_book / customer_quote 引用并推荐替代 SKU，结果落库 deprecation_impact。
// @Tags 模型管理
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "SKU ID"
// @Success 200 {object} api.APIResponse "code=0；data={sku_id,snapshot_id,references,reference_count,replacements,generated_at}"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M1:V"
// @Failure 404 {object} api.APIResponse "code=10004 SKU 不存在"
// @Router /api/internal/models/{id}/deprecation-impact [get]
func (h *ModelHandler) DeprecationImpact(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	op := middleware.OperatorFrom(c)
	res, err := h.svc.AnalyzeDeprecation(c.Request.Context(), id, op.OperatorID)
	if err != nil {
		response.Error(c, modelErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// DeprecateModel 发起退役（强制幂等）。
//
// @Summary 发起退役
// @Description 前置 PUBLISHED/PURCHASABLE；校验 impact snapshot；创建 change_request + 2 步审批。SKU 状态不变。
// @Tags 模型管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "SKU ID"
// @Param Idempotency-Key header string true "幂等键"
// @Param body body model.DeprecateInput true "退役参数"
// @Success 200 {object} api.APIResponse "code=0；data={sku_id,approval_id,sunset_date,lifecycle_status}"
// @Failure 400 {object} api.APIResponse "code=10001 状态不允许 / snapshot 无效或过期"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 无 M1:E"
// @Failure 404 {object} api.APIResponse "code=10004 SKU 不存在"
// @Failure 409 {object} api.APIResponse "code=10005 幂等冲突"
// @Router /api/internal/models/{id}/deprecate [post]
func (h *ModelHandler) DeprecateModel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	var in model.DeprecateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	op := middleware.OperatorFrom(c)
	requestID := requestIDOf(c)
	res, err := h.svc.StartDeprecate(c.Request.Context(), id, in, op.OperatorID, requestID)
	if err != nil {
		response.Error(c, modelErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// DecideApproval 审批动作（双人审批，禁止自审）。
//
// @Summary 审批动作
// @Description 角色匹配 required_role；同一操作员不得完成同一单的两个步骤；两步 APPROVED → SKU 转 DEPRECATING。
// @Tags 模型管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "change_request ID"
// @Param Idempotency-Key header string true "幂等键"
// @Param body body model.DecisionInput true "审批参数"
// @Success 200 {object} api.APIResponse "code=0；data={change_request_id,step_no,decision,final_status}"
// @Failure 400 {object} api.APIResponse "code=10001 参数非法 / 自审"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 403 {object} api.APIResponse "code=10003 角色不匹配"
// @Failure 404 {object} api.APIResponse "code=10004 审批单不存在"
// @Failure 409 {object} api.APIResponse "code=10005 并发冲突"
// @Router /api/internal/approvals/{id}/decision [post]
func (h *ModelHandler) DecideApproval(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	var in model.DecisionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}
	op := middleware.OperatorFrom(c)
	requestID := requestIDOf(c)
	roles := rolesOfOperator(op)
	res, err := h.svc.DecideApprovalByType(c.Request.Context(), id, in, op.OperatorID, roles, requestID)
	if err != nil {
		response.Error(c, modelErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// rolesOfOperator 从操作员上下文提取角色 code 列表（审批 required_role 匹配用）。
func rolesOfOperator(op *middleware.Operator) []string {
	if op == nil {
		return nil
	}
	return op.Roles
}
