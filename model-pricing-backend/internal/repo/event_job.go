// Package repo 的 event_job.go：11b event_outbox 推送任务的 GORM 实现。
//
// 与 cost_task.go 同源纪律：
//   - 认领/推进只动 event_outbox 自己的行；推送（HTTP）不动库——混在一起会让
//     推送失败把已认领的 RUNNING 拖成孤儿（6b-3 复核注意点 1，本批复用）。
//   - 认领谓词：status IN ('PENDING','FAILED') AND COALESCE(next_run_at, created_at) <= now
//     （裁决 9：next_run_at NULL 当过去处理，不回填表）。
//   - 退避 1min × 5^(retry_count-1)，第 5 次失败置 DEAD + alert(EVENT_DELIVERY_FAILED, CRITICAL)。
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"model_bss/internal/infra/db"
)

// EventJobRepo 是 event_outbox 推送任务的仓储。
type EventJobRepo struct {
	base *gorm.DB
}

// NewEventJobRepo 构造。
func NewEventJobRepo(base *gorm.DB) *EventJobRepo {
	return &EventJobRepo{base: base}
}

// txOf 优先取 ctx 中的 tx（幂等中间件注入），否则用 base。
func (r *EventJobRepo) txOf(ctx context.Context) *gorm.DB {
	if tx := db.FromContext(ctx); tx != nil {
		return tx
	}
	return r.base
}

// eventJobRow 是 event_outbox 行模型（读专用）。
type eventJobRow struct {
	ID         int64     `gorm:"column:id"`
	EventType  string    `gorm:"column:event_type"`
	Payload    []byte    `gorm:"column:payload"`
	Status     string    `gorm:"column:status"`
	RetryCount int       `gorm:"column:retry_count"`
	NextRunAt  time.Time `gorm:"column:next_run_at"`
	CreatedAt  time.Time `gorm:"column:created_at"`
}

func (eventJobRow) TableName() string { return "event_outbox" }

// EventRow 是 event_outbox 的领域视图。
type EventRow struct {
	ID         int64
	EventType  string
	Payload    []byte
	Status     string
	RetryCount int
	NextRunAt  time.Time
	CreatedAt  time.Time
}

// PollDue 拉可认领事件：status IN ('PENDING','FAILED') AND COALESCE(next_run_at, created_at) <= now，
// 按 COALESCE(next_run_at, created_at) 升序（先到先服务）。
// 一次只拉一批（limit 50），防止积压把单轮拖垮——留下的下一轮再吃。
func (r *EventJobRepo) PollDue(ctx context.Context, now time.Time, limit int) ([]EventRow, error) {
	var rows []eventJobRow
	err := r.txOf(ctx).Table("event_outbox").
		Where("status IN ('PENDING','FAILED') AND COALESCE(next_run_at, created_at) <= ?", now).
		Order("COALESCE(next_run_at, created_at) ASC, id ASC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("poll due event_outbox: %w", err)
	}
	out := make([]EventRow, 0, len(rows))
	for _, rw := range rows {
		out = append(out, EventRow{
			ID: rw.ID, EventType: rw.EventType, Payload: rw.Payload,
			Status: rw.Status, RetryCount: rw.RetryCount,
			NextRunAt: rw.NextRunAt, CreatedAt: rw.CreatedAt,
		})
	}
	return out, nil
}

// Claim 条件更新认领（PENDING|FAILED → RUNNING）：
// RowsAffected=1 → 领到；=0 → 已被别的消费者拿走/退避未到点 → 跳过。
// 用 COALESCE(next_run_at, created_at)<=now 双重守卫（FAILED 必须在退避到点后才允许再抽调）。
func (r *EventJobRepo) Claim(ctx context.Context, id int64, now time.Time) (bool, error) {
	res := r.txOf(ctx).Exec(
		`UPDATE event_outbox SET status='RUNNING', updated_at=?
		 WHERE id=? AND status IN ('PENDING','FAILED')
		   AND COALESCE(next_run_at, created_at) <= ?`,
		now, id, now)
	if res.Error != nil {
		return false, fmt.Errorf("claim event=%d: %w", id, res.Error)
	}
	return res.RowsAffected > 0, nil
}

// LoadEvent 读事件行。不存在 → ErrEventNotFound。
func (r *EventJobRepo) LoadEvent(ctx context.Context, id int64) (*EventRow, error) {
	var row eventJobRow
	err := r.txOf(ctx).Where("id = ?", id).Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("event_outbox 不存在: id=%d", id)
		}
		return nil, fmt.Errorf("load event=%d: %w", id, err)
	}
	return &EventRow{
		ID: row.ID, EventType: row.EventType, Payload: row.Payload,
		Status: row.Status, RetryCount: row.RetryCount,
		NextRunAt: row.NextRunAt, CreatedAt: row.CreatedAt,
	}, nil
}

