package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CORS 返回跨域资源共享中间件。
// 前端独立域名/端口开发（如 Vite dev server http://localhost:5173），
// 必须允许凭据（Bearer token）跨域携带，并暴露 X-Request-Id 给前端读取。
func CORS(allowOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowOrigins))
	for _, o := range allowOrigins {
		allowed[o] = struct{}{}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if _, ok := allowed[origin]; ok && origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Authorization, Idempotency-Key, X-Request-Id, Content-Type")
			c.Header("Access-Control-Expose-Headers", "X-Request-Id")
			c.Header("Access-Control-Max-Age", "600")
		}
		if c.Request.Method == http.MethodOptions {
			if _, ok := allowed[origin]; ok && origin != "" {
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
		}
		c.Next()
	}
}
