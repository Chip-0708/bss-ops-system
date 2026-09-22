package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AccessLog 是基于 zap 的请求访问日志中间件。
// 每个请求在返回时写一条结构化日志，包含 method/path/status/latency/request_id 等字段。
// 骨架阶段只保留基础字段，后续可在此补充 operator_id、client_ip、body_size 等。
func AccessLog(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("client_ip", c.ClientIP()),
			zap.String("request_id", FromContext(c)),
		}

		switch {
		case c.Writer.Status() >= 500:
			log.Error("request completed", fields...)
		case c.Writer.Status() >= 400:
			log.Warn("request completed", fields...)
		default:
			log.Info("request completed", fields...)
		}
	}
}
