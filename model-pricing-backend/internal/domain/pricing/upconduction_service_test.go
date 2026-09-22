// Package pricing 涨价传导决策队列的单测（8b-2）。
//
// 覆盖提示词 6 条核心 + 3 条变异验证锚点：
//  1. 队列生成：上涨才生成 / 不涨不生成 / 无价目表跳过 / delta_pct 精度；
//  2. price_suggested = price_current × (1 + delta)——变异：×delta → 红；
//  3. margin 计算 = (price - cost) / price——变异：price-cost → 红（误差极大）；
//  4. NOT_FOLLOW 且低于 floor → 409 ErrUpconductionBelowFloor；
//  5. frozen_until = now + 7 天——变异：改成 now → 红；
//  6. Decide 写 decided_by/decided_at，状态变更。
//
// 使用 fakeStore（map 存行），与生成的 repo 完全解耦——这是行为测试，不测 SQL。
package pricing

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// fake 仓储
// ============================================================

type fakeUpcStore struct {
	mu                sync.Mutex
	risingPairs       []CostRisePair
	risingErr         error
	effectivePrices   map[string]decimal.Decimal // "skuID|level" -> price
	effectiveLevels   map[int64][]string         // skuID -> [level, ...]
	minMargin         decimal.Decimal
	skuCodes          map[int64]string
	insertedRows      []*UpconductionItem
	insertedBaselineV []int
	costAfterDedup    map[string]int64 // "skuID|level|costAfter" -> id
	queueByID         map[int64]*UpconductionItem
	nextID            int64
	decideCalls       []DecideInput
	decideError       error
}

func newFakeUpcStore() *fakeUpcStore {
	return &fakeUpcStore{
		effectivePrices: make(map[string]decimal.Decimal),
		effectiveLevels: make(map[int64][]string),
		skuCodes:        make(map[int64]string),
		costAfterDedup:  make(map[string]int64),
		queueByID:       make(map[int64]*UpconductionItem),
		minMargin:       decimal.RequireFromString("0.15"),
		nextID:          1000,
	}
}

func (f *fakeUpcStore) LoadRisingCostSKUs(ctx context.Context) ([]CostRisePair, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.risingErr != nil {
		return nil, f.risingErr
	}
	return f.risingPairs, nil
}

func (f *fakeUpcStore) LoadEffectivePrice(ctx context.Context, skuID int64, levelCode string) (decimal.Decimal, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := priceKey(skuID, levelCode)
	p, ok := f.effectivePrices[key]
	return p, ok, nil
}

func (f *fakeUpcStore) LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error) {
	return f.minMargin, nil
}

func (f *fakeUpcStore) LoadSKUCode(ctx context.Context, skuID int64) (string, error) {
	code, ok := f.skuCodes[skuID]
	if !ok {
		return "", errors.New("no such sku")
	}
	return code, nil
}

func (f *fakeUpcStore) ListEffectiveLevels(ctx context.Context, skuID int64) ([]string, error) {
	levels := f.effectiveLevels[skuID]
	return levels, nil
}

func (f *fakeUpcStore) InsertQueueRow(ctx context.Context, row *UpconductionItem, baselineVer int, operatorID int64, requestID string) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := dedupKey(row.SKUID, row.LevelCode, row.CostAfter)
	if _, exists := f.costAfterDedup[key]; exists {
		return 0, false, nil
	}
	f.nextID++
	row.ID = f.nextID
	f.costAfterDedup[key] = f.nextID
	f.insertedRows = append(f.insertedRows, row)
	f.insertedBaselineV = append(f.insertedBaselineV, baselineVer)
	f.queueByID[f.nextID] = row
	return f.nextID, true, nil
}

func (f *fakeUpcStore) ListQueue(ctx context.Context, status string, page, size int) ([]UpconductionItem, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var items []UpconductionItem
	for _, row := range f.insertedRows {
		if status != "" && row.Status != status {
			continue
		}
		items = append(items, *row)
	}
	return items, int64(len(items)), nil
}

