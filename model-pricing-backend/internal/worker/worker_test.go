package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"model_bss/internal/domain/supplier"
	"model_bss/internal/infra/config"
)

// fakeCronLocker 是 cron_lock 的纯 Go 实现（替代内存 sqlite，Windows 无 cgo）。
// 语义对齐 PRIMARY KEY(job_name, run_date) + ON CONFLICT DO NOTHING：
// 同 key 首次插入返回 true（抢占成功），第二次返回 false（冲突→跳过）。
type fakeCronLocker struct {
	mu   sync.Mutex
	keys map[string]struct{}
}

func newFakeCronLocker() *fakeCronLocker { return &fakeCronLocker{keys: map[string]struct{}{}} }

// TryLock 同 key 第二次返回 false——必须是真实模拟主键冲突，
// 不能写成"每次都成功"，否则防重（TestCronLock_SingleInstance）什么都没验证。
func (f *fakeCronLocker) TryLock(_ context.Context, jobName, runDate string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := jobName + "|" + runDate
	if _, ok := f.keys[k]; ok {
		return false, nil
	}
	f.keys[k] = struct{}{}
	return true, nil
}

// newTestScheduler 用 fake cron locker 构造调度器。
func newTestScheduler(t *testing.T) (*Scheduler, *fakeCronLocker) {
	t.Helper()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	locker := newFakeCronLocker()
	return NewScheduler(locker, zap.NewNop(), loc), locker
}

func TestScheduler_RegisterAndStart(t *testing.T) {
	s, _ := newTestScheduler(t)
	var ran atomic.Int32
	cfg := config.Worker{
		Enable: true,
		Jobs: config.WorkerJobs{
			ActivateQuote:    config.JobConfig{Enable: true, Interval: 50 * time.Millisecond},
			QuoteExpireScan:  config.JobConfig{Enable: false, Daily: "07:00"},
			QuoteExpireFinal: config.JobConfig{Enable: false, Daily: "07:10"},
			QuoteAnomalyScan: config.JobConfig{Enable: false, Daily: "07:20"},
		},
	}
	approveSvc := supplier.NewApproveService(&fakeApproveStore{})
	lifecycleSvc := supplier.NewLifecycleService(&fakeLifecycleStore{}, approveSvc)
	n := RegisterJobs(s, cfg, approveSvc, lifecycleSvc, nil, nil, nil)
	require.Equal(t, 1, n, "只有 enable=true 的 activate_quote 注册")

	s.Register("test-job", func(ctx context.Context, ident supplier.JobIdentity) error {
		ran.Add(1)
		return nil
	}, 50*time.Millisecond, "")

	s.Start(context.Background())
	time.Sleep(180 * time.Millisecond)
	s.Stop()
	require.GreaterOrEqual(t, ran.Load(), int32(2), "分钟级任务至少跑 2 次（启动即跑 + tick）")
}

func TestScheduler_StopGraceful(t *testing.T) {
	s, _ := newTestScheduler(t)
	started := make(chan struct{})
	s.Register("slow-job", func(ctx context.Context, ident supplier.JobIdentity) error {
		close(started)
		time.Sleep(100 * time.Millisecond) // 模拟在跑的批次
		return nil
	}, 50*time.Millisecond, "")
	s.Start(context.Background())
	<-started
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("Stop 超过 5s 超时未返回")
	}
}

// TestCronLock_SingleInstance 同日同名只能抢占一次（防重语义，fake 模拟主键冲突）。
func TestCronLock_SingleInstance(t *testing.T) {
	locker := newFakeCronLocker()
	ctx := context.Background()
	today := "2026-09-14"

	ok, err := locker.TryLock(ctx, "test-job", today)
	require.NoError(t, err)
	require.True(t, ok, "首次抢占必须成功")

	ok, err = locker.TryLock(ctx, "test-job", today)
	require.NoError(t, err)
	require.False(t, ok, "同日同名第二次必须抢占失败（防重）")

	ok, err = locker.TryLock(ctx, "test-job", "2026-09-15")
	require.NoError(t, err)
	require.True(t, ok, "不同日期主键不同，必须能抢占")

	ok, err = locker.TryLock(ctx, "other-job", today)
	require.NoError(t, err)
	require.True(t, ok, "不同任务名主键不同，必须能抢占")
}

