package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"model_bss/internal/infra/db"
	infra_middleware "model_bss/internal/infra/middleware"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// BizKeyFunc 根据请求上下文生成业务语义键，用于幂等命中判定。
// 约定：返回形如 "supplier:{id}|date:{yyyy-MM-dd}|hash:{skus}" 的稳定串，
// Agent/工作流调用不会稳定复用 request_id，biz_key 是唯一可靠防线。
type BizKeyFunc func(*gin.Context) string

// DefaultBizKey 为默认实现：sha256(规范化 body) + operator_id + 实际请求路径。
// 规范化 = 去除首尾空白，避免换行/空格差异导致幂等误判。
//
// 路径必须取 c.Request.URL.Path（含路径参数真值，如 /quotes/9/approve），
// **不能用 c.FullPath()**——FullPath 返回路由模板（/quotes/:id/approve），
// id=9 与 id=10 模板相同，空 body 时 biz_key 会跨 ID 误判（5b 实测阻塞：
// 采购批 id=9 后批 id=10 被误判幂等冲突，且该单永远批不了）。
//
// 注意：默认实现**不**编码 login_id（如已入 operator_id），也不解析 JSON 字段顺序。
// 若业务需按语义幂等（如供应商 + 日期 + SKU 列表），应注册时替换为定制实现。
func DefaultBizKey(c *gin.Context) string {
	op := OperatorFrom(c)
	operator := "0"
	if op != nil {
		operator = fmt.Sprintf("%d", op.OperatorID)
	}
	body := rawBodyOf(c)
	return fmt.Sprintf("sha256:%x|op:%s|path:%s",
		sha256.Sum256([]byte(strings.TrimSpace(body))), operator, c.Request.URL.Path)
}

// rawBodyOf 取得请求体明文。
//
// 背景（重要）：此前实现直接读 gin 上下文键 raw_body，但生产链路里**没有任何中间件设置过它**，
// 导致 requestHash 实际完全没把请求体算进去——同一个 Idempotency-Key 发不同 body
// 也会被当成同一次请求而返回旧结果。故改为真实读取并在读取后还原 Body，
// 保证后续 handler（c.ShouldBindJSON 等）仍能读到。
func rawBodyOf(c *gin.Context) string {
	if v, ok := c.Get("raw_body"); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	b, err := c.GetRawData()
	if err != nil || len(b) == 0 {
		return ""
	}
	// 关键：把已读走的 body 重新塞回，否则下游 handler 读到空
	c.Request.Body = io.NopCloser(bytes.NewReader(b))
	c.Set("raw_body", string(b))
	return string(b)
}