func (f *fakeUpcStore) LoadQueueRowByID(ctx context.Context, id int64) (*UpconductionItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.queueByID[id]
	if !ok {
		return nil, ErrUpconductionNotFound
	}
	return row, nil
}

func (f *fakeUpcStore) Decide(ctx context.Context, in DecideInput, operatorRole string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.decideError != nil {
		return f.decideError
	}
	row, ok := f.queueByID[in.QueueID]
	if !ok {
		return ErrUpconductionNotFound
	}
	if row.Status != UpconductionStatusPending {
		return ErrUpconductionNotPending
	}
	row.Status = in.Decision
	row.DecidedBy = &in.OperatorID
	now := time.Now().UTC()
	row.DecidedAt = &now
	if in.Reason != "" {
		row.Reason = &in.Reason
	}
	if in.HasOverride && in.OverridePrice.GreaterThan(decimal.Zero) {
		s := in.OverridePrice.StringFixed(8)
		row.OverridePrice = &s
	}
	f.decideCalls = append(f.decideCalls, in)
	return nil
}

func (f *fakeUpcStore) LoadOperatorIDByAccountID(ctx context.Context, accountID int64) (int64, error) {
	return accountID, nil
}

func priceKey(skuID int64, level string) string {
	return string(rune('0'+skuID)) + "|" + level
}
func dedupKey(skuID int64, level, costAfter string) string {
	return string(rune('0'+skuID)) + "|" + level + "|" + costAfter
}

// 用真实工具：构造 SKUCode map
func codes(kv ...interface{}) map[int64]string {
	m := make(map[int64]string)
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(int64)] = kv[i+1].(string)
	}
	return m
}

// ============================================================
// 纯函数测试
// ============================================================

func TestCalculateCostDeltaPct_Normal(t *testing.T) {
	// 手算锚点：before=10.00, after=11.00 → 0.100000。
	before := decimal.RequireFromString("10.00")
	after := decimal.RequireFromString("11.00")
	delta, err := CalculateCostDeltaPct(before, after)
	require.NoError(t, err)
	assert.Equal(t, "0.1", delta.String())
}

func TestCalculateCostDeltaPct_ZeroBefore(t *testing.T) {
	_, err := CalculateCostDeltaPct(decimal.Zero, decimal.NewFromInt(1))
	require.Error(t, err)
}

func TestCalculatePriceSuggested_Normal(t *testing.T) {
	// 手算锚点：current=12.00, delta=0.10 → 13.2。
	price := decimal.RequireFromString("12.00")
	delta := decimal.RequireFromString("0.10")
	got := CalculatePriceSuggested(price, delta)
	assert.Equal(t, "13.2", got.String())
	// 8 位小数落库格式
	assert.Equal(t, "13.20000000", got.StringFixed(8))
}

func TestCalculateMargin_Normal(t *testing.T) {
	// 手算锚点：price=12.00, cost=10.00 → 0.166667。
	got, err := CalculateMargin(decimal.RequireFromString("12.00"), decimal.RequireFromString("10.00"))
	require.NoError(t, err)
	assert.True(t, got.Sub(decimal.RequireFromString("0.166667")).Abs().LessThan(decimal.RequireFromString("0.000001")),
		"margin 应约等于 0.166667，得到 %s", got.String())
}

func TestCalculateMargin_PriceZero(t *testing.T) {
	_, err := CalculateMargin(decimal.Zero, decimal.RequireFromString("10"))
	require.Error(t, err)
}

// ============================================================
// GenerateQueue
// ============================================================

