package workbench

// 10a workbench service unit tests.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// fakeStore: default returns zero value; override per scenario.
type fakeStore struct {
	ListTodosFunc                  func(ctx context.Context, q TodoQuery, ownerScope bool, operatorID int64) ([]TodoItem, int64, error)
	CountPendingQuotesFunc         func(ctx context.Context, operatorID int64, ownerScope bool) (int64, error)
	CountEffectiveQuotesMonthFunc  func(ctx context.Context, operatorID int64, ownerScope bool, now time.Time) (int64, error)
	CountQuotesExpiring30dFunc     func(ctx context.Context, operatorID int64, ownerScope bool, now time.Time) (int64, error)
	CountSingleDepsFunc            func(ctx context.Context, operatorID int64, ownerScope bool) (int64, error)
	CountPriceBooksPendingFunc     func(ctx context.Context) (int64, error)
	LoadItemsFunc                  func(ctx context.Context) ([]PriceBookItemForFloor, error)
	LoadBaselineFunc               func(ctx context.Context, skuID int64) (*CostBaselineForFloor, error)
	LoadMinMarginFunc              func(ctx context.Context) (decimal.Decimal, error)
	CountPendingUpconductionFunc   func(ctx context.Context) (int64, error)
	CountMyCustomersFunc           func(ctx context.Context, operatorID int64) (int64, error)
	CountPendingCustomerQuotesFunc func(ctx context.Context, operatorID int64) (int64, error)
	CountUnpaidDepositFunc         func(ctx context.Context) (int64, error)
}

func (f *fakeStore) ListTodos(ctx context.Context, q TodoQuery, ownerScope bool, operatorID int64) ([]TodoItem, int64, error) {
	if f.ListTodosFunc != nil {
		return f.ListTodosFunc(ctx, q, ownerScope, operatorID)
	}
	return nil, 0, nil
}
func (f *fakeStore) CountPendingQuotes(ctx context.Context, id int64, scope bool) (int64, error) {
	if f.CountPendingQuotesFunc != nil {
		return f.CountPendingQuotesFunc(ctx, id, scope)
	}
	return 0, nil
}
func (f *fakeStore) CountEffectiveQuotesThisMonth(ctx context.Context, id int64, scope bool, now time.Time) (int64, error) {
	if f.CountEffectiveQuotesMonthFunc != nil {
		return f.CountEffectiveQuotesMonthFunc(ctx, id, scope, now)
	}
	return 0, nil
}
func (f *fakeStore) CountQuotesExpiringIn30Days(ctx context.Context, id int64, scope bool, now time.Time) (int64, error) {
	if f.CountQuotesExpiring30dFunc != nil {
		return f.CountQuotesExpiring30dFunc(ctx, id, scope, now)
	}
	return 0, nil
}
func (f *fakeStore) CountSupplierDepsForBuyer(ctx context.Context, id int64, scope bool) (int64, error) {
	if f.CountSingleDepsFunc != nil {
		return f.CountSingleDepsFunc(ctx, id, scope)
	}
	return 0, nil
}
func (f *fakeStore) CountPriceBooksPending(ctx context.Context) (int64, error) {
	if f.CountPriceBooksPendingFunc != nil {
		return f.CountPriceBooksPendingFunc(ctx)
	}
	return 0, nil
}
func (f *fakeStore) LoadEffectivePriceBookItemsWithFloor(ctx context.Context) ([]PriceBookItemForFloor, error) {
	if f.LoadItemsFunc != nil {
		return f.LoadItemsFunc(ctx)
	}
	return nil, nil
}
func (f *fakeStore) LoadCurrentCostBaseline(ctx context.Context, skuID int64) (*CostBaselineForFloor, error) {
	if f.LoadBaselineFunc != nil {
		return f.LoadBaselineFunc(ctx, skuID)
	}
	return nil, nil
}
func (f *fakeStore) LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error) {
	if f.LoadMinMarginFunc != nil {
		return f.LoadMinMarginFunc(ctx)
	}
	return decimal.Zero, nil
}
func (f *fakeStore) CountPendingUpconduction(ctx context.Context) (int64, error) {
	if f.CountPendingUpconductionFunc != nil {
		return f.CountPendingUpconductionFunc(ctx)
	}
	return 0, nil
}
func (f *fakeStore) CountMyCustomers(ctx context.Context, id int64) (int64, error) {
	if f.CountMyCustomersFunc != nil {
		return f.CountMyCustomersFunc(ctx, id)
	}
	return 0, nil
}
func (f *fakeStore) CountPendingCustomerQuotes(ctx context.Context, id int64) (int64, error) {
	if f.CountPendingCustomerQuotesFunc != nil {
		return f.CountPendingCustomerQuotesFunc(ctx, id)
	}
	return 0, nil
}
func (f *fakeStore) CountUnpaidDeposit(ctx context.Context) (int64, error) {
	if f.CountUnpaidDepositFunc != nil {
		return f.CountUnpaidDepositFunc(ctx)
	}
	return 0, nil
}

