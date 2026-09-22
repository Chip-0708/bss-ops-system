package price

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// ---- fake ConfirmStore ----

type fakeConfirmStore struct {
	stagings   map[int64]ConfirmStagingRow
	current    map[int64]map[string]decimal.Decimal
	params     []CostParamRow
	margin     decimal.Decimal
	marginErr  error
	confirmed  *ConfirmStagingParams
	confirmErr error
}

func (f *fakeConfirmStore) LoadStagings(ctx context.Context, ids []int64) ([]ConfirmStagingRow, error) {
	out := make([]ConfirmStagingRow, 0, len(ids))
	for _, id := range ids {
		if r, ok := f.stagings[id]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeConfirmStore) LoadCurrentPriceMap(ctx context.Context, skuIDs []int64) (map[int64]map[string]decimal.Decimal, error) {
	out := make(map[int64]map[string]decimal.Decimal, len(skuIDs))
	for _, id := range skuIDs {
		if m, ok := f.current[id]; ok {
			out[id] = m
		}
	}
	return out, nil
}

func (f *fakeConfirmStore) LoadCostParams(ctx context.Context, skuIDs []int64) ([]CostParamRow, error) {
	return f.params, nil
}

func (f *fakeConfirmStore) LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error) {
	if f.marginErr != nil {
		return decimal.Zero, f.marginErr
	}
	return f.margin, nil
}

func (f *fakeConfirmStore) ConfirmStaging(ctx context.Context, p ConfirmStagingParams) (int64, error) {
	if f.confirmErr != nil {
		return 0, f.confirmErr
	}
	f.confirmed = &p
	return 9001, nil
}

// ---- 合并与方向纯函数 ----

func TestMergeStagingsBySKU_MaxIDWins(t *testing.T) {
	stagings := []ConfirmStagingRow{
		{ID: 1, SKUID: int64Ptr(40), Payload: map[string]string{"input": "2.50"}},
		{ID: 3, SKUID: int64Ptr(40), Payload: map[string]string{"input": "2.80"}},
		{ID: 2, SKUID: int64Ptr(40), Payload: map[string]string{"input": "2.60"}},
		{ID: 4, SKUID: int64Ptr(41), Payload: map[string]string{"input": "15.00"}},
	}
	merged := mergeStagingsBySKU(stagings)
	require.Len(t, merged, 2)
	require.Equal(t, "2.80000000", merged[40]["input"].StringFixed(8)) // max id=3 胜出
	require.Equal(t, "15.00000000", merged[41]["input"].StringFixed(8))
}

func TestDetectDirection_UpOnAnyComponent(t *testing.T) {
	bySKU := map[int64]map[string]decimal.Decimal{
		40: {"input": decimal.NewFromFloat(2.8)},
	}
	current := map[int64]map[string]decimal.Decimal{
		40: {"input": decimal.NewFromFloat(2.5)},
	}
	require.Equal(t, ChangePriceUp, detectDirection(bySKU, current))
}

func TestDetectDirection_DownWhenAllLowerOrEqual(t *testing.T) {
	bySKU := map[int64]map[string]decimal.Decimal{
		40: {"input": decimal.NewFromFloat(2.3)},
	}
	current := map[int64]map[string]decimal.Decimal{
		40: {"input": decimal.NewFromFloat(2.5)},
	}
	require.Equal(t, ChangePriceDown, detectDirection(bySKU, current))
}

func TestDetectDirection_NewComponentPositiveIsUp(t *testing.T) {
	bySKU := map[int64]map[string]decimal.Decimal{
		40: {"output": decimal.NewFromFloat(10)},
	}
	current := map[int64]map[string]decimal.Decimal{40: {}}
	require.Equal(t, ChangePriceUp, detectDirection(bySKU, current))
}

