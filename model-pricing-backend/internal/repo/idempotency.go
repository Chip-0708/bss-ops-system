// Package repo 中本文件提供幂等键的 GORM 实现（middleware.IdempotencyStore）。
// 同事务约定：所有读/写方法内部先 db.FromContext(ctx) 取事务，取不到回退基础连接；
// 幂等记录与业务变更必须同一事务，进程 crash 时才不会重复产生不可变版本。
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"model_bss/internal/api/middleware"
	"model_bss/internal/infra/db"
)

// IdempotencyRepo 是 GORM 版本的幂等键仓储。
type IdempotencyRepo struct {
	base *gorm.DB
}

// NewIdempotencyRepo 构造幂等仓储。
func NewIdempotencyRepo(base *gorm.DB) *IdempotencyRepo { return &IdempotencyRepo{base: base} }

var _ middleware.IdempotencyStore = (*IdempotencyRepo)(nil)

// DB 返回基础连接（中间件会据此开启事务）。
func (r *IdempotencyRepo) DB() *gorm.DB { return r.base }

// txOf 优先使用 context 中的事务；取不到时使用基础 DB（只读场景安全）。
func (r *IdempotencyRepo) txOf(ctx context.Context) *gorm.DB {
	if tx := db.FromContext(ctx); tx != nil {
		return tx
	}
	return r.base
}

// row 是 idempotency_key 表的 GORM 行模型。
type row struct {
	ID                 int64      `gorm:"column:id;primaryKey"`
	BizType            string     `gorm:"column:biz_type"`
	BizKey             string     `gorm:"column:biz_key"`
	RequestID          string     `gorm:"column:request_id"`
	RequestHash        string     `gorm:"column:request_hash"`
	Status             string     `gorm:"column:status"`
	ResultJSON         []byte     `gorm:"column:result_json"`
	CreatedAt          time.Time  `gorm:"column:created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at"`
	ExpireAt           time.Time  `gorm:"column:expire_at"`
	ProcessingDeadline *time.Time `gorm:"column:processing_deadline"`
}

func (row) TableName() string { return "idempotency_key" }

// idempotencyKeyDTO 是写入侧的别名（简化 CreateProcessing/Updates 的字段清单）。
type idempotencyKeyDTO struct {
	BizType            string
	BizKey             string
	RequestID          string
	RequestHash        string
	Status             string
	ResultJSON         []byte
	ProcessingDeadline time.Time
	ExpireAt           time.Time
}

// bizTypeOf 本实现的 biz_type：路由前缀推导，可配置。
// 本阶段以固定值 "API" 与统一常量 OVERWRITE 区分（设计文档 §8.0.1 未绑定路径）。
func bizTypeOf(c *gin.Context) string {
	return "API"
}

// toMiddleware 把 DB 行转换成中间件视图模型。
func toMiddleware(r *row) *middleware.IdempotencyRow {
	if r == nil {
		return nil
	}
	return &middleware.IdempotencyRow{
		ID:          r.ID,
		RequestID:   r.RequestID,
		RequestHash: r.RequestHash,
		BizKey:      r.BizKey,
		Status:      r.Status,
		ResultJSON:  r.ResultJSON,
		Deadline:    r.ProcessingDeadline,
		HTTPStatus:  0,
	}
}

// GetByRequestID 查幂等记录；不存在返回 (nil, nil)。
func (r *IdempotencyRepo) GetByRequestID(ctx context.Context, requestID string) (*middleware.IdempotencyRow, error) {
	c, _ := ctx.Value("GIN_CONTEXT").(*gin.Context)
	bizType := bizTypeOf(c)
	var row row
	err := r.txOf(ctx).
		Table("idempotency_key").
		Where("biz_type = ? AND request_id = ?", bizType, requestID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toMiddleware(&row), nil
}

// FindByBizKey 按 biz_key 探查语义重复记录（Agent 重试防线）。
func (r *IdempotencyRepo) FindByBizKey(ctx context.Context, bizKey string) (*middleware.IdempotencyRow, error) {
	var row row
	err := r.txOf(ctx).
		Table("idempotency_key").
		Where("biz_key = ?", bizKey).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toMiddleware(&row), nil
}

// CreateProcessing 插入 PROCESSING 记录，唯一键冲突时返回冲突字节 io（调用方重新加载见 gorm.ErrDuplicatedKey）。
func (r *IdempotencyRepo) CreateProcessing(ctx context.Context, requestID, requestHash, bizKey string, deadline time.Time) error {
	now := time.Now()
	ins := idempotencyKeyDTO{
		BizType:            bizTypeOf(nil),
		BizKey:             bizKey,
		RequestID:          requestID,
		RequestHash:        requestHash,
		Status:             "PROCESSING",
		ResultJSON:         nil,
		ProcessingDeadline: deadline,
		ExpireAt:           now.Add(24 * time.Hour),
	}
	err := r.txOf(ctx).Table("idempotency_key").Create(&ins).Error
	// 唯一键冲突（biz_type + request_id 已存在）：返回冲突留中间件判定
	if err != nil && errors.Is(err, gorm.ErrDuplicatedKey) {
		return gorm.ErrDuplicatedKey
	}
	return err
}

// ResetProcessing 重置超时 PROCESSING 记录为可重入：仅供"已超时"的记录调用
// （中间件分支已时序判断，这里做防御式更新：只在该 ID 仍 PROCESSING 且 deadline 已过时生效）。
func (r *IdempotencyRepo) ResetProcessing(ctx context.Context, id int64, requestHash string, deadline time.Time) error {
	err := r.txOf(ctx).
		Table("idempotency_key").
		Where("id = ? AND status = 'PROCESSING' AND processing_deadline IS NOT NULL AND processing_deadline < now()", id).
		Updates(map[string]interface{}{
			"request_hash":        requestHash,
			"processing_deadline": deadline,
			"result_json":         nil,
			"updated_at":          time.Now(),
		}).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return nil
}

// MarkResult 在业务完成后把结果回填为 DONE / FAILED。
// httpStatus >= 200 && < 300 → DONE，同时保存可选的响应快照。
func (r *IdempotencyRepo) MarkResult(c *gin.Context, requestID string, httpStatus int) error {
	status := "FAILED"
	var result interface{} = nil
	if httpStatus >= 200 && httpStatus < 300 {
		status = "DONE"
		if v := middleware.GetIdempotencyResult(c); v != nil {
			if b, err := json.Marshal(v); err == nil {
				result = string(b)
			}
		}
	}
	err := r.txOf(c.Request.Context()).
		Table("idempotency_key").
		Where("biz_type = ? AND request_id = ?", bizTypeOf(c), requestID).
		Updates(map[string]interface{}{
			"status":              status,
			"result_json":         result,
			"processing_deadline": nil,
			"updated_at":          time.Now(),
		}).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return nil
}

// CleanExpired 删除过期记录（cron 清理用，阶段 10 挂载）。
func (r *IdempotencyRepo) CleanExpired(now time.Time) (int64, error) {
	res := r.base.Table("idempotency_key").
		Where("expire_at < ?", now).
		Delete(&row{})
	return res.RowsAffected, res.Error
}
