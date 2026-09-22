// Package price 的 sync_service_test.go：阶段 7a 单测。
// 覆盖提示词「单测要求」三组：
//  1. 差异比对（NEW/UNCHANGED/CHANGED/多出组件跳过/old=0 不除零/UNMATCHED）；
//  2. 录入校验（sku_id 不存在 / raw_sku_code 匹配与不匹配 / payload 空 / 非法 decimal）；
//  3. sync_job（创建即 SUCCESS、started_at=finished_at、列表分页透传）。
package price

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---- fakeSyncStore：内存实现 ----

type fakeSyncStore struct {
	skuExists   map[int64]bool
	skuByCode   map[string]int64
	jobExists   map[int64]bool
	jobs        []SyncJob
	staging     []StagingRow
	stagingRows []StagingRowInsert // 最近一次 CreateStagingPrices 的落库参数
	versions    []CurrentPriceVersion

	createdJob    *CreateJobParams
	nextStagingID int64
	failWith      error
}

func newFakeSyncStore() *fakeSyncStore {
	return &fakeSyncStore{
		skuExists:     map[int64]bool{},
		skuByCode:     map[string]int64{},
		jobExists:     map[int64]bool{},
		nextStagingID: 100,
	}
}

func (f *fakeSyncStore) CreateSyncJob(_ context.Context, p CreateJobParams) (*SyncJob, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	f.createdJob = &p
	job := &SyncJob{
		ID: 1, JobType: p.JobType, Source: p.Source, Status: JobStatusSuccess,
		StartedAt: p.Now.Format(time.RFC3339),
	}
	finished := p.Now.Format(time.RFC3339)
	job.FinishedAt = &finished
	f.jobs = append(f.jobs, *job)
	return job, nil
}

func (f *fakeSyncStore) ListSyncJobs(_ context.Context, page, size int) ([]SyncJob, int64, error) {
	start := (page - 1) * size
	if start >= len(f.jobs) {
		return []SyncJob{}, int64(len(f.jobs)), nil
	}
	end := start + size
	if end > len(f.jobs) {
		end = len(f.jobs)
	}
	return f.jobs[start:end], int64(len(f.jobs)), nil
}

func (f *fakeSyncStore) SyncJobExists(_ context.Context, id int64) (bool, error) {
	return f.jobExists[id], nil
}

func (f *fakeSyncStore) SKUExists(_ context.Context, skuID int64) (bool, error) {
	return f.skuExists[skuID], nil
}

func (f *fakeSyncStore) FindSKUIDByCode(_ context.Context, code string) (int64, bool, error) {
	id, ok := f.skuByCode[code]
	return id, ok, nil
}

func (f *fakeSyncStore) CreateStagingPrices(_ context.Context, p CreateStagingParams) (*CreateStagingResult, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	f.stagingRows = p.Rows
	res := &CreateStagingResult{CreatedCount: len(p.Rows), StagingIDs: []int64{}}
	for range p.Rows {
		res.StagingIDs = append(res.StagingIDs, f.nextStagingID)
		f.nextStagingID++
	}
	return res, nil
}

func (f *fakeSyncStore) ListStagingPrices(_ context.Context, _ StagingQuery) ([]StagingRow, int64, error) {
	return f.staging, int64(len(f.staging)), nil
}

func (f *fakeSyncStore) LoadCurrentPriceVersions(_ context.Context, skuIDs []int64) ([]CurrentPriceVersion, error) {
	out := []CurrentPriceVersion{}
	for _, v := range f.versions {
		for _, id := range skuIDs {
			if v.SKUID == id {
				out = append(out, v)
			}
		}
	}
	return out, nil
}

func fixedNow7a() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC) }

func int64Ptr7a(v int64) *int64 { return &v }

// ---- 1. ComputeDiff 纯函数 ----

func curVer(components map[string]string) *CurrentPriceVersion {
	return &CurrentPriceVersion{SKUID: 40, Currency: "USD", VersionNo: 1, Components: components}
}

func TestComputeDiff_NoVersionIsNew(t *testing.T) {
	status, detail, err := ComputeDiff(map[string]string{"input": "1.00", "output": "5.0"}, nil)
	require.NoError(t, err)
	require.Equal(t, DiffNew, status)
	require.Len(t, detail, 2)
	// 输出按 component_type 字母序（map 遍历序随机，必须排序钉住）
	require.Equal(t, "input", detail[0].ComponentType)
	require.Nil(t, detail[0].OldPrice)
	require.Equal(t, "1.00", detail[0].NewPrice)
	require.Nil(t, detail[0].DeltaPct)
	require.Equal(t, "output", detail[1].ComponentType)
}

