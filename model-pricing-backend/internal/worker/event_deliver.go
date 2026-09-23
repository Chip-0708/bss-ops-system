// Package worker 的 event_deliver.go：11b event_outbox 推送消费者。
//
// 关键纪律（与 6b-3 COST_RECALC 同源）：
//   - 认领与推送分离：认领只动 event_outbox 行；推送是 HTTP 调用，不动库。
//   - 启动复位（ResetStaleRunning）：每个 tick 前先吃 RUNNING 残留，幂等。
//   - 单条失败不中断其他：遍历每条独立消费。
//   - 指数退避 1min × 5^(retry_count-1)，第 5 次置 DEAD + alert(EVENT_DELIVERY_FAILED, CRITICAL)。
//   - webhook_url 为空时 NOOP：状态机推进但跳过 HTTP（裁决 8）。
package worker

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"model_bss/internal/domain/supplier"
	"model_bss/internal/repo"
)

// EventDeliverJob 构造一个 event_outbox 推送消费者 JobFunc。
// 每次执行：先启动复位（每个 tick 前先吃残留，幂等），再批量 Poll 消费。
// webhook 配置每次 tick 从 sys_config 读（部署侧改了不用重启）。
func EventDeliverJob(
	eventJobs *repo.EventJobRepo,
	log *zap.Logger,
) JobFunc {
	return func(ctx context.Context, ident supplier.JobIdentity) error {
		now := time.Now().UTC()
		// 启动复位：进程强杀/超时强退残留的 RUNNING 放回 PENDING。
		// 放在每个 tick 前——第一次跑就吃存量，之后每次空转成本是一发 UPDATE 0 行，可忽略。
		resetN, err := eventJobs.ResetStaleRunning(ctx)
		if err != nil {
			log.Error("event-deliver reset stale running failed", zap.Error(err))
			return err // 复位失败不继续消费，防止并发抢同一批 RUNNING
		}
		if resetN > 0 {
			log.Warn("event-deliver reset stale running events", zap.Int64("count", resetN))
		}

		// webhook 配置每次 tick 从 sys_config 读（部署侧改了不用重启）。
		webhookURL, webhookSecret, err := eventJobs.LoadWebhookConfig(ctx)
		if err != nil {
			log.Error("event-deliver load webhook config failed", zap.Error(err))
			return err
		}
		// webhook_url 为空时 NOOP：状态机推进但跳过 HTTP（裁决 8）。
		if webhookURL == "" {
			log.Debug("event-deliver webhook_url 未配置，跳过推送")
			return nil
		}

		rows, err := eventJobs.PollDue(ctx, now, 50)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		log.Info("event-deliver polling", zap.Int("due", len(rows)))
		client := NewDeliverClient(webhookURL, webhookSecret)
		var firstErr error
		for _, row := range rows {
			claimed, err := eventJobs.Claim(ctx, row.ID, now)
			if err != nil {
				log.Error("event-deliver claim failed", zap.Int64("event_id", row.ID), zap.Error(err))
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if !claimed {
				log.Debug("event-deliver event claimed by another consumer, skip", zap.Int64("event_id", row.ID))
				continue
			}
			// 推送（HTTP，不动库）
			deliverErr := client.Deliver(ctx, row.ID, row.EventType, json.RawMessage(row.Payload), row.CreatedAt)
			if deliverErr == nil {
				if err := eventJobs.MarkDone(ctx, row.ID, now); err != nil {
					log.Error("event-deliver mark done failed", zap.Int64("event_id", row.ID), zap.Error(err))
					if firstErr == nil {
						firstErr = err
					}
				}
				continue
			}
			// 推送失败：MarkFailed（退避 + 第 5 次 DEAD + alert）
			log.Warn("event-deliver push failed", zap.Int64("event_id", row.ID), zap.Error(deliverErr))
			if err := eventJobs.MarkFailed(ctx, row.ID, deliverErr.Error(), now); err != nil {
				log.Error("event-deliver mark failed failed", zap.Int64("event_id", row.ID), zap.Error(err))
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		return firstErr
	}
}
