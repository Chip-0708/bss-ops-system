// Package repo 的 supplier_lifecycle.go：到期闭环/异常/补录的 GORM 实现。
// 遵循：行级过滤（expiring）、比价放开（anomalies）、todo/alert 原子去重、
// 宽限期实时算、补录 uk_quote_pending 兜底带出阻塞单号（5d 裁决 3）。
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"model_bss/internal/domain/supplier"
)

// LifecycleRepo 是 supplier.LifecycleStore 的 GORM 实现。
type LifecycleRepo struct {
	*QuoteRepo
}

// NewLifecycleRepo 构造生命周期仓储。
func NewLifecycleRepo(base *gorm.DB) *LifecycleRepo {
	return &LifecycleRepo{QuoteRepo: NewQuoteRepo(base)}
}

var _ supplier.LifecycleStore = (*LifecycleRepo)(nil)

// ---- §12.1 到期清单 ----

type expiringRow struct {
	ID              int64     `gorm:"column:id"`
	SupplierID      int64     `gorm:"column:supplier_id"`
	SupplierName    string    `gorm:"column:supplier_name"`
	VersionNo       int       `gorm:"column:version_no"`
	ValidTo         time.Time `gorm:"column:valid_to"`
	RemoveConfirmed bool      `gorm:"column:remove_confirmed"`
}

// ListExpiringQuotes 返回 EFFECTIVE 且 valid_to <= now + days 的报价单（按 OwnerScope 行级过滤）。
func (r *LifecycleRepo) ListExpiringQuotes(ctx context.Context, scope supplier.OwnerScope, q supplier.ExpiringQuoteQuery, now time.Time) ([]supplier.ExpiringQuoteItem, int64, error) {
	threshold := now.AddDate(0, 0, q.Days)
	base := func() *gorm.DB {
		tx := r.txOf(ctx).
			Table("quote_sheet qs").
			Joins("JOIN supplier_profile sp ON sp.id = qs.supplier_id").
			Where("qs.status = 'EFFECTIVE' AND qs.valid_to <= ?", threshold)
		return applyOwnerScope(tx, scope)
	}

	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count expiring quotes: %w", err)
	}

	offset := (q.Page - 1) * q.Size
	var rows []expiringRow
	if err := base().
		Select("qs.id, qs.supplier_id, ls.legal_name AS supplier_name, qs.version_no, qs.valid_to, qs.remove_confirmed").
		Joins("JOIN legal_subject ls ON ls.id = sp.subject_id").
		Offset(offset).Limit(q.Size).
		Order("qs.valid_to ASC, qs.id ASC"). // 快到期的排前面
		Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list expiring quotes: %w", err)
	}

	out := make([]supplier.ExpiringQuoteItem, 0, len(rows))
	if len(rows) == 0 {
		return out, total, nil
	}

	sheetIDs := make([]int64, 0, len(rows))
	for _, rr := range rows {
		sheetIDs = append(sheetIDs, rr.ID)
		out = append(out, supplier.ExpiringQuoteItem{
			ID:              rr.ID,
			SupplierID:      rr.SupplierID,
			SupplierName:    rr.SupplierName,
			VersionNo:       rr.VersionNo,
			ValidTo:         rr.ValidTo,
			RemoveConfirmed: rr.RemoveConfirmed,
		})
	}

	// 查本批各单的 skuIDs，直接填进对应行（5d 热修 P0-1：不再写包级 map，消除并发崩溃）
	type itemSkuRow struct {
		SheetID int64 `gorm:"column:quote_sheet_id"`
		SkuID   int64 `gorm:"column:sku_id"`
	}
	var isr []itemSkuRow
	if err := r.txOf(ctx).
		Table("quote_item").
		Select("quote_sheet_id, sku_id").
		Where("quote_sheet_id IN ?", sheetIDs).
		Scan(&isr).Error; err != nil {
		return nil, 0, fmt.Errorf("list expiring sku_ids: %w", err)
	}
	skuBySheet := make(map[int64][]int64, len(rows))
	for _, x := range isr {
		skuBySheet[x.SheetID] = append(skuBySheet[x.SheetID], x.SkuID)
	}
	for i := range out {
		out[i].SkuIDs = skuBySheet[out[i].ID]
	}

	return out, total, nil
}

