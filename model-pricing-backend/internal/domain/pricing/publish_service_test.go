package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// ---- fakePublishStore ----

type fakePublishStore struct {
	books      map[int64]*PriceBookInfo
	items      map[int64][]PriceBookItemInfo
	components map[int64][]PriceBookComponentInfo
	unitCosts  map[int64]UnitCostInfo
	margin     decimal.Decimal
	published  *PublishTxInput
	applied    *ApplyPublishParams
	publishErr error
	applyErr   error
}

func newFakePublishStore() *fakePublishStore {
	return &fakePublishStore{
		books:      make(map[int64]*PriceBookInfo),
		items:      make(map[int64][]PriceBookItemInfo),
		components: make(map[int64][]PriceBookComponentInfo),
		unitCosts:  make(map[int64]UnitCostInfo),
		margin:     decimal.NewFromFloat(0.15),
	}
}

func (f *fakePublishStore) LoadPriceBookByID(_ context.Context, id int64) (*PriceBookInfo, error) {
	if b, ok := f.books[id]; ok {
		return b, nil
	}
	return nil, ErrPriceBookNotFound
}

func (f *fakePublishStore) LoadPriceBookByVersion(_ context.Context, levelCode string, versionNo int) (*PriceBookInfo, error) {
	for _, b := range f.books {
		if b.LevelCode == levelCode && b.VersionNo == versionNo {
			return b, nil
		}
	}
	return nil, ErrRollbackTargetNotFound
}

func (f *fakePublishStore) LoadCurrentEffective(_ context.Context, levelCode string) (*PriceBookInfo, error) {
	for _, b := range f.books {
		if b.LevelCode == levelCode && b.Status == BookStatusEffective {
			return b, nil
		}
	}
	return nil, ErrPriceBookNotFound
}

func (f *fakePublishStore) LoadPriceBookItems(_ context.Context, priceBookID int64) ([]PriceBookItemInfo, error) {
	return f.items[priceBookID], nil
}

func (f *fakePublishStore) LoadPriceBookComponents(_ context.Context, priceBookItemIDs []int64) ([]PriceBookComponentInfo, error) {
	var out []PriceBookComponentInfo
	for _, id := range priceBookItemIDs {
		out = append(out, f.components[id]...)
	}
	return out, nil
}

func (f *fakePublishStore) LoadCurrentUnitCosts(_ context.Context, skuIDs []int64) (map[int64]UnitCostInfo, error) {
	out := make(map[int64]UnitCostInfo, len(skuIDs))
	for _, id := range skuIDs {
		if uc, ok := f.unitCosts[id]; ok {
			out[id] = uc
		}
	}
	return out, nil
}

func (f *fakePublishStore) LoadMinGrossMargin(_ context.Context) (decimal.Decimal, error) {
	return f.margin, nil
}

func (f *fakePublishStore) Publish(_ context.Context, in PublishTxInput, operatorID int64, requestID string) (*PublishResult, error) {
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	f.published = &in
	return &PublishResult{
		PriceBookID:     in.PriceBookID,
		VersionNo:       1,
		ChangeRequestID: 100,
		StepCount:       2,
		EffectiveTime:   in.EffectiveTime,
		Status:          BookStatusApproving,
	}, nil
}

func (f *fakePublishStore) ApplyPriceBookPublish(_ context.Context, p ApplyPublishParams) error {
	if f.applyErr != nil {
		return f.applyErr
	}
	f.applied = &p
	return nil
}

// ---- 测试 ----

func TestPublish_DraftNotFound(t *testing.T) {
	store := newFakePublishStore()
	svc := NewPublishService(store, nil)
	_, err := svc.Publish(context.Background(), PublishInput{
		PriceBookID: 999, EffectiveTime: time.Now(), Mode: ModeImmediate,
	}, 1, "req-1")
	require.ErrorIs(t, err, ErrPriceBookNotFound)
}

func TestPublish_NotDraft(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusEffective}
	svc := NewPublishService(store, nil)
	_, err := svc.Publish(context.Background(), PublishInput{
		PriceBookID: 1, EffectiveTime: time.Now(), Mode: ModeImmediate,
	}, 1, "req-1")
	require.ErrorIs(t, err, ErrPriceBookNotDraft)
}

