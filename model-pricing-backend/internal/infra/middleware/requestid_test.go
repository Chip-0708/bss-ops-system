package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestRequestID_GeneratesIfMissing 验证请求未携带 X-Request-Id 时，中间件自动生成 UUID 并回写。
func TestRequestID_GeneratesIfMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID())

	r.GET("/ping", func(c *gin.Context) {
		id := FromContext(c)
		require.NotEmpty(t, id, "request id should be present in context")
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	headerID := rec.Header().Get("X-Request-Id")
	require.NotEmpty(t, headerID, "response header should carry generated request id")
}

// TestRequestID_RespectsInboundHeader 验证请求头中存在 X-Request-Id 时，直接透传而非覆盖。
func TestRequestID_RespectsInboundHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID())

	r.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusOK, FromContext(c))
	})

	expected := "req-12345"
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("X-Request-Id", expected)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, expected, rec.Header().Get("X-Request-Id"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, expected, rec.Body.String())
}
