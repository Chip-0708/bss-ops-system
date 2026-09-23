package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"model_bss/pkg/response"
)

// fakeIdemStore 为单测的内存幂等存储。
type fakeIdemStore struct {
	byRequest map[string]*IdempotencyRow
	byBizKey  map[string]*IdempotencyRow
	nextID    int64
}

func newFakeIdemStore() *fakeIdemStore {
	return &fakeIdemStore{
		byRequest: map[string]*IdempotencyRow{},
		byBizKey:  map[string]*IdempotencyRow{},
		nextID:    1,
	}
}

func (f *fakeIdemStore) DB() *gorm.DB { return nil }

func (f *fakeIdemStore) GetByRequestID(ctx context.Context, requestID string) (*IdempotencyRow, error) {
	return f.byRequest[requestID], nil
}

func (f *fakeIdemStore) FindByBizKey(ctx context.Context, bizKey string) (*IdempotencyRow, error) {
	return f.byBizKey[bizKey], nil
}

func (f *fakeIdemStore) CreateProcessing(ctx context.Context, requestID, requestHash, bizKey string, deadline time.Time) error {
	row := &IdempotencyRow{
		ID:          f.nextID,
		RequestID:   requestID,
		RequestHash: requestHash,
		BizKey:      bizKey,
		Status:      "PROCESSING",
		Deadline:    &deadline,
	}
	f.nextID++
	f.byRequest[requestID] = row
	if bizKey != "" {
		f.byBizKey[bizKey] = row
	}
	return nil
}

func (f *fakeIdemStore) ResetProcessing(ctx context.Context, id int64, requestHash string, deadline time.Time) error {
	for _, row := range f.byRequest {
		if row.ID == id {
			row.RequestHash = requestHash
			row.Deadline = &deadline
			return nil
		}
	}
	return errors.New("row not found")
}

func (f *fakeIdemStore) MarkResult(c *gin.Context, requestID string, httpStatus int) error {
	row := f.byRequest[requestID]
	if row == nil {
		return errors.New("row not found")
	}
	row.HTTPStatus = httpStatus
	if httpStatus >= 200 && httpStatus < 300 {
		row.Status = "DONE"
		// 与真实实现（repo.IdempotencyRepo.MarkResult）一致：保存可重放的响应快照。
		// 中间件的 bodyCapture 兜底会把未显式挂的结果也填进来，故此处与生产行为同构。
		if v := GetIdempotencyResult(c); v != nil {
			if b, err := json.Marshal(v); err == nil {
				row.ResultJSON = b
			}
		}
	} else {
		row.Status = "FAILED"
	}
	return nil
}

func (f *fakeIdemStore) CleanExpired(now time.Time) (int64, error) {
	var n int64
	for k, row := range f.byRequest {
		if row.Deadline != nil && row.Deadline.Before(now) {
			delete(f.byRequest, k)
			n++
		}
	}
	return n, nil
}

// stubOperator 简单满足 OperatorFrom。
func stubOperator(opID int64) *Operator {
	return &Operator{PortalType: "INTERNAL", OperatorType: "STAFF", OperatorID: opID}
}

func performIdemRequest(t *testing.T, store IdempotencyStore, biz BizKeyFunc, handler gin.HandlerFunc, idemKey, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("raw_body", body)
		c.Next()
	})
	r.Use(func(c *gin.Context) {
		c.Set(ctxKeyOperator, stubOperator(42))
		c.Next()
	})
	r.Use(Idempotency(store, biz))
	r.POST("/x", handler)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("Idempotency-Key", idemKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestIdempotency_FirstRequestExecutesAndMarksDone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeIdemStore()

	calls := 0
	rec := performIdemRequest(t, store, DefaultBizKey, func(c *gin.Context) {
		calls++
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}, "idem-001", `{"a":1}`)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, calls)
	row := store.byRequest["idem-001"]
	require.NotNil(t, row)
	require.Equal(t, "DONE", row.Status)
	require.NotNil(t, row.Deadline)
}

