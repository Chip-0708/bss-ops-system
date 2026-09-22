package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"model_bss/internal/api"
	"model_bss/internal/infra/middleware"
)

// stubPinger 仅实现 api.Pinger 接口，用于在不真实连接数据库的情况下验证路由逻辑。
type stubPinger func(ctx context.Context) error

func (p stubPinger) Ping(ctx context.Context) error { return p(ctx) }

// TestHealthz_OK 验证数据库 Ping 成功时返回 200 与正确 payload，且响应头携带 X-Request-Id。
func TestHealthz_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	r.GET("/healthz", api.HealthzHandler(stubPinger(func(context.Context) error { return nil })))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, rec.Header().Get("X-Request-Id"))
	require.JSONEq(t, `{"status":"ok","db":"up"}`, rec.Body.String())
}

// TestHealthz_DBDown 验证数据库 Ping 失败时返回 503，并显式标记 db 为 down。
func TestHealthz_DBDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	r.GET("/healthz", api.HealthzHandler(stubPinger(func(context.Context) error {
		return errors.New("connection refused")
	})))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"status":"degraded","db":"down"}`, rec.Body.String())
}