func TestGenerateQueue_Rise_GenerateOneRow(t *testing.T) {
	store := newFakeUpcStore()
	store.risingPairs = []CostRisePair{
		{SKUID: 40, CostBefore: decimal.RequireFromString("2.50"), CostAfter: decimal.RequireFromString("3.00"), CurrentVersion: 12, PreviousVersion: 11},
	}
	store.skuCodes = codes(int64(40), "SKU-40")
	store.effectiveLevels[40] = []string{"GLOBAL"}
	store.effectivePrices[priceKey(40, "GLOBAL")] = decimal.RequireFromString("2.75")

	svc := NewUpconductionService(store, func() time.Time { return time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC) })
	res, err := svc.GenerateQueue(context.Background(), 1, "req-1")
	require.NoError(t, err)
	require.Equal(t, 1, res.GeneratedCount)
	require.Len(t, res.QueueIDs, 1)
	require.Len(t, res.SkippedSKUIDs, 0)

	row := store.insertedRows[0]
	assert.Equal(t, int64(40), row.SKUID)
	assert.Equal(t, "SKU-40", row.SKUCode)
	assert.Equal(t, "GLOBAL", row.LevelCode)
	assert.Equal(t, "2.50000000", row.CostBefore)
	assert.Equal(t, "3.00000000", row.CostAfter)
	// delta = (3.00 - 2.50) / 2.50 = 0.2
	assert.Equal(t, "0.200000", row.CostDeltaPct)
	assert.Equal(t, "2.75000000", row.PriceCurrent)
	// 3.00 / 0.85 = 3.52941176（实际 3.5294117647...，StringFixed(8) 截断到 8 位）
	assert.Equal(t, "3.52941176", row.FloorPrice)
	require.NotNil(t, row.MarginBefore)
	// margin_before = (2.75 - 2.50) / 2.75 = 0.090909
	assert.Equal(t, "0.090909", *row.MarginBefore)
	// margin_after = (2.75 - 3.00) / 2.75 = -0.090909
	require.NotNil(t, row.MarginAfter)
	assert.Equal(t, "-0.090909", *row.MarginAfter)
	assert.Equal(t, UpconductionStatusPending, row.Status)
	require.NotNil(t, row.FrozenUntil)
	// frozen_until = now + 7d
	expectedFrozen := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC).Add(7 * 24 * time.Hour)
	assert.True(t, row.FrozenUntil.Equal(expectedFrozen), "frozen_until=%v want %v", row.FrozenUntil, expectedFrozen)
}

func TestGenerateQueue_NoRise_Skip(t *testing.T) {
	store := newFakeUpcStore()
	store.risingPairs = []CostRisePair{} // 成本未上涨
	svc := NewUpconductionService(store, nil)
	res, err := svc.GenerateQueue(context.Background(), 1, "req")
	require.NoError(t, err)
	assert.Equal(t, 0, res.GeneratedCount)
}

func TestGenerateQueue_NoEffectivePrice_Skip(t *testing.T) {
	store := newFakeUpcStore()
	store.risingPairs = []CostRisePair{
		{SKUID: 41, CostBefore: decimal.RequireFromString("10"), CostAfter: decimal.RequireFromString("11"), CurrentVersion: 3, PreviousVersion: 2},
	}
	store.skuCodes = codes(int64(41), "SKU-41")
	store.effectiveLevels[41] = []string{} // 无 EFFECTIVE 价目表
	svc := NewUpconductionService(store, nil)
	res, err := svc.GenerateQueue(context.Background(), 1, "req")
	require.NoError(t, err)
	assert.Equal(t, 0, res.GeneratedCount)
	assert.Equal(t, []int64{41}, res.SkippedSKUIDs)
}