var testNow = time.Date(2026, 1, 18, 12, 0, 0, 0, time.UTC)

// TestListTodos_PROCUREMENT_onlyOwn: PROCUREMENT sees assignee_id=operatorID (ownerScope=true).
func TestListTodos_PROCUREMENT_onlyOwn(t *testing.T) {
	fs := &fakeStore{
		ListTodosFunc: func(ctx context.Context, q TodoQuery, ownerScope bool, operatorID int64) ([]TodoItem, int64, error) {
			require.True(t, ownerScope, "non-PLATFORM_ADMIN must ownerScope=true")
			require.Equal(t, int64(4), operatorID)
			return []TodoItem{
				{ID: 20, BizType: "QUOTE_EXPIRE", BizID: 24, Title: "t1", Priority: "MID", Status: "OPEN"},
			}, 1, nil
		},
	}
	svc := NewService(fs, func() time.Time { return testNow })
	res, err := svc.ListTodos(context.Background(), []string{"PROCUREMENT"}, 4, TodoQuery{Page: 1, Size: 20})
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)
	require.Len(t, res.List, 1)
	require.Equal(t, "/supplier/quotes/24", res.List[0].Deeplink)
}

// TestListTodos_PLATFORM_ADMIN_seeAll: PLATFORM_ADMIN bypasses owner filter.
func TestListTodos_PLATFORM_ADMIN_seeAll(t *testing.T) {
	fs := &fakeStore{
		ListTodosFunc: func(ctx context.Context, q TodoQuery, ownerScope bool, operatorID int64) ([]TodoItem, int64, error) {
			require.False(t, ownerScope, "PLATFORM_ADMIN must ownerScope=false")
			return []TodoItem{
				{ID: 19, BizType: "QUOTE", BizID: 1, Title: "a", Priority: "MID", Status: "DONE"},
				{ID: 20, BizType: "QUOTE_EXPIRE", BizID: 24, Title: "b", Priority: "MID", Status: "OPEN"},
			}, 2, nil
		},
	}
	svc := NewService(fs, func() time.Time { return testNow })
	res, err := svc.ListTodos(context.Background(), []string{"PLATFORM_ADMIN", "PROCUREMENT"}, 1, TodoQuery{Page: 1, Size: 20})
	require.NoError(t, err)
	require.Equal(t, 2, res.Total)
	require.Len(t, res.List, 2)
	require.Equal(t, "/quotes/1/approval", res.List[0].Deeplink)
}