func TestComputeDiff_SameValueIsUnchanged(t *testing.T) {
	// 数值等价即 UNCHANGED："2.50" ≡ "2.50000000"（decimal.Equal，不是字符串相等）
	status, detail, err := ComputeDiff(
		map[string]string{"input": "2.50"},
		curVer(map[string]string{"input": "2.50000000", "output": "10.00000000"}),
	)
	require.NoError(t, err)
	require.Equal(t, DiffUnchanged, status)
	require.Empty(t, detail)
}

func TestComputeDiff_DifferentValueIsChanged(t *testing.T) {
	status, detail, err := ComputeDiff(
		map[string]string{"input": "2.80"},
		curVer(map[string]string{"input": "2.50000000"}),
	)
	require.NoError(t, err)
	require.Equal(t, DiffChanged, status)
	require.Len(t, detail, 1)
	require.Equal(t, "input", detail[0].ComponentType)
	require.NotNil(t, detail[0].OldPrice)
	require.Equal(t, "2.50000000", *detail[0].OldPrice) // DB 原文透传
	require.Equal(t, "2.80", detail[0].NewPrice)
	require.NotNil(t, detail[0].DeltaPct)
	require.Equal(t, "0.120000", *detail[0].DeltaPct) // (2.80−2.50)/2.50 = 0.12
}

func TestComputeDiff_ExtraPayloadComponentSkipped(t *testing.T) {
	// payload 里有 reasoning，当前版本没有 → 跳过不比对（裁决 4）；
	// input 相同 → 整体仍 UNCHANGED。
	status, detail, err := ComputeDiff(
		map[string]string{"input": "2.50", "reasoning": "9.99"},
		curVer(map[string]string{"input": "2.50000000"}),
	)
	require.NoError(t, err)
	require.Equal(t, DiffUnchanged, status)
	require.Empty(t, detail)
}

func TestComputeDiff_OldZeroDeltaNull(t *testing.T) {
	// old=0 → delta_pct=NULL，绝不除零（变异验证 2 的挂接点）
	status, detail, err := ComputeDiff(
		map[string]string{"input": "2.80"},
		curVer(map[string]string{"input": "0.00000000"}),
	)
	require.NoError(t, err)
	require.Equal(t, DiffChanged, status)
	require.Len(t, detail, 1)
	require.NotNil(t, detail[0].OldPrice)
	require.Equal(t, "0.00000000", *detail[0].OldPrice)
	require.Nil(t, detail[0].DeltaPct)
}

func TestComputeDiff_NegativeDelta(t *testing.T) {
	status, detail, err := ComputeDiff(
		map[string]string{"input": "2.00"},
		curVer(map[string]string{"input": "2.50000000"}),
	)
	require.NoError(t, err)
	require.Equal(t, DiffChanged, status)
	require.Equal(t, "-0.200000", *detail[0].DeltaPct) // (2.00−2.50)/2.50
}

func TestComputeDiff_InvalidDecimalErrors(t *testing.T) {
	// 数据被绕过应用层写脏 → 报错不静默
	_, _, err := ComputeDiff(map[string]string{"input": "abc"}, curVer(map[string]string{"input": "2.5"}))
	require.Error(t, err)
	_, _, err = ComputeDiff(map[string]string{"input": "2.5"}, curVer(map[string]string{"input": "xyz"}))
	require.Error(t, err)
}

// ---- 2. 录入校验（normalizeItem 经 CreateStaging 全链路） ----

func stagingInput(items ...StagingItemInput) CreateStagingInput {
	return CreateStagingInput{SyncJobID: 3, Items: items}
}

func op7a() Operator { return Operator{OperatorID: 1, OperatorRole: "MODEL_OPS"} }

func TestCreateStaging_SKUIDNotFound(t *testing.T) {
	f := newFakeSyncStore()
	f.jobExists[3] = true
	svc := NewSyncService(f, fixedNow7a)
	_, err := svc.CreateStaging(context.Background(), stagingInput(StagingItemInput{
		SKUID: int64Ptr7a(999), Currency: "USD", Payload: map[string]any{"input": "1.00"},
	}), op7a(), "req-1")
	require.ErrorIs(t, err, ErrStagingSKUNotFound)
	require.Empty(t, f.stagingRows, "校验失败绝不落库")
}

