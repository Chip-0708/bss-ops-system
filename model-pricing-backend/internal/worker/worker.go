// Package worker 提供后台任务调度（阶段 6a：worker 骨架）。
//
// 设计要点（6a 裁决与补充要求）：
//   - 每日任务（07:00/07:10/07:20）默认关闭，用 cron_lock(job_name, run_date) 抢占防重；
//     分钟级任务（activate-quote）默认开启，靠领域方法本身幂等，不用 cron_lock。
//   - 每日时间按 db.timezone（Asia/Shanghai）解析，run_date 也用上海时区的今天——
//     用 UTC 会把 07:00 算成 UTC 07:00（北京 15:00）触发，跨零点会归属错日期。
//   - 单实例防重：cron_lock PRIMARY KEY 抢占，抢不到 = 本日已跑/他实例在跑 → 跳过。
//   - 优雅退出：先 httpServer.Shutdown()（等在途请求入队完），再 worker.Stop()（等当前批次 ≤5s）。
//   - 单任务 panic 不带走 goroutine：defer recover() 记 error 日志，下次 tick 继续。
//   - 统一身份：worker 无人值守，OperatorID=0 / OperatorRole="SYSTEM" / SourceType="WORKER"，
//     每次执行生成 request_id（UUID）注入，把一次 job 的所有改动串起来。
package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"model_bss/internal/domain/supplier"
)

// JobFunc 是一个任务的执行体。ident 由调度器统一注入（SYSTEM/WORKER + 本次 request_id）。
type JobFunc func(ctx context.Context, ident supplier.JobIdentity) error

// CronLocker 是 cron_lock 的抽象（6a 补充要求：单测用纯 Go fake，不依赖 cgo sqlite）。
// TryLock 抢占当日锁：抢到返回 true 执行；抢不到（同名同日已存在）返回 false 跳过。
// 真库实现靠 PRIMARY KEY(job_name, run_date) + ON CONFLICT DO NOTHING；
// fake 必须真实模拟主键冲突（同 key 第二次插入返回 false），否则防重语义什么都没验证。
type CronLocker interface {
	TryLock(ctx context.Context, jobName, runDate string) (bool, error)
}

// gormCronLocker 是 CronLocker 的 GORM 实现（真库）。
type gormCronLocker struct{ db *gorm.DB }

// NewCronLocker 构造真库 CronLocker。
func NewCronLocker(db *gorm.DB) CronLocker { return &gormCronLocker{db: db} }

// TryLock 用 INSERT ... ON CONFLICT DO NOTHING 抢 (job_name, run_date) 主键：
// RowsAffected=1 抢到；=0 冲突（本日已跑/他实例在跑）→ 跳过。
func (l *gormCronLocker) TryLock(ctx context.Context, jobName, runDate string) (bool, error) {
	res := l.db.WithContext(ctx).Exec(
		"INSERT INTO cron_lock (job_name, run_date) VALUES (?, ?) ON CONFLICT DO NOTHING",
		jobName, runDate)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// job 是注册的任务。
type job struct {
	name     string
	fn       JobFunc
	interval time.Duration // >0：分钟级轮询
	daily    string        // "HH:MM"：每日任务（cron_lock 防重）
}

// Scheduler 是任务调度器。
type Scheduler struct {
	locker CronLocker
	log    *zap.Logger
	loc    *time.Location
	jobs   []job
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewScheduler 构造调度器。locker 为 cron_lock 实现（真库 NewCronLocker(db)，测试用 fake）；
// loc 为每日时间/日期的解析时区（来自 db.timezone，Asia/Shanghai）。
func NewScheduler(locker CronLocker, log *zap.Logger, loc *time.Location) *Scheduler {
	return &Scheduler{locker: locker, log: log, loc: loc}
}

// Register 注册一个任务。interval>0 为分钟级；daily 非空（"HH:MM"）为每日任务。
func (s *Scheduler) Register(name string, fn JobFunc, interval time.Duration, daily string) {
	s.jobs = append(s.jobs, job{name: name, fn: fn, interval: interval, daily: daily})
}

// Start 启动所有已注册任务（每个一个 goroutine）。
// 6b 预埋（补充要求 4）：COST_RECALC 消费者会用「条件更新 PENDING→RUNNING」认领任务，
// 进程被强杀/优雅超时强退时会残留 RUNNING 永远不动的任务。6b 在此追加一次复位：
//
//	UPDATE task_job SET status='PENDING', retry_count=retry_count+1 WHERE status='RUNNING'
//
// 本批（6a）的 activate-quote 直接调领域方法、不走 task_job 认领，无 RUNNING 态，暂不实现。
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	for _, j := range s.jobs {
		s.wg.Add(1)
		go s.run(ctx, j)
	}
}

// Stop 优雅退出：cancel 所有 goroutine，等当前批次跑完（≤5s 超时强退）。
// 调用顺序（6a 裁决②）：先 httpServer.Shutdown() 再 worker.Stop()——
// 在途请求入队的任务发生在 Shutdown 返回前，此时 worker 还活着能顺手处理，遗留 PENDING 更少。
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.log.Warn("worker stop timeout, force exit")
	}
}

// run 是单个任务的 goroutine 主循环。
func (s *Scheduler) run(ctx context.Context, j job) {
	defer s.wg.Done()
	if j.interval > 0 {
		s.runInterval(ctx, j)
		return
	}
	s.runDaily(ctx, j)
}

// runInterval 分钟级轮询（activate-quote）：靠领域方法幂等，不用 cron_lock。
func (s *Scheduler) runInterval(ctx context.Context, j job) {
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()
	// 启动即跑一次（不等第一个 tick）
	s.exec(ctx, j)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.exec(ctx, j)
		}
	}
}

// runDaily 每日任务：到点执行，cron_lock 防重。
func (s *Scheduler) runDaily(ctx context.Context, j job) {
	for {
		now := time.Now().In(s.loc)
		next := s.nextDailyRun(now, j.daily)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			s.execDaily(ctx, j)
		}
	}
}

// nextDailyRun 计算下一次执行时间（上海时区的 HH:MM；今天已过点则明天）。
func (s *Scheduler) nextDailyRun(now time.Time, hhmm string) time.Time {
	var h, m int
	_, _ = fmt.Sscanf(hhmm, "%d:%d", &h, &m)
	next := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, s.loc)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// execDaily 每日任务执行：cron_lock 抢占，抢不到跳过。
func (s *Scheduler) execDaily(ctx context.Context, j job) {
	today := time.Now().In(s.loc).Format("2006-01-02")
	ok, err := s.locker.TryLock(ctx, j.name, today)
	if err != nil {
		s.log.Error("cron_lock insert failed", zap.String("job", j.name), zap.Error(err))
		return
	}
	if !ok {
		s.log.Info("job already locked today, skip", zap.String("job", j.name), zap.String("run_date", today))
		return
	}
	s.exec(ctx, j)
}

// exec 执行一次任务：注入统一身份 + defer recover（单任务 panic 不带走 goroutine）。
func (s *Scheduler) exec(ctx context.Context, j job) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("job panicked, will retry next tick",
				zap.String("job", j.name), zap.Any("panic", r))
		}
	}()
	ident := supplier.SystemJob(uuid.NewString())
	if err := j.fn(ctx, ident); err != nil {
		s.log.Error("job failed", zap.String("job", j.name), zap.Error(err))
	}
}
