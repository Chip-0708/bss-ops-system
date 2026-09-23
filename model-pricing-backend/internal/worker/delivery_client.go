// Package worker 的 delivery_client.go：11b event_outbox 推送的 HTTP 客户端。
//
// 裁决 6：支持签名头（可选）——X-Event-Signature: HMAC-SHA256(body, webhook_secret)。
// webhook_secret 为空就不带（兼容本地测试）。
package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DeliverClient 推送事件的 HTTP 客户端。
type DeliverClient struct {
	webhookURL    string
	webhookSecret string
	client        *http.Client
}

// NewDeliverClient 构造。
// webhookURL 为空时 client 仍构造（NOOP 由 EventDeliverJob 判定）。
func NewDeliverClient(webhookURL, webhookSecret string) *DeliverClient {
	return &DeliverClient{
		webhookURL:    webhookURL,
		webhookSecret: webhookSecret,
		client: &http.Client{
			Timeout: 10 * time.Second, // 裁决 3：超时 10s 判定失败
		},
	}
}

// DeliverBody 是 POST body 的 JSON 结构（裁决 2）。
type DeliverBody struct {
	ID        int64           `json:"id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"` // RFC3339
}

// Deliver 推送一条事件：
//   - POST webhookURL
//   - body = {id, event_type, payload, created_at}
//   - 签名头 X-Event-Signature（HMAC-SHA256(body, webhook_secret)），secret 为空不带
//   - 返回 error：非 2xx / 超时 / 连接错误
func (c *DeliverClient) Deliver(ctx context.Context, id int64, eventType string, payload json.RawMessage, createdAt time.Time) error {
	if c.webhookURL == "" {
		return fmt.Errorf("webhook_url 未配置")
	}
	body := DeliverBody{
		ID:        id,
		EventType: eventType,
		Payload:   payload,
		CreatedAt: createdAt.UTC().Format(time.RFC3339),
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal deliver body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.webhookURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.webhookSecret != "" {
		sig := hmac.New(sha256.New, []byte(c.webhookSecret))
		_, _ = sig.Write(bodyBytes)
		req.Header.Set("X-Event-Signature", hex.EncodeToString(sig.Sum(nil)))
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("deliver event=%d: %w", id, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("deliver event=%d: HTTP %d", id, resp.StatusCode)
	}
	return nil
}
