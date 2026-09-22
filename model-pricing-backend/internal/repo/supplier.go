// Package repo 的 supplier.go：供应商身份解析的 GORM 实现。
package repo

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"model_bss/internal/domain/supplier"
	"model_bss/internal/infra/db"
)

// SupplierRepo 是 supplier.Store 的 GORM 实现。
type SupplierRepo struct {
	base *gorm.DB
}

// NewSupplierRepo 构造供应商仓储。
func NewSupplierRepo(base *gorm.DB) *SupplierRepo { return &SupplierRepo{base: base} }

var _ supplier.Store = (*SupplierRepo)(nil)

// txOf 优先使用 context 中的事务；取不到回退基础 DB（与 ModelRepo 同一约定，
// 保证幂等中间件注入的连接被复用）。
func (r *SupplierRepo) txOf(ctx context.Context) *gorm.DB {
	if tx := db.FromContext(ctx); tx != nil {
		return tx
	}
	return r.base
}

// listRow 是列表查询专用行模型（只用于扫描，绝不用于写入）。
type listRow struct {
	ID             int64   `gorm:"column:id"`
	SkuCode        string  `gorm:"column:sku_code"`
	ModelType      string  `gorm:"column:model_type"`
	NativeCurrency string  `gorm:"column:native_currency"`
	ContextWindow  *int    `gorm:"column:context_window"`
	TierTag        *string `gorm:"column:tier_tag"`
	VendorID       int64   `gorm:"column:vendor_id"`
	VendorName     string  `gorm:"column:vendor_name"`
	FamilyID       int64   `gorm:"column:family_id"`
	FamilyName     string  `gorm:"column:family_name"`
}

// priceVersionRow 是官方价版本扫描行。
type priceVersionRow struct {
	ID        int64  `gorm:"column:id"`
	SkuID     int64  `gorm:"column:sku_id"`
	VersionNo int    `gorm:"column:version_no"`
	Currency  string `gorm:"column:currency"`
	TaxBasis  string `gorm:"column:tax_basis"`
}

// priceComponentRow 是官方价组件扫描行；unit_price 走 ::text 避免 numeric → float 丢精度。
type priceComponentRow struct {
	PriceVersionID int64  `gorm:"column:price_version_id"`
	ComponentType  string `gorm:"column:component_type"`
	UnitPrice      string `gorm:"column:unit_price"`
}

