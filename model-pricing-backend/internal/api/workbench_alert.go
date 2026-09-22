// Package api 的 workbench_alert.go：10b 告警处理接口。
package api

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/workbench"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// WorkbenchAlertHandler 告警处理。
type WorkbenchAlertHandler struct {
	alertSvc *workbench.AlertService
}

// NewWorkbenchAlertHandler 构造。
func NewWorkbenchAlertHandler(alertSvc *workbench.AlertService) *WorkbenchAlertHandler {
	return &WorkbenchAlertHandler{alertSvc: alertSvc}
}

// alertErrToAppErr 本 handler 的领域错误 → HTTP 错误码。
func alertErrToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, workbench.ErrAlertNotFound):
		return apperr.New(apperr.ErrNotFound.Code, err.Error(), apperr.ErrNotFound.Status) // 404
	case errors.Is(err, workbench.ErrAlertTerminal):
		return apperr.New(apperr.ErrIdempotency.Code, err.Error(), apperr.ErrIdempotency.Status) // 409 业务冲突
	case errors.Is(err, workbench.ErrAlertActionInvalid):
		return apperr.New(apperr.ErrInvalidParams.Code, err.Error(), apperr.ErrInvalidParams.Status) // 400
	default:
		return apperr.ErrSystem
	}
}

// ListAlerts 告警列表。
//
//	@Summary	告警列表
//	@Tags		workbench
//	@Param		severity	 query	 string	false	"严重级别（CRITICAL/HIGH/MID/LOW）"
//	@Param		status		 query	 string	false	"状态（OPEN/HANDLING/RESOLVED/IGNORED）"
//	@Param		alert_type	 query	 string	false	"告警类型（QUOTE_EXPIRE/QUOTE_ANOMALY/RETRO_LIMIT）"
//	@Param		page		 query	 int		false	"页码（默认 1）"
//	@Param		size		 query	 int		false	"每页大小（默认 20，最大 100）"
//	@Success	200	{object}	api.APIResponse	"code=0；data=AlertListResult{list,total,page,size}"
//	@Router		/internal/alerts [get]
//	@Security	BearerAuth
func (h *WorkbenchAlertHandler) ListAlerts(c *gin.Context) {
	var q workbench.AlertQuery
	q.Severity = c.Query("severity")
	q.Status = c.Query("status")
	q.AlertType = c.Query("alert_type")
	q.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	q.Size, _ = strconv.Atoi(c.DefaultQuery("size", "20"))

	result, err := h.alertSvc.ListAlerts(c.Request.Context(), q)
	if err != nil {
		response.Error(c, apperr.ErrSystem)
		return
	}
	response.Success(c, result)
}

// HandleAlert 告警处理。
//
//	@Summary	告警处理
//	@Tags		workbench
//	@Param		body	body		workbench.AlertActionInput	true	"处理动作"
//	@Success	200	{object}	api.APIResponse	"code=0；data=AlertActionResult{alert_id,status,todo_id}"
//	@Router		/internal/alerts [post]
//	@Security	BearerAuth
func (h *WorkbenchAlertHandler) HandleAlert(c *gin.Context) {
	var in workbench.AlertActionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, apperr.New(apperr.ErrInvalidParams.Code, err.Error(), apperr.ErrInvalidParams.Status))
		return
	}

	op := middleware.OperatorFrom(c)
	operatorRole := ""
	if len(op.Roles) > 0 {
		operatorRole = op.Roles[0]
	}
	result, err := h.alertSvc.HandleAlert(c.Request.Context(), in, op.OperatorID, operatorRole)
	if err != nil {
		response.Error(c, alertErrToAppErr(err))
		return
	}
	response.Success(c, result)
}
