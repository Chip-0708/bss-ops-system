// Package middleware 提供 HTTP 请求生命周期中的横切组件。
//
// 本文件实现 RequestID 中间件：为每个请求提供稳定的 request_id，
// 写入响应头 X-Request-Id，供调用方/服务端串联日志与排查问题。
package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequestIDHeader 是约定对外返回的请求 ID 头名称。
const RequestIDHeader = "X-Request-Id"

// ctxKeyRequestID 用于在 gin.Context 中保存当前请求的 request_id。
const ctxKeyRequestID = "request_id"

// RequestID 是 Gin 中间件：若请求头 X-Request-Id 已存在则沿用（允许网关/代理串联），
// 否则生成一个新的 UUID v4，并将其写入响应头并注入 gin.Context。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		c.Header(RequestIDHeader, id)
		c.Set(ctxKeyRequestID, id)
		c.Next()
	}
}

// FromContext 提取当前请求上的 request_id；若不存在则返回空字符串。
func FromContext(c *gin.Context) string {
	if v, ok := c.Get(ctxKeyRequestID); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