// TestListTodos_Dedup: dedupe by (biz_type, biz_id, priority).
func TestListTodos_Dedup(t *testing.T) {
	fs := &fakeStore{
		ListTodosFunc: func(ctx context.Context, q TodoQuery, ownerScope bool, operatorID int64) ([]TodoItem, int64, error) {
			return []TodoItem{
				{ID: 1, BizType: "QUOTE", BizID: 7, Title: "t1", Priority: "MID", Status: "OPEN"},
				{ID: 2, BizType: "QUOTE", BizID: 7, Title: "t1-dup", Priority: "MID", Status: "OPEN"},
				{ID: 3, BizType: "QUOTE", BizID: 7, Title: "t2-priority-high", Priority: "HIGH", Status: "OPEN"},
			}, 3, nil
		},
	}
	svc := NewService(fs, func() time.Time { return testNow })
	res, err := svc.ListTodos(context.Background(), []string{"PROCUREMENT"}, 4, TodoQuery{Page: 1, Size: 20})
	require.NoError(t, err)
	require.Len(t, res.List, 2, "same (biz_type,biz_id,priority) must be deduped")
}

// TestListTodos_PaginationBounds: page=0 -> 1, size>200 -> 200.
func TestListTodos_PaginationBounds(t *testing.T) {
	var captured TodoQuery
	fs := &fakeStore{
		ListTodosFunc: func(ctx context.Context, q TodoQuery, ownerScope bool, operatorID int64) ([]TodoItem, int64, error) {
			captured = q
			return nil, 0, nil
		},
	}
	svc := NewService(fs, nil)
	_, err := svc.ListTodos(context.Background(), []string{"PROCUREMENT"}, 4, TodoQuery{Page: 0, Size: 9999})
	require.NoError(t, err)
	require.Equal(t, 1, captured.Page)
	require.Equal(t, 200, captured.Size, "size>200 must be capped to 200")
}

// TestDeeplinkFor: each biz_type maps to detail page.
func TestDeeplinkFor(t *testing.T) {
	require.Equal(t, "/quotes/42/approval", deeplinkFor("QUOTE", 42))
	require.Equal(t, "/supplier/quotes/7", deeplinkFor("QUOTE_EXPIRE", 7))
	require.Equal(t, "/todos/99", deeplinkFor("UNKNOWN_BIZ", 99))
}

// TestListMetrics_PROCUREMENT_4Cards: PROCUREMENT sees 4 cards (not pricing/sales/finance).
func TestListMetrics_PROCUREMENT_4Cards(t *testing.T) {
	fs := &fakeStore{
		CountPendingQuotesFunc: func(ctx context.Context, id int64, scope bool) (int64, error) {
			require.True(t, scope)
			require.Equal(t, int64(4), id)
			return 3, nil
		},
		CountEffectiveQuotesMonthFunc: func(ctx context.Context, id int64, scope bool, now time.Time) (int64, error) {
			return 2, nil
		},
		CountQuotesExpiring30dFunc: func(ctx context.Context, id int64, scope bool, now time.Time) (int64, error) {
			return 1, nil
		},
		CountSingleDepsFunc: func(ctx context.Context, id int64, scope bool) (int64, error) { return 1, nil },
	}
	svc := NewService(fs, func() time.Time { return testNow })
	res, err := svc.ListMetrics(context.Background(), []string{"PROCUREMENT"}, 4)
	require.NoError(t, err)
	keys := collectKeys(res.Cards)
	require.Contains(t, keys, "proc_pending_quotes")
	require.Contains(t, keys, "proc_effective_this_month")
	require.Contains(t, keys, "proc_expire_30d")
	require.Contains(t, keys, "proc_single_dep")
	require.NotContains(t, keys, "pricing_pending_books")
	require.NotContains(t, keys, "sales_my_customers")
	require.NotContains(t, keys, "fin_deposit_unpaid")
	require.Len(t, res.Cards, 4)
	require.Equal(t, "3", lookupCard(res.Cards, "proc_pending_quotes").Value)
}

