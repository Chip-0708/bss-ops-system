// Package worker 的 delivery_client_test.go：11b 推送 HTTP 客户端的单测。
package worker

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDeliver_Success 推送成功：HTTP 200 → 无 error。
func TestDeliver_Success(t *testing.T) {
	var gotBody []byte
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Event-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewDeliverClient(srv.URL, "test-secret")
	payload := json.RawMessage(`{"sku_id":40}`)
	err := c.Deliver(context.Background(), 123, "price.effective", payload, time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	// 校验 body 结构（裁决 2）
	var body DeliverBody
	require.NoError(t, json.Unmarshal(gotBody, &body))
	require.Equal(t, int64(123), body.ID)
	require.Equal(t, "price.effective", body.EventType)
	require.JSONEq(t, `{"sku_id":40}`, string(body.Payload))
	require.Equal(t, "2026-09-17T12:00:00Z", body.CreatedAt)

	// 校验签名头（裁决 6：secret 非空必须带）
	expectedSig := hmac.New(sha256.New, []byte("test-secret"))
	expectedSig.Write(gotBody)
	require.Equal(t, hex.EncodeToString(expectedSig.Sum(nil)), gotSig)
}

// TestDeliver_NoSecret 无 secret：不带签名头。
func TestDeliver_NoSecret(t *testing.T) {
	var gotSig string
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Event-Signature")
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewDeliverClient(srv.URL, "")
	err := c.Deliver(context.Background(), 1, "test", json.RawMessage(`{}`), time.Now())
	require.NoError(t, err)
	require.Empty(t, gotSig) // 变异 #3 锚点：secret 为空不应带签名头
	require.Equal(t, "application/json", gotContentType)
}

// TestDeliver_HTTPError 非 2xx → error。
func TestDeliver_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewDeliverClient(srv.URL, "")
	err := c.Deliver(context.Background(), 1, "test", json.RawMessage(`{}`), time.Now())
	require.Error(t, err)
	require.Contains(t, err.Error(), "HTTP 500")
}

// TestDeliver_EmptyURL webhook_url 为空 → error（裁决 8：NOOP 由 EventDeliverJob 判定，client 仍报错）。
func TestDeliver_EmptyURL(t *testing.T) {
	c := NewDeliverClient("", "")
	err := c.Deliver(context.Background(), 1, "test", json.RawMessage(`{}`), time.Now())
	require.Error(t, err)
	require.Contains(t, err.Error(), "webhook_url 未配置")
}

// TestDeliver_ConnectionRefused 连接错误 → error（判定失败，走退避）。
func TestDeliver_ConnectionRefused(t *testing.T) {
	c := NewDeliverClient("http://127.0.0.1:1", "") // 端口 1 必失败
	err := c.Deliver(context.Background(), 1, "test", json.RawMessage(`{}`), time.Now())
	require.Error(t, err)
}
