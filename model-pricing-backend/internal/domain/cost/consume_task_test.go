package cost

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTaskStore 是 TaskStore 的内存实现，记录每次调用用于断言状态推进顺序。
type fakeTaskStore struct {
	claimOK  bool
	claimErr error
	claimN   int

	loadTask *TaskJob
	loadErr  error

	doneN     int
	failedN   int
	lastCause string

	resetN   int64
	resetErr error
}

func (f *fakeTaskStore) ResetStaleRunning(_ context.Context, _ string) (int64, error) {
	return f.resetN, f.resetErr
}

func (f *fakeTaskStore) Claim(_ context.Context, _ int64, _ time.Time) (bool, error) {
	f.claimN++
	return f.claimOK, f.claimErr
}

func (f *fakeTaskStore) LoadTask(_ context.Context, taskID int64) (*TaskJob, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	t := *f.loadTask
	t.ID = taskID
	return &t, nil
}

func (f *fakeTaskStore) MarkDone(_ context.Context, _ int64, _ time.Time) error {
	f.doneN++
	return nil
}

func (f *fakeTaskStore) MarkFailed(_ context.Context, _ int64, cause string, _ time.Time) error {
	f.failedN++
	f.lastCause = cause
	return nil
}

// hookStore 包装 fakeStore，在 CalcSKU 里捕获 eventTime（验证 effective_time 覆盖）。
// CalcSKU 是领域方法（不在 Store 接口上），所以 hook 点放在 LoadRecalcInput：
// ConsumeTask 的链路是 LoadTask → 决定 effTime → RecalcFromTask(effTime) → RecalcSKU(effTime)
// → LoadRecalcInput(...) → CalcSKU(input, effTime)。eventTime 不会经过 LoadRecalcInput，
// 因此改为断言「service.CalcSKU 收到的 now = payload.effective_time」这一可观察副作用：
// 把 CaptureNowService 嵌进测试，并在 CalcSKU 里读取第一个 quote 的 ValidFrom 是行不通的。
// 最稳的观测点：RecalcSKU 里调 ApplyNewVersion 时透传的 ApplyParams.EventTime。
// 但 fakeStore.ApplyNewVersion 不记录；给 fakeStore 加一个 onApply 回调，这里只断言回调被触发
// 且收到的 EventTime 等于 payload.effective_time。
type applyCaptureStore struct {
	*fakeStore
	captured *time.Time
}

func (a *applyCaptureStore) ApplyNewVersion(ctx context.Context, p ApplyParams) (int64, int, error) {
	if a.captured != nil {
		*a.captured = p.EventTime
	}
	return a.fakeStore.ApplyNewVersion(ctx, p)
}

func TestConsumeTask_ClaimStolen(t *testing.T) {
	ts := &fakeTaskStore{claimOK: false}
	svc := NewService(&fakeStore{}, ts, nil)
	claimed, err := svc.ConsumeTask(context.Background(), 99, time.Now().UTC(), JobIdentity{})
	require.NoError(t, err)
	assert.False(t, claimed, "未认领成功不应推进业务")
	assert.Equal(t, 1, ts.claimN)
	assert.Zero(t, ts.doneN)
	assert.Zero(t, ts.failedN)
}

func TestConsumeTask_Success(t *testing.T) {
	ts := &fakeTaskStore{
		claimOK: true,
		loadTask: &TaskJob{
			ID:      1,
			JobType: "COST_RECALC",
			Payload: TaskPayload{SupplierID: 1, QuoteSheetID: 27, Reason: ReasonQuoteEffective},
			Status:  "RUNNING",
		},
	}
	svc := NewService(&fakeStore{
		targets: []int64{40},
		input:   baseInput(),
	}, ts, nil)
	claimed, err := svc.ConsumeTask(context.Background(), 1, time.Now().UTC(), JobIdentity{RequestID: "r1"})
	require.NoError(t, err)
	assert.True(t, claimed)
	assert.Equal(t, 1, ts.doneN, "成功应 MarkDone")
	assert.Zero(t, ts.failedN)
}

