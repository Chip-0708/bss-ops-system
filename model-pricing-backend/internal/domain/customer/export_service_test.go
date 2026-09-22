package customer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// ============================================================
// fake store
// ============================================================

type fakeExportStore struct {
	quote     *QuoteRow
	quoteErr  error
	items     []QuoteItemDetail
	itemsErr  error
	legalName string
	legalErr  error
}

func (f *fakeExportStore) LoadQuote(_ context.Context, _ int64) (*QuoteRow, error) {
	return f.quote, f.quoteErr
}
func (f *fakeExportStore) LoadQuoteItemsDetail(_ context.Context, _ int64) ([]QuoteItemDetail, error) {
	return f.items, f.itemsErr
}
func (f *fakeExportStore) LoadCustomerLegalName(_ context.Context, _ int64) (string, error) {
	return f.legalName, f.legalErr
}

// ============================================================
// fixture
// ============================================================

func baseExportQuote() *QuoteRow {
	v := time.Now().Add(24 * time.Hour)
	return &QuoteRow{
		ID: 1, CustomerID: 1, VersionNo: 1, Status: QuoteStatusDraft,
		QuoteType: QuoteTypeTemp, ValidUntil: &v,
	}
}

func baseExportItems() []QuoteItemDetail {
	return []QuoteItemDetail{
		{SKUID: 40, Currency: "CNY", UnitPrice: dec("5.00000000"), FloorPrice: dec("3.52941176")},
		{SKUID: 41, Currency: "CNY", UnitPrice: dec("12.00000000"), FloorPrice: dec("10.58823529")},
	}
}

// ============================================================
// 测试
// ============================================================

func readXLSX(t *testing.T, buf []byte) (*excelize.File, string) {
	t.Helper()
	xls, err := excelize.OpenReader(io.NopCloser(bytes.NewReader(buf)))
	if err != nil {
		t.Fatalf("重开 XLSX 失败：%v", err)
	}
	sheet := xls.GetSheetName(0)
	if sheet == "" {
		t.Fatalf("no sheet")
	}
	return xls, sheet
}

func TestExport_Success(t *testing.T) {
	f := &fakeExportStore{
		quote:     baseExportQuote(),
		items:     baseExportItems(),
		legalName: "东信公司",
	}
	svc := NewExportService(f, fixedNow, nil)
	buf, err := svc.GenerateXLSX(context.Background(), 1, "xlsx")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(buf) == 0 {
		t.Fatalf("buffer empty")
	}
	xls, sheet := readXLSX(t, buf)
	defer func() { _ = xls.Close() }()
	// 标题 A1 含 quote id 与版本号
	title, _ := xls.GetCellValue(sheet, "A1")
	if !strings.Contains(title, "1") {
		t.Fatalf("A1=%q", title)
	}
	// 客户名 B2
	name, _ := xls.GetCellValue(sheet, "B2")
	if name != "东信公司" {
		t.Fatalf("B2=%q", name)
	}
	// quote_type B3
	qt, _ := xls.GetCellValue(sheet, "B3")
	if qt != "TEMP" {
		t.Fatalf("B3=%q", qt)
	}
	// 表头 A8
	hdr, _ := xls.GetCellValue(sheet, "A8")
	if hdr != "sku_id" {
		t.Fatalf("A8=%q", hdr)
	}
	// 行 9 sku 40；行 10 sku 41
	sku40, _ := xls.GetCellValue(sheet, "A9")
	if sku40 != "40" {
		t.Fatalf("A9=%q", sku40)
	}
	sku41, _ := xls.GetCellValue(sheet, "A10")
	if sku41 != "41" {
		t.Fatalf("A10=%q", sku41)
	}
}