func TestCreateStaging_RawCodeMatched(t *testing.T) {
	f := newFakeSyncStore()
	f.jobExists[3] = true
	f.skuByCode["gpt-5-2026-04-11"] = 40
	svc := NewSyncService(f, fixedNow7a)
	code := "gpt-5-2026-04-11"
	res, err := svc.CreateStaging(context.Background(), stagingInput(StagingItemInput{
		RawSkuCode: &code, Currency: "USD", Payload: map[string]any{"input": "2.50"},
	}), op7a(), "req-1")
	require.NoError(t, err)
	require.Equal(t, 1, res.CreatedCount)
	require.Len(t, f.stagingRows, 1)
	require.NotNil(t, f.stagingRows[0].SKUID)
	require.Equal(t, int64(40), *f.stagingRows[0].SKUID) // 匹配到 → 回填 sku_id
	require.NotNil(t, f.stagingRows[0].RawSkuCode)
	require.Equal(t, code, *f.stagingRows[0].RawSkuCode) // 原始码保留
	require.Equal(t, MatchMatched, f.stagingRows[0].MatchStatus)
}

func TestCreateStaging_RawCodeUnmatched(t *testing.T) {
	f := newFakeSyncStore()
	f.jobExists[3] = true
	svc := NewSyncService(f, fixedNow7a)
	code := "unknown-sku"
	_, err := svc.CreateStaging(context.Background(), stagingInput(StagingItemInput{
		RawSkuCode: &code, Currency: "USD", Payload: map[string]any{"input": "1.00"},
	}), op7a(), "req-1")
	require.NoError(t, err) // UNMATCHED 是合法录入（只标记，不拒绝——裁决 6）
	require.Len(t, f.stagingRows, 1)
	require.Nil(t, f.stagingRows[0].SKUID, "匹配不到绝不硬填 sku_id（变异验证 3）")
	require.Equal(t, MatchUnmatched, f.stagingRows[0].MatchStatus)
}

func TestCreateStaging_SKUIDWinsOverRawCode(t *testing.T) {
	f := newFakeSyncStore()
	f.jobExists[3] = true
	f.skuExists[40] = true
	f.skuByCode["other-code"] = 41
	svc := NewSyncService(f, fixedNow7a)
	code := "other-code"
	_, err := svc.CreateStaging(context.Background(), stagingInput(StagingItemInput{
		SKUID: int64Ptr7a(40), RawSkuCode: &code, Currency: "USD", Payload: map[string]any{"input": "1.00"},
	}), op7a(), "req-1")
	require.NoError(t, err)
	require.Equal(t, int64(40), *f.stagingRows[0].SKUID) // sku_id 优先
	require.Nil(t, f.stagingRows[0].RawSkuCode)          // raw_sku_code 被忽略
}

func TestCreateStaging_EmptyPayloadRejected(t *testing.T) {
	f := newFakeSyncStore()
	f.jobExists[3] = true
	f.skuExists[40] = true
	svc := NewSyncService(f, fixedNow7a)
	_, err := svc.CreateStaging(context.Background(), stagingInput(StagingItemInput{
		SKUID: int64Ptr7a(40), Currency: "USD", Payload: map[string]any{},
	}), op7a(), "req-1")
	require.ErrorIs(t, err, ErrStagingPayloadEmpty)
}

func TestCreateStaging_InvalidDecimalRejected(t *testing.T) {
	f := newFakeSyncStore()
	f.jobExists[3] = true
	f.skuExists[40] = true
	svc := NewSyncService(f, fixedNow7a)
	// 注："1e5" 不在非法名单——decimal.NewFromString 接受科学计数法，
	// 与供应商报价提交（quote.go 步骤 7）同一解析口径，不另立 stricter 规则。
	for _, bad := range []any{"abc", "", "-1.0", 2.5, nil} {
		_, err := svc.CreateStaging(context.Background(), stagingInput(StagingItemInput{
			SKUID: int64Ptr7a(40), Currency: "USD", Payload: map[string]any{"input": bad},
		}), op7a(), "req-1")
		require.Error(t, err, "payload 值 %v 必须被拒", bad)
		if bad != nil { // 非字符串/非法串都归 ErrStagingPriceInvalid
			require.True(t, errors.Is(err, ErrStagingPriceInvalid), "值 %v 应报 ErrStagingPriceInvalid，实得 %v", bad, err)
		}
	}
	require.Empty(t, f.stagingRows)
}

func TestCreateStaging_UnknownComponentRejected(t *testing.T) {
	f := newFakeSyncStore()
	f.jobExists[3] = true
	f.skuExists[40] = true
	svc := NewSyncService(f, fixedNow7a)
	_, err := svc.CreateStaging(context.Background(), stagingInput(StagingItemInput{
		SKUID: int64Ptr7a(40), Currency: "USD", Payload: map[string]any{"foo_bar": "1.00"},
	}), op7a(), "req-1")
	require.ErrorIs(t, err, ErrStagingComponentInvalid)
}