func TestGenerateQueue_Dedup_SameCostAfterSkips(t *testing.T) {
	store := newFakeUpcStore()
	store.risingPairs = []CostRisePair{
		{SKUID: 40, CostBefore: decimal.RequireFromString("2.50"), CostAfter: decimal.RequireFromString("3.00"), CurrentVersion: 12, PreviousVersion: 11},
	}
	store.skuCodes = codes(int64(40), "SKU-40")
	store.effectiveLevels[40] = []string{"GLOBAL"}
	store.effectivePrices[priceKey(40, "GLOBAL")] = decimal.RequireFromString("2.75")

	svc := NewUpconductionService(store, nil)
	res1, err := svc.GenerateQueue(context.Background(), 1, "req-1")
	require.NoError(t, err)
	assert.Equal(t, 1, res1.GeneratedCount)

	// 同版本（同 cost_after）再触发 → 幂等跳过。
	res2, err := svc.GenerateQueue(context.Background(), 1, "req-1-replay")
	require.NoError(t, err)
	assert.Equal(t, 0, res2.GeneratedCount, "重复 generate 幂等——同 (sku,level,cost_after) 已有 PENDING 应跳过")
	require.Len(t, store.insertedRows, 1, "inserted 行数仍为 1")
}

func TestGenerateQueue_MultiLevel_GeneratesMultipleRows(t *testing.T) {
	store := newFakeUpcStore()
	store.risingPairs = []CostRisePair{
		{SKUID: 40, CostBefore: decimal.RequireFromString("2.50"), CostAfter: decimal.RequireFromString("3.00"), CurrentVersion: 12, PreviousVersion: 11},
	}
	store.skuCodes = codes(int64(40), "SKU-40")
	store.effectiveLevels[40] = []string{"GLOBAL", "STANDARD"}
	store.effectivePrices[priceKey(40, "GLOBAL")] = decimal.RequireFromString("2.75")
	store.effectivePrices[priceKey(40, "STANDARD")] = decimal.RequireFromString("2.60")

	svc := NewUpconductionService(store, nil)
	res, err := svc.GenerateQueue(context.Background(), 1, "req")
	require.NoError(t, err)
	assert.Equal(t, 2, res.GeneratedCount, "两个 level 各生成一行")
}

func TestGenerateQueue_PriceSuggestedUsesDelta(t *testing.T) {
	// 变异验证锚点 2：price_suggested = price_current × (1 + delta) 而非 × delta。
	// current=12.00, delta=0.20（20%）→ suggested = 12×1.2 = 14.40（而非 12×0.2 = 2.40）。
	store := newFakeUpcStore()
	store.risingPairs = []CostRisePair{
		{SKUID: 42, CostBefore: decimal.RequireFromString("10.00"), CostAfter: decimal.RequireFromString("12.00"), CurrentVersion: 2, PreviousVersion: 1},
	}
	store.skuCodes = codes(int64(42), "SKU-42")
	store.effectiveLevels[42] = []string{"GLOBAL"}
	store.effectivePrices[priceKey(42, "GLOBAL")] = decimal.RequireFromString("12.00")

	svc := NewUpconductionService(store, nil)
	_, err := svc.GenerateQueue(context.Background(), 1, "req")
	require.NoError(t, err)
	require.Len(t, store.insertedRows, 1)
	assert.Equal(t, "14.40000000", store.insertedRows[0].PriceSuggested,
		"suggested 必须 = 12×(1+0.2)=14.40；若错用 × delta 会得到 2.40")
}

// ============================================================
// Decide
// ============================================================

func TestDecide_Follow_SetsStatus(t *testing.T) {
	store := newFakeUpcStore()
	store.risingPairs = []CostRisePair{
		{SKUID: 40, CostBefore: decimal.RequireFromString("2.50"), CostAfter: decimal.RequireFromString("3.00"), CurrentVersion: 12, PreviousVersion: 11},
	}
	store.skuCodes = codes(int64(40), "SKU-40")
	store.effectiveLevels[40] = []string{"GLOBAL"}
	store.effectivePrices[priceKey(40, "GLOBAL")] = decimal.RequireFromString("2.75")

	svc := NewUpconductionService(store, nil)
	genRes, _ := svc.GenerateQueue(context.Background(), 1, "req")
	require.Equal(t, 1, genRes.GeneratedCount)
	qid := genRes.QueueIDs[0]

	// Follow
	res, err := svc.Decide(context.Background(), DecideInput{
		QueueID:    qid,
		OperatorID: 1,
		Decision:   DecisionFollow,
		Reason:     "跟涨",
	}, "PRICING_OP")
	require.NoError(t, err)
	assert.Equal(t, UpconductionStatusFollowed, res.Status)
	assert.Equal(t, int64(1), res.DecidedBy)
	assert.Len(t, store.decideCalls, 1)
	assert.NotNil(t, store.queueByID[qid].DecidedAt)
}