// TestDailyJob_ExecDailySkipsWhenLocked 每日任务：同日第二次 execDaily 被 cron_lock 拦下。
func TestDailyJob_ExecDailySkipsWhenLocked(t *testing.T) {
	s, _ := newTestScheduler(t)
	var ran atomic.Int32
	j := job{
		name: "daily-job",
		fn: func(ctx context.Context, ident supplier.JobIdentity) error {
			ran.Add(1)
			return nil
		},
	}
	s.execDaily(context.Background(), j)
	s.execDaily(context.Background(), j)
	require.Equal(t, int32(1), ran.Load(), "同日第二次 execDaily 必须被 cron_lock 拦下")
}

func TestDailyJob_RunAtTime(t *testing.T) {
	s, _ := newTestScheduler(t)
	loc := s.loc
	// 上海时区 07:00：今天 06:59 → 今天 07:00；今天 07:01 → 明天 07:00
	at659 := time.Date(2026, 9, 14, 6, 59, 0, 0, loc)
	next := s.nextDailyRun(at659, "07:00")
	require.Equal(t, 14, next.Day(), "06:59 的下一次 07:00 是今天")
	require.Equal(t, 7, next.Hour())

	at701 := time.Date(2026, 9, 14, 7, 1, 0, 0, loc)
	next = s.nextDailyRun(at701, "07:00")
	require.Equal(t, 15, next.Day(), "07:01 的下一次 07:00 是明天")

	// 跨零点 run_date 归属：上海时区 00:30 的"今天"与 UTC 不同（UTC 是前一天 16:30）
	at0030 := time.Date(2026, 9, 14, 0, 30, 0, 0, loc)
	todayShanghai := at0030.In(loc).Format("2006-01-02")
	todayUTC := at0030.UTC().Format("2006-01-02")
	require.Equal(t, "2026-09-14", todayShanghai, "上海时区 00:30 属 9-14")
	require.Equal(t, "2026-09-13", todayUTC, "UTC 下是 9-13（证明必须用上海时区取 run_date）")
	require.NotEqual(t, todayUTC, todayShanghai, "跨零点日期归属必须按上海时区")
}

// TestDailyJob_PanicRecovery 单任务 panic 不带走 goroutine（补充要求 3）。
func TestDailyJob_PanicRecovery(t *testing.T) {
	s, _ := newTestScheduler(t)
	var calls atomic.Int32
	s.Register("panic-job", func(ctx context.Context, ident supplier.JobIdentity) error {
		calls.Add(1)
		panic("boom")
	}, 50*time.Millisecond, "")
	s.Start(context.Background())
	time.Sleep(180 * time.Millisecond)
	s.Stop()
	require.GreaterOrEqual(t, calls.Load(), int32(2), "panic 后下次 tick 必须继续（goroutine 不被带走）")
}

// ---- 测试用 fake（复用 domain 层 fake 的轻量版） ----

type fakeApproveStore struct{}

func (f *fakeApproveStore) ListPendingQuotes(ctx context.Context, _ supplier.OwnerScope, q supplier.PendingQuoteQuery, _ time.Time) (*supplier.PendingQuoteResult, error) {
	return &supplier.PendingQuoteResult{List: []supplier.PendingQuoteItem{}, Page: q.Page, Size: q.Size}, nil
}
func (f *fakeApproveStore) CheckQuoteScope(ctx context.Context, _ int64, _ supplier.OwnerScope) (bool, bool, error) {
	return true, true, nil
}
func (f *fakeApproveStore) ApproveQuote(ctx context.Context, _ supplier.ApproveParams) (*supplier.ApproveResult, error) {
	return &supplier.ApproveResult{}, nil
}
func (f *fakeApproveStore) RejectQuote(ctx context.Context, _ supplier.RejectParams) (*supplier.RejectResult, error) {
	return &supplier.RejectResult{}, nil
}
func (f *fakeApproveStore) ListDueQuotes(ctx context.Context, _ time.Time) ([]supplier.DueQuote, error) {
	return nil, nil
}
func (f *fakeApproveStore) ActivateOne(ctx context.Context, quoteID int64, _ supplier.ActivateParams) (*supplier.ActivateDueItem, error) {
	return &supplier.ActivateDueItem{ID: quoteID}, nil
}
func (f *fakeApproveStore) MarkActivateError(ctx context.Context, _ int64, _ string) error {
	return nil
}
func (f *fakeApproveStore) LoadQuoteDiff(ctx context.Context, _ int64) (*supplier.QuoteDiffRaw, error) {
	return nil, nil
}
func (f *fakeApproveStore) GetSysConfigDecimal(ctx context.Context, _ string) (decimal.Decimal, error) {
	return decimal.Zero, nil
}