func TestDetectDirection_MultiSKUAnyUpIsUp(t *testing.T) {
	bySKU := map[int64]map[string]decimal.Decimal{
		40: {"input": decimal.NewFromFloat(2.3)}, // 降
		41: {"input": decimal.NewFromFloat(16)},  // 升
	}
	current := map[int64]map[string]decimal.Decimal{
		40: {"input": decimal.NewFromFloat(2.5)},
		41: {"input": decimal.NewFromFloat(15)},
	}
	require.Equal(t, ChangePriceUp, detectDirection(bySKU, current))
}

// ---- Confirm 校验链 ----

func TestConfirm_EmptyStagingIDs(t *testing.T) {
	svc := NewConfirmService(&fakeConfirmStore{}, nil)
	_, err := svc.Confirm(context.Background(), ConfirmInput{SyncJobID: 1, EffectiveTime: time.Now().UTC().Format(time.RFC3339)}, 1, "MODEL_OPS", "req")
	require.ErrorIs(t, err, ErrConfirmStagingsEmpty)
}

func TestConfirm_FutureEffectiveTime(t *testing.T) {
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	svc := NewConfirmService(&fakeConfirmStore{
		stagings: map[int64]ConfirmStagingRow{1: {ID: 1, SyncJobID: 1, SKUID: int64Ptr(40), Payload: map[string]string{"input": "2.80"}}},
	}, nil)
	_, err := svc.Confirm(context.Background(), ConfirmInput{SyncJobID: 1, StagingIDs: []int64{1}, EffectiveTime: future}, 1, "MODEL_OPS", "req")
	require.ErrorIs(t, err, ErrConfirmEffectiveFuture)
}

func TestConfirm_StagingNotFound(t *testing.T) {
	svc := NewConfirmService(&fakeConfirmStore{stagings: map[int64]ConfirmStagingRow{}}, nil)
	_, err := svc.Confirm(context.Background(), ConfirmInput{SyncJobID: 1, StagingIDs: []int64{99}, EffectiveTime: time.Now().UTC().Format(time.RFC3339)}, 1, "MODEL_OPS", "req")
	require.ErrorIs(t, err, ErrConfirmStagingNotFound)
}

func TestConfirm_StagingProcessed(t *testing.T) {
	svc := NewConfirmService(&fakeConfirmStore{
		stagings: map[int64]ConfirmStagingRow{1: {ID: 1, SyncJobID: 1, Processed: true, SKUID: int64Ptr(40), Payload: map[string]string{"input": "2.80"}}},
	}, nil)
	_, err := svc.Confirm(context.Background(), ConfirmInput{SyncJobID: 1, StagingIDs: []int64{1}, EffectiveTime: time.Now().UTC().Format(time.RFC3339)}, 1, "MODEL_OPS", "req")
	require.ErrorIs(t, err, ErrConfirmStagingProcessed)
}

func TestConfirm_StagingJobMismatch(t *testing.T) {
	svc := NewConfirmService(&fakeConfirmStore{
		stagings: map[int64]ConfirmStagingRow{1: {ID: 1, SyncJobID: 2, SKUID: int64Ptr(40), Payload: map[string]string{"input": "2.80"}}},
	}, nil)
	_, err := svc.Confirm(context.Background(), ConfirmInput{SyncJobID: 1, StagingIDs: []int64{1}, EffectiveTime: time.Now().UTC().Format(time.RFC3339)}, 1, "MODEL_OPS", "req")
	require.ErrorIs(t, err, ErrConfirmStagingJobMismatch)
}

func TestConfirm_UnmatchedSKURejected(t *testing.T) {
	svc := NewConfirmService(&fakeConfirmStore{
		stagings: map[int64]ConfirmStagingRow{1: {ID: 1, SyncJobID: 1, SKUID: nil, Payload: map[string]string{"input": "2.80"}}},
	}, nil)
	_, err := svc.Confirm(context.Background(), ConfirmInput{SyncJobID: 1, StagingIDs: []int64{1}, EffectiveTime: time.Now().UTC().Format(time.RFC3339)}, 1, "MODEL_OPS", "req")
	require.ErrorIs(t, err, ErrConfirmStagingNotFound)
}