// TestListMetrics_PRICING_OP_3Cards: PRICING_OP sees 3 cards.
func TestListMetrics_PRICING_OP_3Cards(t *testing.T) {
	fs := &fakeStore{
		CountPriceBooksPendingFunc:   func(ctx context.Context) (int64, error) { return 2, nil },
		LoadItemsFunc:                func(ctx context.Context) ([]PriceBookItemForFloor, error) { return nil, nil },
		LoadMinMarginFunc:            func(ctx context.Context) (decimal.Decimal, error) { return decimal.NewFromFloat(0.15), nil },
		CountPendingUpconductionFunc: func(ctx context.Context) (int64, error) { return 1, nil },
	}
	svc := NewService(fs, func() time.Time { return testNow })
	res, err := svc.ListMetrics(context.Background(), []string{"PRICING_OP"}, 3)
	require.NoError(t, err)
	keys := collectKeys(res.Cards)
	require.Contains(t, keys, "pricing_pending_books")
	require.Contains(t, keys, "pricing_floor_violations")
	require.Contains(t, keys, "pricing_pending_upconduction")
	require.NotContains(t, keys, "proc_pending_quotes")
	require.NotContains(t, keys, "sales_my_customers")
}

// TestListMetrics_SALES_3Cards: SALES sees 3 cards (quarterly is placeholder).
func TestListMetrics_SALES_3Cards(t *testing.T) {
	fs := &fakeStore{
		CountMyCustomersFunc:           func(ctx context.Context, id int64) (int64, error) { return 2, nil },
		CountPendingCustomerQuotesFunc: func(ctx context.Context, id int64) (int64, error) { return 1, nil },
	}
	svc := NewService(fs, nil)
	res, err := svc.ListMetrics(context.Background(), []string{"SALES"}, 2)
	require.NoError(t, err)
	keys := collectKeys(res.Cards)
	require.Contains(t, keys, "sales_my_customers")
	require.Contains(t, keys, "sales_pending_quotes")
	require.Contains(t, keys, "sales_quarterly_deal")
	require.NotContains(t, keys, "proc_pending_quotes")
	require.Equal(t, "0.00", lookupCard(res.Cards, "sales_quarterly_deal").Value)
}

// TestListMetrics_FINANCE_2Cards: FINANCE sees 2 cards.
func TestListMetrics_FINANCE_2Cards(t *testing.T) {
	fs := &fakeStore{
		CountUnpaidDepositFunc: func(ctx context.Context) (int64, error) { return 1, nil },
	}
	svc := NewService(fs, nil)
	res, err := svc.ListMetrics(context.Background(), []string{"FINANCE"}, 6)
	require.NoError(t, err)
	keys := collectKeys(res.Cards)
	require.Contains(t, keys, "fin_fx_pending_month")
	require.Contains(t, keys, "fin_deposit_unpaid")
	require.NotContains(t, keys, "proc_pending_quotes")
	require.Equal(t, "0", lookupCard(res.Cards, "fin_fx_pending_month").Value)
}

// TestListMetrics_MultiRole_union: PLATFORM_ADMIN+PROCUREMENT union.
func TestListMetrics_MultiRole_union(t *testing.T) {
	fs := &fakeStore{
		CountPendingQuotesFunc:        func(ctx context.Context, id int64, scope bool) (int64, error) { return 7, nil },
		CountEffectiveQuotesMonthFunc: func(ctx context.Context, id int64, scope bool, now time.Time) (int64, error) { return 1, nil },
		CountQuotesExpiring30dFunc:    func(ctx context.Context, id int64, scope bool, now time.Time) (int64, error) { return 0, nil },
		CountSingleDepsFunc:           func(ctx context.Context, id int64, scope bool) (int64, error) { return 2, nil },
	}
	svc := NewService(fs, func() time.Time { return testNow })
	res, err := svc.ListMetrics(context.Background(), []string{"PLATFORM_ADMIN", "PROCUREMENT"}, 1)
	require.NoError(t, err)
	keys := collectKeys(res.Cards)
	require.Contains(t, keys, "proc_pending_quotes")
}