// Idempotency 处理写接口的幂等保护。
//
// 事务约定（§8.0.1）：
//  1. 仅在显式注册幂等的路由上生效（见 RequireIdempotency 返回中间件）。
//  2. 插入 idempotency_key 记录与业务变更必须在**同一个事务**：
//     幂等中间件开启 tx，经 db.WithDB 注入 context，handler 必须用 db.FromContext(c) 取同一连接执行写；
//     handler 返回后，由中间件按结果 UPDATE status 并统一 commit / rollback。
//  3. 业务 handler 返回 apperr.Error / nil；中间件据此把 status 置为 FAILED / DONE。
//
// 约定：result_json 由 handler 通过 c.JSON 之外的写入通道（如 response.Success）写一次，
// 中间件在 POST 处理结果时读取 c.Writer 的最终输出，并从 ctxKeyResult 中提取「客户端视角的 data」。
// 若需要自定义快照，请在 handler 里先调用 SetIdempotencyResult(c, data) 再响应。
func Idempotency(store IdempotencyStore, bizKey BizKeyFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawKey := c.GetHeader("Idempotency-Key")
		if rawKey == "" {
			response.Error(c, apperr.ErrInvalidParams)
			c.Abort()
			return
		}
		requestID := rawKey
		reqHash := requestHashOf(c, rawKey)

		now := time.Now()
		deadline := now.Add(5 * time.Minute)

		// 读取/开启事务：幂等记录与业务共用同一 *gorm.DB
		tx := db.FromContext(c.Request.Context())
		var ownTx *gorm.DB
		if tx == nil {
			if storeDB := store.DB(); storeDB != nil {
				ownTx = storeDB.Begin()
				tx = ownTx
				c.Request = c.Request.WithContext(db.WithDB(c.Request.Context(), tx))
			}
			// storeDB 为 nil 时 tx 保持 nil（单测 / 无库场景）：跳过 tx 管理，仅做状态机
		}
		if ownTx != nil {
			defer func() {
				if r := recover(); r != nil {
					ownTx.Rollback()
					panic(r)
				}
			}()
		}

		// 1) 同 request_id 命中（唯一键）→ 根据 hash/status/deadline 决策
		existing, err := store.GetByRequestID(c.Request.Context(), requestID)
		if err != nil {
			if tx != nil {
				tx.Rollback()
			}
			response.Error(c, apperr.ErrSystem)
			c.Abort()
			return
		}
		if existing != nil {
			if existing.RequestHash != reqHash {
				if tx != nil {
					tx.Rollback()
				}
				// §8.0.1：幂等键复用但参数不同 → 400
				response.Error(c, apperr.ErrInvalidParams)
				c.Abort()
				return
			}
			switch existing.Status {
			case "DONE":
				replayResult(c, existing)
				if tx != nil {
					tx.Commit()
				}
				c.Abort()
				return
			case "PROCESSING":
				// 卡死防护：已超时 → 视为上次执行已死，重置后重入
				if existing.Deadline != nil && now.After(*existing.Deadline) {
					if err := store.ResetProcessing(c.Request.Context(), existing.ID, reqHash, deadline); err != nil {
						if tx != nil {
							tx.Rollback()
						}
						response.Error(c, apperr.ErrSystem)
						c.Abort()
						return
					}
				} else {
					if tx != nil {
						tx.Rollback()
					}
					response.Error(c, apperr.ErrIdempotency)
					c.Abort()
					return
				}
			default: // FAILED
				if tx != nil {
					tx.Rollback()
				}
				response.Error(c, apperr.ErrIdempotency)
				c.Abort()
				return
			}
		} else {
			// 2) 按 (biz_type, biz_key) 查语义重复（Agent 重试）
			if bizKey != nil {
				bk := bizKey(c)
				if bk != "" {
					dup, err := store.FindByBizKey(c.Request.Context(), bk)
					if err != nil {
						if tx != nil {
							tx.Rollback()
						}
						response.Error(c, apperr.ErrSystem)
						c.Abort()
						return
					}
					if dup != nil && dup.Status != "FAILED" {
						if tx != nil {
							tx.Rollback()
						}
						response.Error(c, apperr.ErrIdempotency)
						c.Abort()
						return
					}
				}
			}

			// 首次注册幂等键
			if err := store.CreateProcessing(c.Request.Context(), requestID, reqHash, bizKeyOf(c, bizKey), deadline); err != nil {
				if tx != nil {
					tx.Rollback()
				}
				response.Error(c, apperr.ErrSystem)
				c.Abort()
				return
			}
		}

		// 执行业务
		c.Next()

		// 写回结果：由 handler 侧通过 c.JSON 写出的响应体回读。
		if err := store.MarkResult(c, requestID, c.Writer.Status()); err != nil {
			if tx != nil {
				tx.Rollback()
			}
			response.Error(c, apperr.ErrSystem)
			c.Abort()
			return
		}
		if tx != nil {
			tx.Commit()
		}
	}
}

// SetIdempotencyResult 由 handler 在返回前调用，把本次响应的 data 挂到幂等记录上。
func SetIdempotencyResult(c *gin.Context, result any) {
	c.Set(ctxKeyIdemResult, result)
}