func TestCreateStaging_CurrencyNormalized(t *testing.T) {
	f := newFakeSyncStore()
	f.jobExists[3] = true
	f.skuExists[40] = true
	svc := NewSyncService(f, fixedNow7a)
	_, err := svc.CreateStaging(context.Background(), stagingInput(StagingItemInput{
		SKUID: int64Ptr7a(40), Currency: " usd ", Payload: map[string]any{"input": " 2.50 "},
	}), op7a(), "req-1")
	require.NoError(t, err)
	require.Equal(t, "USD", f.stagingRows[0].Currency)
	require.Equal(t, "2.50", f.stagingRows[0].Payload["input"]) // trim 规整
}

func TestCreateStaging_InvalidCurrencyRejected(t *testing.T) {
	f := newFakeSyncStore()
	f.jobExists[3] = true
	f.skuExists[40] = true
	svc := NewSyncService(f, fixedNow7a)
	for _, cur := range []string{"", "US", "USDD"} {
		_, err := svc.CreateStaging(context.Background(), stagingInput(StagingItemInput{
			SKUID: int64Ptr7a(40), Currency: cur, Payload: map[string]any{"input": "1.00"},
		}), op7a(), "req-1")
		require.ErrorIs(t, err, ErrStagingCurrencyInvalid)
	}
}

func TestCreateStaging_JobNotFound(t *testing.T) {
	f := newFakeSyncStore()
	svc := NewSyncService(f, fixedNow7a)
	_, err := svc.CreateStaging(context.Background(), stagingInput(StagingItemInput{
		SKUID: int64Ptr7a(40), Currency: "USD", Payload: map[string]any{"input": "1.00"},
	}), op7a(), "req-1")
	require.ErrorIs(t, err, ErrSyncJobNotFound)
}

func TestCreateStaging_EmptyItemsRejected(t *testing.T) {
	f := newFakeSyncStore()
	f.jobExists[3] = true
	svc := NewSyncService(f, fixedNow7a)
	_, err := svc.CreateStaging(context.Background(), CreateStagingInput{SyncJobID: 3, Items: nil}, op7a(), "req-1")
	require.ErrorIs(t, err, ErrStagingItemsEmpty)
}

func TestCreateStaging_NoSKUAtAllRejected(t *testing.T) {
	f := newFakeSyncStore()
	f.jobExists[3] = true
	svc := NewSyncService(f, fixedNow7a)
	_, err := svc.CreateStaging(context.Background(), stagingInput(StagingItemInput{
		Currency: "USD", Payload: map[string]any{"input": "1.00"},
	}), op7a(), "req-1")
	require.ErrorIs(t, err, ErrStagingItemNoSKU)
}

// ---- 3. sync_job ----

func TestCreateJob_SuccessOnCreate(t *testing.T) {
	f := newFakeSyncStore()
	svc := NewSyncService(f, fixedNow7a)
	job, err := svc.CreateJob(context.Background(), CreateJobInput{
		JobType: JobTypeSyncPrices, Source: "OFFICIAL_SITE", SkuIDs: []int64{40, 41, 42},
	}, op7a(), "req-1")
	require.NoError(t, err)
	require.Equal(t, JobStatusSuccess, job.Status) // 创建即完成（裁决 2，DDL 枚举 SUCCESS）
	require.NotNil(t, job.FinishedAt)
	require.Equal(t, job.StartedAt, *job.FinishedAt) // started_at = finished_at
	require.Nil(t, job.ErrorMsg)
	require.NotNil(t, f.createdJob)
	require.Equal(t, 3, f.createdJob.ItemCount)
}

func TestCreateJob_Validation(t *testing.T) {
	f := newFakeSyncStore()
	svc := NewSyncService(f, fixedNow7a)
	_, err := svc.CreateJob(context.Background(), CreateJobInput{JobType: "DONE", Source: "X"}, op7a(), "r")
	require.ErrorIs(t, err, ErrJobTypeInvalid) // "DONE" 不是 job_type 枚举（红线 4）
	_, err = svc.CreateJob(context.Background(), CreateJobInput{JobType: JobTypeSyncPrices, Source: "  "}, op7a(), "r")
	require.ErrorIs(t, err, ErrJobSourceEmpty)
	_, err = svc.CreateJob(context.Background(), CreateJobInput{JobType: JobTypeSyncPrices, Source: "S", SkuIDs: []int64{0}}, op7a(), "r")
	require.ErrorIs(t, err, ErrJobSKUInvalid)
	require.Nil(t, f.createdJob, "校验失败绝不落库")
}