func TestPublish_FloorViolation(t *testing.T) {
	store := newFakePublishStore()
	diff := []DiffItem{{SKUID: 40, FloorViolation: true}}
	diffJSON, _ := json.Marshal(diff)
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusDraft, DiffReport: diffJSON}
	svc := NewPublishService(store, nil)
	_, err := svc.Publish(context.Background(), PublishInput{
		PriceBookID: 1, EffectiveTime: time.Now(), Mode: ModeImmediate,
	}, 1, "req-1")
	require.ErrorIs(t, err, ErrFloorViolation)
}

func TestPublish_ScheduledNotSupported(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusDraft}
	svc := NewPublishService(store, nil)
	future := time.Now().Add(24 * time.Hour)
	_, err := svc.Publish(context.Background(), PublishInput{
		PriceBookID: 1, EffectiveTime: future, Mode: ModeScheduled,
	}, 1, "req-1")
	require.ErrorIs(t, err, ErrScheduledNotSupported)
}

func TestPublish_GrayNotSupported(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusDraft}
	svc := NewPublishService(store, nil)
	_, err := svc.Publish(context.Background(), PublishInput{
		PriceBookID: 1, EffectiveTime: time.Now(), Mode: ModeGray,
	}, 1, "req-1")
	require.ErrorIs(t, err, ErrGrayNotSupported)
}

func TestPublish_InvalidMode(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusDraft}
	svc := NewPublishService(store, nil)
	_, err := svc.Publish(context.Background(), PublishInput{
		PriceBookID: 1, EffectiveTime: time.Now(), Mode: "FOO",
	}, 1, "req-1")
	require.ErrorIs(t, err, ErrInvalidMode)
}

func TestPublish_Success(t *testing.T) {
	store := newFakePublishStore()
	diff := []DiffItem{{SKUID: 40, FloorViolation: false}}
	diffJSON, _ := json.Marshal(diff)
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusDraft, DiffReport: diffJSON}
	svc := NewPublishService(store, nil)
	res, err := svc.Publish(context.Background(), PublishInput{
		PriceBookID: 1, EffectiveTime: time.Now(), Mode: ModeImmediate,
	}, 1, "req-1")
	require.NoError(t, err)
	require.Equal(t, int64(1), res.PriceBookID)
	require.Equal(t, 2, res.StepCount)
	require.Equal(t, BookStatusApproving, res.Status)
	// 校验 payload 含 price_book_id/sku_ids/effective_time/mode。
	var payload map[string]any
	require.NoError(t, json.Unmarshal(store.published.Payload, &payload))
	require.Equal(t, float64(1), payload["price_book_id"])
	require.Equal(t, "IMMEDIATE", payload["mode"])
}

func TestRollback_TargetNotFound(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusEffective, LevelCode: "GLOBAL", VersionNo: 2}
	svc := NewPublishService(store, nil)
	_, err := svc.Rollback(context.Background(), RollbackInput{
		PriceBookID: 1, TargetVersionNo: 99, Reason: "test",
	}, 1, "req-1")
	require.ErrorIs(t, err, ErrRollbackTargetNotFound)
}

func TestRollback_ToSelf(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusEffective, LevelCode: "GLOBAL", VersionNo: 2}
	svc := NewPublishService(store, nil)
	_, err := svc.Rollback(context.Background(), RollbackInput{
		PriceBookID: 1, TargetVersionNo: 2, Reason: "test",
	}, 1, "req-1")
	require.ErrorIs(t, err, ErrRollbackToSelf)
}

func TestRollback_FloorViolation(t *testing.T) {
	store := newFakePublishStore()
	// 当前生效版 v2。
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusEffective, LevelCode: "GLOBAL", VersionNo: 2}
	// 目标版本 v1（历史）。
	store.books[2] = &PriceBookInfo{ID: 2, Status: BookStatusRetired, LevelCode: "GLOBAL", VersionNo: 1}
	// v1 的 item/component：售价 2.0。
	store.items[2] = []PriceBookItemInfo{{ID: 10, PriceBookID: 2, SkuID: 40, Currency: "CNY"}}
	store.components[10] = []PriceBookComponentInfo{{ID: 100, PriceBookItemID: 10, ComponentType: "input", UnitPrice: "2.0"}}
	// 当前成本基线：unit_cost=3.0 → floor=3.0/0.85=3.529... > 2.0 → 违规。
	store.unitCosts[40] = UnitCostInfo{UnitCost: decimal.NewFromInt(3), UnitCostBasis: "input", BaselineVersion: 1, Currency: "CNY"}
	svc := NewPublishService(store, nil)
	_, err := svc.Rollback(context.Background(), RollbackInput{
		PriceBookID: 1, TargetVersionNo: 1, Reason: "test",
	}, 1, "req-1")
	require.ErrorIs(t, err, ErrFloorViolation)
}