// TestListMetrics_FloorViolation_boundary: price==floor must NOT count (mutation #2 anchor).
func TestListMetrics_FloorViolation_boundary(t *testing.T) {
	bl := &CostBaselineForFloor{
		SKUID: 40, Currency: "CNY", LossRate: "0", ChannelRate: "0",
		Components: []CostComponentSnapshot{{ComponentType: "input", UnitCost: "3.4"}},
	}
	fs := &fakeStore{
		CountPriceBooksPendingFunc: func(ctx context.Context) (int64, error) { return 0, nil },
		LoadItemsFunc: func(ctx context.Context) ([]PriceBookItemForFloor, error) {
			return []PriceBookItemForFloor{{SKUID: 40, UnitPrice: "4.0", Currency: "CNY"}}, nil
		},
		LoadBaselineFunc: func(ctx context.Context, skuID int64) (*CostBaselineForFloor, error) { return bl, nil },
		LoadMinMarginFunc: func(ctx context.Context) (decimal.Decimal, error) {
			// floor = 3.4 * 1 * 1 / (1 - 0.15) = 4.0
			return decimal.NewFromFloat(0.15), nil
		},
		CountPendingUpconductionFunc: func(ctx context.Context) (int64, error) { return 0, nil },
	}
	svc := NewService(fs, func() time.Time { return testNow })
	res, err := svc.ListMetrics(context.Background(), []string{"PRICING_OP"}, 3)
	require.NoError(t, err)
	require.Equal(t, "0", lookupCard(res.Cards, "pricing_floor_violations").Value,
		"price==floor must NOT count; mutating < to <= must fail this test")
}

// TestListMetrics_FloorViolation_belowCount: price < floor counts.
func TestListMetrics_FloorViolation_belowCount(t *testing.T) {
	bl := &CostBaselineForFloor{
		SKUID: 40, Currency: "CNY", LossRate: "0", ChannelRate: "0",
		Components: []CostComponentSnapshot{{ComponentType: "input", UnitCost: "3.4"}},
	}
	fs := &fakeStore{
		CountPriceBooksPendingFunc: func(ctx context.Context) (int64, error) { return 0, nil },
		LoadItemsFunc: func(ctx context.Context) ([]PriceBookItemForFloor, error) {
			return []PriceBookItemForFloor{{SKUID: 40, UnitPrice: "3.9", Currency: "CNY"}}, nil
		},
		LoadBaselineFunc:             func(ctx context.Context, skuID int64) (*CostBaselineForFloor, error) { return bl, nil },
		LoadMinMarginFunc:            func(ctx context.Context) (decimal.Decimal, error) { return decimal.NewFromFloat(0.15), nil },
		CountPendingUpconductionFunc: func(ctx context.Context) (int64, error) { return 0, nil },
	}
	svc := NewService(fs, nil)
	res, err := svc.ListMetrics(context.Background(), []string{"PRICING_OP"}, 3)
	require.NoError(t, err)
	require.Equal(t, "1", lookupCard(res.Cards, "pricing_floor_violations").Value)
}

// TestListMetrics_AllCards_haveMetadata: all cards have key/title/unit/deeplink.
func TestListMetrics_AllCards_haveMetadata(t *testing.T) {
	fs := &fakeStore{}
	svc := NewService(fs, nil)
	res, err := svc.ListMetrics(context.Background(), []string{"PROCUREMENT", "PRICING_OP", "SALES", "FINANCE"}, 1)
	require.NoError(t, err)
	for _, c := range res.Cards {
		require.NotEmpty(t, c.Key)
		require.NotEmpty(t, c.Title)
		require.NotEmpty(t, c.Unit)
		require.NotEmpty(t, c.Deeplink)
	}
}