func TestConsumeTask_NoQuoteStillDone(t *testing.T) {
	// ListRecalcTargets 空集 → 无任何 SKU 可算，按 6b-2 复核裁决记 DONE，不重试。
	ts := &fakeTaskStore{
		claimOK: true,
		loadTask: &TaskJob{
			ID:      2,
			JobType: "COST_RECALC",
			Payload: TaskPayload{SupplierID: 9, QuoteSheetID: 9, Reason: ReasonExpireRemove},
			Status:  "RUNNING",
		},
	}
	svc := NewService(&fakeStore{targets: []int64{}}, ts, nil)
	claimed, err := svc.ConsumeTask(context.Background(), 2, time.Now().UTC(), JobIdentity{})
	require.NoError(t, err)
	assert.True(t, claimed)
	assert.Equal(t, 1, ts.doneN, "空目标集（等价于全 NO_QUOTE）应记 DONE")
	assert.Zero(t, ts.failedN)
}

func TestConsumeTask_BusinessError_MarkFailed(t *testing.T) {
	// RecalcSKU 返回 ErrVersionConflict → MarkFailed，cause 必须带可识别关键字
	// （真实 DB 路径下是约束名 uk_cost_current / ex_cost_no_overlap）。
	ts := &fakeTaskStore{
		claimOK: true,
		loadTask: &TaskJob{
			ID:      3,
			JobType: "COST_RECALC",
			Payload: TaskPayload{SupplierID: 1, QuoteSheetID: 27, Reason: "QUOTE_EFFECTIVE"},
			Status:  "RUNNING",
		},
	}
	svc := NewService(&fakeStore{
		targets:  []int64{40},
		input:    baseInput(),
		applyErr: ErrVersionConflict,
	}, ts, nil)
	claimed, err := svc.ConsumeTask(context.Background(), 3, time.Now().UTC(), JobIdentity{})
	require.NoError(t, err, "业务错误被 MarkFailed 消化，ConsumeTask 本身不应再报错")
	assert.True(t, claimed)
	assert.Equal(t, 1, ts.failedN)
	assert.Zero(t, ts.doneN)
	assert.NotEmpty(t, ts.lastCause, "last_error 必须写入")
}

func TestConsumeTask_EffectiveTimeOverride(t *testing.T) {
	// payload 带 effective_time → ApplyNewVersion 收到的 EventTime 应是 effective_time，
	// 而不是ConsumeTask 入参的 now（消费时刻）。
	etro := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	var captured time.Time
	base := &fakeStore{targets: []int64{40}, input: baseInput()}
	st := &applyCaptureStore{fakeStore: base, captured: &captured}
	ts := &fakeTaskStore{
		claimOK: true,
		loadTask: &TaskJob{
			ID:      4,
			JobType: "COST_RECALC",
			Payload: TaskPayload{SupplierID: 1, QuoteSheetID: 27, Reason: ReasonRetro, EffectiveTime: &etro},
			Status:  "RUNNING",
		},
	}
	svc := NewService(st, ts, nil)
	now := time.Now().UTC()
	_, err := svc.ConsumeTask(context.Background(), 4, now, JobIdentity{})
	require.NoError(t, err)
	require.False(t, captured.IsZero(), "ApplyNewVersion 应被调用并捕获 EventTime")
	assert.True(t, captured.Equal(etro),
		"effective_time 应覆盖 eventTime: got %v want %v", captured, etro)
}

func TestConsumeTask_NilTaskStore(t *testing.T) {
	svc := NewService(&fakeStore{}, nil, nil)
	claimed, err := svc.ConsumeTask(context.Background(), 1, time.Now().UTC(), JobIdentity{})
	assert.False(t, claimed)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TaskStore")
}

func TestConsumeTask_ClaimError_Propagates(t *testing.T) {
	ts := &fakeTaskStore{claimErr: errors.New("db down")}
	svc := NewService(&fakeStore{}, ts, nil)
	claimed, err := svc.ConsumeTask(context.Background(), 1, time.Now().UTC(), JobIdentity{})
	assert.False(t, claimed)
	require.Error(t, err)
}

func TestResetStaleRunning_Delegates(t *testing.T) {
	ts := &fakeTaskStore{resetN: 3}
	svc := NewService(&fakeStore{}, ts, nil)
	n, err := svc.ResetStaleRunning(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
}
