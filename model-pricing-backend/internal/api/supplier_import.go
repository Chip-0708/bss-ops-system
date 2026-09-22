// Package api 的 supplier_import.go：批量导入接口（05-quotes.md §6）。
// CSV 解析在 domain 层（[]byte 进出）；本层只负责 multipart 读取与响应。
package api

import (
	"io"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/supplier"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// SupplierImportHandler 是批量导入接口的处理器。
type SupplierImportHandler struct {
	supplierSvc *supplier.Service
	importSvc   *supplier.ImportService
}

// NewSupplierImportHandler 构造导入处理器。
func NewSupplierImportHandler(supplierSvc *supplier.Service, importSvc *supplier.ImportService) *SupplierImportHandler {
	return &SupplierImportHandler{supplierSvc: supplierSvc, importSvc: importSvc}
}

// importErrToAppErr 把导入领域错误映射为 HTTP 错误码（message 透传）。
func importErrToAppErr(err error) apperr.Error {
	// 导入参数类错误 → 400；落库阶段的报价领域错误复用 quoteErrToAppErr（409 互斥等）
	if quoteErr := quoteErrToAppErr(err); quoteErr.Code != apperr.ErrSystem.Code {
		return quoteErr
	}
	return apperr.New(apperr.ErrInvalidParams.Code, err.Error(), apperr.ErrInvalidParams.Status)
}

// DownloadQuoteTemplate 下载导入模板。
//
// @Summary 下载导入模板（CSV，UTF-8 带 BOM）
// @Description 列按可报价 SKU 的官方价组件并集动态生成（official_/multiplier_/price_ × N 组件）；valid_from/valid_to 不在 CSV 里（由 confirm 请求体传入），last_valid_to 为该 SKU 上次报价到期日只读参考。绝不含他人报价/平台成本/毛利。?format=xlsx 预留但本阶段返回 400。
// @Tags 供应商门户
// @Produce text/csv
// @Security BearerAuth
// @Param scope query string false "history / vendor / family / active（默认）"
// @Param vendor_id query int64 false "scope=vendor 时必填"
// @Param family_id query int64 false "scope=family 时必填"
// @Param format query string false "csv（默认）/ xlsx（预留，返回 400）"
// @Success 200 {string} string "CSV 文件（带 BOM）"
// @Failure 400 {object} api.APIResponse "code=10001 format=xlsx 暂未开放"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 404 {object} api.APIResponse "code=10004 供应商档案不存在"
// @Router /api/supplier/quotes/template [get]
func (h *SupplierImportHandler) DownloadQuoteTemplate(c *gin.Context) {
	if c.Query("format") == "xlsx" {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, supplier.ErrImportFormatNotSupported.Error(), 400))
		return
	}
	sup, _, ok := resolveSupplierFrom(c, h.supplierSvc)
	if !ok {
		return
	}
	data, err := h.importSvc.Template(c.Request.Context(), sup.ID, supplier.TemplateQuery{
		Scope:    c.DefaultQuery("scope", "active"),
		VendorID: parseInt64Query(c, "vendor_id"),
		FamilyID: parseInt64Query(c, "family_id"),
	})
	if err != nil {
		response.Error(c, importErrToAppErr(err))
		return
	}
	c.Header("Content-Disposition", `attachment; filename="quote_template.csv"`)
	c.Data(200, "text/csv; charset=utf-8", data)
}

// PreviewQuoteImport 上传校验预览。
//
// @Summary 上传校验预览（不落库）
// @Description 逐行校验（SKU 命中/币种锁定/汇率档位/四种填法），返回 OK/WARN/ERROR 与 preview_items（只含 OK+WARN）。ERROR 行不得入库。confirm 时前端把修正后的行整体回传，服务端重新全量校验（不信任前端）。行号从 2 开始（1=表头）。
// @Tags 供应商门户
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param file formData file true "CSV 文件（≤2MB，≤500 行）"
// @Success 200 {object} api.APIResponse "code=0；data={token,total,ok_count,warn_count,error_count,rows,preview_items}"
// @Failure 400 {object} api.APIResponse "code=10001 文件超限 / 行数超限 / CSV 格式非法"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Router /api/supplier/quotes/import/preview [post]
func (h *SupplierImportHandler) PreviewQuoteImport(c *gin.Context) {
	sup, _, ok := resolveSupplierFrom(c, h.supplierSvc)
	if !ok {
		return
	}

	fh, err := c.FormFile("file")
	if err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "缺少 file 字段", 400))
		return
	}
	if fh.Size > 2<<20 {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, supplier.ErrImportFileTooLarge.Error(), 400))
		return
	}
	f, err := fh.Open()
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	defer func() { _ = f.Close() }()
	content, err := io.ReadAll(f)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}

	res, err := h.importSvc.Preview(c.Request.Context(), sup, content)
	if err != nil {
		response.Error(c, importErrToAppErr(err))
		return
	}
	response.Success(c, res)
}

// ConfirmQuoteImport 确认入库。
//
// @Summary 确认导入（幂等，不跳过审批）
// @Description 请求体与手工提交 §3 同构（valid_from/valid_to/remark/items[]）；与 §3 共用同一套九步校验（不信任前端，全量重校验）。不做「必须是 preview_items 的 sku 子集」校验（技术上无法闭环，属前端纪律）。落库 source='IMPORT'，审计 QUOTE_IMPORT，同样进入 APPROVING + todo_task。
// @Tags 供应商门户
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param Idempotency-Key header string true "幂等键"
// @Param body body supplier.SubmitQuoteInput true "报价单（valid_from/valid_to/items 必填）"
// @Success 200 {object} api.APIResponse "code=0；data 同 §3 提交报价"
// @Failure 400 {object} api.APIResponse "code=10001 校验失败（message 可直接展示）"
// @Failure 401 {object} api.APIResponse "code=10002 未认证"
// @Failure 409 {object} api.APIResponse "code=10005 非终态互斥 / 幂等冲突"
// @Router /api/supplier/quotes/import/confirm [post]
func (h *SupplierImportHandler) ConfirmQuoteImport(c *gin.Context) {
	sup, op, ok := resolveSupplierFrom(c, h.supplierSvc)
	if !ok {
		return
	}

	var in supplier.SubmitQuoteInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.ErrInvalidParams)
		return
	}

	res, err := h.importSvc.Confirm(c.Request.Context(), sup, in, op.OperatorID, requestIDOf(c))
	if err != nil {
		response.Error(c, quoteErrToAppErr(err))
		return
	}
	middleware.SetIdempotencyResult(c, res)
	response.Success(c, res)
}

// resolveSupplierFrom 复用 SupplierHandler 的身份解析（同包内共享）。
func resolveSupplierFrom(c *gin.Context, svc *supplier.Service) (*supplier.Supplier, *middleware.Operator, bool) {
	// 复用 supplier.go 的 resolveSupplier（同包）
	h := &SupplierHandler{supplierSvc: svc}
	return h.resolveSupplier(c)
}