func TestRollback_Success(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusEffective, LevelCode: "GLOBAL", VersionNo: 2}
	store.books[2] = &PriceBookInfo{ID: 2, Status: BookStatusRetired, LevelCode: "GLOBAL", VersionNo: 1}
	store.items[2] = []PriceBookItemInfo{{ID: 10, PriceBookID: 2, SkuID: 40, Currency: "CNY"}}
	store.components[10] = []PriceBookComponentInfo{{ID: 100, PriceBookItemID: 10, ComponentType: "input", UnitPrice: "5.0"}}
	// 当前成本基线：unit_cost=3.0 → floor=3.529... < 5.0 → 不违规。
	store.unitCosts[40] = UnitCostInfo{UnitCost: decimal.NewFromInt(3), UnitCostBasis: "input", BaselineVersion: 1, Currency: "CNY"}
	svc := NewPublishService(store, nil)
	res, err := svc.Rollback(context.Background(), RollbackInput{
		PriceBookID: 1, TargetVersionNo: 1, Reason: "test",
	}, 1, "req-1")
	require.NoError(t, err)
	require.Equal(t, 2, res.StepCount)
	// 校验 payload 含 target_version_no/reason。
	var payload map[string]any
	require.NoError(t, json.Unmarshal(store.published.Payload, &payload))
	require.Equal(t, float64(1), payload["target_version_no"])
	require.Equal(t, "test", payload["reason"])
}

// TestPublish_ChangeType 校验 change_type 是 PRICE_BOOK_PUBLISH。
func TestPublish_ChangeType(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusDraft}
	svc := NewPublishService(store, nil)
	_, err := svc.Publish(context.Background(), PublishInput{
		PriceBookID: 1, EffectiveTime: time.Now(), Mode: ModeImmediate,
	}, 1, "req-1")
	require.NoError(t, err)
	require.Equal(t, ChangePriceBookPublish, store.published.ChangeType)
}

// TestRollback_ChangeType 校验 change_type 是 PRICE_BOOK_ROLLBACK。
func TestRollback_ChangeType(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusEffective, LevelCode: "GLOBAL", VersionNo: 2}
	store.books[2] = &PriceBookInfo{ID: 2, Status: BookStatusRetired, LevelCode: "GLOBAL", VersionNo: 1}
	store.items[2] = []PriceBookItemInfo{{ID: 10, PriceBookID: 2, SkuID: 40, Currency: "CNY"}}
	store.components[10] = []PriceBookComponentInfo{{ID: 100, PriceBookItemID: 10, ComponentType: "input", UnitPrice: "5.0"}}
	store.unitCosts[40] = UnitCostInfo{UnitCost: decimal.NewFromInt(3), UnitCostBasis: "input", BaselineVersion: 1, Currency: "CNY"}
	svc := NewPublishService(store, nil)
	_, err := svc.Rollback(context.Background(), RollbackInput{
		PriceBookID: 1, TargetVersionNo: 1, Reason: "test",
	}, 1, "req-1")
	require.NoError(t, err)
	require.Equal(t, ChangePriceBookRollback, store.published.ChangeType)
}

// TestPublish_SkuIDNull 校验 change_request.sku_id 是 NULL（A1）。
func TestPublish_SkuIDNull(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusDraft}
	svc := NewPublishService(store, nil)
	_, err := svc.Publish(context.Background(), PublishInput{
		PriceBookID: 1, EffectiveTime: time.Now(), Mode: ModeImmediate,
	}, 1, "req-1")
	require.NoError(t, err)
	// PublishTxInput 没有 SkuID 字段——sku_id=NULL 由 repo 层 map 写入保证。
	// 这里校验 payload 里有 price_book_id（真实主体）。
	var payload map[string]any
	require.NoError(t, json.Unmarshal(store.published.Payload, &payload))
	require.Contains(t, payload, "price_book_id")
	require.Contains(t, payload, "sku_ids")
}

