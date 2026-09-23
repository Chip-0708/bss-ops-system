package customer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"model_bss/internal/domain/auth"
)

// ============================================================
// fakeStore
// ============================================================

type fakeStore struct {
	// 客户列表
	listRes *CustomerListResult
	listErr error
	byID    *CustomerItem
	byIDErr error
	// 归属校验
	scopeFound   bool
	scopeInScope bool
	scopeErr     error
	// 移交
	impact       *TransferImpact
	impactErr    error
	transferRes  *TransferResult
	transferErr  error
	salesID      int64
	salesName    string
	salesActive  bool
	salesHasRole bool
	salesErr     error
	// 生成报价
	levelCode         string
	ownerSalesID      int64
	levelErr          error
	bookID            int64
	bookVersion       int
	bookFound         bool
	bookErr           error
	pbItems           []PriceBookItemRow
	pbItemsErr        error
	quoteItems        []QuoteItemRow
	quoteItemsErr     error
	quoteOwnerCustID  int64
	quoteOwnerSalesID int64
	quoteOwnerFound   bool
	quoteOwnerErr     error
	unitCosts         map[int64]UnitCostInfo
	unitCostsErr      error
	margin            decimal.Decimal
	marginErr         error
	skuCode           string
	skuCodeErr        error
	quoteHistory      *QuoteHistoryPage
	quoteHistoryErr   error
	genRes            *GeneratedQuote
	genErr            error
	// 捕获
	lastGenInput GenerateQuoteTxInput
}

func (f *fakeStore) ListCustomers(_ context.Context, _ OwnerScope, _ string, _, _ int) (*CustomerListResult, error) {
	return f.listRes, f.listErr
}
func (f *fakeStore) LoadCustomerByID(_ context.Context, _ int64) (*CustomerItem, error) {
	return f.byID, f.byIDErr
}
func (f *fakeStore) CheckCustomerScope(_ context.Context, _ int64, _ OwnerScope) (bool, bool, error) {
	return f.scopeFound, f.scopeInScope, f.scopeErr
}
func (f *fakeStore) LoadTransferImpact(_ context.Context, _, _ int64) (*TransferImpact, error) {
	return f.impact, f.impactErr
}
func (f *fakeStore) TransferCustomerTx(_ context.Context, _ TransferInput, _ int64, _ string) (*TransferResult, error) {
	return f.transferRes, f.transferErr
}
func (f *fakeStore) LoadSalesOperator(_ context.Context, _ int64) (int64, string, bool, bool, error) {
	return f.salesID, f.salesName, f.salesActive, f.salesHasRole, f.salesErr
}
func (f *fakeStore) LoadCustomerLevel(_ context.Context, _ int64) (string, int64, error) {
	return f.levelCode, f.ownerSalesID, f.levelErr
}
func (f *fakeStore) LoadEffectivePriceBook(_ context.Context, _ string) (int64, int, bool, error) {
	return f.bookID, f.bookVersion, f.bookFound, f.bookErr
}
func (f *fakeStore) LoadPriceBookItems(_ context.Context, _ int64) ([]PriceBookItemRow, error) {
	return f.pbItems, f.pbItemsErr
}
func (f *fakeStore) LoadPriceBookItemsForSKUs(_ context.Context, _ int64, _ []int64) ([]PriceBookItemRow, error) {
	return f.pbItems, f.pbItemsErr
}
func (f *fakeStore) LoadQuoteItems(_ context.Context, _ int64) ([]QuoteItemRow, error) {
	return f.quoteItems, f.quoteItemsErr
}
func (f *fakeStore) LoadQuoteOwner(_ context.Context, _ int64) (int64, int64, bool, error) {
	return f.quoteOwnerCustID, f.quoteOwnerSalesID, f.quoteOwnerFound, f.quoteOwnerErr
}
func (f *fakeStore) LoadCurrentUnitCosts(_ context.Context, _ []int64) (map[int64]UnitCostInfo, error) {
	return f.unitCosts, f.unitCostsErr
}
func (f *fakeStore) LoadMinGrossMargin(_ context.Context) (decimal.Decimal, error) {
	return f.margin, f.marginErr
}
func (f *fakeStore) LoadSKUCode(_ context.Context, _ int64) (string, error) {
	return f.skuCode, f.skuCodeErr
}
func (f *fakeStore) ListCustomerQuotePreviews(_ context.Context, _ int64, _, _ int) (*QuoteHistoryPage, error) {
	return f.quoteHistory, f.quoteHistoryErr
}
func (f *fakeStore) GenerateQuoteTx(_ context.Context, in GenerateQuoteTxInput, _ int64, _ string) (*GeneratedQuote, error) {
	f.lastGenInput = in
	return f.genRes, f.genErr
}

