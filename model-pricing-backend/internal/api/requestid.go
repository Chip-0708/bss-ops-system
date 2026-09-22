// Package api 的 requestid.go：request_id 统一取值 helper。
// 审计/业务字段的 request_id 一律取 X-Request-Id（响应 requestId），
// 不再用 Idempotency-Key（幂等键是客户端生成的去重键，不是请求追踪号——5d 热修 P1-4）。
package api

import (
	"github.com/gin-gonic/gin"

	infra_middleware "model_bss/internal/infra/middleware"
)

// requestIDOf 返回本次请求的 request_id（X-Request-Id，与响应 requestId 一致）。
// 用于 audit_log / task_job / event_outbox 等所有写库的 request_id 字段。
// 幂等判定仍由 Idempotency-Key 独立承担，二者不再混用。
func requestIDOf(c *gin.Context) string {
	return infra_middleware.FromContext(c)
}
