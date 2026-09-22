// Package worker 的 cost_recalc.go：COST_RECALC 消费者（6b-3）。
//
// 关键纪律：
//   - 业务事务与任务状态推进严格分离：认领/推进只动 task_job 行；
//     业务写（基线版本切换）在 cost.Service 内部另行开事务——混在一起会让业务回滚
//     把已认领的 RUNNING 拖成孤儿（6b-3 复核注意点 1）。
//   - 退避 1min × 5^(retry_count-1)：1min / 5min / 第 3 次置 DEAD
//     并写 alert(COST_RECALC_DEAD, CRITICAL)。
//   - 启动复位（ResetStaleRunning）：COST_RECALC 的 RUNNING 残留放回 PENDING 且
//     retry_count+1。在 RegisterJobs 注册前执行一次，不单独 commit（6b-3 复核注意点 3）。
//   - 单条失败不中断其他任务（Poll 遍历每条独立消费）。
package worker

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"model_bss/internal/domain/cost"
	"model_bss/internal/domain/supplier"
)

// CostJobRepo 是消费者拉任务的最小仓储（不走 CronLocker——分钟级轮询，认领靠条件更新）。
type CostJobRepo struct {
	db *gorm.DB
}

// NewCostJobRepo 构造任务拉取仓储。
func NewCostJobRepo(db *gorm.DB) *CostJobRepo { return &CostJobRepo{db: db} }

// PollDue 取可认领任务：status IN ('PENDING','FAILED') AND next_run_at<=now，
// 按 next_run_at 升序（先到先服务）。FAILED 是"等退避"中间态而非终态——
// 终态只有 DONE / DEAD；Claim 只允许从 (PENDING|FAILED) 抽调，与 PollDue 谓词保持同步。
// 一次只拉一批（limit 50），防止存量 13 条以上的积压把单轮拖垮——留下的下一轮再吃。
func (r *CostJobRepo) PollDue(ctx context.Context, jobType string, now time.Time, limit int) ([]int64, error) {
	var ids []int64
	err := r.db.WithContext(ctx).
		Table("task_job").
		Where("job_type = ? AND status IN ('PENDING','FAILED') AND next_run_at <= ?", jobType, now).
		Order("next_run_at ASC, id ASC").
		Limit(limit).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("poll due %s: %w", jobType, err)
	}
	return ids, nil
}

// CostRecalcJob 是一个注册到 Scheduler 的消费者 JobFunc 工厂。
// 每次执行：先启动复位（每个 tick 前先吃残留，幂等），再批量 Poll 消费。
func CostRecalcJob(svc *cost.Service, jobs *CostJobRepo, log *zap.Logger) JobFunc {
	return func(ctx context.Context, ident supplier.JobIdentity) error {
		now := time.Now().UTC()
		// 启动复位：进程强杀/超时强退残留的 RUNNING 放回 PENDING。
		// 放在每个 tick 前——第一次跑就吃存量，之后每次空转成本是一发 UPDATE 0 行，可忽略。
		resetN, err := svc.ResetStaleRunning(ctx)
		if err != nil {
			log.Error("cost-recalc reset stale running failed", zap.Error(err))
			return err // 复位失败不继续消费，防止并发抢同一批 RUNNING
		}
		if resetN > 0 {
			log.Warn("cost-recalc reset stale running tasks", zap.Int64("count", resetN))
		}

		ids, err := jobs.PollDue(ctx, "COST_RECALC", now, 50)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		log.Info("cost-recalc polling", zap.Int("due", len(ids)))
		var firstErr error
		for _, id := range ids {
			claimed, err := svc.ConsumeTask(ctx, id, now, cost.JobIdentity{
				OperatorID:   ident.OperatorID,
				OperatorRole: ident.OperatorRole,
				SourceType:   ident.SourceType,
				RequestID:    ident.RequestID,
			})
			if err != nil {
				log.Error("cost-recalc consume failed", zap.Int64("task_id", id), zap.Error(err))
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if !claimed {
				log.Debug("cost-recalc task claimed by another consumer, skip", zap.Int64("task_id", id))
			}
		}
		return firstErr
	}
}