// CountEffectiveSuppliersBySku 批量算单点依赖：
// 每个 SKU 的 EFFECTIVE 报价涉及的**不同 supplier_id 数量**（一次 GROUP BY，5d 裁决 3）。
func (r *LifecycleRepo) CountEffectiveSuppliersBySku(ctx context.Context, skuIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(skuIDs))
	if len(skuIDs) == 0 {
		return out, nil
	}
	type countRow struct {
		SkuID int64 `gorm:"column:sku_id"`
		Count int   `gorm:"column:cnt"`
	}
	var rows []countRow
	if err := r.txOf(ctx).
		Table("quote_item qi").
		Select("qi.sku_id, COUNT(DISTINCT qs.supplier_id) AS cnt").
		Joins("JOIN quote_sheet qs ON qs.id = qi.quote_sheet_id").
		Where("qs.status = 'EFFECTIVE' AND qi.sku_id IN ?", skuIDs).
		Group("qi.sku_id").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count effective suppliers by sku: %w", err)
	}
	for _, rr := range rows {
		out[rr.SkuID] = rr.Count
	}
	return out, nil
}

// ConfirmRemoveQuote 确认剔除（§12.2，立即执行剔除——5d 裁决 2）。
func (r *LifecycleRepo) ConfirmRemoveQuote(ctx context.Context, p supplier.ConfirmRemoveParams) (*supplier.ConfirmRemoveResult, error) {
	tx := r.txOf(ctx)

	var sheet quoteSheetRow
	if err := tx.Where("id = ?", p.QuoteID).Take(&sheet).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, supplier.ErrQuoteNotFound
		}
		return nil, fmt.Errorf("load quote %d: %w", p.QuoteID, err)
	}
	if sheet.Status != "EFFECTIVE" {
		return nil, supplier.ErrQuoteNotEffective
	}

	// 宽限期实时算（valid_to + quote_grace_days）
	graceDays, err := r.GetSysConfigInt(ctx, "quote_grace_days")
	if err != nil || graceDays <= 0 {
		graceDays = 3
	}
	graceUntil := sheet.ValidTo.AddDate(0, 0, graceDays)

	if p.Confirm {
		// 未过宽限期 → 409
		if !p.Now.After(graceUntil) {
			return nil, supplier.ErrQuoteInGracePeriod
		}

		// 同事务：EXPIRED + remove_confirmed=true + COST_RECALC + event_outbox + audit
		res := tx.Table("quote_sheet").
			Where("id = ? AND status = 'EFFECTIVE'", p.QuoteID).
			Updates(map[string]any{
				"status":           "EXPIRED",
				"remove_confirmed": true,
				"updated_at":       p.Now,
				"updated_by":       p.OperatorID,
			})
		if res.Error != nil {
			return nil, fmt.Errorf("expire quote: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return nil, supplier.ErrQuoteNotEffective
		}

		// task_job(COST_RECALC, reason='EXPIRE_REMOVE')
		payload, _ := json.Marshal(map[string]any{
			"supplier_id":    sheet.SupplierID,
			"quote_sheet_id": p.QuoteID,
			"reason":         "EXPIRE_REMOVE",
		})
		job := taskJobRow{
			JobType:   "COST_RECALC",
			Payload:   payload,
			Status:    "PENDING",
			NextRunAt: p.Now,
			CreatedAt: p.Now,
			UpdatedAt: p.Now,
			RequestID: strPtr(p.RequestID),
			CreatedBy: int64Ptr(p.OperatorID),
			UpdatedBy: int64Ptr(p.OperatorID),
		}
		if err := tx.Create(&job).Error; err != nil {
			return nil, fmt.Errorf("insert task_job EXPIRE_REMOVE: %w", err)
		}

		// event_outbox('quote.expired')
		eventPayload, _ := json.Marshal(map[string]any{
			"quote_sheet_id": p.QuoteID,
			"supplier_id":    sheet.SupplierID,
			"reason":         "EXPIRE_REMOVE",
		})
		ev := eventOutboxRow{
			EventType: "quote.expired",
			Payload:   eventPayload,
			Status:    "PENDING",
			NextRunAt: p.Now,
			CreatedAt: p.Now,
			UpdatedAt: p.Now,
			RequestID: strPtr(p.RequestID),
			CreatedBy: int64Ptr(p.OperatorID),
			UpdatedBy: int64Ptr(p.OperatorID),
		}
		if err := tx.Create(&ev).Error; err != nil {
			return nil, fmt.Errorf("insert event_outbox quote.expired: %w", err)
		}

		// audit_log
		if err := r.audit.Record(ctx, AuditEntry{
			OperatorID:   p.OperatorID,
			OperatorRole: p.OperatorRole,
			Action:       "QUOTE_CONFIRM_REMOVE",
			TargetType:   "QUOTE_SHEET",
			TargetID:     p.QuoteID,
			AfterValue:   map[string]any{"status": "EXPIRED", "remove_confirmed": true, "reason": p.Reason},
			Reason:       p.Reason,
			SourceType:   "HUMAN",
			RequestID:    p.RequestID,
		}); err != nil {
			return nil, err
		}

		return &supplier.ConfirmRemoveResult{
			ID:              p.QuoteID,
			Status:          "EXPIRED",
			RemoveConfirmed: true,
			ExecutedAt:      p.Now,
		}, nil
	}

	// confirm=false：撤销确认（仅当尚未执行剔除，本分支针对此前打标的历史单）
	if sheet.Status == "EXPIRED" {
		return nil, supplier.ErrQuoteAlreadyRemoved
	}
	if err := tx.Table("quote_sheet").
		Where("id = ?", p.QuoteID).
		Updates(map[string]any{
			"remove_confirmed": false,
			"updated_at":       p.Now,
			"updated_by":       p.OperatorID,
		}).Error; err != nil {
		return nil, fmt.Errorf("undo confirm remove: %w", err)
	}
	return &supplier.ConfirmRemoveResult{
		ID:              p.QuoteID,
		Status:          sheet.Status,
		RemoveConfirmed: false,
		ExecutedAt:      p.Now,
	}, nil
}

