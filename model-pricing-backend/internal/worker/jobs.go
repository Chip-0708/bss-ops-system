// Package worker 的 jobs.go：把 5b/5d/6b 已有的领域方法包成 JobFunc。
// 6b-3 起注册 cost-recalc（COST_RECALC 消费者）：认领靠条件更新 PENDING→RUNNING，
// 业务事务与任务状态推进分离，退避 1min×5^(n-1)，第 3 次置 DEAD+CRITICAL 告警。
package worker

import (
	"context"
	"time"

	"model_bss/internal/domain/cost"
	"model_bss/internal/domain/supplier"
	"model_bss/internal/infra/config"
	"model_bss/internal/repo"
)

// RegisterJobs 按配置注册任务。返回注册的任务数（供启动日志）。
// costSvc / costJobs 为 6b-3 新增：COST_RECALC 消费者；传 nil 时跳过注册（老调用点兼容）。
// eventJobs 为 11b 新增：event_outbox 推送消费者；传 nil 时跳过注册。
func RegisterJobs(s *Scheduler, cfg config.Worker, approveSvc *supplier.ApproveService, lifecycleSvc *supplier.LifecycleService, costSvc *cost.Service, costJobs *CostJobRepo, eventJobs *repo.EventJobRepo) int {
	if !cfg.Enable {
		return 0
	}
	n := 0
	if cfg.Jobs.ActivateQuote.Enable {
		interval := cfg.Jobs.ActivateQuote.Interval
		if interval <= 0 {
			interval = time.Minute // 默认 1 分钟
		}
		s.Register("activate-quote", func(ctx context.Context, ident supplier.JobIdentity) error {
			_, err := approveSvc.ActivateDueQuotes(ctx, supplier.ActivateParams{
				OperatorID:   ident.OperatorID,
				OperatorRole: ident.OperatorRole,
				SourceType:   ident.SourceType,
				RequestID:    ident.RequestID,
			})
			return err
		}, interval, "")
		n++
	}
	if cfg.Jobs.CostRecalc.Enable && costSvc != nil && costJobs != nil {
		interval := cfg.Jobs.CostRecalc.Interval
		if interval <= 0 {
			interval = time.Minute // 默认 1 分钟
		}
		s.Register("cost-recalc", CostRecalcJob(costSvc, costJobs, s.log), interval, "")
		n++
	}
	if cfg.Jobs.EventDeliver.Enable && eventJobs != nil {
		interval := cfg.Jobs.EventDeliver.Interval
		if interval <= 0 {
			interval = time.Minute // 默认 1 分钟
		}
		s.Register("event-deliver", EventDeliverJob(eventJobs, s.log), interval, "")
		n++
	}
	if cfg.Jobs.QuoteExpireScan.Enable {
		daily := cfg.Jobs.QuoteExpireScan.Daily
		if daily == "" {
			daily = "07:00" // 兜底（05-quotes §12.3）
		}
		s.Register("quote-expire-scan", func(ctx context.Context, ident supplier.JobIdentity) error {
			_, err := lifecycleSvc.RunExpireScan(ctx, ident)
			return err
		}, 0, daily)
		n++
	}
	if cfg.Jobs.QuoteExpireFinal.Enable {
		daily := cfg.Jobs.QuoteExpireFinal.Daily
		if daily == "" {
			daily = "07:10"
		}
		s.Register("quote-expire-final", func(ctx context.Context, ident supplier.JobIdentity) error {
			_, err := lifecycleSvc.RunExpireFinal(ctx, ident)
			return err
		}, 0, daily)
		n++
	}
	if cfg.Jobs.QuoteAnomalyScan.Enable {
		daily := cfg.Jobs.QuoteAnomalyScan.Daily
		if daily == "" {
			daily = "07:20"
		}
		s.Register("quote-anomaly-scan", func(ctx context.Context, ident supplier.JobIdentity) error {
			_, err := lifecycleSvc.RunAnomalyScan(ctx, 30, ident)
			return err
		}, 0, daily)
		n++
	}
	return n
}
