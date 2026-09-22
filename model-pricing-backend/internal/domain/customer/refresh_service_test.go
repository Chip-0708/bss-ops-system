package customer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// ============================================================
// fake store
// ============================================================

type fakeRefreshStore struct {
	quote        *QuoteRow
	quoteErr     error
	items        []QuoteItemDetail
	itemsErr     error
	unitCosts    map[int64]UnitCostInfo
	unitCostsErr error
	margin       decimal.Decimal
	marginErr    error
	refreshRes   *RefreshResult
	refreshErr   error

	lastQuote   QuoteRow
	lastItems   []QuoteItemDetail
	lastChanged []RefreshChangedItem
	lastReason  string
}

func (f *fakeRefreshStore) LoadQuote(_ context.Context, _ int64) (*QuoteRow, error) {
	return f.quote, f.quoteErr
}
func (f *fakeRefreshStore) LoadQuoteItemsDetail(_ context.Context, _ int64) ([]QuoteItemDetail, error) {
	return f.items, f.itemsErr
}
func (f *fakeRefreshStore) LoadCurrentUnitCosts(_ context.Context, _ []int64) (map[int64]UnitCostInfo, error) {
	return f.unitCosts, f.unitCostsErr
}
func (f *fakeRefreshStore) LoadMinGrossMargin(_ context.Context) (decimal.Decimal, error) {
	return f.margin, f.marginErr
}
func (f *fakeRefreshStore) RefreshTx(_ context.Context, quote QuoteRow, items []QuoteItemDetail, changed []RefreshChangedItem, reason string, opID int64, reqID string, now time.Time) (*RefreshResult, error) {
	f.lastQuote = quote
	f.lastItems = items
	f.lastChanged = changed
	f.lastReason = reason
	return f.refreshRes, f.refreshErr
}

// ============================================================
// fixture
// ============================================================

func baseRefreshQuote() *QuoteRow {
	return &QuoteRow{
		ID: 1, CustomerID: 1, VersionNo: 1, Status: QuoteStatusDraft,
		QuoteType: QuoteTypeTemp,
	}
}

func baseRefreshItems() []QuoteItemDetail {
	return []QuoteItemDetail{
		{SKUID: 40, Currency: "CNY", UnitPrice: dec("5.00000000"), FloorPrice: dec("3.52941176")},
		{SKUID: 41, Currency: "CNY", UnitPrice: dec("12.00000000"), FloorPrice: dec("10.58823529")},
	}
}

func baseUnitCosts() map[int64]UnitCostInfo {
	return map[int64]UnitCostInfo{
		40: {UnitCost: dec("3.00000000"), Currency: "CNY"},
		41: {UnitCost: dec("9.00000000"), Currency: "CNY"},
	}
}

// ============================================================
// 测试
// ============================================================

func TestRefresh_Unchanged(t *testing.T) {
	// 关键路径：floor_price 完全没变 → Unchanged=true + 不产新版本
	f := &fakeRefreshStore{
		quote:     baseRefreshQuote(),
		items:     baseRefreshItems(),
		unitCosts: baseUnitCosts(),
		margin:    dec("0.15"), // 3.0/0.85 = 3.52941176; 9.0/0.85 = 10.58823529 — 与 items 里的 floor_price 一致
	}
	svc := NewRefreshService(f, fixedNow)
	res, err := svc.Refresh(context.Background(), RefreshInput{QuoteID: 1, Reason: "r"}, 7, "req-1")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !res.Unchanged {
		t.Fatalf("unchanged=%v want true", res.Unchanged)
	}
	if res.NewVersionNo != res.OldVersionNo {
		t.Fatalf("newVer=%d old=%d", res.NewVersionNo, res.OldVersionNo)
	}
	if len(res.ChangedItems) != 0 {
		t.Fatalf("changed=%+v", res.ChangedItems)
	}
}