func TestExport_QuoteNotFound(t *testing.T) {
	f := &fakeExportStore{quote: nil}
	svc := NewExportService(f, fixedNow, nil)
	_, err := svc.GenerateXLSX(context.Background(), 999, "xlsx")
	if !errors.Is(err, ErrQuoteForExportNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestExport_FormatUnsupported(t *testing.T) {
	f := &fakeExportStore{}
	svc := NewExportService(f, fixedNow, nil)
	_, err := svc.GenerateXLSX(context.Background(), 1, "pdf")
	if !errors.Is(err, ErrExportFormatUnsupported) {
		t.Fatalf("err=%v", err)
	}
}

func TestExport_Expired(t *testing.T) {
	q := baseExportQuote()
	past := time.Now().Add(-24 * time.Hour)
	q.ValidUntil = &past
	f := &fakeExportStore{
		quote: q,
		items: baseExportItems(),
	}
	svc := NewExportService(f, fixedNow, nil)
	_, err := svc.GenerateXLSX(context.Background(), 1, "xlsx")
	if !errors.Is(err, ErrQuoteExpired) {
		t.Fatalf("err=%v", err)
	}
}

func TestExport_ApplyNotExpiredEvenIfPastValidUntil(t *testing.T) {
	// 非 TEMP 不受 valid_until 拦截（APPLY/CLONE 长期价格）
	q := baseExportQuote()
	q.QuoteType = QuoteTypeApply
	past := time.Now().Add(-24 * time.Hour)
	q.ValidUntil = &past
	f := &fakeExportStore{
		quote:     q,
		items:     baseExportItems(),
		legalName: "东信公司",
	}
	svc := NewExportService(f, fixedNow, nil)
	_, err := svc.GenerateXLSX(context.Background(), 1, "xlsx")
	if err != nil {
		t.Fatalf("APPLY 即使有过期 valid_until 也不应 409：%v", err)
	}
}

func TestExport_TempWatermarkPresent(t *testing.T) {
	f := &fakeExportStore{
		quote:     baseExportQuote(), // TEMP
		items:     baseExportItems(),
		legalName: "东信公司",
	}
	svc := NewExportService(f, fixedNow, nil)
	buf, err := svc.GenerateXLSX(context.Background(), 1, "xlsx")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	xls, sheet := readXLSX(t, buf)
	defer func() { _ = xls.Close() }()
	// 水印在 items 之后 +2 行：表头 row 8，items row 9,10 → markRow = 13
	watermark, _ := xls.GetCellValue(sheet, "A13")
	if !strings.Contains(watermark, "临时报价") {
		t.Fatalf("watermark=%q（应含『临时报价』）", watermark)
	}
	if !strings.Contains(watermark, "东信公司") {
		t.Fatalf("watermark 未含客户名：%q", watermark)
	}
}

func TestExport_ApplyNoWatermark(t *testing.T) {
	q := baseExportQuote()
	q.QuoteType = QuoteTypeApply
	f := &fakeExportStore{
		quote:     q,
		items:     baseExportItems(),
		legalName: "东信公司",
	}
	svc := NewExportService(f, fixedNow, nil)
	buf, err := svc.GenerateXLSX(context.Background(), 1, "xlsx")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	xls, sheet := readXLSX(t, buf)
	defer func() { _ = xls.Close() }()
	// APPLY 不应有水印（markRow=13 应为空）
	wm, _ := xls.GetCellValue(sheet, "A13")
	if wm != "" {
		t.Fatalf("APPLY 不应有水印，A13=%q", wm)
	}
}

func TestExport_BelowFloorComputedOnFly(t *testing.T) {
	// customer_quote_item 无 below_floor 列，导出时应按 UnitPrice < FloorPrice 实时算
	items := []QuoteItemDetail{
		{SKUID: 40, Currency: "CNY", UnitPrice: dec("3.00000000"), FloorPrice: dec("3.52941176")},   // 破线
		{SKUID: 41, Currency: "CNY", UnitPrice: dec("12.00000000"), FloorPrice: dec("10.58823529")}, // 不破
	}
	f := &fakeExportStore{
		quote:     baseExportQuote(),
		items:     items,
		legalName: "东信",
	}
	svc := NewExportService(f, fixedNow, nil)
	buf, err := svc.GenerateXLSX(context.Background(), 1, "xlsx")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	xls, sheet := readXLSX(t, buf)
	defer func() { _ = xls.Close() }()
	e9, _ := xls.GetCellValue(sheet, "E9") // below_floor of sku40
	if !strings.EqualFold(e9, "TRUE") {
		t.Fatalf("E9=%q（应 true，破线）", e9)
	}
	e10, _ := xls.GetCellValue(sheet, "E10")
	if !strings.EqualFold(e10, "FALSE") {
		t.Fatalf("E10=%q（应 false）", e10)
	}
}