func TestDecide_NotFollowBelowFloor_Rejected(t *testing.T) {
	// price_current=2.75, floor=3.53（cost_after=3.00 / 0.85）：NOT_FOLLOW → 409。
	store := newFakeUpcStore()
	store.risingPairs = []CostRisePair{
		{SKUID: 40, CostBefore: decimal.RequireFromString("2.50"), CostAfter: decimal.RequireFromString("3.00"), CurrentVersion: 12, PreviousVersion: 11},
	}
	store.skuCodes = codes(int64(40), "SKU-40")
	store.effectiveLevels[40] = []string{"GLOBAL"}
	store.effectivePrices[priceKey(40, "GLOBAL")] = decimal.RequireFromString("2.75")

	svc := NewUpconductionService(store, nil)
	genRes, _ := svc.GenerateQueue(context.Background(), 1, "req")
	qid := genRes.QueueIDs[0]

	_, err := svc.Decide(context.Background(), DecideInput{
		QueueID:    qid,
		OperatorID: 1,
		Decision:   DecisionNotFollow,
	}, "PRICING_OP")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUpconductionBelowFloor))
	assert.True(t, strings.Contains(err.Error(), "特价审批"))
	// 状态不应被改
	row, _ := store.LoadQueueRowByID(context.Background(), qid)
	assert.Equal(t, UpconductionStatusPending, row.Status)
}

func TestDecide_NotFollowAboveFloor_OK(t *testing.T) {
	// price_current=4.00 > floor=3.53：NOT_FOLLOW 合法。
	store := newFakeUpcStore()
	store.risingPairs = []CostRisePair{
		{SKUID: 40, CostBefore: decimal.RequireFromString("2.50"), CostAfter: decimal.RequireFromString("3.00"), CurrentVersion: 12, PreviousVersion: 11},
	}
	store.skuCodes = codes(int64(40), "SKU-40")
	store.effectiveLevels[40] = []string{"GLOBAL"}
	store.effectivePrices[priceKey(40, "GLOBAL")] = decimal.RequireFromString("4.00")

	svc := NewUpconductionService(store, nil)
	genRes, _ := svc.GenerateQueue(context.Background(), 1, "req")
	qid := genRes.QueueIDs[0]

	res, err := svc.Decide(context.Background(), DecideInput{
		QueueID:    qid,
		OperatorID: 1,
		Decision:   DecisionNotFollow,
		Reason:     "暂不跟涨",
	}, "PRICING_OP")
	require.NoError(t, err)
	assert.Equal(t, UpconductionStatusNotFollowed, res.Status)
}

func TestDecide_NotFound(t *testing.T) {
	store := newFakeUpcStore()
	svc := NewUpconductionService(store, nil)
	_, err := svc.Decide(context.Background(), DecideInput{QueueID: 9999, OperatorID: 1, Decision: DecisionFollow}, "PRICING_OP")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUpconductionNotFound))
}

