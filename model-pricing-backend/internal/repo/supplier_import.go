// Package repo 的 supplier_import.go：批量导入（05-quotes.md §6）的 GORM 实现。
// 模板 SKU 查询（active/vendor/family/history 四种 scope）+ 动态组件列扫描。
// confirm 落库直接复用 QuoteRepo.CreateQuoteWithTodo（source='IMPORT'），不重复实现。
package repo

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"model_bss/internal/domain/supplier"
)

// ImportRepo 是 supplier.ImportStore 的 GORM 实现。
type ImportRepo struct {
	*SupplierRepo
}

// NewImportRepo 构造导入仓储。
func NewImportRepo(base *gorm.DB) *ImportRepo {
	return &ImportRepo{SupplierRepo: NewSupplierRepo(base)}
}

var _ supplier.ImportStore = (*ImportRepo)(nil)

// templateSkuRow 是模板 SKU 的扫描行。
type templateSkuRow struct {
	ID             int64  `gorm:"column:id"`
	SkuCode        string `gorm:"column:sku_code"`
	NativeCurrency string `gorm:"column:native_currency"`
	FamilyName     string `gorm:"column:family_name"`
}

// ListTemplateSKUs 模板 SKU 查询（05-quotes.md §6.1 四种 scope）。
// 隔离红线：绝不含他人报价/平台成本/毛利；history scope 只回本供应商报过价的 SKU。
func (r *ImportRepo) ListTemplateSKUs(ctx context.Context, supplierID int64, q supplier.TemplateQuery) ([]supplier.TemplateSKU, error) {
	tx := r.txOf(ctx).
		Table("model_sku s").
		Joins("LEFT JOIN model_family f ON f.id = s.family_id").
		Where("s.lifecycle_status IN ?", []string{"PUBLISHED", "PURCHASABLE", "PENDING_VERIFY"})

	switch q.Scope {
	case "history":
		// 本供应商报过价的 SKU（任意历史版本，去重）
		tx = tx.Where(`EXISTS (
			SELECT 1 FROM quote_item qi
			JOIN quote_sheet qs ON qs.id = qi.quote_sheet_id
			WHERE qi.sku_id = s.id AND qs.supplier_id = ?
		)`, supplierID)
	case "vendor":
		if q.VendorID != nil {
			tx = tx.Where("s.vendor_id = ?", *q.VendorID)
		}
	case "family":
		if q.FamilyID != nil {
			tx = tx.Where("s.family_id = ?", *q.FamilyID)
		}
	}

	var rows []templateSkuRow
	if err := tx.
		Select("s.id, s.sku_code, s.native_currency, f.name AS family_name").
		Order("s.id").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list template skus: %w", err)
	}

	out := make([]supplier.TemplateSKU, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}

	skuIDs := make([]int64, 0, len(rows))
	for _, rr := range rows {
		skuIDs = append(skuIDs, rr.ID)
	}

	// 官方价只读列（is_current 且同币种）
	officials, err := NewQuoteRepo(r.base).FindOfficialComponents(ctx, skuIDs)
	if err != nil {
		return nil, err
	}

	// 该供应商各 SKU 最近一次报价的到期日（只读参考列 last_valid_to）
	type lastQuoteRow struct {
		SkuID   int64     `gorm:"column:sku_id"`
		ValidTo time.Time `gorm:"column:valid_to"`
	}
	lastValidTo := make(map[int64]time.Time)
	var lq []lastQuoteRow
	if err := r.txOf(ctx).
		Table("quote_item qi").
		Select("qi.sku_id, MAX(qs.valid_to) AS valid_to").
		Joins("JOIN quote_sheet qs ON qs.id = qi.quote_sheet_id").
		Where("qs.supplier_id = ? AND qi.sku_id IN ?", supplierID, skuIDs).
		Group("qi.sku_id").
		Scan(&lq).Error; err != nil {
		return nil, fmt.Errorf("list last valid_to: %w", err)
	}
	for _, l := range lq {
		lastValidTo[l.SkuID] = l.ValidTo
	}

	for _, rr := range rows {
		item := supplier.TemplateSKU{
			ID:             rr.ID,
			SkuCode:        rr.SkuCode,
			ModelName:      rr.FamilyName + " " + rr.SkuCode,
			NativeCurrency: rr.NativeCurrency,
			OfficialPrice:  map[string]string{},
		}
		for _, oc := range officials[rr.ID] {
			item.OfficialPrice[oc.ComponentType] = oc.UnitPrice.String()
		}
		if t, ok := lastValidTo[rr.ID]; ok {
			item.LastValidTo = &t
		}
		out = append(out, item)
	}
	return out, nil
}

// ListComponentTypes 扫描可报价 SKU 的官方价组件并集（排序后，列顺序稳定）。
func (r *ImportRepo) ListComponentTypes(ctx context.Context) ([]string, error) {
	var cts []string
	if err := r.txOf(ctx).
		Table("price_component pc").
		Joins("JOIN price_version pv ON pv.id = pc.price_version_id").
		Joins("JOIN model_sku ms ON ms.id = pv.sku_id").
		Where("pv.is_current = true AND ms.lifecycle_status IN ?",
			[]string{"PUBLISHED", "PURCHASABLE", "PENDING_VERIFY"}).
		Distinct("pc.component_type").
		Order("pc.component_type").
		Pluck("pc.component_type", &cts).Error; err != nil {
		return nil, fmt.Errorf("list component types: %w", err)
	}
	return cts, nil
}