// ============================================================
// 公共 fixture
// ============================================================

func fixedNow() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC) }

func scopeALL() OwnerScope {
	return OwnerScope{DataScope: auth.ScopeALL, StaffID: 1, MyOrgID: 1}
}

func scopeSELF(staffID int64) OwnerScope {
	return OwnerScope{DataScope: auth.ScopeSELF, StaffID: staffID, MyOrgID: 1}
}

func dec(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

// baseFake 一个可用的最小 fake（APPLY 全量带出，floor 通过）。
func baseFake() *fakeStore {
	return &fakeStore{
		scopeFound:   true,
		scopeInScope: true,
		levelCode:    "GLOBAL",
		ownerSalesID: 2,
		bookID:       3,
		bookVersion:  1,
		bookFound:    true,
		// 带出价必须 >= floor（sku40 floor=3.52941176 / sku41 floor=10.58823529），
		// 否则合法场景会被 floor 校验挡下，测试失去区分度。
		pbItems: []PriceBookItemRow{
			{SKUID: 40, SKUCode: "gpt-5", Currency: "CNY", UnitPrice: dec("3.80000000"), BaselineVersion: 13},
			{SKUID: 41, SKUCode: "claude-opus-4", Currency: "CNY", UnitPrice: dec("12.00000000"), BaselineVersion: 4},
		},
		unitCosts: map[int64]UnitCostInfo{
			40: {UnitCost: dec("3.00000000"), Currency: "CNY", BaselineVersion: 13},
			41: {UnitCost: dec("9.00000000"), Currency: "CNY", BaselineVersion: 4},
		},
		margin:  dec("0.15"),
		skuCode: "gpt-5",
		genRes: &GeneratedQuote{
			ID: 1, CustomerID: 1, VersionNo: 1, Status: QuoteStatusDraft,
			QuoteType: QuoteTypeApply, ItemCount: 2, PriceBookVersion: 1,
			OwnerSalesID: 2, CreatedAt: fixedNow(),
		},
	}
}

// ============================================================
// ListCustomers
// ============================================================

func TestListCustomers_PageGuard(t *testing.T) {
	f := &fakeStore{listRes: &CustomerListResult{List: []CustomerItem{}, Total: 0, Page: 1, Size: 20}}
	svc := NewService(f, fixedNow)
	res, err := svc.ListCustomers(context.Background(), scopeALL(), "", 0, 0)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res.Page != 1 || res.Size != 20 {
		t.Fatalf("page=%d size=%d, want 1/20", res.Page, res.Size)
	}
}

func TestGetQuoteContext_ReturnsPriceBookAndHistory(t *testing.T) {
	f := baseFake()
	f.quoteHistory = &QuoteHistoryPage{List: []QuoteHistoryPreview{{ID: 9, VersionNo: 2, Status: QuoteStatusApproved,
		QuoteType: QuoteTypeApply, Items: []QuotePreviewItem{{SKUID: 40, SKUCode: "gpt-5", Currency: "CNY", UnitPrice: "3.80000000"}}}},
		Total: 1, Page: 1, Size: 20}
	svc := NewService(f, fixedNow)
	got, err := svc.GetQuoteContext(context.Background(), 1, scopeALL(), 1, 20)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got.PriceBook == nil || got.PriceBook.ID != 3 || len(got.PriceBook.Items) != 2 {
		t.Fatalf("price_book=%+v", got.PriceBook)
	}
	if len(got.History.List) != 1 || got.History.List[0].ID != 9 {
		t.Fatalf("history=%+v", got.History)
	}
}

func TestGetQuoteContext_EnforcesScope(t *testing.T) {
	f := baseFake()
	f.scopeInScope = false
	_, err := NewService(f, fixedNow).GetQuoteContext(context.Background(), 1, scopeSELF(8), 1, 20)
	if !errors.Is(err, ErrCustomerOutOfScope) {
		t.Fatalf("err=%v, want ErrCustomerOutOfScope", err)
	}
}

func TestListCustomers_NilRes(t *testing.T) {
	f := &fakeStore{}
	svc := NewService(f, fixedNow)
	res, err := svc.ListCustomers(context.Background(), scopeALL(), "", 1, 20)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res == nil || res.List == nil {
		t.Fatalf("res=%v, want non-nil empty", res)
	}
}

// ============================================================
// TransferPreview / Transfer
// ============================================================

func TestTransferPreview_ReturnsImpact(t *testing.T) {
	f := &fakeStore{
		impact: &TransferImpact{
			CustomerID: 1, FromOperatorID: 2, FromOperatorName: "smoke_sales",
			ToOperatorID: 6, ToOperatorName: "smoke_finance",
			QuoteCount: 3, PriceBookCount: 1,
		},
	}
	svc := NewService(f, fixedNow)
	imp, err := svc.TransferPreview(context.Background(), TransferInput{CustomerID: 1, ToOperatorID: 6, Reason: "r"})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if imp.QuoteCount != 3 || imp.FromOperatorName != "smoke_sales" {
		t.Fatalf("imp=%+v", imp)
	}
	if imp.Reason != "r" {
		t.Fatalf("reason=%q", imp.Reason)
	}
}

func TestTransferPreview_NotFound(t *testing.T) {
	f := &fakeStore{}
	svc := NewService(f, fixedNow)
	_, err := svc.TransferPreview(context.Background(), TransferInput{CustomerID: 99, ToOperatorID: 6})
	if !errors.Is(err, ErrCustomerNotFound) {
		t.Fatalf("err=%v, want ErrCustomerNotFound", err)
	}
}

func TestTransfer_ConfirmRequired(t *testing.T) {
	f := &fakeStore{}
	svc := NewService(f, fixedNow)
	_, err := svc.Transfer(context.Background(), TransferInput{CustomerID: 1, ToOperatorID: 6, Confirm: false}, 1, "req-1")
	if !errors.Is(err, ErrConfirmRequired) {
		t.Fatalf("err=%v, want ErrConfirmRequired", err)
	}
}

func TestTransfer_InvalidTarget(t *testing.T) {
	f := &fakeStore{salesID: 0, salesActive: false, salesHasRole: false}
	svc := NewService(f, fixedNow)
	_, err := svc.Transfer(context.Background(), TransferInput{CustomerID: 1, ToOperatorID: 99, Confirm: true}, 1, "req-1")
	if !errors.Is(err, ErrInvalidTransferTarget) {
		t.Fatalf("err=%v, want ErrInvalidTransferTarget", err)
	}
}

func TestTransfer_ToSelf(t *testing.T) {
	f := &fakeStore{
		salesID: 2, salesActive: true, salesHasRole: true,
		levelCode: "GLOBAL", ownerSalesID: 2, // 当前归属就是 2
	}
	svc := NewService(f, fixedNow)
	_, err := svc.Transfer(context.Background(), TransferInput{CustomerID: 1, ToOperatorID: 2, Confirm: true}, 1, "req-1")
	if !errors.Is(err, ErrTransferToSelf) {
		t.Fatalf("err=%v, want ErrTransferToSelf", err)
	}
}

func TestTransfer_Success(t *testing.T) {
	f := &fakeStore{
		salesID: 6, salesActive: true, salesHasRole: true,
		levelCode: "GLOBAL", ownerSalesID: 2,
		transferRes: &TransferResult{
			CustomerID: 1, FromOperatorID: 2, ToOperatorID: 6,
			QuotesMigrated: 3, TransferredAt: fixedNow(),
		},
	}
	svc := NewService(f, fixedNow)
	res, err := svc.Transfer(context.Background(), TransferInput{CustomerID: 1, ToOperatorID: 6, Confirm: true}, 1, "req-1")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res.QuotesMigrated != 3 || res.FromOperatorID != 2 || res.ToOperatorID != 6 {
		t.Fatalf("res=%+v", res)
	}
}

// ============================================================
// GenerateQuote — 类型校验
// ============================================================

func TestGenerateQuote_InvalidType(t *testing.T) {
	f := baseFake()
	svc := NewService(f, fixedNow)
	_, err := svc.GenerateQuote(context.Background(), GenerateQuoteInput{
		CustomerID: 1, QuoteType: "SPECIAL", // 9a 不支持
	}, scopeALL(), 1, "req-1")
	if !errors.Is(err, ErrInvalidQuoteType) {
		t.Fatalf("err=%v, want ErrInvalidQuoteType", err)
	}
}

func TestGenerateQuote_CustomerNotFound(t *testing.T) {
	f := baseFake()
	f.scopeFound = false
	svc := NewService(f, fixedNow)
	_, err := svc.GenerateQuote(context.Background(), GenerateQuoteInput{
		CustomerID: 99, QuoteType: QuoteTypeApply,
	}, scopeALL(), 1, "req-1")
	if !errors.Is(err, ErrCustomerNotFound) {
		t.Fatalf("err=%v, want ErrCustomerNotFound", err)
	}
}

func TestGenerateQuote_OutOfScope(t *testing.T) {
	f := baseFake()
	f.scopeInScope = false
	svc := NewService(f, fixedNow)
	_, err := svc.GenerateQuote(context.Background(), GenerateQuoteInput{
		CustomerID: 1, QuoteType: QuoteTypeApply,
	}, scopeSELF(2), 2, "req-1")
	if !errors.Is(err, ErrCustomerOutOfScope) {
		t.Fatalf("err=%v, want ErrCustomerOutOfScope", err)
	}
}

// ============================================================
// GenerateQuote — APPLY
// ============================================================

func TestGenerateQuote_ApplyFullItems(t *testing.T) {
	f := baseFake()
	svc := NewService(f, fixedNow)
	res, err := svc.GenerateQuote(context.Background(), GenerateQuoteInput{
		CustomerID: 1, QuoteType: QuoteTypeApply,
		// items 省略 → 全量带出
	}, scopeALL(), 1, "req-1")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res.ItemCount != 2 || res.QuoteType != QuoteTypeApply {
		t.Fatalf("res=%+v", res)
	}
	// 验证落库输入：floor 已算（3.00/0.85=3.52941176）
	if len(f.lastGenInput.Items) != 2 {
		t.Fatalf("items=%d", len(f.lastGenInput.Items))
	}
	if f.lastGenInput.Items[0].FloorPrice.StringFixed(8) != "3.52941176" {
		t.Fatalf("floor=%s", f.lastGenInput.Items[0].FloorPrice.StringFixed(8))
	}
}

func TestGenerateQuote_ApplyNoEffectiveBook(t *testing.T) {
	f := baseFake()
	f.bookFound = false
	svc := NewService(f, fixedNow)
	_, err := svc.GenerateQuote(context.Background(), GenerateQuoteInput{
		CustomerID: 1, QuoteType: QuoteTypeApply,
	}, scopeALL(), 1, "req-1")
	if !errors.Is(err, ErrNoEffectivePriceBook) {
		t.Fatalf("err=%v, want ErrNoEffectivePriceBook", err)
	}
}

func TestGenerateQuote_ApplyBelowFloor(t *testing.T) {
	f := baseFake()
	// 把 sku40 的 unit_price 改成低于 floor（floor=3.52941176）
	f.pbItems[0].UnitPrice = dec("1.00000000")
	svc := NewService(f, fixedNow)
	_, err := svc.GenerateQuote(context.Background(), GenerateQuoteInput{
		CustomerID: 1, QuoteType: QuoteTypeApply,
	}, scopeALL(), 1, "req-1")
	var floorErr *QuoteFloorError
	if !errors.As(err, &floorErr) {
		t.Fatalf("err=%v, want QuoteFloorError", err)
	}
	if !errors.Is(err, ErrBelowFloor) {
		t.Fatalf("err=%v, want ErrBelowFloor", err)
	}
	if len(floorErr.Violations) != 1 || floorErr.Violations[0].SKUID != 40 {
		t.Fatalf("violations=%+v", floorErr.Violations)
	}
	if floorErr.Violations[0].FloorPrice != "3.52941176" {
		t.Fatalf("floor=%s", floorErr.Violations[0].FloorPrice)
	}
}

func TestGenerateQuote_ApplyPartialSKUsNotInBook(t *testing.T) {
	f := baseFake()
	// LoadPriceBookItemsForSKUs 只返回 1 行（请求 2 个 SKU）
	f.pbItems = f.pbItems[:1]
	svc := NewService(f, fixedNow)
	_, err := svc.GenerateQuote(context.Background(), GenerateQuoteInput{
		CustomerID: 1, QuoteType: QuoteTypeApply,
		Items: []QuoteItemInput{{SKUID: 40}, {SKUID: 41}},
	}, scopeALL(), 1, "req-1")
	if !errors.Is(err, ErrInvalidQuoteType) {
		t.Fatalf("err=%v, want ErrInvalidQuoteType", err)
	}
}

// ============================================================
// GenerateQuote — CLONE
// ============================================================

func TestGenerateQuote_CloneSuccess(t *testing.T) {
	f := baseFake()
	f.quoteOwnerCustID = 1
	f.quoteOwnerSalesID = 2
	f.quoteOwnerFound = true
	f.quoteItems = []QuoteItemRow{
		{SKUID: 40, Currency: "CNY", UnitPrice: dec("3.80000000"), FloorPrice: dec("3.52941176")},
	}
	f.genRes = &GeneratedQuote{
		ID: 2, CustomerID: 1, VersionNo: 2, Status: QuoteStatusDraft,
		QuoteType: QuoteTypeClone, ItemCount: 1, OwnerSalesID: 2, CreatedAt: fixedNow(),
	}
	svc := NewService(f, fixedNow)
	srcID := int64(10)
	res, err := svc.GenerateQuote(context.Background(), GenerateQuoteInput{
		CustomerID: 1, QuoteType: QuoteTypeClone, SourceQuoteID: &srcID,
	}, scopeALL(), 1, "req-1")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res.QuoteType != QuoteTypeClone || res.ItemCount != 1 {
		t.Fatalf("res=%+v", res)
	}
}

func TestGenerateQuote_CloneCrossCustomer(t *testing.T) {
	f := baseFake()
	f.quoteOwnerCustID = 2 // 源报价是 customer 2 的
	f.quoteOwnerFound = true
	svc := NewService(f, fixedNow)
	srcID := int64(10)
	_, err := svc.GenerateQuote(context.Background(), GenerateQuoteInput{
		CustomerID: 1, QuoteType: QuoteTypeClone, SourceQuoteID: &srcID,
	}, scopeALL(), 1, "req-1")
	if !errors.Is(err, ErrSourceQuoteCrossCustomer) {
		t.Fatalf("err=%v, want ErrSourceQuoteCrossCustomer", err)
	}
}

func TestGenerateQuote_CloneSourceNotFound(t *testing.T) {
	f := baseFake()
	f.quoteOwnerFound = false
	svc := NewService(f, fixedNow)
	srcID := int64(99)
	_, err := svc.GenerateQuote(context.Background(), GenerateQuoteInput{
		CustomerID: 1, QuoteType: QuoteTypeClone, SourceQuoteID: &srcID,
	}, scopeALL(), 1, "req-1")
	if !errors.Is(err, ErrSourceQuoteNotFound) {
		t.Fatalf("err=%v, want ErrSourceQuoteNotFound", err)
	}
}

func TestGenerateQuote_CloneBelowFloor(t *testing.T) {
	f := baseFake()
	f.quoteOwnerCustID = 1
	f.quoteOwnerFound = true
	// 克隆时成本已上涨：unit_price 3.00 < floor 3.52941176
	f.quoteItems = []QuoteItemRow{
		{SKUID: 40, Currency: "CNY", UnitPrice: dec("3.00000000"), FloorPrice: dec("3.52941176")},
	}
	svc := NewService(f, fixedNow)
	srcID := int64(10)
	_, err := svc.GenerateQuote(context.Background(), GenerateQuoteInput{
		CustomerID: 1, QuoteType: QuoteTypeClone, SourceQuoteID: &srcID,
	}, scopeALL(), 1, "req-1")
	var floorErr *QuoteFloorError
	if !errors.As(err, &floorErr) {
		t.Fatalf("err=%v, want QuoteFloorError", err)
	}
	if len(floorErr.Violations) != 1 {
		t.Fatalf("violations=%+v", floorErr.Violations)
	}
}

// ============================================================
// GenerateQuote — TEMP
// ============================================================

func TestGenerateQuote_TempMissingValidTo(t *testing.T) {
	f := baseFake()
	svc := NewService(f, fixedNow)
	_, err := svc.GenerateQuote(context.Background(), GenerateQuoteInput{
		CustomerID: 1, QuoteType: QuoteTypeTemp,
		Items: []QuoteItemInput{{SKUID: 40, UnitPrice: "5.00"}},
	}, scopeALL(), 1, "req-1")
	if !errors.Is(err, ErrTempValidToRequired) {
		t.Fatalf("err=%v, want ErrTempValidToRequired", err)
	}
}

func TestGenerateQuote_TempBelowFloor(t *testing.T) {
	f := baseFake()
	svc := NewService(f, fixedNow)
	validTo := fixedNow().Add(7 * 24 * time.Hour)
	_, err := svc.GenerateQuote(context.Background(), GenerateQuoteInput{
		CustomerID: 1, QuoteType: QuoteTypeTemp, ValidTo: &validTo,
		Items: []QuoteItemInput{{SKUID: 40, UnitPrice: "1.00"}}, // floor=3.52941176
	}, scopeALL(), 1, "req-1")
	var floorErr *QuoteFloorError
	if !errors.As(err, &floorErr) {
		t.Fatalf("err=%v, want QuoteFloorError", err)
	}
	if floorErr.Violations[0].UnitPrice != "1.00000000" {
		t.Fatalf("unit_price=%s", floorErr.Violations[0].UnitPrice)
	}
}

func TestGenerateQuote_TempSuccess(t *testing.T) {
	f := baseFake()
	f.genRes = &GeneratedQuote{
		ID: 3, CustomerID: 1, VersionNo: 1, Status: QuoteStatusDraft,
		QuoteType: QuoteTypeTemp, ItemCount: 1, OwnerSalesID: 2, CreatedAt: fixedNow(),
	}
	svc := NewService(f, fixedNow)
	validTo := fixedNow().Add(7 * 24 * time.Hour)
	res, err := svc.GenerateQuote(context.Background(), GenerateQuoteInput{
		CustomerID: 1, QuoteType: QuoteTypeTemp, ValidTo: &validTo,
		Items: []QuoteItemInput{{SKUID: 40, UnitPrice: "5.00"}},
	}, scopeALL(), 1, "req-1")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res.QuoteType != QuoteTypeTemp {
		t.Fatalf("res=%+v", res)
	}
	// 验证 valid_until 透传
	if f.lastGenInput.ValidUntil == nil || !f.lastGenInput.ValidUntil.Equal(validTo) {
		t.Fatalf("valid_until=%v", f.lastGenInput.ValidUntil)
	}
}

// ============================================================
// 纯函数
// ============================================================

func TestValidQuoteType(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want bool
	}{
		{"APPLY", true},
		{"CLONE", true},
		{"TEMP", true},
		{"SPECIAL", false},
		{"CONTRACT", false},
		{"", false},
	} {
		if got := ValidQuoteType(tt.in); got != tt.want {
			t.Errorf("ValidQuoteType(%q)=%v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestCheckFloor_Boundary(t *testing.T) {
	// 边界：unit_price == floor → 不违规（LessThan 严格小于）
	items := []QuoteItemWithFloor{
		{SKUID: 40, SKUCode: "gpt-5", UnitPrice: dec("3.52941176"), FloorPrice: dec("3.52941176")},
		{SKUID: 41, SKUCode: "claude", UnitPrice: dec("3.52941175"), FloorPrice: dec("3.52941176")},
	}
	violations := CheckFloor(items)
	if len(violations) != 1 || violations[0].SKUID != 41 {
		t.Fatalf("violations=%+v, want only sku41", violations)
	}
}

func TestCalcFloor(t *testing.T) {
	floor, err := CalcFloor(dec("3.00000000"), dec("0.15"))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if floor.StringFixed(8) != "3.52941176" {
		t.Fatalf("floor=%s", floor.StringFixed(8))
	}
}