// ---- 步数与 margin_preview ----

func TestConfirm_PriceUpTwoStepsWithMarginPreview(t *testing.T) {
	store := &fakeConfirmStore{
		stagings: map[int64]ConfirmStagingRow{
			1: {ID: 1, SyncJobID: 1, SKUID: int64Ptr(40), Payload: map[string]string{"input": "2.80"}},
		},
		current: map[int64]map[string]decimal.Decimal{
			40: {"input": decimal.NewFromFloat(2.5)},
		},
		params: []CostParamRow{
			{ScopeType: "GLOBAL", ScopeID: 0, LossRate: decimal.NewFromFloat(0.03), ChannelRate: decimal.NewFromFloat(0.01)},
		},
		margin: decimal.NewFromFloat(0.15),
	}
	svc := NewConfirmService(store, nil)
	res, err := svc.Confirm(context.Background(), ConfirmInput{SyncJobID: 1, StagingIDs: []int64{1}, EffectiveTime: time.Now().UTC().Format(time.RFC3339)}, 1, "MODEL_OPS", "req")
	require.NoError(t, err)
	require.Equal(t, int64(9001), res.ChangeRequestID)
	require.Equal(t, 2, res.StepCount)
	require.Equal(t, ChangePriceUp, store.confirmed.ChangeType)
	require.NotNil(t, store.confirmed.MarginPreview)
	// floor = 2.80 × 1.03 × 1.01 / 0.85 = 2.91284 / 0.85 ≈ 3.42687059
	require.Contains(t, string(store.confirmed.MarginPreview), "3.42687059")
}

func TestConfirm_PriceDownOneStepNoMarginPreview(t *testing.T) {
	store := &fakeConfirmStore{
		stagings: map[int64]ConfirmStagingRow{
			1: {ID: 1, SyncJobID: 1, SKUID: int64Ptr(40), Payload: map[string]string{"input": "2.30"}},
		},
		current: map[int64]map[string]decimal.Decimal{
			40: {"input": decimal.NewFromFloat(2.5)},
		},
		margin: decimal.NewFromFloat(0.15),
	}
	svc := NewConfirmService(store, nil)
	res, err := svc.Confirm(context.Background(), ConfirmInput{SyncJobID: 1, StagingIDs: []int64{1}, EffectiveTime: time.Now().UTC().Format(time.RFC3339)}, 1, "MODEL_OPS", "req")
	require.NoError(t, err)
	require.Equal(t, 1, res.StepCount)
	require.Equal(t, ChangePriceDown, store.confirmed.ChangeType)
	require.Nil(t, store.confirmed.MarginPreview)
}

func TestConfirm_MarginPreviewFloorAnchor(t *testing.T) {
	// 锚点：官方价 2.50 → 2.80，loss=0.03，channel=0.01，margin=0.15
	// floor = 2.80 × 1.03 × 1.01 / 0.85 = 2.9128×... 精确手算：
	// 2.80 × 1.03 = 2.884；2.884 × 1.01 = 2.91284；/ 0.85 = 3.426870588... 取 8 位 = 3.42687059?
	// 用 decimal 精确：2.80×1.03×1.01 = 2.912840；/0.85 = 3.426870588235... → StringFixed(8) = 3.42687059
	store := &fakeConfirmStore{
		stagings: map[int64]ConfirmStagingRow{
			1: {ID: 1, SyncJobID: 1, SKUID: int64Ptr(40), Payload: map[string]string{"input": "2.80"}},
		},
		current: map[int64]map[string]decimal.Decimal{
			40: {"input": decimal.RequireFromString("2.50")},
		},
		params: []CostParamRow{
			{ScopeType: "GLOBAL", ScopeID: 0, LossRate: decimal.RequireFromString("0.03"), ChannelRate: decimal.RequireFromString("0.01")},
		},
		margin: decimal.RequireFromString("0.15"),
	}
	svc := NewConfirmService(store, nil)
	_, err := svc.Confirm(context.Background(), ConfirmInput{SyncJobID: 1, StagingIDs: []int64{1}, EffectiveTime: time.Now().UTC().Format(time.RFC3339)}, 1, "MODEL_OPS", "req")
	require.NoError(t, err)
	require.Contains(t, string(store.confirmed.MarginPreview), "3.42687059")
}

