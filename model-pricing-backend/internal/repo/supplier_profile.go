package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"model_bss/internal/domain/supplier"
)

var _ supplier.ProfileStore = (*SupplierRepo)(nil)

func (r *SupplierRepo) profileQuery(ctx context.Context, scope supplier.OwnerScope) *gorm.DB {
	tx := r.txOf(ctx).Table("supplier_profile sp").
		Joins("JOIN legal_subject ls ON ls.id = sp.subject_id").
		Joins("JOIN internal_staff owner_staff ON owner_staff.id = sp.owner_procurement_operator_id")
	return applyOwnerScope(tx, scope)
}

func (r *SupplierRepo) ListProfiles(ctx context.Context, scope supplier.OwnerScope, q supplier.ProfileQuery) (*supplier.ProfileListResult, error) {
	base := func() *gorm.DB {
		tx := r.profileQuery(ctx, scope)
		if q.Keyword != "" {
			tx = tx.Where("ls.legal_name ILIKE ?", "%"+q.Keyword+"%")
		}
		if q.QualStatus != "" {
			tx = tx.Where("sp.qual_status = ?", q.QualStatus)
		}
		if q.Status != "" {
			tx = tx.Where("sp.status = ?", q.Status)
		}
		return tx
	}

	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count supplier profiles: %w", err)
	}
	result := &supplier.ProfileListResult{List: []supplier.ProfileSummary{}, Total: total, Page: q.Page, Size: q.Size}
	if total == 0 {
		return result, nil
	}

	var rows []supplier.ProfileSummary
	err := profileListPageQuery(base(), q).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list supplier profiles: %w", err)
	}
	result.List = rows
	return result, nil
}

func profileListPageQuery(tx *gorm.DB, q supplier.ProfileQuery) *gorm.DB {
	return tx.Select(`sp.id, sp.subject_id, ls.legal_name,
		sp.settlement_currency, sp.qual_status, sp.settle_status, sp.status,
		sp.owner_procurement_operator_id, owner_staff.name AS owner_procurement_name,
		sp.updated_at,
		(SELECT COUNT(DISTINCT qi.sku_id) FROM quote_sheet qs
		 JOIN quote_item qi ON qi.quote_sheet_id = qs.id
		 WHERE qs.supplier_id = sp.id AND qs.status = 'EFFECTIVE'
		 AND qs.valid_from <= now() AND qs.valid_to > now()) AS sku_count,
		(SELECT COUNT(*) FROM quote_sheet qs
		 WHERE qs.supplier_id = sp.id AND qs.status = 'EFFECTIVE'
		 AND qs.valid_from <= now() AND qs.valid_to > now()) AS effective_quote_count,
		(SELECT COUNT(*) FROM quote_sheet qs
		 WHERE qs.supplier_id = sp.id AND qs.status = 'EFFECTIVE'
		 AND qs.valid_from <= now() AND qs.valid_to > now()
		 AND qs.valid_to <= now() + interval '30 days') AS expiring_soon`).
		Order("sp.updated_at DESC, sp.id DESC").Limit(q.Size).Offset((q.Page - 1) * q.Size)
}

func (r *SupplierRepo) GetProfile(ctx context.Context, scope supplier.OwnerScope, id int64) (*supplier.ProfileDetail, error) {
	var row struct {
		ID                         int64     `gorm:"column:id"`
		SubjectID                  int64     `gorm:"column:subject_id"`
		LegalName                  string    `gorm:"column:legal_name"`
		SettlementCurrency         string    `gorm:"column:settlement_currency"`
		SettleType                 string    `gorm:"column:settle_type"`
		BillingCycle               int       `gorm:"column:billing_cycle"`
		MinRecharge                string    `gorm:"column:min_recharge"`
		CreditLine                 string    `gorm:"column:credit_line"`
		CreditUsed                 string    `gorm:"column:credit_used"`
		DepositAmount              string    `gorm:"column:deposit_amount"`
		SettleStatus               string    `gorm:"column:settle_status"`
		QualStatus                 string    `gorm:"column:qual_status"`
		OwnerProcurementOperatorID int64     `gorm:"column:owner_procurement_operator_id"`
		OwnerProcurementName       string    `gorm:"column:owner_procurement_name"`
		Status                     string    `gorm:"column:status"`
		CreatedAt                  time.Time `gorm:"column:created_at"`
		UpdatedAt                  time.Time `gorm:"column:updated_at"`
	}
	err := r.profileQuery(ctx, scope).
		Select(`sp.id, sp.subject_id, ls.legal_name,
		 sp.settlement_currency, sp.settle_type, sp.billing_cycle,
		 sp.min_recharge::text AS min_recharge, sp.credit_line::text AS credit_line,
		 sp.credit_used::text AS credit_used, sp.deposit_amount::text AS deposit_amount,
		 sp.settle_status, sp.qual_status, sp.owner_procurement_operator_id,
		 owner_staff.name AS owner_procurement_name, sp.status, sp.created_at, sp.updated_at`).
		Where("sp.id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var exists struct{ ID int64 }
		existsErr := r.txOf(ctx).Table("supplier_profile").Select("id").Where("id = ?", id).Take(&exists).Error
		if errors.Is(existsErr, gorm.ErrRecordNotFound) {
			return nil, supplier.ErrNotFound
		}
		if existsErr != nil {
			return nil, fmt.Errorf("check supplier profile %d exists: %w", id, existsErr)
		}
		return nil, supplier.ErrProfileOutOfScope
	}
	if err != nil {
		return nil, fmt.Errorf("get supplier profile %d: %w", id, err)
	}
	return &supplier.ProfileDetail{
		ID: row.ID, SubjectID: row.SubjectID, LegalName: row.LegalName,
		SettlementCurrency: row.SettlementCurrency,
		SettleType:         row.SettleType, BillingCycle: row.BillingCycle,
		MinRecharge: row.MinRecharge, CreditLine: row.CreditLine,
		CreditUsed: row.CreditUsed, DepositAmount: row.DepositAmount,
		SettleStatus: row.SettleStatus, QualStatus: row.QualStatus,
		OwnerProcurementOperatorID: row.OwnerProcurementOperatorID,
		OwnerProcurementName:       row.OwnerProcurementName, Status: row.Status,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}