// ListSupplierSKUs 返回 lifecycle_status ∈ (PUBLISHED, PURCHASABLE, PENDING_VERIFY) 的 SKU，
// 含官方价基准只读列；不含他人报价/平台成本/毛利。
//
// 查询次数固定为 3（本页 SKU / 官方价版本 / 官方价组件），不随页大小线性增长。
func (r *SupplierRepo) ListSupplierSKUs(ctx context.Context, q supplier.ListSupplierSKUQuery) (*supplier.ListSupplierSKUResult, error) {
	tx := r.txOf(ctx)

	query := tx.Table("model_sku AS s").
		Select(`s.id, s.sku_code, s.model_type, s.native_currency, s.context_window,
			s.tier_tag, s.vendor_id, v.name AS vendor_name, s.family_id, f.name AS family_name`).
		Joins("LEFT JOIN vendor v ON v.id = s.vendor_id").
		Joins("LEFT JOIN model_family f ON f.id = s.family_id").
		Where("s.lifecycle_status IN ?", []string{"PUBLISHED", "PURCHASABLE", "PENDING_VERIFY"})

	if q.Keyword != "" {
		query = query.Where("s.sku_code ILIKE ?", "%"+q.Keyword+"%")
	}
	if q.VendorID != nil {
		query = query.Where("s.vendor_id = ?", *q.VendorID)
	}
	if q.FamilyID != nil {
		query = query.Where("s.family_id = ?", *q.FamilyID)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}

	offset := (q.Page - 1) * q.Size
	var rows []listRow
	if err := query.Offset(offset).Limit(q.Size).Order("s.id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	list := make([]supplier.SKU, 0, len(rows))
	if len(rows) == 0 {
		return &supplier.ListSupplierSKUResult{List: list, Total: total, Page: q.Page, Size: q.Size}, nil
	}

	skuIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		skuIDs = append(skuIDs, row.ID)
	}

	// 官方价版本：一次取回本页全部 SKU 的当前版本（币种必须与 SKU 原厂币种一致）
	var pvs []priceVersionRow
	if err := tx.Table("price_version AS pv").
		Select("pv.id, pv.sku_id, pv.version_no, pv.currency, pv.tax_basis").
		Joins("JOIN model_sku ms ON ms.id = pv.sku_id").
		Where("pv.is_current = true AND pv.currency = ms.native_currency AND pv.sku_id IN ?", skuIDs).
		Scan(&pvs).Error; err != nil {
		return nil, fmt.Errorf("list price_version: %w", err)
	}

	priceBySku := make(map[int64]priceVersionRow, len(pvs))
	versionIDs := make([]int64, 0, len(pvs))
	for _, pv := range pvs {
		priceBySku[pv.SkuID] = pv
		versionIDs = append(versionIDs, pv.ID)
	}

	// 官方价组件：一次取回上述全部版本
	compsByVersion := make(map[int64][]supplier.PriceComponent)
	if len(versionIDs) > 0 {
		var comps []priceComponentRow
		if err := tx.Table("price_component").
			Select("price_version_id, component_type, unit_price::text AS unit_price").
			Where("price_version_id IN ?", versionIDs).
			Order("price_version_id, component_type").
			Scan(&comps).Error; err != nil {
			return nil, fmt.Errorf("list price_component: %w", err)
		}
		for _, c := range comps {
			compsByVersion[c.PriceVersionID] = append(compsByVersion[c.PriceVersionID],
				supplier.PriceComponent{ComponentType: c.ComponentType, UnitPrice: c.UnitPrice})
		}
	}

	for _, row := range rows {
		item := supplier.SKU{
			ID:             row.ID,
			SkuCode:        row.SkuCode,
			ModelType:      row.ModelType,
			NativeCurrency: row.NativeCurrency,
			ContextWindow:  row.ContextWindow,
			TierTag:        row.TierTag,
			VendorID:       row.VendorID,
			VendorName:     row.VendorName,
			FamilyID:       row.FamilyID,
			FamilyName:     row.FamilyName,
			ModelName:      row.FamilyName + " " + row.SkuCode,
		}

		// 官方价基准（只读）：is_current=true 且 currency = native_currency。
		// 组件为空时不标记 has_official_price——无组件价的版本对报价没有基准意义。
		if pv, ok := priceBySku[row.ID]; ok {
			if comps := compsByVersion[pv.ID]; len(comps) > 0 {
				item.OfficialPrice = &supplier.OfficialPrice{
					VersionNo:  pv.VersionNo,
					Currency:   pv.Currency,
					TaxBasis:   pv.TaxBasis,
					Components: comps,
				}
				item.HasOfficialPrice = true
			}
		}

		list = append(list, item)
	}

	return &supplier.ListSupplierSKUResult{
		List:  list,
		Total: total,
		Page:  q.Page,
		Size:  q.Size,
	}, nil
}

// FindByOperator 按 subject_operator.id 解析供应商身份。
func (r *SupplierRepo) FindByOperator(ctx context.Context, operatorID int64) (*supplier.Supplier, error) {
	type supplierRow struct {
		ID                         int64  `gorm:"column:id"`
		SubjectID                  int64  `gorm:"column:subject_id"`
		LegalName                  string `gorm:"column:legal_name"`
		OwnerProcurementOperatorID int64  `gorm:"column:owner_procurement_operator_id"`
		QualStatus                 string `gorm:"column:qual_status"`
		SettleStatus               string `gorm:"column:settle_status"`
		Status                     string `gorm:"column:status"`
	}
	var result supplierRow
	err := r.txOf(ctx).
		Table("subject_operator AS o").
		Select(`sp.id, sp.subject_id, ls.legal_name, sp.owner_procurement_operator_id,
			sp.qual_status, sp.settle_status, sp.status`).
		Joins("JOIN legal_subject ls ON ls.id = o.subject_id").
		Joins("JOIN supplier_profile sp ON sp.subject_id = o.subject_id").
		Where("o.id = ?", operatorID).
		Take(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find supplier by operator %d: %w", operatorID, err)
	}
	return &supplier.Supplier{
		ID:                         result.ID,
		SubjectID:                  result.SubjectID,
		LegalName:                  result.LegalName,
		OwnerProcurementOperatorID: result.OwnerProcurementOperatorID,
		QualStatus:                 result.QualStatus,
		SettleStatus:               result.SettleStatus,
		Status:                     result.Status,
	}, nil
}