func TestConfirm_MarginPreviewDegradeOnReadFail(t *testing.T) {
	store := &fakeConfirmStore{
		stagings: map[int64]ConfirmStagingRow{
			1: {ID: 1, SyncJobID: 1, SKUID: int64Ptr(40), Payload: map[string]string{"input": "2.80"}},
		},
		current: map[int64]map[string]decimal.Decimal{
			40: {"input": decimal.NewFromFloat(2.5)},
		},
		marginErr: errors.New("sys_config read fail"),
	}
	svc := NewConfirmService(store, nil)
	res, err := svc.Confirm(context.Background(), ConfirmInput{SyncJobID: 1, StagingIDs: []int64{1}, EffectiveTime: time.Now().UTC().Format(time.RFC3339)}, 1, "MODEL_OPS", "req")
	require.NoError(t, err)                       // 读失败降级不阻断
	require.Equal(t, 2, res.StepCount)            // 涨价仍 2 步
	require.Nil(t, store.confirmed.MarginPreview) // 但 margin_preview 为 nil
}

// ---- payload 结构 ----

func TestConfirm_PayloadContainsMergedPrices(t *testing.T) {
	store := &fakeConfirmStore{
		stagings: map[int64]ConfirmStagingRow{
			1: {ID: 1, SyncJobID: 1, SKUID: int64Ptr(40), Payload: map[string]string{"input": "2.80"}},
			2: {ID: 2, SyncJobID: 1, SKUID: int64Ptr(40), Payload: map[string]string{"input": "2.90"}}, // max id 胜出
		},
		current: map[int64]map[string]decimal.Decimal{
			40: {"input": decimal.NewFromFloat(2.5)},
		},
		margin: decimal.NewFromFloat(0.15),
	}
	svc := NewConfirmService(store, nil)
	_, err := svc.Confirm(context.Background(), ConfirmInput{SyncJobID: 1, StagingIDs: []int64{1, 2}, EffectiveTime: time.Now().UTC().Format(time.RFC3339)}, 1, "MODEL_OPS", "req")
	require.NoError(t, err)
	payload := string(store.confirmed.Payload)
	require.Contains(t, payload, `"2.90000000"`) // max id=2 的价
	require.Contains(t, payload, `"direction":"PRICE_UP"`)
	require.Contains(t, payload, `"sku_ids":[40]`)
}

func TestConfirm_StoreErrorPropagates(t *testing.T) {
	store := &fakeConfirmStore{
		stagings: map[int64]ConfirmStagingRow{
			1: {ID: 1, SyncJobID: 1, SKUID: int64Ptr(40), Payload: map[string]string{"input": "2.80"}},
		},
		current:    map[int64]map[string]decimal.Decimal{40: {"input": decimal.NewFromFloat(2.5)}},
		margin:     decimal.NewFromFloat(0.15),
		confirmErr: fmt.Errorf("db down"),
	}
	svc := NewConfirmService(store, nil)
	_, err := svc.Confirm(context.Background(), ConfirmInput{SyncJobID: 1, StagingIDs: []int64{1}, EffectiveTime: time.Now().UTC().Format(time.RFC3339)}, 1, "MODEL_OPS", "req")
	require.ErrorContains(t, err, "db down")
}

func int64Ptr(v int64) *int64 { return &v }