// MarkDone 标记推送成功（RUNNING → DONE）。
func (r *EventJobRepo) MarkDone(ctx context.Context, id int64, now time.Time) error {
	res := r.txOf(ctx).Exec(
		`UPDATE event_outbox SET status='DONE', updated_at=?
		 WHERE id=? AND status='RUNNING'`, now, id)
	if res.Error != nil {
		return fmt.Errorf("done event=%d: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("done event=%d: 非 RUNNING 态（并发已完成？）", id)
	}
	return nil
}

// MarkFailed 置 FAILED + retry_count+1 + 退避 next_run_at；
// 第 5 次失败置 DEAD 并原子写 alert(EVENT_DELIVERY_FAILED, CRITICAL)。
func (r *EventJobRepo) MarkFailed(ctx context.Context, id int64, cause string, now time.Time) error {
	return r.txOf(ctx).Transaction(func(tx *gorm.DB) error {
		var cur eventJobRow
		if err := tx.Where("id = ?", id).Take(&cur).Error; err != nil {
			return fmt.Errorf("mark failed load event=%d: %w", id, err)
		}
		newRetry := cur.RetryCount + 1
		backoff := EventBackoff(newRetry)
		newStatus := "FAILED"
		if newRetry >= EventMaxRetry {
			newStatus = "DEAD"
		}
		res := tx.Exec(
			`UPDATE event_outbox SET status=?, retry_count=?, next_run_at=?, updated_at=?
			 WHERE id=?`,
			newStatus, newRetry, now.Add(backoff), now, id)
		if res.Error != nil {
			return fmt.Errorf("mark failed event=%d: %w", id, res.Error)
		}
		if newStatus != "DEAD" {
			return nil
		}
		// DEAD → 告警（NOT EXISTS 原子去重：同事件重复 DEAD 不重复告警）
		msg := fmt.Sprintf("event_outbox 事件 %d 连续失败 %d 次置 DEAD：%s", id, newRetry, cause)
		aRes := tx.Exec(
			`INSERT INTO alert (alert_type, severity, target_type, target_id, message, status, created_at, updated_at)
			 SELECT 'EVENT_DELIVERY_FAILED', 'CRITICAL', 'EVENT_OUTBOX', ?, ?, 'OPEN', ?, ?
			 WHERE NOT EXISTS (
			   SELECT 1 FROM alert
			   WHERE alert_type = 'EVENT_DELIVERY_FAILED' AND target_type = 'EVENT_OUTBOX'
			     AND target_id = ? AND status = 'OPEN'
			 )`,
			id, msg, now, now, id)
		if aRes.Error != nil {
			return fmt.Errorf("alert dead event=%d: %w", id, aRes.Error)
		}
		return nil
	})
}

// ResetStaleRunning 启动复位：进程强杀/超时强退残留的 RUNNING 一律放回 PENDING，
// retry_count+1（复位本身就是一次"没跑完"的失败，计数并入退避——6b-3 复核注意点 3）。
func (r *EventJobRepo) ResetStaleRunning(ctx context.Context) (int64, error) {
	res := r.txOf(ctx).Exec(
		`UPDATE event_outbox SET status='PENDING', retry_count=retry_count+1, updated_at=now()
		 WHERE status='RUNNING'`)
	if res.Error != nil {
		return 0, fmt.Errorf("reset stale running event_outbox: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// LoadWebhookConfig 读 sys_config.open_api.webhook_url / webhook_secret。
// 不存在 → ( "", "", nil )（视为未配置，worker NOOP）。
func (r *EventJobRepo) LoadWebhookConfig(ctx context.Context) (string, string, error) {
	var url, secret string
	err := r.txOf(ctx).Table("sys_config").
		Where("config_key = ?", "open_api.webhook_url").
		Select("config_value").
		Take(&url).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "", fmt.Errorf("load sys_config open_api.webhook_url: %w", err)
	}
	err = r.txOf(ctx).Table("sys_config").
		Where("config_key = ?", "open_api.webhook_secret").
		Select("config_value").
		Take(&secret).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "", fmt.Errorf("load sys_config open_api.webhook_secret: %w", err)
	}
	return url, secret, nil
}

// EventBackoff 指数退避：retry_count=1 → 1min，=2 → 5min，=3 → 25min，=4 → 125min。
// n<=0 按 1 计（防御）。
func EventBackoff(retryCount int) time.Duration {
	if retryCount <= 1 {
		return time.Minute
	}
	d := time.Minute
	for i := 1; i < retryCount; i++ {
		d *= 5
	}
	return d
}

// EventMaxRetry 是 event_outbox 推送的最大重试次数（含首次执行）：
// retry_count 达到 EventMaxRetry 即置 DEAD 并写告警，不再重试（契约 §4）。
const EventMaxRetry = 5