// TestListMetrics_DTO_no_cost_fields: DTO JSON physically lacks unit_cost/floor_price/margin/baseline.
func TestListMetrics_DTO_no_cost_fields(t *testing.T) {
	fs := &fakeStore{
		CountPriceBooksPendingFunc: func(ctx context.Context) (int64, error) { return 1, nil },
		LoadItemsFunc: func(ctx context.Context) ([]PriceBookItemForFloor, error) {
			return []PriceBookItemForFloor{{SKUID: 40, UnitPrice: "3.9", Currency: "CNY"}}, nil
		},
		LoadBaselineFunc: func(ctx context.Context, skuID int64) (*CostBaselineForFloor, error) {
			return &CostBaselineForFloor{
				SKUID: 40, LossRate: "0", ChannelRate: "0",
				Components: []CostComponentSnapshot{{ComponentType: "input", UnitCost: "3.4"}},
			}, nil
		},
		LoadMinMarginFunc:            func(ctx context.Context) (decimal.Decimal, error) { return decimal.NewFromFloat(0.15), nil },
		CountPendingUpconductionFunc: func(ctx context.Context) (int64, error) { return 0, nil },
	}
	svc := NewService(fs, nil)
	res, err := svc.ListMetrics(context.Background(), []string{"PRICING_OP"}, 3)
	require.NoError(t, err)
	jsonBytes, err := json.Marshal(res)
	require.NoError(t, err)
	jsonStr := string(jsonBytes)
	require.NotContains(t, jsonStr, "unit_cost")
	require.NotContains(t, jsonStr, "floor_price")
	require.NotContains(t, jsonStr, "margin")
	require.NotContains(t, jsonStr, "baseline")
}

// TestListTodos_StoreError: store error passes through.
func TestListTodos_StoreError(t *testing.T) {
	fs := &fakeStore{
		ListTodosFunc: func(ctx context.Context, q TodoQuery, ownerScope bool, operatorID int64) ([]TodoItem, int64, error) {
			return nil, 0, errors.New("db connection lost")
		},
	}
	svc := NewService(fs, nil)
	_, err := svc.ListTodos(context.Background(), []string{"PROCUREMENT"}, 4, TodoQuery{Page: 1, Size: 20})
	require.Error(t, err)
}

// TestListMetrics_BaselineLoadError_belowFloor: baseline load error skips SKU.
func TestListMetrics_BaselineLoadError_belowFloor(t *testing.T) {
	fs := &fakeStore{
		CountPriceBooksPendingFunc: func(ctx context.Context) (int64, error) { return 0, nil },
		LoadItemsFunc: func(ctx context.Context) ([]PriceBookItemForFloor, error) {
			return []PriceBookItemForFloor{{SKUID: 40, UnitPrice: "3.9", Currency: "CNY"}}, nil
		},
		LoadBaselineFunc: func(ctx context.Context, skuID int64) (*CostBaselineForFloor, error) {
			return nil, errors.New("json unmarshal error")
		},
		LoadMinMarginFunc:            func(ctx context.Context) (decimal.Decimal, error) { return decimal.NewFromFloat(0.15), nil },
		CountPendingUpconductionFunc: func(ctx context.Context) (int64, error) { return 0, nil },
	}
	svc := NewService(fs, nil)
	res, err := svc.ListMetrics(context.Background(), []string{"PRICING_OP"}, 3)
	require.NoError(t, err)
	require.Equal(t, "0", lookupCard(res.Cards, "pricing_floor_violations").Value,
		"baseline-load-error SKU must be skipped")
}

// helpers
func collectKeys(cards []MetricCard) []string {
	out := make([]string, 0, len(cards))
	for _, c := range cards {
		out = append(out, c.Key)
	}
	return out
}

func lookupCard(cards []MetricCard, key string) MetricCard {
	for _, c := range cards {
		if c.Key == key {
			return c
		}
	}
	return MetricCard{}
}
