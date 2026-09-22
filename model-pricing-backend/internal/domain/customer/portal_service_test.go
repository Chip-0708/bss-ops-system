// Package customer 的 portal_service_test.go：9c §6 客户门户 6 接口单测。
//
// 覆盖（对应 stage9c 提示词 §8 测试清单）：
//
//  1. price-book 返回当前生效（level→book 命中）/ 404 客户不存在 / 404 无生效价目表。
//  2. price-book 不含 floor_price/unit_cost/margin/baseline（field_mask 裁决 6，
//     DTO 序列化后 JSON 无这些 key——变异验证 #3 锚点）。
//  3. quotes 只返回自己报价 + 合同（行级过滤在 store，本层信任；fake store 捕获 customerID 断言）。
//  4. accept APPROVED → 合同生成；非 APPROVED → 409；他人报价 → 404；已过期 → 409。
//  5. billing 返回授信/押金/账期 + bills=[] 占位。
//  6. notifications 按 created_at DESC 分页（fake store 自己排，本层只透传）。
//  7. home 返回余额 + 待处理 + 未读 + 常用模型（常用模型不含成本/毛利字段）。
//  8. **变异验证 #2 锚点**：home 的 common_models 只从 level→EFFECTIVE price_book_item 取
//     （fake store 捕获 levelCode，断言它 == 该客户的 level_code）。
package customer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// fakePortalStore 是 PortalStore 的内存实现，捕获调用参数以便断言行级过滤。
type fakePortalStore struct {
	// 捕获调用
	gotCustomerIDForLevel   int64
	gotLevelForPriceBook    string
	gotCustomerIDForQuotes  int64
	gotQuoteIDForAccept     int64
	gotCustomerIDForAccept  int64
	gotCustomerIDForBilling int64
	gotCustomerIDForNotif   int64
	gotCustomerIDForHome    int64
	gotLevelForHome         string
	gotAcceptTxItems        []QuoteItemDetail
	gotAcceptTxValidUntil   *time.Time
	gotAcceptTxOperatorID   int64
	gotAcceptTxRequestID    string

	// 返回
	levelCode     string
	levelFound    bool
	priceBookView *PortalPriceBookView
	quotesResult  *PortalQuoteListResult
	quoteRow      *QuoteRow
	quoteItems    []QuoteItemDetail
	acceptResult  *PortalAcceptResult
	billingView   *PortalBillingView
	notifResult   *PortalNotificationListResult
	homeView      *PortalHomeView

	// 注入错误
	errLoadLevel error
	errAcceptTx  error
}

func (f *fakePortalStore) LoadCustomerLevelCode(ctx context.Context, customerID int64) (string, bool, error) {
	f.gotCustomerIDForLevel = customerID
	return f.levelCode, f.levelFound, f.errLoadLevel
}

func (f *fakePortalStore) LoadEffectivePriceBookView(ctx context.Context, levelCode string) (*PortalPriceBookView, error) {
	f.gotLevelForPriceBook = levelCode
	return f.priceBookView, nil
}

func (f *fakePortalStore) ListPortalQuotes(ctx context.Context, customerID int64, q PortalQuoteQuery) (*PortalQuoteListResult, error) {
	f.gotCustomerIDForQuotes = customerID
	return f.quotesResult, nil
}

func (f *fakePortalStore) LoadQuoteForAccept(ctx context.Context, quoteID int64) (*QuoteRow, []QuoteItemDetail, error) {
	f.gotQuoteIDForAccept = quoteID
	return f.quoteRow, f.quoteItems, nil
}

func (f *fakePortalStore) AcceptQuoteTx(ctx context.Context, quoteID, customerID int64, items []QuoteItemDetail, validUntil *time.Time, operatorID int64, requestID string, now time.Time) (*PortalAcceptResult, error) {
	f.gotQuoteIDForAccept = quoteID
	f.gotCustomerIDForAccept = customerID
	f.gotAcceptTxItems = items
	f.gotAcceptTxValidUntil = validUntil
	f.gotAcceptTxOperatorID = operatorID
	f.gotAcceptTxRequestID = requestID
	if f.errAcceptTx != nil {
		return nil, f.errAcceptTx
	}
	return f.acceptResult, nil
}