// ---- §12.3 扫描 ----

// ExpireScanTargets 取到期扫描目标（EFFECTIVE 且 valid_to <= now + 14d）。
func (r *LifecycleRepo) ExpireScanTargets(ctx context.Context, now time.Time) ([]supplier.ExpiringScanTarget, error) {
	threshold := now.AddDate(0, 0, 14)
	type targetRow struct {
		ID              int64     `gorm:"column:id"`
		SupplierID      int64     `gorm:"column:supplier_id"`
		SupplierName    string    `gorm:"column:supplier_name"`
		OwnerID         int64     `gorm:"column:owner_procurement_operator_id"`
		VersionNo       int       `gorm:"column:version_no"`
		ValidTo         time.Time `gorm:"column:valid_to"`
		RemoveConfirmed bool      `gorm:"column:remove_confirmed"`
	}
	var rows []targetRow
	if err := r.txOf(ctx).
		Table("quote_sheet qs").
		Select("qs.id, qs.supplier_id, ls.legal_name AS supplier_name, sp.owner_procurement_operator_id, qs.version_no, qs.valid_to, qs.remove_confirmed").
		Joins("JOIN supplier_profile sp ON sp.id = qs.supplier_id").
		Joins("JOIN legal_subject ls ON ls.id = sp.subject_id").
		Where("qs.status = 'EFFECTIVE' AND qs.valid_to <= ?", threshold).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("query expire scan targets: %w", err)
	}

	sheetIDs := make([]int64, 0, len(rows))
	for _, rr := range rows {
		sheetIDs = append(sheetIDs, rr.ID)
	}
	skuBySheet := make(map[int64][]int64)
	if len(sheetIDs) > 0 {
		type skuRow struct {
			SheetID int64 `gorm:"column:quote_sheet_id"`
			SkuID   int64 `gorm:"column:sku_id"`
		}
		var srows []skuRow
		if err := r.txOf(ctx).
			Table("quote_item").
			Select("quote_sheet_id, sku_id").
			Where("quote_sheet_id IN ?", sheetIDs).
			Scan(&srows).Error; err != nil {
			return nil, fmt.Errorf("query item skus: %w", err)
		}
		for _, sr := range srows {
			skuBySheet[sr.SheetID] = append(skuBySheet[sr.SheetID], sr.SkuID)
		}
	}

	out := make([]supplier.ExpiringScanTarget, 0, len(rows))
	for _, rr := range rows {
		out = append(out, supplier.ExpiringScanTarget{
			ID:              rr.ID,
			SupplierID:      rr.SupplierID,
			SupplierName:    rr.SupplierName,
			OwnerID:         rr.OwnerID,
			VersionNo:       rr.VersionNo,
			ValidTo:         rr.ValidTo,
			RemoveConfirmed: rr.RemoveConfirmed,
			SkuIDs:          skuBySheet[rr.ID],
		})
	}
	return out, nil
}