// GetIdempotencyResult 读取 handler 写入的结果快照（中间件 / 测试使用）。
func GetIdempotencyResult(c *gin.Context) any {
	if v, ok := c.Get(ctxKeyIdemResult); ok {
		return v
	}
	return nil
}

// IdempotencyStore 抽象幂等键的读写。
type IdempotencyStore interface {
	DB() *gorm.DB
	GetByRequestID(ctx context.Context, requestID string) (*IdempotencyRow, error)
	FindByBizKey(ctx context.Context, bizKey string) (*IdempotencyRow, error)
	CreateProcessing(ctx context.Context, requestID, requestHash, bizKey string, deadline time.Time) error
	ResetProcessing(ctx context.Context, id int64, requestHash string, deadline time.Time) error
	MarkResult(c *gin.Context, requestID string, httpStatus int) error
	// CleanExpired 供 cron 调用（阶段 10 挂载），删除 expire_at < now 的记录。
	CleanExpired(now time.Time) (int64, error)
}

// IdempotencyRow 是 idempotency_key 表的最小查询模型。
type IdempotencyRow struct {
	ID          int64
	RequestID   string
	RequestHash string
	BizKey      string
	Status      string
	ResultJSON  []byte
	Deadline    *time.Time
	HTTPStatus  int
}

// requestHashOf 由请求构造规范化哈希（body + path + method + operator）。
// request_id（Idempotency-Key）由客户端提供，按 §8.0.1 仅用于唯一定位，不参与 hash 计算。
func requestHashOf(c *gin.Context, requestID string) string {
	op := OperatorFrom(c)
	operator := "0"
	if op != nil {
		operator = fmt.Sprintf("%d", op.OperatorID)
	}
	body := rawBodyOf(c)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s", c.Request.Method, c.Request.URL.Path, strings.TrimSpace(body), operator)))
	return hex.EncodeToString(sum[:])
}

// bizKeyOf 兜底：无自定义时退化为 requestHash（保证仍有唯一约束依赖）。
func bizKeyOf(c *gin.Context, fn BizKeyFunc) string {
	if fn == nil {
		return requestHashOf(c, c.GetHeader("Idempotency-Key"))
	}
	if v := fn(c); v != "" {
		return v
	}
	return requestHashOf(c, c.GetHeader("Idempotency-Key"))
}

// replayResult 重放已有 DONE 记录。
//
// result_json 存的是「客户端视角的 data」，由中间件统一套上响应外壳（§8.0：
// code / message / data / requestId），保证重放与首次响应结构完全一致。
// requestId 一律取**本次请求**的新值，不复用缓存里的旧值。
func replayResult(c *gin.Context, row *IdempotencyRow) {
	// 若上游挂了 RequestID 中间件则沿用其生成的本次请求 ID；否则兜底生成。
	rid := infra_middleware.FromContext(c)
	if rid == "" {
		rid = newRequestID()
	}
	c.Header("X-Request-Id", rid)

	var data interface{}
	if len(row.ResultJSON) > 0 {
		if err := json.Unmarshal(row.ResultJSON, &data); err != nil {
			// 反序列化失败时原样透传，避免把坏数据包装成"成功"
			data = json.RawMessage(row.ResultJSON)
		}
	}
	response.Success(c, data)
}

// ctxKeyIdemResult 是 handler 写入、中间件读取的响应快照键。
const ctxKeyIdemResult = "idem.result"

// newRequestID 生成新的请求 ID（与 infra/middleware 的 UUID 策略一致）。
func newRequestID() string { return uuid.NewString() }

// CleanExpired 供 cron 调用（阶段 10 挂载），删除过期记录。
func CleanExpired(store IdempotencyStore, now time.Time) (int64, error) {
	return store.CleanExpired(now)
}

// ExpiredCleaner 由 store 实现：清理 expire_at < now 的记录。
type ExpiredCleaner interface {
	CleanExpired(now time.Time) (int64, error)
}