// fakeLifecycleStore 最小实现（RunExpireScan/RunExpireFinal/RunAnomalyScan 返回空）。
type fakeLifecycleStore struct{ fakeApproveStore }

func (f *fakeLifecycleStore) FindQuotableSKUs(ctx context.Context, _ []int64) (map[int64]supplier.QuotableSKU, error) {
	return map[int64]supplier.QuotableSKU{}, nil
}
func (f *fakeLifecycleStore) FindOfficialComponents(ctx context.Context, _ []int64) (map[int64][]supplier.OfficialComponent, error) {
	return map[int64][]supplier.OfficialComponent{}, nil
}
func (f *fakeLifecycleStore) ListExpiringQuotes(ctx context.Context, _ supplier.OwnerScope, _ supplier.ExpiringQuoteQuery, _ time.Time) ([]supplier.ExpiringQuoteItem, int64, error) {
	return nil, 0, nil
}
func (f *fakeLifecycleStore) CountEffectiveSuppliersBySku(ctx context.Context, _ []int64) (map[int64]int, error) {
	return map[int64]int{}, nil
}
func (f *fakeLifecycleStore) ConfirmRemoveQuote(ctx context.Context, _ supplier.ConfirmRemoveParams) (*supplier.ConfirmRemoveResult, error) {
	return &supplier.ConfirmRemoveResult{}, nil
}
func (f *fakeLifecycleStore) ExpireScanTargets(ctx context.Context, _ time.Time) ([]supplier.ExpiringScanTarget, error) {
	return nil, nil
}
func (f *fakeLifecycleStore) CreateExpireTodoOnce(ctx context.Context, _, _ int64, _, _ string, _ time.Time, _ supplier.JobIdentity) (bool, error) {
	return false, nil
}
func (f *fakeLifecycleStore) CreateAlertOnce(ctx context.Context, _ string, _ *int64, _, _ string, _ time.Time, _ supplier.JobIdentity) (bool, error) {
	return false, nil
}
func (f *fakeLifecycleStore) ListConfirmedPendingFinal(ctx context.Context) ([]int64, error) {
	return nil, nil
}
func (f *fakeLifecycleStore) FinalExpireOne(ctx context.Context, _ int64, _ time.Time, _ supplier.JobIdentity) error {
	return nil
}
func (f *fakeLifecycleStore) LoadAnomalyRows(ctx context.Context, _ int, _ time.Time) ([]supplier.AnomalyRawRow, error) {
	return nil, nil
}
func (f *fakeLifecycleStore) FindSupplierByID(ctx context.Context, _ int64) (*supplier.Supplier, error) {
	return nil, nil
}
func (f *fakeLifecycleStore) CreateRetroQuote(ctx context.Context, _ supplier.CreateRetroParams) (*supplier.RetroSheetMeta, error) {
	return &supplier.RetroSheetMeta{}, nil
}
func (f *fakeLifecycleStore) CountRetroThisMonth(ctx context.Context, _ time.Time) (int, error) {
	return 0, nil
}
func (f *fakeLifecycleStore) CreateRetroLimitAlertOnce(ctx context.Context, _, _ int, _ time.Time) (bool, error) {
	return false, nil
}
func (f *fakeLifecycleStore) GetSysConfigInt(ctx context.Context, _ string) (int, error) {
	return 3, nil
}