// CreateExpireTodoOnce 去重写待办：同一 biz_type+biz_id+priority 已有 OPEN 则跳过（5d 热修 P1-3）。
// 原子实现：INSERT ... WHERE NOT EXISTS。priority 映射见 domain 层 todoPriorityOf。
// ident 写入 request_id/created_by（6a 补充要求 3：一次 job 的所有改动用同一 request_id 串起来）。
func (r *LifecycleRepo) CreateExpireTodoOnce(ctx context.Context, sheetID, assigneeID int64, title, level string, now time.Time, ident supplier.JobIdentity) (bool, error) {
	priority := supplier.TodoPriorityOf(level)
	sql := `
INSERT INTO todo_task (biz_type, biz_id, assignee_id, title, priority, status, created_at, updated_at, request_id, created_by, updated_by)
SELECT 'QUOTE_EXPIRE', ?, ?, ?, ?, 'OPEN', ?, ?, ?, ?, ?
WHERE NOT EXISTS (
    SELECT 1 FROM todo_task
    WHERE biz_type = 'QUOTE_EXPIRE' AND biz_id = ? AND status = 'OPEN' AND priority = ?
)`
	res := r.txOf(ctx).Exec(sql, sheetID, assigneeID, title, priority, now, now,
		ident.RequestID, ident.OperatorID, ident.OperatorID, sheetID, priority)
	if res.Error != nil {
		return false, fmt.Errorf("create expire todo: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}

// CreateAlertOnce 去重写告警：同一 alert_type+target+severity 已有 OPEN/HANDLING 则跳过。
// alertType 由调用方传入（5d 热修 P1-2）：到期扫描 'QUOTE_EXPIRE'、异常扫描 'QUOTE_ANOMALY'、
// 补录激活失败 'QUOTE_ACTIVATE_FAILED'——否则不同类型告警同 target 同 severity 会互相吞掉。
// ident 写入 request_id/created_by（6a 补充要求 3）。
func (r *LifecycleRepo) CreateAlertOnce(ctx context.Context, alertType string, sheetID *int64, severity, message string, now time.Time, ident supplier.JobIdentity) (bool, error) {
	var sql string
	var args []any
	if sheetID != nil {
		sql = `
INSERT INTO alert (alert_type, severity, target_type, target_id, message, status, created_at, updated_at, request_id, created_by, updated_by)
SELECT ?, ?, 'QUOTE_SHEET', ?, ?, 'OPEN', ?, ?, ?, ?, ?
WHERE NOT EXISTS (
    SELECT 1 FROM alert
    WHERE alert_type = ? AND target_type = 'QUOTE_SHEET' AND target_id = ?
      AND severity = ? AND status IN ('OPEN', 'HANDLING')
)`
		args = []any{alertType, severity, *sheetID, message, now, now,
			ident.RequestID, ident.OperatorID, ident.OperatorID, alertType, *sheetID, severity}
	} else {
		// target 为 NULL 的告警（如 RETRO_LIMIT）：按 alert_type+severity+当月 去重
		sql = `
INSERT INTO alert (alert_type, severity, message, status, created_at, updated_at, request_id, created_by, updated_by)
SELECT ?, ?, ?, 'OPEN', ?, ?, ?, ?, ?
WHERE NOT EXISTS (
    SELECT 1 FROM alert
    WHERE alert_type = ? AND severity = ? AND target_type IS NULL
      AND status IN ('OPEN', 'HANDLING')
)`
		args = []any{alertType, severity, message, now, now,
			ident.RequestID, ident.OperatorID, ident.OperatorID, alertType, severity}
	}
	res := r.txOf(ctx).Exec(sql, args...)
	if res.Error != nil {
		return false, fmt.Errorf("create alert type=%s: %w", alertType, res.Error)
	}
	return res.RowsAffected > 0, nil
}

// ListConfirmedPendingFinal 兜底：remove_confirmed=true AND status='EFFECTIVE'。
// 5d 裁决 2：confirm-remove 已立即执行剔除，本查询正常情况下查不到任何行——纯兜底，勿"修复"。
func (r *LifecycleRepo) ListConfirmedPendingFinal(ctx context.Context) ([]int64, error) {
	var ids []int64
	err := r.txOf(ctx).
		Table("quote_sheet").
		Where("status = 'EFFECTIVE' AND remove_confirmed = true").
		Pluck("id", &ids).Error
	return ids, err
}

// FinalExpireOne 兜底执行剔除（WORKER 身份，同事务 EXPIRED + COST_RECALC + event_outbox + audit）。
func (r *LifecycleRepo) FinalExpireOne(ctx context.Context, quoteID int64, now time.Time, ident supplier.JobIdentity) error {
	return r.txOf(ctx).Transaction(func(tx *gorm.DB) error {
		var sheet quoteSheetRow
		if err := tx.Where("id = ?", quoteID).Take(&sheet).Error; err != nil {
			return err
		}
		if sheet.Status != "EFFECTIVE" {
			return nil // 已不是 EFFECTIVE，幂等跳过
		}
		if err := tx.Table("quote_sheet").Where("id = ?", quoteID).
			Updates(map[string]any{"status": "EXPIRED", "updated_at": now}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"supplier_id": sheet.SupplierID, "quote_sheet_id": quoteID, "reason": "EXPIRE_REMOVE"})
		if err := tx.Create(&taskJobRow{
			JobType: "COST_RECALC", Payload: payload, Status: "PENDING", NextRunAt: now, CreatedAt: now, UpdatedAt: now,
			RequestID: strPtr(ident.RequestID), CreatedBy: int64Ptr(ident.OperatorID), UpdatedBy: int64Ptr(ident.OperatorID),
		}).Error; err != nil {
			return err
		}
		// event_outbox('quote.expired')（5d 热修 P2-6：此前漏写，与 ConfirmRemoveQuote 对齐）
		eventPayload, _ := json.Marshal(map[string]any{"quote_sheet_id": quoteID, "supplier_id": sheet.SupplierID, "reason": "EXPIRE_REMOVE"})
		if err := tx.Create(&eventOutboxRow{
			EventType: "quote.expired", Payload: eventPayload, Status: "PENDING", NextRunAt: now, CreatedAt: now, UpdatedAt: now,
			RequestID: strPtr(ident.RequestID), CreatedBy: int64Ptr(ident.OperatorID), UpdatedBy: int64Ptr(ident.OperatorID),
		}).Error; err != nil {
			return err
		}
		return r.audit.Record(ctx, AuditEntry{
			OperatorID: ident.OperatorID, OperatorRole: ident.OperatorRole, Action: "QUOTE_EXPIRE_FINAL",
			TargetType: "QUOTE_SHEET", TargetID: quoteID, SourceType: ident.SourceType, RequestID: ident.RequestID,
		})
	})
}

// ---- §13 异常检测 ----

// LoadAnomalyRows 取近 days 天内 status ∈ ('APPROVING','APPROVED_PENDING','EFFECTIVE') 的报价组件，
// 附上一版本同组件价与市场最低价。**不做归属过滤**（§3.2 放开比价决议，5d 裁决 8）。
func (r *LifecycleRepo) LoadAnomalyRows(ctx context.Context, days int, now time.Time) ([]supplier.AnomalyRawRow, error) {
	since := now.AddDate(0, 0, -days)
	tx := r.txOf(ctx)

	type row struct {
		SheetID       int64  `gorm:"column:quote_sheet_id"`
		SupplierID    int64  `gorm:"column:supplier_id"`
		SupplierName  string `gorm:"column:supplier_name"`
		VersionNo     int    `gorm:"column:version_no"`
		SkuID         int64  `gorm:"column:sku_id"`
		SkuCode       string `gorm:"column:sku_code"`
		ComponentType string `gorm:"column:component_type"`
		UnitPrice     string `gorm:"column:unit_price"`
	}
	var rows []row
	if err := tx.Table("quote_component qc").
		Select(`qs.id AS quote_sheet_id, qs.supplier_id, ls.legal_name AS supplier_name,
			qs.version_no, qi.sku_id, ms.sku_code, qc.component_type, qc.unit_price::text AS unit_price`).
		Joins("JOIN quote_item qi ON qi.id = qc.quote_item_id").
		Joins("JOIN quote_sheet qs ON qs.id = qi.quote_sheet_id").
		Joins("JOIN model_sku ms ON ms.id = qi.sku_id").
		Joins("JOIN supplier_profile sp ON sp.id = qs.supplier_id").
		Joins("JOIN legal_subject ls ON ls.id = sp.subject_id").
		Where("qs.status IN ('APPROVING','APPROVED_PENDING','EFFECTIVE') AND qs.created_at >= ?", since).
		Order("qs.id DESC, qi.sku_id, qc.component_type").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load anomaly base: %w", err)
	}

	if len(rows) == 0 {
		return nil, nil
	}

	// 收集涉及的 skuID（查市场最低价）
	skuIDSet := map[int64]bool{}
	var skuIDs []int64
	for _, rr := range rows {
		if !skuIDSet[rr.SkuID] {
			skuIDSet[rr.SkuID] = true
			skuIDs = append(skuIDs, rr.SkuID)
		}
	}

	// 市场最低价：全市场 EFFECTIVE 报价同组件最低价（不过滤归属）
	marketByKey := make(map[string]decimal.Decimal)
	type mktRow struct {
		SkuID int64  `gorm:"column:sku_id"`
		CType string `gorm:"column:component_type"`
		Best  string `gorm:"column:best"`
	}
	var mrows []mktRow
	if err := tx.Table("quote_component qc").
		Select("qi.sku_id, qc.component_type, MIN(qc.unit_price)::text AS best").
		Joins("JOIN quote_item qi ON qi.id = qc.quote_item_id").
		Joins("JOIN quote_sheet qs ON qs.id = qi.quote_sheet_id").
		Where("qs.status = 'EFFECTIVE' AND qi.sku_id IN ?", skuIDs).
		Group("qi.sku_id, qc.component_type").
		Scan(&mrows).Error; err != nil {
		return nil, fmt.Errorf("query anomaly market best: %w", err)
	}
	for _, mr := range mrows {
		if d, perr := decimal.NewFromString(mr.Best); perr == nil {
			marketByKey[fmt.Sprintf("%d|%s", mr.SkuID, mr.CType)] = d
		}
	}

	// 上一版本价：一次性窗口函数批量取（5d 热修 P1-5，替代逐行 N+1）。
	// 排除 REJECTED（被驳回的价从未生效过，不能作为对比基准——5d 热修 P2-7）；
	// 不排除 EXPIRED（被覆盖/到期的价曾真实生效过，是合法对比基准）。
	supplierIDSet := map[int64]bool{}
	var supplierIDs []int64
	for _, rr := range rows {
		if !supplierIDSet[rr.SupplierID] {
			supplierIDSet[rr.SupplierID] = true
			supplierIDs = append(supplierIDs, rr.SupplierID)
		}
	}
	type prevRow struct {
		SupplierID    int64   `gorm:"column:supplier_id"`
		SkuID         int64   `gorm:"column:sku_id"`
		ComponentType string  `gorm:"column:component_type"`
		VersionNo     int     `gorm:"column:version_no"`
		PrevPrice     *string `gorm:"column:prev_price"`
	}
	prevByKey := make(map[string]decimal.Decimal)
	var prows []prevRow
	if err := tx.Raw(`
SELECT qs.supplier_id, qi.sku_id, qc.component_type, qs.version_no,
       LAG(qc.unit_price::text) OVER (
           PARTITION BY qs.supplier_id, qi.sku_id, qc.component_type
           ORDER BY qs.version_no
       ) AS prev_price
FROM quote_component qc
JOIN quote_item   qi ON qi.id = qc.quote_item_id
JOIN quote_sheet  qs ON qs.id = qi.quote_sheet_id
WHERE qs.supplier_id IN ? AND qi.sku_id IN ? AND qs.status <> 'REJECTED'`,
		supplierIDs, skuIDs).Scan(&prows).Error; err != nil {
		return nil, fmt.Errorf("query anomaly prev prices: %w", err)
	}
	for _, pr := range prows {
		if pr.PrevPrice == nil {
			continue
		}
		if d, perr := decimal.NewFromString(*pr.PrevPrice); perr == nil {
			prevByKey[fmt.Sprintf("%d|%d|%s|%d", pr.SupplierID, pr.SkuID, pr.ComponentType, pr.VersionNo)] = d
		}
	}

	out := make([]supplier.AnomalyRawRow, 0, len(rows))
	for _, rr := range rows {
		p, perr := decimal.NewFromString(rr.UnitPrice)
		if perr != nil {
			continue
		}
		raw := supplier.AnomalyRawRow{
			QuoteSheetID:  rr.SheetID,
			SupplierName:  rr.SupplierName,
			SkuID:         rr.SkuID,
			SkuCode:       rr.SkuCode,
			ComponentType: rr.ComponentType,
			UnitPrice:     p,
		}
		if d, ok := prevByKey[fmt.Sprintf("%d|%d|%s|%d", rr.SupplierID, rr.SkuID, rr.ComponentType, rr.VersionNo)]; ok {
			raw.PrevPrice = &d
		}
		if best, ok := marketByKey[fmt.Sprintf("%d|%s", rr.SkuID, rr.ComponentType)]; ok {
			raw.MarketBest = &best
		}
		out = append(out, raw)
	}
	return out, nil
}

// ---- §11 特权补录 ----

// FindSupplierByID 按 ID 取供应商。
func (r *LifecycleRepo) FindSupplierByID(ctx context.Context, supplierID int64) (*supplier.Supplier, error) {
	var row struct {
		ID                         int64  `gorm:"column:id"`
		SubjectID                  int64  `gorm:"column:subject_id"`
		LegalName                  string `gorm:"column:legal_name"`
		OwnerProcurementOperatorID int64  `gorm:"column:owner_procurement_operator_id"`
		QualStatus                 string `gorm:"column:qual_status"`
		SettleStatus               string `gorm:"column:settle_status"`
		Status                     string `gorm:"column:status"`
	}
	err := r.txOf(ctx).
		Table("supplier_profile sp").
		Select("sp.id, sp.subject_id, ls.legal_name, sp.owner_procurement_operator_id, sp.qual_status, sp.settle_status, sp.status").
		Joins("JOIN legal_subject ls ON ls.id = sp.subject_id").
		Where("sp.id = ?", supplierID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &supplier.Supplier{
		ID: row.ID, SubjectID: row.SubjectID, LegalName: row.LegalName,
		OwnerProcurementOperatorID: row.OwnerProcurementOperatorID,
		QualStatus:                 row.QualStatus, SettleStatus: row.SettleStatus, Status: row.Status,
	}, nil
}

// CreateRetroQuote 补录落库（source='RETRO', retroactive=true, status='APPROVED_PENDING'）。
// 撞 uk_quote_pending 时返回带阻塞单 id/版本号的 ErrQuoteConflict（5d 裁决 3）。
func (r *LifecycleRepo) CreateRetroQuote(ctx context.Context, p supplier.CreateRetroParams) (*supplier.RetroSheetMeta, error) {
	tx := r.txOf(ctx)

	// uk_quote_pending 预检：查该供应商是否已有待生效报价（有则带出单号与版本——5d 裁决 3）
	var pending quoteSheetRow
	err := tx.Where("supplier_id = ? AND status = 'APPROVED_PENDING'", p.Supplier.ID).Take(&pending).Error
	if err == nil {
		return nil, fmt.Errorf("%w：该供应商已有待生效报价 #%d v%d，请先处理后再补录",
			supplier.ErrQuoteConflict, pending.ID, pending.VersionNo)
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("check pending conflict: %w", err)
	}

	var maxVer *int
	if err := tx.Table("quote_sheet").
		Where("supplier_id = ?", p.Supplier.ID).
		Select("MAX(version_no)").
		Scan(&maxVer).Error; err != nil {
		return nil, err
	}
	versionNo := 1
	if maxVer != nil {
		versionNo = *maxVer + 1
	}

	sheet := quoteSheetRow{
		SupplierID:  p.Supplier.ID,
		VersionNo:   versionNo,
		Status:      "APPROVED_PENDING", // 免审批，直接进 APPROVED_PENDING
		ValidFrom:   p.ValidFrom,
		ValidTo:     p.ValidTo,
		Source:      "RETRO",
		Retroactive: true,
		AuditReason: &p.AuditReason,
		SubmittedBy: int64Ptr(p.OperatorID), // 操作员（非供应商）
		SubmittedAt: &p.SubmittedAt,
		CreatedAt:   p.SubmittedAt,
		UpdatedAt:   p.SubmittedAt,
		RequestID:   strPtr(p.RequestID),
		CreatedBy:   int64Ptr(p.OperatorID),
		UpdatedBy:   int64Ptr(p.OperatorID),
	}
	if err := tx.Create(&sheet).Error; err != nil {
		if isUniqueViolation(err, "uk_quote_pending") {
			return nil, fmt.Errorf("%w：该供应商已有待生效报价，请先处理后再补录", supplier.ErrQuoteConflict)
		}
		return nil, fmt.Errorf("insert retro quote_sheet: %w", err)
	}

	for _, it := range p.Items {
		var constraintsJSON []byte
		if it.Constraints != nil {
			constraintsJSON, _ = json.Marshal(it.Constraints)
		}
		item := quoteItemRow{
			QuoteSheetID: sheet.ID,
			SkuID:        it.SKUID,
			Currency:     it.Currency,
			FxTier:       it.FxTier,
			Constraints:  constraintsJSON,
			CreatedAt:    p.SubmittedAt,
			UpdatedAt:    p.SubmittedAt,
			RequestID:    strPtr(p.RequestID),
			CreatedBy:    int64Ptr(p.OperatorID),
			UpdatedBy:    int64Ptr(p.OperatorID),
		}
		if err := tx.Create(&item).Error; err != nil {
			return nil, fmt.Errorf("insert retro quote_item: %w", err)
		}
		for _, c := range it.Components {
			comp := quoteComponentRow{
				QuoteItemID:   item.ID,
				ComponentType: c.ComponentType,
				UnitPrice:     c.UnitPrice.String(),
				CreatedAt:     p.SubmittedAt,
				UpdatedAt:     p.SubmittedAt,
				RequestID:     strPtr(p.RequestID),
				CreatedBy:     int64Ptr(p.OperatorID),
				UpdatedBy:     int64Ptr(p.OperatorID),
			}
			if c.Multiplier != nil {
				s := c.Multiplier.String()
				comp.Multiplier = &s
			}
			if err := tx.Create(&comp).Error; err != nil {
				return nil, fmt.Errorf("insert retro quote_component: %w", err)
			}
		}
	}

	// 审计：QUOTE_RETRO，after 含 audit_reason 原文（红线 10）
	if err := r.audit.Record(ctx, AuditEntry{
		OperatorID:   p.OperatorID,
		OperatorRole: p.OperatorRole,
		Action:       "QUOTE_RETRO",
		TargetType:   "QUOTE_SHEET",
		TargetID:     sheet.ID,
		AfterValue: map[string]any{
			"id": sheet.ID, "supplier_id": sheet.SupplierID, "version_no": versionNo,
			"status": "APPROVED_PENDING", "valid_from": sheet.ValidFrom, "valid_to": sheet.ValidTo,
			"source": "RETRO", "retroactive": true, "audit_reason": p.AuditReason,
		},
		Reason:     p.AuditReason,
		SourceType: "HUMAN",
		RequestID:  p.RequestID,
	}); err != nil {
		return nil, err
	}

	return &supplier.RetroSheetMeta{
		ID: sheet.ID, SupplierID: sheet.SupplierID, VersionNo: versionNo, ValidFrom: sheet.ValidFrom,
	}, nil
}

// CountRetroThisMonth 当月 retroactive=true 条数。
func (r *LifecycleRepo) CountRetroThisMonth(ctx context.Context, now time.Time) (int, error) {
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	var cnt int64
	err := r.txOf(ctx).
		Table("quote_sheet").
		Where("retroactive = true AND created_at >= ?", startOfMonth).
		Count(&cnt).Error
	return int(cnt), err
}

// CreateRetroLimitAlertOnce 当月已有 OPEN 的 RETRO_LIMIT 则跳过（去重）。
func (r *LifecycleRepo) CreateRetroLimitAlertOnce(ctx context.Context, count, limit int, now time.Time) (bool, error) {
	msg := fmt.Sprintf("特权补录月度告警：本月已累计补录 %d 次（阈值 %d 次）", count, limit)
	sql := `
INSERT INTO alert (alert_type, severity, message, status, created_at, updated_at)
SELECT 'RETRO_LIMIT', 'HIGH', ?, 'OPEN', ?, ?
WHERE NOT EXISTS (
    SELECT 1 FROM alert
    WHERE alert_type = 'RETRO_LIMIT' AND status IN ('OPEN', 'HANDLING')
      AND created_at >= date_trunc('month', ?::timestamptz)
)`
	res := r.txOf(ctx).Exec(sql, msg, now, now, now)
	if res.Error != nil {
		return false, fmt.Errorf("create retro limit alert: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}

// GetSysConfigInt 读 sys_config 整数配置。
func (r *LifecycleRepo) GetSysConfigInt(ctx context.Context, key string) (int, error) {
	var val string
	err := r.txOf(ctx).Table("sys_config").
		Where("config_key = ?", key).
		Select("config_value").
		Scan(&val).Error
	if err != nil {
		return 0, err
	}
	if val == "" {
		return 0, fmt.Errorf("sys_config %s not found", key)
	}
	var n int
	_, err = fmt.Sscanf(val, "%d", &n)
	return n, err
}

// GetSysConfigDecimal 读 sys_config 数值配置（复用 ApproveRepo 逻辑）。
func (r *LifecycleRepo) GetSysConfigDecimal(ctx context.Context, key string) (decimal.Decimal, error) {
	var val string
	err := r.txOf(ctx).Table("sys_config").
		Where("config_key = ?", key).
		Select("config_value").
		Scan(&val).Error
	if err != nil {
		return decimal.Zero, fmt.Errorf("read sys_config %s: %w", key, err)
	}
	if val == "" {
		return decimal.Zero, fmt.Errorf("sys_config %s not found", key)
	}
	d, err := decimal.NewFromString(val)
	if err != nil {
		return decimal.Zero, fmt.Errorf("parse sys_config %s=%q: %w", key, val, err)
	}
	return d, nil
}
