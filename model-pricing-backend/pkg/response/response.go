// Package response 定义并帮助构造所有业务接口统一返回的响应包。
// 格式遵循设计文档 §8.0 通用约定：{ "code": 0, "message": "ok", "data": {...}, "requestId": "..." }。
package response

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"model_bss/internal/infra/middleware"
	"model_bss/pkg/apperr"
)

// Body 是所有业务接口的统一响应结构体。
type Body struct {
	Code      int         `json:"code"`      // 业务错误码；0 表示成功
	Message   string      `json:"message"`   // 返回文字，成功时固定 "ok"
	Data      interface{} `json:"data"`      // 实际业务载荷
	RequestID string      `json:"requestId"` // 请求追踪号，来自 X-Request-Id
}

// Success 返回 code=0 且 HTTP status=200 的统一响应。
func Success(c *gin.Context, data interface{}) {
	rid := middleware.FromContext(c)
	c.JSON(http.StatusOK, Body{
		Code:      0,
		Message:   "ok",
		Data:      data,
		RequestID: rid,
	})
}

// Error 根据业务错误对象返回带有对应 code/message/status 的统一响应。
func Error(c *gin.Context, err apperr.Error) {
	ErrorData(c, err, nil)
}

// ErrorData 返回业务错误，并保留调用方需要展示的结构化安全明细。
func ErrorData(c *gin.Context, err apperr.Error, data interface{}) {
	rid := middleware.FromContext(c)
	c.JSON(err.Status, Body{
		Code:      err.Code,
		Message:   err.Message,
		Data:      data,
		RequestID: rid,
	})
}