func TestListJobs_Paging(t *testing.T) {
	f := newFakeSyncStore()
	for i := 0; i < 5; i++ {
		f.jobs = append(f.jobs, SyncJob{ID: int64(i + 1), Status: JobStatusSuccess})
	}
	svc := NewSyncService(f, fixedNow7a)
	res, err := svc.ListJobs(context.Background(), 2, 2)
	require.NoError(t, err)
	require.Equal(t, int64(5), res.Total)
	require.Len(t, res.List, 2)
	require.Equal(t, int64(3), res.List[0].ID)
	require.Equal(t, 2, res.Page)
	require.Equal(t, 2, res.Size)
}

// ---- 4. ListStaging 集成（fake store 上的 diff 装配） ----

func TestListStaging_DiffAssembly(t *testing.T) {
	f := newFakeSyncStore()
	f.versions = []CurrentPriceVersion{
		{SKUID: 40, Currency: "USD", VersionNo: 1, Components: map[string]string{"input": "2.50000000"}},
	}
	now := fixedNow7a()
	f.staging = []StagingRow{
		{ID: 1, SyncJobID: 3, SKUID: int64Ptr7a(40), Currency: "USD",
			Payload: map[string]string{"input": "2.50"}, MatchStatus: MatchMatched, CreatedAt: now},
		{ID: 2, SyncJobID: 3, SKUID: int64Ptr7a(40), Currency: "USD",
			Payload: map[string]string{"input": "2.80"}, MatchStatus: MatchMatched, CreatedAt: now},
		{ID: 3, SyncJobID: 3, SKUID: int64Ptr7a(43), Currency: "CNY",
			Payload: map[string]string{"input": "1.00"}, MatchStatus: MatchMatched, CreatedAt: now},
		{ID: 4, SyncJobID: 3, RawSkuCode: strPtr("unknown-sku"), Currency: "USD",
			Payload: map[string]string{"input": "1.00"}, MatchStatus: MatchUnmatched, CreatedAt: now},
	}
	svc := NewSyncService(f, fixedNow7a)
	res, err := svc.ListStaging(context.Background(), StagingQuery{Page: 1, Size: 20})
	require.NoError(t, err)
	require.Equal(t, int64(4), res.Total)
	require.Len(t, res.List, 4)

	// E2E 四连（与提示词验收场景同构）
	require.Equal(t, DiffUnchanged, res.List[0].DiffStatus)
	require.Empty(t, res.List[0].DiffDetail)

	require.Equal(t, DiffChanged, res.List[1].DiffStatus)
	require.Len(t, res.List[1].DiffDetail, 1)
	require.Equal(t, "0.120000", *res.List[1].DiffDetail[0].DeltaPct)

	require.Equal(t, DiffNew, res.List[2].DiffStatus) // sku43 无当前版本
	require.Len(t, res.List[2].DiffDetail, 1)
	require.Nil(t, res.List[2].DiffDetail[0].OldPrice)

	require.Equal(t, DiffUnmatched, res.List[3].DiffStatus)
	require.Nil(t, res.List[3].SKUID)
	require.Empty(t, res.List[3].DiffDetail)
	require.NotNil(t, res.List[3].DiffDetail, "diff_detail 必须是 [] 不是 null")
}

func TestListStaging_CurrencyMismatchIsNew(t *testing.T) {
	// 当前版本是 USD，录入 CNY → 不算「有当前官方价」→ NEW（裁决 4：同币种才算）
	f := newFakeSyncStore()
	f.versions = []CurrentPriceVersion{
		{SKUID: 40, Currency: "USD", VersionNo: 1, Components: map[string]string{"input": "2.50000000"}},
	}
	f.staging = []StagingRow{
		{ID: 1, SyncJobID: 3, SKUID: int64Ptr7a(40), Currency: "CNY",
			Payload: map[string]string{"input": "18.00"}, MatchStatus: MatchMatched, CreatedAt: fixedNow7a()},
	}
	svc := NewSyncService(f, fixedNow7a)
	res, err := svc.ListStaging(context.Background(), StagingQuery{Page: 1, Size: 20})
	require.NoError(t, err)
	require.Equal(t, DiffNew, res.List[0].DiffStatus)
}

func TestNewSyncService_NilStoreDefended(t *testing.T) {
	var svc *SyncService
	_, err := svc.CreateJob(context.Background(), CreateJobInput{}, op7a(), "r")
	require.ErrorIs(t, err, ErrSyncStoreNil)
	svc2 := NewSyncService(nil, nil)
	_, err = svc2.ListJobs(context.Background(), 1, 20)
	require.ErrorIs(t, err, ErrSyncStoreNil)
}
