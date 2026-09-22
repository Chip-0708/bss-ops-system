package api

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	infra_middleware "model_bss/internal/infra/middleware"
)

// TestRequestIDOf_ReturnsResponseRequestID 锁住 5d 热修 P1-4：
// audit_log / task_job / event_outbox 的 request_id 必须等于响应 requestId（X-Request-Id），
// 不再用 Idempotency-Key。
func TestRequestIDOf_ReturnsResponseRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(infra_middleware.RequestID())
	r.POST("/x", func(c *gin.Context) {
		// handler 内取到的 request_id 必须与响应头 X-Request-Id 一致
		rid := requestIDOf(c)
		require.NotEmpty(t, rid)
		require.Equal(t, rid, c.Writer.Header().Get("X-Request-Id"))
		c.JSON(200, gin.H{"rid": rid})
	})

	req := httptest.NewRequest("POST", "/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, 200, rec.Code)
	require.NotEmpty(t, rec.Header().Get("X-Request-Id"))
	require.Contains(t, rec.Body.String(), rec.Header().Get("X-Request-Id"))
}