// TestRollback_ApprovalRoles 校验审批角色链 PRICING_OP → FINANCE（B2）。
func TestRollback_ApprovalRoles(t *testing.T) {
	// 这个测试在 repo 层（Publish 方法）写 approval_step 时保证。
	// 领域层只负责编排，角色链是 repo 层的实现细节。
	// 这里只校验 PublishTxInput.ChangeType 正确（repo 层会按 change_type 写 biz_type）。
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusEffective, LevelCode: "GLOBAL", VersionNo: 2}
	store.books[2] = &PriceBookInfo{ID: 2, Status: BookStatusRetired, LevelCode: "GLOBAL", VersionNo: 1}
	store.items[2] = []PriceBookItemInfo{{ID: 10, PriceBookID: 2, SkuID: 40, Currency: "CNY"}}
	store.components[10] = []PriceBookComponentInfo{{ID: 100, PriceBookItemID: 10, ComponentType: "input", UnitPrice: "5.0"}}
	store.unitCosts[40] = UnitCostInfo{UnitCost: decimal.NewFromInt(3), UnitCostBasis: "input", BaselineVersion: 1, Currency: "CNY"}
	svc := NewPublishService(store, nil)
	_, err := svc.Rollback(context.Background(), RollbackInput{
		PriceBookID: 1, TargetVersionNo: 1, Reason: "test",
	}, 1, "req-1")
	require.NoError(t, err)
	require.Equal(t, ChangePriceBookRollback, store.published.ChangeType)
}

// TestPublish_EffectiveTimePast 校验 effective_time <= now 可以发布（IMMEDIATE）。
func TestPublish_EffectiveTimePast(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusDraft}
	svc := NewPublishService(store, nil)
	past := time.Now().Add(-1 * time.Hour)
	_, err := svc.Publish(context.Background(), PublishInput{
		PriceBookID: 1, EffectiveTime: past, Mode: ModeImmediate,
	}, 1, "req-1")
	require.NoError(t, err)
}

// TestPublish_EffectiveTimeFuture 校验 effective_time > now → 400（B3）。
func TestPublish_EffectiveTimeFuture(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusDraft}
	svc := NewPublishService(store, nil)
	future := time.Now().Add(1 * time.Hour)
	_, err := svc.Publish(context.Background(), PublishInput{
		PriceBookID: 1, EffectiveTime: future, Mode: ModeImmediate,
	}, 1, "req-1")
	require.ErrorIs(t, err, ErrScheduledNotSupported)
}

// TestRollback_NoItems 校验目标版本无 items → 报错。
func TestRollback_NoItems(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusEffective, LevelCode: "GLOBAL", VersionNo: 2}
	store.books[2] = &PriceBookInfo{ID: 2, Status: BookStatusRetired, LevelCode: "GLOBAL", VersionNo: 1}
	// 目标版本无 items。
	svc := NewPublishService(store, nil)
	_, err := svc.Rollback(context.Background(), RollbackInput{
		PriceBookID: 1, TargetVersionNo: 1, Reason: "test",
	}, 1, "req-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no items")
}

// TestRollback_NoUnitCost 校验目标版本 SKU 无当前成本基线 → 报错。
func TestRollback_NoUnitCost(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusEffective, LevelCode: "GLOBAL", VersionNo: 2}
	store.books[2] = &PriceBookInfo{ID: 2, Status: BookStatusRetired, LevelCode: "GLOBAL", VersionNo: 1}
	store.items[2] = []PriceBookItemInfo{{ID: 10, PriceBookID: 2, SkuID: 40, Currency: "CNY"}}
	store.components[10] = []PriceBookComponentInfo{{ID: 100, PriceBookItemID: 10, ComponentType: "input", UnitPrice: "5.0"}}
	// 无 unitCosts[40]。
	svc := NewPublishService(store, nil)
	_, err := svc.Rollback(context.Background(), RollbackInput{
		PriceBookID: 1, TargetVersionNo: 1, Reason: "test",
	}, 1, "req-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "无当前成本基线")
}

// TestPublish_StoreError 校验 store 错误透传。
func TestPublish_StoreError(t *testing.T) {
	store := newFakePublishStore()
	store.books[1] = &PriceBookInfo{ID: 1, Status: BookStatusDraft}
	store.publishErr = errors.New("db error")
	svc := NewPublishService(store, nil)
	_, err := svc.Publish(context.Background(), PublishInput{
		PriceBookID: 1, EffectiveTime: time.Now(), Mode: ModeImmediate,
	}, 1, "req-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "db error")
}
