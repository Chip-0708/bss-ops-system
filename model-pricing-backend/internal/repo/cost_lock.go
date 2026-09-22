// Package repo 的 cost_lock.go：手动锁定的 GORM 实现（06-cost.md §6，6d-3）。
// 与所有其他仓储同一纪律：写操作统一经 txOf(ctx)（幂等中间件注入的事务优先——
// 版本切换、审计行、幂等记录三者在同一事务里提交/回滚）。
package repo

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"

	"model_bss/internal/domain/cost"
)

// CostLockRepo 是 cost.LockStore 的 GORM 实现（窄——只做锁定需要的那几个查询/写入）。
// 复用 SupplierRepo 的 txOf 与审计行模型（与 CostBaselineRepo 同一字段集合）。
type CostLockRepo struct {
	*SupplierRepo
}

// NewCostLockRepo 构造锁定仓储。
func NewCostLockRepo(base *gorm.DB) *CostLockRepo {
	return &CostLockRepo{SupplierRepo: NewSupplierRepo(base)}
}

var _ cost.LockStore = (*CostLockRepo)(nil)

// SupplierExists 校验 supplier_profile.id 存在（404 门槛）。
func (r *CostLockRepo) SupplierExists(ctx context.Context, supplierID int64) (bool, error) {
	var n int64
	if err := r.txOf(ctx).Table("supplier_profile").Where("id = ?", supplierID).Count(&n).Error; err != nil {
		return false, fmt.Errorf("check supplier_profile id=%d: %w", supplierID, err)
	}
	return n > 0, nil
}

// SupplierStatusOf 读 supplier_profile 的三态（qual/settle/status）。
// 语义与 LoadRecalcInput 的 statuses 加载完全同构——同一列名、同一表，没有任何旁路。
func (r *CostLockRepo) SupplierStatusOf(ctx context.Context, supplierID int64) (cost.SupplierStatus, bool, error) {
	var rows []cost.SupplierStatus
	if err := r.txOf(ctx).Table("supplier_profile").
		Select("id AS supplier_id, qual_status, settle_status, status").
		Where("id = ?", supplierID).Scan(&rows).Error; err != nil {
		return cost.SupplierStatus{}, false, fmt.Errorf("load supplier_profile id=%d: %w", supplierID, err)
	}
	if len(rows) == 0 {
		return cost.SupplierStatus{}, false, nil
	}
	return rows[0], true, nil
}

// HasEffectiveQuoteOnSKU 该供应商在该 SKU 上是否有当前 EFFECTIVE 报价。
// 判定基准是 quote_item（按 SKU 维度）在 EFFECTIVE 的 quote_sheet（按单维度）里——
// 只用 quote_sheet 单维度会误判（一张单覆盖多 SKU，其他 SKU 生效不等于本 SKU 生效）。
func (r *CostLockRepo) HasEffectiveQuoteOnSKU(ctx context.Context, supplierID, skuID int64) (bool, error) {
	var n int64
	if err := r.txOf(ctx).Table("quote_item qi").
		Joins("JOIN quote_sheet qs ON qs.id = qi.quote_sheet_id").
		Where("qs.supplier_id = ? AND qs.status = 'EFFECTIVE' AND qi.sku_id = ?", supplierID, skuID).
		Count(&n).Error; err != nil {
		return false, fmt.Errorf("check effective quote supplier=%d sku=%d: %w", supplierID, skuID, err)
	}
	return n > 0, nil
}

// LoadCurrentBaselineForUpdate 与 CostBaselineRepo 同语义（FOR UPDATE 行锁）。
// 这里只借同一张表——走 txOf 保证与幂等事务同事务（基线锁本身必须参与幂等事务，
// 否则锁定判定 + 版本切换 + 审计就分家了）。
func (r *CostLockRepo) LoadCurrentBaselineForUpdate(ctx context.Context, skuID int64) (*cost.Baseline, error) {
	return NewCostBaselineRepo(r.base).LoadCurrentBaselineForUpdate(ctx, skuID)
}

// WriteLockAudit 写 audit_log（action=COST_BASELINE_LOCK_PRIMARY，target_id = 新版本基线 id）。
// 与 RecalcSKU 内部的 ApplyNewVersion 在同一事务——幂等中间件把 tx 挂在 ctx 上，
// 任一失败整体回滚：绝不能出现「版本已经切了但审计没写」的脏窗口（红线 10）。
func (r *CostLockRepo) WriteLockAudit(ctx context.Context, e cost.LockAuditEntry) error {
	beforeJSON, err := json.Marshal(e.Before)
	if err != nil {
		return fmt.Errorf("marshal lock audit before: %w", err)
	}
	afterJSON, err := json.Marshal(e.After)
	if err != nil {
		return fmt.Errorf("marshal lock audit after: %w", err)
	}
	var reason *string
	if e.Reason != "" {
		reason = strPtrIfNotEmpty(e.Reason)
	}
	row := auditLogRow{
		OperatorID:   e.OperatorID,
		OperatorRole: e.OperatorRole,
		Action:       "COST_BASELINE_LOCK_PRIMARY",
		TargetType:   "COST_BASELINE",
		TargetID:     e.TargetID,
		BeforeValue:  beforeJSON,
		AfterValue:   afterJSON,
		Reason:       reason,
		SourceType:   "HUMAN",
		CreatedAt:    e.Now,
		UpdatedAt:    e.Now,
		RequestID:    strPtrIfNotEmpty(e.RequestID),
		CreatedBy:    int64Ptr(e.OperatorID),
		UpdatedBy:    int64Ptr(e.OperatorID),
	}
	if err := r.txOf(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("insert audit_log COST_BASELINE_LOCK_PRIMARY: %w", err)
	}
	return nil
}
