// Package api 的 workbench_audit.go：10b 审计日志查询与导出接口。
package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"model_bss/internal/domain/workbench"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// WorkbenchAuditHandler 审计日志。
type WorkbenchAuditHandler struct {
	auditSvc *workbench.AuditService
}

// NewWorkbenchAuditHandler 构造。
func NewWorkbenchAuditHandler(auditSvc *workbench.AuditService) *WorkbenchAuditHandler {
	return &WorkbenchAuditHandler{auditSvc: auditSvc}
}

// ListAuditLogs 审计日志列表。
//
//	@Summary	审计日志列表
//	@Tags		workbench
//	@Param		action			query		string	false	"动作（如 CUSTOMER_QUOTE_ACCEPTED）"
//	@Param		target_type		query		string	false	"目标类型（如 CUSTOMER_QUOTE）"
//	@Param		target_id		query		int		false	"目标 ID"
//	@Param		operator_id		query		int		false	"操作员 ID"
//	@Param		operator_role	query		string	false	"操作员角色"
//	@Param		source_type		query		string	false	"来源类型（INTERNAL/WORKER/SYSTEM）"
//	@Param		from			query		string	false	"起始时间（RFC3339，左闭）"
//	@Param		to				query		string	false	"结束时间（RFC3339，右开）"
//	@Param		page			query		int		false	"页码（默认 1）"
//	@Param		size			query		int		false	"每页大小（默认 20，最大 100）"
//	@Success	200	{object}	api.APIResponse	"code=0；data=AuditListResult{items,total}"
//	@Router		/internal/audit-logs [get]
//	@Security	BearerAuth
func (h *WorkbenchAuditHandler) ListAuditLogs(c *gin.Context) {
	var q workbench.AuditQuery
	q.Action = c.Query("action")
	q.TargetType = c.Query("target_type")
	q.OperatorRole = c.Query("operator_role")
	q.SourceType = c.Query("source_type")

	if v := c.Query("target_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			q.TargetID = &id
		}
	}
	if v := c.Query("operator_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			q.OperatorID = &id
		}
	}
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.From = &t
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.To = &t
		}
	}
	q.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	q.Size, _ = strconv.Atoi(c.DefaultQuery("size", "20"))

	result, err := h.auditSvc.ListAuditLogs(c.Request.Context(), q)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	response.Success(c, result)
}

// ExportAuditLogs 导出审计日志。
//
//	@Summary	导出审计日志
//	@Tags		workbench
//	@Param		format	query		string	true	"格式（csv/xlsx）"
//	@Param		from	query		string	true	"起始时间（RFC3339，左闭）"
//	@Param		to		query		string	true	"结束时间（RFC3339，右开）"
//	@Success	200	{file}		file
//	@Router		/internal/audit-logs/export [get]
//	@Security	BearerAuth
func (h *WorkbenchAuditHandler) ExportAuditLogs(c *gin.Context) {
	var q workbench.AuditExportQuery
	q.Format = c.DefaultQuery("format", "csv")

	if v := c.Query("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.From = &t
		} else {
			response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "from 时间格式错误（需 RFC3339）", apperr.ErrInvalidParams.Status))
			return
		}
	} else {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "from 参数必填", apperr.ErrInvalidParams.Status))
		return
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.To = &t
		} else {
			response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "to 时间格式错误（需 RFC3339）", apperr.ErrInvalidParams.Status))
			return
		}
	} else {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, "to 参数必填", apperr.ErrInvalidParams.Status))
		return
	}

	result, err := h.auditSvc.ExportAuditLogs(c.Request.Context(), q)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, result.Filename))
	c.Data(http.StatusOK, result.ContentType, result.Data)
}