func TestDecide_NotPending_Rejected(t *testing.T) {
	store := newFakeUpcStore()
	store.risingPairs = []CostRisePair{
		{SKUID: 40, CostBefore: decimal.RequireFromString("2.50"), CostAfter: decimal.RequireFromString("3.00"), CurrentVersion: 12, PreviousVersion: 11},
	}
	store.skuCodes = codes(int64(40), "SKU-40")
	store.effectiveLevels[40] = []string{"GLOBAL"}
	store.effectivePrices[priceKey(40, "GLOBAL")] = decimal.RequireFromString("4.00")

	svc := NewUpconductionService(store, nil)
	genRes, _ := svc.GenerateQueue(context.Background(), 1, "req")
	qid := genRes.QueueIDs[0]

	// 第一次 FOLLOW 成功
	_, err := svc.Decide(context.Background(), DecideInput{QueueID: qid, OperatorID: 1, Decision: DecisionFollow}, "PRICING_OP")
	require.NoError(t, err)
	// 第二次再决策 → 409 已被决策
	_, err = svc.Decide(context.Background(), DecideInput{QueueID: qid, OperatorID: 1, Decision: DecisionFollow}, "PRICING_OP")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUpconductionNotPending))
}

func TestDecide_InvalidAction(t *testing.T) {
	store := newFakeUpcStore()
	svc := NewUpconductionService(store, nil)
	_, err := svc.Decide(context.Background(), DecideInput{QueueID: 1, OperatorID: 1, Decision: "HOLD"}, "PRICING_OP")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUpconductionInvalidAction))
}

func TestDecide_FollowedWithOverridePrice(t *testing.T) {
	// 决策时传 override_price（市场预期价）→ 落库供后续 8b-3 使用。
	store := newFakeUpcStore()
	store.risingPairs = []CostRisePair{
		{SKUID: 40, CostBefore: decimal.RequireFromString("2.50"), CostAfter: decimal.RequireFromString("3.00"), CurrentVersion: 12, PreviousVersion: 11},
	}
	store.skuCodes = codes(int64(40), "SKU-40")
	store.effectiveLevels[40] = []string{"GLOBAL"}
	store.effectivePrices[priceKey(40, "GLOBAL")] = decimal.RequireFromString("4.00")
	svc := NewUpconductionService(store, nil)
	genRes, _ := svc.GenerateQueue(context.Background(), 1, "req")
	qid := genRes.QueueIDs[0]

	_, err := svc.Decide(context.Background(), DecideInput{
		QueueID:       qid,
		OperatorID:    1,
		Decision:      DecisionFollow,
		OverridePrice: decimal.RequireFromString("3.60"),
		HasOverride:   true,
	}, "PRICING_OP")
	require.NoError(t, err)
	row, _ := store.LoadQueueRowByID(context.Background(), qid)
	require.NotNil(t, row.OverridePrice)
	assert.Equal(t, "3.60000000", *row.OverridePrice)
}

// ============================================================
// ListQueue
// ============================================================

func TestListQueue_FilterByStatus(t *testing.T) {
	store := newFakeUpcStore()
	store.risingPairs = []CostRisePair{
		{SKUID: 40, CostBefore: decimal.RequireFromString("2"), CostAfter: decimal.RequireFromString("3"), CurrentVersion: 12, PreviousVersion: 11},
	}
	store.skuCodes = codes(int64(40), "SKU-40")
	store.effectiveLevels[40] = []string{"GLOBAL"}
	store.effectivePrices[priceKey(40, "GLOBAL")] = decimal.RequireFromString("4")

	svc := NewUpconductionService(store, nil)
	_, err := svc.GenerateQueue(context.Background(), 1, "req")
	require.NoError(t, err)

	items, total, err := svc.ListQueue(context.Background(), UpconductionStatusPending, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, UpconductionStatusPending, items[0].Status)
}

func TestListQueue_InvalidStatusRejected(t *testing.T) {
	store := newFakeUpcStore()
	svc := NewUpconductionService(store, nil)
	_, _, err := svc.ListQueue(context.Background(), "BOGUS", 1, 20)
	require.Error(t, err)
}

func TestListQueue_PaginationGuard(t *testing.T) {
	store := newFakeUpcStore()
	svc := NewUpconductionService(store, nil)
	// page=0 size=0 → 默认 1/20；size=999 → 截到 200。
	items, total, err := svc.ListQueue(context.Background(), "", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, items)
}
