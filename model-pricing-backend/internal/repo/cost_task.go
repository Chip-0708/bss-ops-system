// Package repo 的 cost_task.go：COST_RECALC 任务队列的 GORM 实现（6b-3）。
// 纪律与 QuoteRepo 同源：全部走 txOf(ctx)；认领/推进只动 task_job 自己的行，
// 业务写入（cost_baseline）在 domain 层另行开事务——两者必须分离，否则业务回滚
// 会把已认领的 RUNNING 拖成孤儿（6b-3 复核注意点 1）。
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"model_bss/internal/domain/cost"
)

// CostTaskRepo 是 cost.TaskStore 的 GORM 实现。
type CostTaskRepo struct {
	*SupplierRepo
}

// NewCostTaskRepo 构造任务仓储。
func NewCostTaskRepo(base *gorm.DB) *CostTaskRepo {
	return &CostTaskRepo{SupplierRepo: NewSupplierRepo(base)}
}

var _ cost.TaskStore = (*CostTaskRepo)(nil)

// ResetStaleRunning 启动复位：进程被强杀/优雅超时强退残留的 RUNNING 一律放回 PENDING，
// retry_count+1（复位本身就是一次"没跑完"的失败，计数并入退避——6b-3 复核注意点 3）。
// 按 job_type 过滤，避免误伤其他消费者的任务。
func (r *CostTaskRepo) ResetStaleRunning(ctx context.Context, jobType string) (int64, error) {
	res := r.txOf(ctx).Exec(
		`UPDATE task_job SET status = 'PENDING', retry_count = retry_count + 1, updated_at = now()
		 WHERE job_type = ? AND status = 'RUNNING'`, jobType)
	if res.Error != nil {
		return 0, fmt.Errorf("reset stale running %s: %w", jobType, res.Error)
	}
	return res.RowsAffected, nil
}

// Claim 条件更新认领（§9 防重第一道）：从 (PENDING|FAILED) 任一 → RUNNING，
// 用 next_run_at<=now 双重守卫（FAILED 必须在退避到点后才允许再抽调）。
// 0 行 = 已被别的消费者拿走 / 退避未到点，返回 ok=false 跳过，不报错。
// 注意：FAILED**不是终态**——它等价于"等退避再试一次"，终态只有 DONE / DEAD。
// GORM Exec 用 ? 占位符按位置绑参：1=now(updated_at) 2=id 3=now(next_run_at)——
// 别按"逻辑顺序"写（那样 id 会绑到 next_run_at 的参数槽，14830 编码错）。
func (r *CostTaskRepo) Claim(ctx context.Context, taskID int64, now time.Time) (bool, error) {
	res := r.txOf(ctx).Exec(
		`UPDATE task_job SET status = 'RUNNING', updated_at = ?
		 WHERE id = ? AND next_run_at <= ? AND status IN ('PENDING','FAILED')`,
		now, taskID, now)
	if res.Error != nil {
		return false, fmt.Errorf("claim task=%d: %w", taskID, res.Error)
	}
	return res.RowsAffected > 0, nil
}

// LoadTask 读任务行并解 payload。不存在 → ErrTaskJobNotFound。
func (r *CostTaskRepo) LoadTask(ctx context.Context, taskID int64) (*cost.TaskJob, error) {
	var row taskJobRow
	err := r.txOf(ctx).Where("id = ?", taskID).Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: id=%d", cost.ErrTaskJobNotFound, taskID)
		}
		return nil, fmt.Errorf("load task=%d: %w", taskID, err)
	}
	var p cost.TaskPayload
	if err := json.Unmarshal(row.Payload, &p); err != nil {
		return nil, fmt.Errorf("task=%d payload 解析失败: %w", taskID, err)
	}
	return &cost.TaskJob{
		ID: row.ID, JobType: row.JobType, Payload: p,
		Status: row.Status, RetryCount: row.RetryCount,
	}, nil
}

// MarkDone 认领后推进到 DONE 并清 last_error。
func (r *CostTaskRepo) MarkDone(ctx context.Context, taskID int64, now time.Time) error {
	res := r.txOf(ctx).Exec(
		`UPDATE task_job SET status = 'DONE', last_error = NULL, updated_at = ?
		 WHERE id = ? AND status = 'RUNNING'`, now, taskID)
	if res.Error != nil {
		return fmt.Errorf("done task=%d: %w", taskID, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("done task=%d: 非 RUNNING 态（并发已完成？）", taskID)
	}
	return nil
}

// MarkFailed 置 FAILED + retry_count+1 + 指数退避 next_run_at；
// 超限（retry_count+1 >= MaxRetry）置 DEAD 并原子写告警（06-cost §9：
// 超限置 DEAD 不重试并写 alert）。告警与状态推进同事务——不能只推进不告警。
func (r *CostTaskRepo) MarkFailed(ctx context.Context, taskID int64, cause string, now time.Time) error {
	return r.txOf(ctx).Transaction(func(tx *gorm.DB) error {
		var cur taskJobRow
		if err := tx.Where("id = ?", taskID).Take(&cur).Error; err != nil {
			return fmt.Errorf("mark failed load task=%d: %w", taskID, err)
		}
		newRetry := cur.RetryCount + 1
		backoff := cost.Backoff(newRetry)
		newStatus := "FAILED"
		if newRetry >= cost.MaxRetry {
			newStatus = "DEAD"
		}
		res := tx.Exec(
			`UPDATE task_job SET status = ?, retry_count = ?, last_error = ?, next_run_at = ?, updated_at = ?
			 WHERE id = ?`,
			newStatus, newRetry, cause, now.Add(backoff), now, taskID)
		if res.Error != nil {
			return fmt.Errorf("mark failed task=%d: %w", taskID, res.Error)
		}
		if newStatus != "DEAD" {
			return nil
		}
		// DEAD → 告警（NOT EXISTS 原子去重：同任务重复 DEAD 不重复告警）
		msg := fmt.Sprintf("COST_RECALC 任务 %d 连续失败 %d 次置 DEAD：%s", taskID, newRetry, cause)
		aRes := tx.Exec(
			`INSERT INTO alert (alert_type, severity, target_type, target_id, message, status, created_at, updated_at)
			 SELECT 'COST_RECALC_DEAD', 'CRITICAL', 'TASK_JOB', ?, ?, 'OPEN', ?, ?
			 WHERE NOT EXISTS (
			   SELECT 1 FROM alert
			   WHERE alert_type = 'COST_RECALC_DEAD' AND target_type = 'TASK_JOB'
			     AND target_id = ? AND status = 'OPEN'
			 )`,
			taskID, msg, now, now, taskID)
		if aRes.Error != nil {
			return fmt.Errorf("alert dead task=%d: %w", taskID, aRes.Error)
		}
		return nil
	})
}

// IsVersionConflictConstraint 识别 23505/23P01 且约束名落在成本基线那两条上——
// 供消费者把「预期约束冲突」与真异常分流（last_error 必须含约束名，6b-3 复核收尾）。
func IsVersionConflictConstraint(err error) (constraint string, ok bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return "", false
	}
	if pgErr.Code != "23505" && pgErr.Code != "23P01" {
		return "", false
	}
	switch pgErr.ConstraintName {
	case "uk_cost_current", "uk_cost_ver", "ex_cost_no_overlap":
		return pgErr.ConstraintName, true
	}
	return "", false
}