func TestIdempotency_ReplaySameKeyReturnsCachedResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeIdemStore()
	// result_json 存的是「客户端视角的 data」，响应外壳由 replayResult 统一套上
	payload := []byte(`{"r":1}`)
	// 先插入 DONE 记录，模拟上次已完成
	store.byRequest["idem-002"] = &IdempotencyRow{
		RequestID:   "idem-002",
		RequestHash: requestHashOfRaw("idem-002", 42),
		Status:      "DONE",
		ResultJSON:  payload,
		Deadline:    nil,
	}

	calls := 0
	rec := performIdemRequest(t, store, DefaultBizKey, func(c *gin.Context) {
		calls++
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}, "idem-002", `{"a":1}`)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 0, calls, "replay must not execute business")

	// 重放响应必须是统一响应包（§8.0），data 为缓存值
	var body struct {
		Code      int             `json:"code"`
		Message   string          `json:"message"`
		Data      json.RawMessage `json:"data"`
		RequestID string          `json:"requestId"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.Equal(t, "ok", body.Message)
	require.JSONEq(t, string(payload), string(body.Data))
	require.NotEmpty(t, rec.Header().Get("X-Request-Id"), "requestId must be fresh")
	require.NotEqual(t, "old", rec.Header().Get("X-Request-Id"), "requestId must not reuse cached value")
}

func TestIdempotency_SameKeyDifferentBodyReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeIdemStore()
	store.byRequest["idem-003"] = &IdempotencyRow{
		RequestID:   "idem-003",
		RequestHash: "another-hash",
		Status:      "DONE",
	}

	rec := performIdemRequest(t, store, DefaultBizKey, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}, "idem-003", `{"a":2}`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, float64(10001), bodyCodeOf(t, rec.Body.String()), "参数校验失败码 10001")
}

func TestIdempotency_ProcessingNotExpiredReturns409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeIdemStore()
	future := time.Now().Add(3 * time.Minute)
	store.byRequest["idem-004"] = &IdempotencyRow{
		RequestID:   "idem-004",
		RequestHash: requestHashOfRaw("idem-004", 42),
		Status:      "PROCESSING",
		Deadline:    &future,
	}

	rec := performIdemRequest(t, store, DefaultBizKey, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}, "idem-004", `{"a":1}`)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, float64(10005), bodyCodeOf(t, rec.Body.String()), "幂等冲突码 10005")
}

func TestIdempotency_ProcessingExpiredAllowsReentry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeIdemStore()
	past := time.Now().Add(-time.Minute)
	store.byRequest["idem-005"] = &IdempotencyRow{
		ID:          7,
		RequestID:   "idem-005",
		RequestHash: requestHashOfRaw("idem-005", 42),
		Status:      "PROCESSING",
		Deadline:    &past,
	}

	calls := 0
	rec := performIdemRequest(t, store, DefaultBizKey, func(c *gin.Context) {
		calls++
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}, "idem-005", `{"a":1}`)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, calls)
	row := store.byRequest["idem-005"]
	require.Equal(t, "DONE", row.Status)
	require.NotNil(t, row.Deadline)
	require.True(t, row.Deadline.After(time.Now()), "deadline must be reset forward")
}

// 联调 P1-3：语义重复（同 biz_key、不同 request_id）在 DONE 且**有快照**时，
// 必须返回首次的原结果，而不是 409——客户端换幂等键重试不应被挡。
func TestIdempotency_SameBizKeyDoneWithSnapshotReplays(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeIdemStore()
	store.byBizKey["bk-1"] = &IdempotencyRow{
		RequestID: "old-req", Status: "DONE", BizKey: "bk-1",
		ResultJSON: []byte(`{"quote_id":7,"new_status":"EFFECTIVE"}`),
	}

	var handlerRan bool
	rec := performIdemRequest(t, store, func(c *gin.Context) string {
		return "bk-1"
	}, func(c *gin.Context) {
		handlerRan = true
		c.JSON(http.StatusOK, gin.H{"should": "not run"})
	}, "new-req", `{"a":1}`)

	require.Equal(t, http.StatusOK, rec.Code, "DONE+biz_key 命中应重放原结果，不是 409")
	require.False(t, handlerRan, "重放不应再次执行业务")
	require.Contains(t, rec.Body.String(), "quote_id", "应返回首次结果的快照")
	require.Contains(t, rec.Body.String(), "EFFECTIVE")
}

// 联调 P1-3：DONE 但无快照（历史数据）→ 明确 409，不静默返回 data:null。
func TestIdempotency_SameBizKeyDoneWithoutSnapshotReturns409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeIdemStore()
	store.byBizKey["bk-2"] = &IdempotencyRow{RequestID: "old-req", Status: "DONE", BizKey: "bk-2"}

	rec := performIdemRequest(t, store, func(c *gin.Context) string {
		return "bk-2"
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}, "new-req-2", `{"a":1}`)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, float64(10005), bodyCodeOf(t, rec.Body.String()),
		"无快照不可重放，须明确报错而不是返回 data:null")
}

// 联调 P1-3：biz_key 命中 PROCESSING（未超时）→ 409，避免并发双写。
func TestIdempotency_SameBizKeyProcessingReturns409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeIdemStore()
	dl := time.Now().Add(5 * time.Minute)
	store.byBizKey["bk-3"] = &IdempotencyRow{
		RequestID: "old-req", Status: "PROCESSING", BizKey: "bk-3", Deadline: &dl,
	}

	rec := performIdemRequest(t, store, func(c *gin.Context) string {
		return "bk-3"
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}, "new-req-3", `{"a":1}`)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, float64(10005), bodyCodeOf(t, rec.Body.String()))
}

// 联调 P1-3：handler 未显式调用 SetIdempotencyResult 时，中间件应从响应体兜底提取快照，
// 保证 DONE 记录可重放（此前 result_json=NULL → 重放 data:null）。
func TestIdempotency_ResultSnapshotFallbackFromResponseBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeIdemStore()
	rec := performIdemRequest(t, store, DefaultBizKey, func(c *gin.Context) {
		// 故意不调 SetIdempotencyResult，直接写统一信封。
		response.Success(c, gin.H{"id": 99})
	}, "idem-fallback", `{"a":1}`)

	require.Equal(t, http.StatusOK, rec.Code)
	row := store.byRequest["idem-fallback"]
	require.NotNil(t, row)
	require.Equal(t, "DONE", row.Status)
	require.NotEmpty(t, row.ResultJSON, "兜底应写入响应快照")
	require.Contains(t, string(row.ResultJSON), "99")
}

func TestIdempotency_BusinessFailureMarksFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeIdemStore()
	rec := performIdemRequest(t, store, DefaultBizKey, func(c *gin.Context) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "boom"})
	}, "idem-006", `{"a":1}`)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	row := store.byRequest["idem-006"]
	require.NotNil(t, row)
	require.Equal(t, "FAILED", row.Status)
}

func TestIdempotency_ReplayUsesFreshRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeIdemStore()
	payload := []byte(`{"x":1}`)
	store.byRequest["idem-007"] = &IdempotencyRow{
		RequestID:   "idem-007",
		RequestHash: requestHashOfRaw("idem-007", 42),
		Status:      "DONE",
		ResultJSON:  payload,
	}

	rec := performIdemRequest(t, store, DefaultBizKey, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}, "idem-007", `{"a":1}`)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, rec.Header().Get("X-Request-Id"))
	require.NotEqual(t, "cached", rec.Header().Get("X-Request-Id"), "replay must not reuse cached requestId")
	require.NotContains(t, rec.Body.String(), "cached", "响应体不得出现缓存里的旧 requestId")
	// 统一响应包：data 为缓存的 data
	require.Contains(t, rec.Body.String(), `"data":{"x":1}`)
}

func TestIdempotency_CleanExpiredRemovesStaleRows(t *testing.T) {
	store := newFakeIdemStore()
	now := time.Now()
	past := now.Add(-time.Hour)
	store.byRequest["old"] = &IdempotencyRow{RequestID: "old", Status: "DONE", Deadline: &past}
	store.byRequest["new"] = &IdempotencyRow{RequestID: "new", Status: "DONE", Deadline: &now}

	n, err := CleanExpired(store, now)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	require.Nil(t, store.byRequest["old"])
	require.NotNil(t, store.byRequest["new"])
}

// requestHashOfRaw 与中间件内部 requestHashOf 一致的测试辅助。
// 参数与中间件内部 requestHashOf 的计算源保持一致：body 默认按 POST /x + 对应 operator。
func requestHashOfRaw(requestID string, operatorID int64) string {
	return requestHashOfWithBody(operatorID, `{"a":1}`)
}

func requestHashOfWithBody(operatorID int64, body string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("POST|/x|%s|%d", body, operatorID))))
}

// TestIdempotency_RealBodyDifferentPayloadReturns400 是真链路回归用例。
//
// 背景：早期实现用 c.GetString("raw_body") 取请求体，而生产环境没有任何中间件设置该键，
// 导致 requestHash 实际未包含 body，单测却因为手动 c.Set("raw_body", ...) 而全部通过。
// 本用例**不伪造** raw_body，直接走真实请求体，确保 GetRawData 路径生效。
func TestIdempotency_RealBodyDifferentPayloadReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeIdemStore()

	// 不注入 raw_body，请求体由 httptest 真实提供
	do := func(body string) *httptest.ResponseRecorder {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set(ctxKeyOperator, stubOperator(42))
			c.Next()
		})
		r.Use(Idempotency(store, DefaultBizKey))
		// 注意：handler 必须用统一响应信封（response.Success）——
		// 幂等中间件的快照兜底依赖信封的 data 字段提取；裸 c.JSON 无法提取，
		// 会导致 DONE 记录无快照、重放退化为 409。生产 handler 统一走 response.Success。
		r.POST("/x", func(c *gin.Context) { response.Success(c, gin.H{"ok": true}) })

		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
		req.Header.Set("Idempotency-Key", "idem-real")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	first := do(`{"sku":"A-1","qty":1}`)
	require.Equal(t, http.StatusOK, first.Code)

	// 同 key 相同 body → 重放，业务不重跑
	same := do(`{"sku":"A-1","qty":1}`)
	require.Equal(t, http.StatusOK, same.Code)

	// 同 key 不同 body → 必须 400，绝不能返回上一次的缓存结果
	diff := do(`{"sku":"B-9","qty":9}`)
	require.Equal(t, http.StatusBadRequest, diff.Code, "同 key 不同 body 必须拒绝")
	require.Equal(t, float64(10001), bodyCodeOf(t, diff.Body.String()))
}

// bodyCodeOf 从统一响应 JSON 提取 code 字段。
func bodyCodeOf(t *testing.T, body string) float64 {
	t.Helper()
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(body), &m))
	v, _ := m["code"].(float64)
	return v
}

// TestDefaultBizKey_PathDistinguishesSameEmptyBody 是 5b 阻塞 bug 的回归用例。
//
// 背景：DefaultBizKey 早期只含 sha256(body)+operator，空 body POST 的 biz_key 跨路径
// 完全相同——采购批 /quotes/9/approve 后批 /quotes/10/approve 被误判幂等冲突（409），
// 且该单永远批不了。修复：biz_key 纳入 c.Request.URL.Path（实际路径含 :id 真值，
// **不能用 c.FullPath()**——路由模板 /quotes/:id/approve 对 id=9/10 完全相同）。
func TestDefaultBizKey_PathDistinguishesSameEmptyBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeIdemStore()

	do := func(path, key string) *httptest.ResponseRecorder {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set(ctxKeyOperator, stubOperator(42))
			c.Next()
		})
		r.Use(Idempotency(store, DefaultBizKey))
		r.POST("/quotes/:id/approve", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

		req := httptest.NewRequest(http.MethodPost, path, nil) // 空 body
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// 同一操作员、相同空 body、不同路径参数：两次都必须成功，biz_key 必须不同
	rec1 := do("/quotes/1/approve", "idem-path-1")
	require.Equal(t, http.StatusOK, rec1.Code, "第一次 approve 必须成功")
	rec2 := do("/quotes/2/approve", "idem-path-2")
	require.Equal(t, http.StatusOK, rec2.Code, "不同 id 的 approve 不得被误判幂等冲突")

	row1 := store.byRequest["idem-path-1"]
	row2 := store.byRequest["idem-path-2"]
	require.NotNil(t, row1)
	require.NotNil(t, row2)
	require.NotEqual(t, row1.BizKey, row2.BizKey, "不同路径的 biz_key 必须不同")
	require.Contains(t, row1.BizKey, "path:/quotes/1/approve")
	require.Contains(t, row2.BizKey, "path:/quotes/2/approve")
}
