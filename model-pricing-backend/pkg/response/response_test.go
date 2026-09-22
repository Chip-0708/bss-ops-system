package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"model_bss/pkg/apperr"
)

// performRequest 是一个极简的测试辅助函数，模拟一次 HTTP 调用。
func performRequest(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSuccessSerialization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	r.GET("/success", func(c *gin.Context) {
		Success(c, map[string]string{"foo": "bar"})
	})

	w := performRequest(r, http.MethodGet, "/success")
	require.Equal(t, http.StatusOK, w.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	require.Equal(t, float64(0), body["code"])
	require.Equal(t, "ok", body["message"])
	require.Equal(t, map[string]interface{}{"foo": "bar"}, body["data"])
	require.Contains(t, body, "requestId", "requestId field must be present for consistent API contract")
}

func TestErrorSerialization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	r.GET("/error", func(c *gin.Context) {
		Error(c, apperr.ErrInvalidParams)
	})

	w := performRequest(r, http.MethodGet, "/error")
	require.Equal(t, http.StatusBadRequest, w.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	require.Equal(t, float64(10001), body["code"])
	require.Equal(t, "参数校验失败", body["message"])
	require.Nil(t, body["data"])
	require.Contains(t, body, "requestId", "requestId field must be present even on error")
}

func TestErrorDataSerialization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/error-data", func(c *gin.Context) {
		ErrorData(c, apperr.ErrIdempotency, map[string]interface{}{
			"floor_violations": []map[string]string{{"sku_id": "40", "floor_price": "1.25"}},
		})
	})

	w := performRequest(r, http.MethodGet, "/error-data")
	require.Equal(t, http.StatusConflict, w.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, float64(10005), body["code"])
	data := body["data"].(map[string]interface{})
	violations := data["floor_violations"].([]interface{})
	require.Len(t, violations, 1)
}