func (f *fakePortalStore) LoadBilling(ctx context.Context, customerID int64) (*PortalBillingView, error) {
	f.gotCustomerIDForBilling = customerID
	return f.billingView, nil
}

func (f *fakePortalStore) ListNotifications(ctx context.Context, customerID int64, q PortalPageQuery) (*PortalNotificationListResult, error) {
	f.gotCustomerIDForNotif = customerID
	return f.notifResult, nil
}

func (f *fakePortalStore) LoadHome(ctx context.Context, customerID int64, levelCode string) (*PortalHomeView, error) {
	f.gotCustomerIDForHome = customerID
	f.gotLevelForHome = levelCode
	return f.homeView, nil
}

// ------------------------------------------------------------
// 1. GetPriceBook
// ------------------------------------------------------------

func TestPortal_GetPriceBook_Found(t *testing.T) {
	f := &fakePortalStore{
		levelCode:  "GLOBAL",
		levelFound: true,
		priceBookView: &PortalPriceBookView{
			LevelCode: "GLOBAL", VersionNo: 1, Currency: "CNY",
			Items: []PortalPriceBookItem{
				{SKUID: 40, SKUCode: "gpt-4o", Currency: "CNY", UnitPrice: "3.52941176"},
				{SKUID: 41, SKUCode: "gpt-4o-mini", Currency: "CNY", UnitPrice: "0.10000000"},
			},
		},
	}
	svc := NewPortalService(f, nil)

	view, err := svc.GetPriceBook(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if view.LevelCode != "GLOBAL" || view.VersionNo != 1 || len(view.Items) != 2 {
		t.Fatalf("unexpected view: %+v", view)
	}
	if f.gotCustomerIDForLevel != 1 {
		t.Fatalf("customerID 未透传：%d", f.gotCustomerIDForLevel)
	}
	if f.gotLevelForPriceBook != "GLOBAL" {
		t.Fatalf("levelCode 未透传：%q", f.gotLevelForPriceBook)
	}
}

func TestPortal_GetPriceBook_CustomerNotFound(t *testing.T) {
	f := &fakePortalStore{levelFound: false}
	svc := NewPortalService(f, nil)
	_, err := svc.GetPriceBook(context.Background(), 999)
	if !errors.Is(err, ErrPortalCustomerNotFound) {
		t.Fatalf("expected ErrPortalCustomerNotFound, got %v", err)
	}
}

func TestPortal_GetPriceBook_NoEffectiveBook(t *testing.T) {
	f := &fakePortalStore{levelCode: "GLOBAL", levelFound: true, priceBookView: nil}
	svc := NewPortalService(f, nil)
	_, err := svc.GetPriceBook(context.Background(), 1)
	if !errors.Is(err, ErrPortalNoEffectivePriceBook) {
		t.Fatalf("expected ErrPortalNoEffectivePriceBook, got %v", err)
	}
}

// TestPortal_GetPriceBook_NoCostFields 验证 field_mask 裁决 6：
// PortalPriceBookView JSON 序列化后**不得**含 unit_cost/floor_price/margin/baseline/cost_before/cost_after/cost_delta_pct。
//
// 变异验证 #3 锚点：若后续有人给 DTO 加回 FloorPrice / UnitCost / Margin 字段，本测试应红。
func TestPortal_GetPriceBook_NoCostFields(t *testing.T) {
	view := &PortalPriceBookView{
		LevelCode: "GLOBAL", VersionNo: 1, Currency: "CNY",
		Items: []PortalPriceBookItem{
			{SKUID: 40, SKUCode: "gpt-4o", Currency: "CNY", UnitPrice: "3.52941176"},
		},
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// 顶层 key
	forbiddenTop := []string{"unit_cost", "floor_price", "margin", "baseline", "cost_before", "cost_after", "cost_delta_pct"}
	for _, k := range forbiddenTop {
		if _, exists := m[k]; exists {
			t.Fatalf("PortalPriceBookView 不应包含字段 %q", k)
		}
	}
	// items 元素 key
	items, ok := m["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("items 缺失")
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("items[0] 非 object")
	}
	for _, k := range forbiddenTop {
		if _, exists := item[k]; exists {
			t.Fatalf("PortalPriceBookItem 不应包含字段 %q（变异验证 #3 锚点）", k)
		}
	}
	// 应有字段
	for _, k := range []string{"sku_id", "sku_code", "currency", "unit_price"} {
		if _, exists := item[k]; !exists {
			t.Fatalf("PortalPriceBookItem 应包含字段 %q", k)
		}
	}
}

// ------------------------------------------------------------
// 2. ListQuotes
// ------------------------------------------------------------

func TestPortal_ListQuotes_RowFilter(t *testing.T) {
	now := time.Now().UTC()
	f := &fakePortalStore{
		quotesResult: &PortalQuoteListResult{
			List: []PortalQuoteItem{
				{ID: 4, VersionNo: 4, Status: "DRAFT", QuoteType: "TEMP", ValidUntil: &now, ItemCount: 2, TotalAmount: "6.66", Currency: "CNY", SourceKind: "QUOTE"},
				{ID: 100, VersionNo: 1, Status: "EFFECTIVE", QuoteType: "CONTRACT", ItemCount: 1, TotalAmount: "3.53", Currency: "CNY", SourceKind: "CONTRACT"},
			},
			Total: 2, Page: 1, Size: 20,
		},
	}
	svc := NewPortalService(f, nil)

	res, err := svc.ListQuotes(context.Background(), 7, PortalQuoteQuery{Page: 1, Size: 20})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Total != 2 || len(res.List) != 2 {
		t.Fatalf("unexpected res: %+v", res)
	}
	if f.gotCustomerIDForQuotes != 7 {
		t.Fatalf("customerID 未透传：%d", f.gotCustomerIDForQuotes)
	}
	// 合同行 SourceKind=CONTRACT
	if res.List[1].SourceKind != "CONTRACT" {
		t.Fatalf("合同行 SourceKind 应为 CONTRACT，got %q", res.List[1].SourceKind)
	}
}

func TestPortal_ListQuotes_PageDefaults(t *testing.T) {
	f := &fakePortalStore{quotesResult: &PortalQuoteListResult{List: nil, Total: 0, Page: 1, Size: 20}}
	svc := NewPortalService(f, nil)
	_, _ = svc.ListQuotes(context.Background(), 1, PortalQuoteQuery{Page: 0, Size: 0})
	// 本层归一化默认值后透传给 store——fake 看不到，但归一化是分支，须走通。
}

// ------------------------------------------------------------
// 3. AcceptQuote
// ------------------------------------------------------------

func TestPortal_AcceptQuote_HappyPath(t *testing.T) {
	now := time.Now().UTC()
	items := []QuoteItemDetail{
		{SKUID: 40, Currency: "CNY", UnitPrice: decimal.RequireFromString("3.52941176")},
		{SKUID: 41, Currency: "CNY", UnitPrice: decimal.RequireFromString("0.10000000")},
	}
	f := &fakePortalStore{
		quoteRow: &QuoteRow{
			ID: 2, CustomerID: 1, VersionNo: 1, Status: QuoteStatusApproved,
			QuoteType: "APPLY", ValidUntil: nil, OwnerSalesID: 7,
		},
		quoteItems:   items,
		acceptResult: &PortalAcceptResult{QuoteID: 2, NewStatus: "EFFECTIVE", ContractCnt: 2},
	}
	svc := NewPortalService(f, func() time.Time { return now })

	res, err := svc.AcceptQuote(context.Background(), 2, 1, 1, "req-001")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.NewStatus != "EFFECTIVE" || res.ContractCnt != 2 {
		t.Fatalf("unexpected res: %+v", res)
	}
	// 断言行级过滤 + 操作员 + requestID 透传
	if f.gotQuoteIDForAccept != 2 || f.gotCustomerIDForAccept != 1 {
		t.Fatalf("行级过滤未透传：quote=%d customer=%d", f.gotQuoteIDForAccept, f.gotCustomerIDForAccept)
	}
	if f.gotAcceptTxOperatorID != 1 || f.gotAcceptTxRequestID != "req-001" {
		t.Fatalf("operator/requestID 未透传")
	}
	if len(f.gotAcceptTxItems) != 2 {
		t.Fatalf("items 未透传：%d", len(f.gotAcceptTxItems))
	}
}

func TestPortal_AcceptQuote_SpecialPriceApproved(t *testing.T) {
	// 特价单：status=DRAFT 但 special_price_status=APPROVED → 可接受。
	sp := SpecialPriceApproved
	now := time.Now().UTC()
	f := &fakePortalStore{
		quoteRow: &QuoteRow{
			ID: 1, CustomerID: 1, VersionNo: 1, Status: QuoteStatusDraft,
			QuoteType: "TEMP", SpecialPriceStatus: &sp, ValidUntil: nil,
		},
		quoteItems:   []QuoteItemDetail{{SKUID: 40, Currency: "CNY", UnitPrice: decimal.RequireFromString("3.0")}},
		acceptResult: &PortalAcceptResult{QuoteID: 1, NewStatus: "EFFECTIVE", ContractCnt: 1},
	}
	svc := NewPortalService(f, func() time.Time { return now })
	res, err := svc.AcceptQuote(context.Background(), 1, 1, 1, "req")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.ContractCnt != 1 {
		t.Fatalf("unexpected res: %+v", res)
	}
}

func TestPortal_AcceptQuote_NotFound_OtherCustomer(t *testing.T) {
	// 行级过滤命中：他人报价 → 404。
	f := &fakePortalStore{
		quoteRow: &QuoteRow{ID: 9, CustomerID: 99, Status: QuoteStatusApproved},
	}
	svc := NewPortalService(f, nil)
	_, err := svc.AcceptQuote(context.Background(), 9, 1, 1, "req")
	if !errors.Is(err, ErrPortalQuoteNotFound) {
		t.Fatalf("expected ErrPortalQuoteNotFound（他人报价 404），got %v", err)
	}
}

func TestPortal_AcceptQuote_NotFound_Missing(t *testing.T) {
	f := &fakePortalStore{quoteRow: nil}
	svc := NewPortalService(f, nil)
	_, err := svc.AcceptQuote(context.Background(), 999, 1, 1, "req")
	if !errors.Is(err, ErrPortalQuoteNotFound) {
		t.Fatalf("expected ErrPortalQuoteNotFound, got %v", err)
	}
}

func TestPortal_AcceptQuote_NotApprovable(t *testing.T) {
	f := &fakePortalStore{
		quoteRow: &QuoteRow{ID: 2, CustomerID: 1, Status: QuoteStatusDraft, QuoteType: "APPLY"},
	}
	svc := NewPortalService(f, nil)
	_, err := svc.AcceptQuote(context.Background(), 2, 1, 1, "req")
	if !errors.Is(err, ErrPortalQuoteNotApprovable) {
		t.Fatalf("expected ErrPortalQuoteNotApprovable, got %v", err)
	}
}

func TestPortal_AcceptQuote_Expired(t *testing.T) {
	now := time.Now().UTC()
	yesterday := now.Add(-24 * time.Hour)
	f := &fakePortalStore{
		quoteRow: &QuoteRow{
			ID: 3, CustomerID: 1, Status: QuoteStatusApproved,
			QuoteType: "TEMP", ValidUntil: &yesterday,
		},
	}
	svc := NewPortalService(f, func() time.Time { return now })
	_, err := svc.AcceptQuote(context.Background(), 3, 1, 1, "req")
	if !errors.Is(err, ErrPortalQuoteExpired) {
		t.Fatalf("expected ErrPortalQuoteExpired, got %v", err)
	}
}

func TestPortal_AcceptQuote_NotExpired_NoValidUntil(t *testing.T) {
	// APPLY/CLONE 无 valid_until → 不过期。
	now := time.Now().UTC()
	f := &fakePortalStore{
		quoteRow: &QuoteRow{
			ID: 2, CustomerID: 1, Status: QuoteStatusApproved,
			QuoteType: "APPLY", ValidUntil: nil,
		},
		quoteItems:   []QuoteItemDetail{{SKUID: 40, Currency: "CNY", UnitPrice: decimal.RequireFromString("3.5")}},
		acceptResult: &PortalAcceptResult{QuoteID: 2, NewStatus: "EFFECTIVE", ContractCnt: 1},
	}
	svc := NewPortalService(f, func() time.Time { return now })
	_, err := svc.AcceptQuote(context.Background(), 2, 1, 1, "req")
	if err != nil {
		t.Fatalf("APPLY 无 valid_until 不应判过期：%v", err)
	}
}

// ------------------------------------------------------------
// 4. GetBilling
// ------------------------------------------------------------

func TestPortal_GetBilling(t *testing.T) {
	f := &fakePortalStore{
		billingView: &PortalBillingView{
			CreditLimit: "100000.00", CreditUsed: "12345.67",
			DepositAmount: "5000.00", DepositStatus: "PAID", BillingCycle: 30,
			Bills: []interface{}{},
		},
	}
	svc := NewPortalService(f, nil)
	view, err := svc.GetBilling(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if view.CreditLimit != "100000.00" || view.DepositStatus != "PAID" || view.BillingCycle != 30 {
		t.Fatalf("unexpected view: %+v", view)
	}
	if view.Bills == nil {
		t.Fatalf("Bills 应为空数组占位（9c-①），不是 nil")
	}
	if len(view.Bills) != 0 {
		t.Fatalf("Bills 应为空：%d", len(view.Bills))
	}
	if f.gotCustomerIDForBilling != 1 {
		t.Fatalf("customerID 未透传")
	}
}

// ------------------------------------------------------------
// 5. ListNotifications
// ------------------------------------------------------------

func TestPortal_ListNotifications_RowFilter(t *testing.T) {
	now := time.Now().UTC()
	readAt := now.Add(-24 * time.Hour)
	f := &fakePortalStore{
		notifResult: &PortalNotificationListResult{
			List: []PortalNotificationItem{
				{ID: 2, Type: "DEPRECATE", Title: "模型退役", Content: "gpt-3.5 将退役", ReadAt: nil, CreatedAt: now},
				{ID: 1, Type: "PRICE_UP", Title: "价格调整", Content: "gpt-4o 涨价", ReadAt: &readAt, CreatedAt: readAt},
			},
			Total: 2, Page: 1, Size: 20,
		},
	}
	svc := NewPortalService(f, nil)
	res, err := svc.ListNotifications(context.Background(), 7, PortalPageQuery{Page: 1, Size: 20})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Total != 2 || len(res.List) != 2 {
		t.Fatalf("unexpected res: %+v", res)
	}
	if f.gotCustomerIDForNotif != 7 {
		t.Fatalf("customerID 未透传：%d", f.gotCustomerIDForNotif)
	}
	// 应 created_at DESC（id=2 在前）
	if res.List[0].ID != 2 || res.List[1].ID != 1 {
		t.Fatalf("顺序应 created_at DESC：%+v", res.List)
	}
}

// ------------------------------------------------------------
// 6. GetHome
// ------------------------------------------------------------

func TestPortal_GetHome_Aggregate(t *testing.T) {
	f := &fakePortalStore{
		levelCode:  "GLOBAL",
		levelFound: true,
		homeView: &PortalHomeView{
			Balance: PortalHomeBalance{
				CreditLimit: "100000.00", CreditUsed: "12345.67",
				DepositAmount: "5000.00", DepositStatus: "PAID",
			},
			PendingCount:        3,
			UnreadNotifications: 1,
			CommonModels: []PortalHomeCommonModel{
				{SKUID: 40, SKUCode: "gpt-4o", Currency: "CNY"},
				{SKUID: 41, SKUCode: "gpt-4o-mini", Currency: "CNY"},
			},
		},
	}
	svc := NewPortalService(f, nil)
	view, err := svc.GetHome(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if view.Balance.CreditLimit != "100000.00" || view.PendingCount != 3 || view.UnreadNotifications != 1 {
		t.Fatalf("unexpected view: %+v", view)
	}
	if len(view.CommonModels) != 2 {
		t.Fatalf("common_models 应有 2 项：%d", len(view.CommonModels))
	}
	if f.gotCustomerIDForHome != 1 {
		t.Fatalf("customerID 未透传")
	}
}

// TestPortal_GetHome_PassesLevelCodeToRepo 变异验证 #2 锁点：
// service.GetHome 必须先查 level_code 并透传给 store.LoadHome。
// 若 service 被改为不传 levelCode（或 repo 忽略 levelCode 返回所有 SKU），本测试应红。
func TestPortal_GetHome_PassesLevelCodeToRepo(t *testing.T) {
	f := &fakePortalStore{
		levelCode:  "VIP_PLUS",
		levelFound: true,
		homeView:   &PortalHomeView{},
	}
	svc := NewPortalService(f, nil)
	_, err := svc.GetHome(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if f.gotCustomerIDForLevel != 42 {
		t.Fatalf("service 未先查 level_code：got customerID=%d", f.gotCustomerIDForLevel)
	}
	if f.gotLevelForHome != "VIP_PLUS" {
		t.Fatalf("service 未将 level_code 透传给 store.LoadHome：got %q（变异验证 #2 锁点）", f.gotLevelForHome)
	}
}

// TestPortal_GetHome_CustomerNotFound home 的客户不存在 → 404。
func TestPortal_GetHome_CustomerNotFound(t *testing.T) {
	f := &fakePortalStore{levelFound: false}
	svc := NewPortalService(f, nil)
	_, err := svc.GetHome(context.Background(), 999)
	if !errors.Is(err, ErrPortalCustomerNotFound) {
		t.Fatalf("expected ErrPortalCustomerNotFound, got %v", err)
	}
}

// TestPortal_GetHome_CommonModels_NoCostFields 变异验证 #2+#3 组合锚点：
// PortalHomeCommonModel JSON 序列化后**不得**含 unit_price / unit_cost / floor_price / margin / baseline。
// 且字段只应有 sku_id/sku_code/currency（SKU 标识）。
func TestPortal_GetHome_CommonModels_NoCostFields(t *testing.T) {
	view := &PortalHomeView{
		CommonModels: []PortalHomeCommonModel{
			{SKUID: 40, SKUCode: "gpt-4o", Currency: "CNY"},
		},
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	models, ok := m["common_models"].([]any)
	if !ok || len(models) == 0 {
		t.Fatalf("common_models 缺失")
	}
	cm, ok := models[0].(map[string]any)
	if !ok {
		t.Fatalf("common_models[0] 非 object")
	}
	forbidden := []string{"unit_price", "unit_cost", "floor_price", "margin", "baseline", "cost_before", "cost_after", "cost_delta_pct"}
	for _, k := range forbidden {
		if _, exists := cm[k]; exists {
			t.Fatalf("PortalHomeCommonModel 不应包含字段 %q（变异验证 #2+#3 锚点）", k)
		}
	}
	for _, k := range []string{"sku_id", "sku_code", "currency"} {
		if _, exists := cm[k]; !exists {
			t.Fatalf("PortalHomeCommonModel 应包含字段 %q", k)
		}
	}
	// 且只有这 3 个字段（防未来添加 cost 而不改测试）
	if len(cm) != 3 {
		t.Fatalf("PortalHomeCommonModel 应只有 3 字段，实际 %d：%v", len(cm), cm)
	}
}
