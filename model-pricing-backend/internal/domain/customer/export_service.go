// Package customer 的 export_service.go：9b §5 导出卖货报价单。
//
// 范围（裁决 4）：
//   - **只做 XLSX**（excelize）；PDF 本批不支持，登记为 9b-② 遗留。
//   - TEMP 必须带水印；valid_until 过期 → 409。
//   - 其余类型（APPLY/CLONE/SPECIAL/CONTRACT）不带水印。
package customer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/xuri/excelize/v2"
)

// 领域错误。
var (
	ErrExportFormatUnsupported = errors.New("导出格式暂不支持（本批仅支持 xlsx；pdf 登记为 9b-② 遗留）")
	ErrQuoteExpired            = errors.New("临时报价已过期（valid_until < now），不能导出")
	ErrQuoteForExportNotFound  = errors.New("客户报价不存在")
)

// ExportService 客户报价导出。
type ExportService struct {
	store ExportStore
	now   func() time.Time
	loc   *time.Location
}

// NewExportService 构造；now=nil 用 time.Now；loc=nil 用 Asia/Shanghai。
func NewExportService(store ExportStore, now func() time.Time, loc *time.Location) *ExportService {
	if now == nil {
		now = time.Now
	}
	if loc == nil {
		loc, _ = time.LoadLocation("Asia/Shanghai")
	}
	return &ExportService{store: store, now: now, loc: loc}
}

// ExportStore 是导出所需只读视图。
type ExportStore interface {
	// LoadQuote 同 SpecialPriceStore 的读取口径。
	LoadQuote(ctx context.Context, quoteID int64) (*QuoteRow, error)
	// LoadQuoteItemsDetail 读明细。
	LoadQuoteItemsDetail(ctx context.Context, quoteID int64) ([]QuoteItemDetail, error)
	// LoadCustomerLegalName 读 legal_subject.legal_name（水印用）。
	LoadCustomerLegalName(ctx context.Context, customerID int64) (string, error)
}

// GenerateXLSX 生成 XLSX 文件流（直返 []byte）；TEMP 带水印。
func (s *ExportService) GenerateXLSX(ctx context.Context, quoteID int64, format string) ([]byte, error) {
	if format != "xlsx" {
		return nil, ErrExportFormatUnsupported
	}
	quote, err := s.store.LoadQuote(ctx, quoteID)
	if err != nil {
		return nil, fmt.Errorf("load quote %d: %w", quoteID, err)
	}
	if quote == nil {
		return nil, ErrQuoteForExportNotFound
	}
	// TEMP 才校验 valid_until（其余类型可以不带；但本规则只限 TEMP，
	// 因为 APPLY/CLONE 是长期价格条目，不该因临时校验被拒——裁决 5/EXPIRED 联动本批不做）。
	if quote.QuoteType == QuoteTypeTemp {
		if quote.ValidUntil != nil && s.now().After(*quote.ValidUntil) {
			return nil, ErrQuoteExpired
		}
	}
	items, err := s.store.LoadQuoteItemsDetail(ctx, quoteID)
	if err != nil {
		return nil, fmt.Errorf("load items: %w", err)
	}
	custName, err := s.store.LoadCustomerLegalName(ctx, quote.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("load customer name: %w", err)
	}
	return s.renderXLSX(quote, items, custName)
}

// renderXLSX 真正渲染（可独立测）。
func (s *ExportService) renderXLSX(quote *QuoteRow, items []QuoteItemDetail, custName string) ([]byte, error) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	const sheet = "Sheet1"
	// 标题行
	_ = f.SetCellValue(sheet, "A1", fmt.Sprintf("客户报价 #%d（v%d）", quote.ID, quote.VersionNo))
	_ = f.MergeCell(sheet, "A1", "F1")
	_ = f.SetCellValue(sheet, "A2", "客户")
	_ = f.SetCellValue(sheet, "B2", custName)
	_ = f.SetCellValue(sheet, "A3", "quote_type")
	_ = f.SetCellValue(sheet, "B3", quote.QuoteType)
	_ = f.SetCellValue(sheet, "A4", "status")
	_ = f.SetCellValue(sheet, "B4", quote.Status)
	_ = f.SetCellValue(sheet, "A5", "exported_at")
	_ = f.SetCellValue(sheet, "B5", s.now().In(s.loc).Format("2006-01-02 15:04:05"))
	if quote.ValidUntil != nil {
		expiry := quote.ValidUntil.In(s.loc).Format("2006-01-02")
		_ = f.SetCellValue(sheet, "A6", "valid_until")
		_ = f.SetCellValue(sheet, "B6", expiry)
	}

	// 明细表头
	_ = f.SetCellValue(sheet, "A8", "sku_id")
	_ = f.SetCellValue(sheet, "B8", "currency")
	_ = f.SetCellValue(sheet, "C8", "unit_price")
	_ = f.SetCellValue(sheet, "D8", "floor_price")
	_ = f.SetCellValue(sheet, "E8", "below_floor")
	row := 9
	for _, it := range items {
		_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), it.SKUID)
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), it.Currency)
		_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", row), it.UnitPrice.StringFixed(8))
		_ = f.SetCellValue(sheet, fmt.Sprintf("D%d", row), it.FloorPrice.StringFixed(8))
		_ = f.SetCellBool(sheet, fmt.Sprintf("E%d", row), it.UnitPrice.LessThan(it.FloorPrice))
		row++
	}

	// 水印：仅 TEMP。
	if quote.QuoteType == QuoteTypeTemp {
		validStr := "未知"
		if quote.ValidUntil != nil {
			validStr = quote.ValidUntil.In(s.loc).Format("2006-01-02")
		}
		mark := fmt.Sprintf("临时报价，有效期至 %s，仅限 %s 使用", validStr, custName)
		markRow := row + 2
		_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", markRow), mark)
		_ = f.MergeCell(sheet, fmt.Sprintf("A%d", markRow), fmt.Sprintf("F%d", markRow))
		style, err := f.NewStyle(&excelize.Style{
			Font:      &excelize.Font{Bold: true, Color: "FF0000", Size: 12},
			Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
			Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFFFF2CC"}},
		})
		if err == nil {
			_ = f.SetCellStyle(sheet, fmt.Sprintf("A%d", markRow), fmt.Sprintf("F%d", markRow), style)
		}
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, fmt.Errorf("write xlsx: %w", err)
	}
	return buf.Bytes(), nil
}