func TestRefresh_Changed(t *testing.T) {
	// 成本变了 → 新 floor → 新版本
	f := &fakeRefreshStore{
		quote: baseRefreshQuote(),
		items: baseRefreshItems(),
		unitCosts: map[int64]UnitCostInfo{
			40: {UnitCost: dec("3.50000000")}, // 3.0 → 3.5（成本涨了）
			41: {UnitCost: dec("9.00000000")}, // 不变
		},
		margin: dec("0.15"),
		refreshRes: &RefreshResult{
			QuoteID: 2, OldVersionNo: 1, NewVersionNo: 2,
			ChangedItems: []RefreshChangedItem{
				{SKUID: 40, OldFloorPrice: "3.52941176", NewFloorPrice: "4.11764706"},
			},
			Unchanged: false, Status: QuoteStatusDraft,
		},
	}
	svc := NewRefreshService(f, fixedNow)
	res, err := svc.Refresh(context.Background(), RefreshInput{QuoteID: 1, Reason: "成本上涨"}, 7, "req-1")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res.Unchanged {
		t.Fatalf("unchanged=true, want false")
	}
	if res.NewVersionNo != 2 {
		t.Fatalf("newVer=%d", res.NewVersionNo)
	}
	if len(res.ChangedItems) != 1 || res.ChangedItems[0].SKUID != 40 {
		t.Fatalf("changed=%+v", res.ChangedItems)
	}
	// 3.5 / 0.85 = 4.11764706
	if res.ChangedItems[0].NewFloorPrice != "4.11764706" {
		t.Fatalf("new floor=%s", res.ChangedItems[0].NewFloorPrice)
	}
	// 验证 store 拿到的 items 仍然带 unit_price 不动
	if len(f.lastItems) != 2 {
		t.Fatalf("store items=%d", len(f.lastItems))
	}
	if !f.lastItems[0].UnitPrice.Equal(dec("5.00000000")) {
		t.Fatalf("unit_price 被改了！%s", f.lastItems[0].UnitPrice)
	}
	// floor_price 应更新为 4.11764706
	if !f.lastItems[0].FloorPrice.Equal(dec("4.11764706")) {
		t.Fatalf("floor_price 没更新：%s", f.lastItems[0].FloorPrice)
	}
	// 41 不变
	if !f.lastItems[1].FloorPrice.Equal(dec("10.58823529")) {
		t.Fatalf("sku41 floor 变了：%s", f.lastItems[1].FloorPrice)
	}
}

func TestRefresh_QuoteNotFound(t *testing.T) {
	f := &fakeRefreshStore{}
	svc := NewRefreshService(f, fixedNow)
	_, err := svc.Refresh(context.Background(), RefreshInput{QuoteID: 999}, 7, "req-1")
	if !errors.Is(err, ErrQuoteForRefreshNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestRefresh_TerminalState(t *testing.T) {
	for _, tt := range []struct{ status string }{
		{QuoteStatusApproved},
		{QuoteStatusEffective},
		{QuoteStatusExpired},
		{QuoteStatusRejected},
	} {
		q := baseRefreshQuote()
		q.Status = tt.status
		f := &fakeRefreshStore{quote: q}
		svc := NewRefreshService(f, fixedNow)
		_, err := svc.Refresh(context.Background(), RefreshInput{QuoteID: 1}, 7, "req-1")
		if !errors.Is(err, ErrQuoteTerminal) {
			t.Fatalf("status=%s err=%v", tt.status, err)
		}
	}
}

func TestRefresh_PendingAllowed(t *testing.T) {
	q := baseRefreshQuote()
	q.Status = QuoteStatusPending
	f := &fakeRefreshStore{
		quote:     q,
		items:     baseRefreshItems(),
		unitCosts: baseUnitCosts(),
		margin:    dec("0.15"),
	}
	svc := NewRefreshService(f, fixedNow)
	res, err := svc.Refresh(context.Background(), RefreshInput{QuoteID: 1}, 7, "req-1")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !res.Unchanged {
		t.Fatalf("want unchanged (PENDING 但 floor 没变)")
	}
}

func TestRefresh_NoUnitCostForSKU(t *testing.T) {
	f := &fakeRefreshStore{
		quote:     baseRefreshQuote(),
		items:     baseRefreshItems(),
		unitCosts: map[int64]UnitCostInfo{40: {UnitCost: dec("3.00")}}, // 缺 41
		margin:    dec("0.15"),
	}
	svc := NewRefreshService(f, fixedNow)
	_, err := svc.Refresh(context.Background(), RefreshInput{QuoteID: 1}, 7, "req-1")
	if err == nil {
		t.Fatalf("want err（缺 baseline）")
	}
}
