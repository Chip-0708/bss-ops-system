// Package repo 的 supplier_approve.go：报价审批/激活/diff（05-quotes.md §7/§8/§9/§10/§14）的 GORM 实现。
// 行级过滤（设计 §3.2）：报价单随供应商归属（JOIN supplier_profile 的
// owner_procurement_operator_id）；成本/比价视图（market_best）不过滤（放开比价决议）。
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

// ApproveRepo 是 supplier.ApproveStore 的 GORM 实现。
type ApproveRepo struct {
	*SupplierRepo
	audit *AuditRepo
}

// NewApproveRepo 构造审批仓储。
func NewApproveRepo(base *gorm.DB) *ApproveRepo {
	return &ApproveRepo{
		SupplierRepo: NewSupplierRepo(base),
		audit:        NewAuditRepo(base),
	}
}

var _ supplier.ApproveStore = (*ApproveRepo)(nil)

// ---- 数据域 Scope（设计 §3.2 图 D-3） ----

// applyOwnerScope 把数据域过滤应用到「JOIN supplier_profile sp 之后」的查询上。
// 调用方必须已 JOIN supplier_profile sp。ScopePaths 元素形如 "/1/3/"（含首尾斜杠），
// 与 org_unit.path 格式一致，DEPT_SUB 用 u.path LIKE ANY(paths || '%')。
func applyOwnerScope(tx *gorm.DB, scope supplier.OwnerScope) *gorm.DB {
	switch scope.DataScope {
	case "ALL":
		return tx
	case "DEPT", "DEPT_SUB":
		tx = tx.Joins("JOIN internal_staff os_ ON os_.id = sp.owner_procurement_operator_id")
		if scope.DataScope == "DEPT" {
			return tx.Where("os_.org_unit_id = ?", scope.MyOrgID)
		}
		// DEPT_SUB：负责人可见下属整棵子树
		if len(scope.ScopePaths) == 0 {
			// 无下属节点的负责人：退化为仅本部门（与 Synthesize 语义一致）
			return tx.Where("os_.org_unit_id = ?", scope.MyOrgID)
		}
		patterns := make([]string, 0, len(scope.ScopePaths))
		for _, p := range scope.ScopePaths {
			patterns = append(patterns, p+"%")
		}
		return tx.Joins("JOIN org_unit ou_ ON ou_.id = os_.org_unit_id").
			Where("ou_.path LIKE ANY(?)", patterns)
	default: // SELF
		return tx.Where("sp.owner_procurement_operator_id = ?", scope.StaffID)
	}
}

// ---- 行模型（复用 supplier_quote.go 的 quoteSheetRow / quoteItemRow / todoTaskRow） ----

type taskJobRow struct {
	ID         int64     `gorm:"primaryKey"`
	JobType    string    `gorm:"column:job_type"`
	Payload    []byte    `gorm:"column:payload"`
	Status     string    `gorm:"column:status"`
	RetryCount int       `gorm:"column:retry_count"`
	NextRunAt  time.Time `gorm:"column:next_run_at"`
	LastError  *string   `gorm:"column:last_error"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
	RequestID  *string   `gorm:"column:request_id"`
	CreatedBy  *int64    `gorm:"column:created_by"`
	UpdatedBy  *int64    `gorm:"column:updated_by"`
}

func (taskJobRow) TableName() string { return "task_job" }

type eventOutboxRow struct {
	ID         int64     `gorm:"primaryKey"`
	EventType  string    `gorm:"column:event_type"`
	Payload    []byte    `gorm:"column:payload"`
	Status     string    `gorm:"column:status"`
	RetryCount int       `gorm:"column:retry_count"`
	NextRunAt  time.Time `gorm:"column:next_run_at"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
	RequestID  *string   `gorm:"column:request_id"`
	CreatedBy  *int64    `gorm:"column:created_by"`
	UpdatedBy  *int64    `gorm:"column:updated_by"`
}

func (eventOutboxRow) TableName() string { return "event_outbox" }

// ---- §7 待审批列表 ----

// pendingRow 是待审批列表的扫描行（列表查询专用）。
type pendingRow struct {
	ID           int64     `gorm:"column:id"`
	SupplierID   int64     `gorm:"column:supplier_id"`
	SupplierName string    `gorm:"column:supplier_name"`
	VersionNo    int       `gorm:"column:version_no"`
	ItemCount    int       `gorm:"column:item_count"`
	Source       string    `gorm:"column:source"`
	Retroactive  bool      `gorm:"column:retroactive"`
	ValidFrom    time.Time `gorm:"column:valid_from"`
	ValidTo      time.Time `gorm:"column:valid_to"`
	SubmittedAt  time.Time `gorm:"column:submitted_at"`
	HasPrevious  bool      `gorm:"column:has_previous"`
}

// ListPendingQuotes 待审批列表：status='APPROVING' + 数据域行级过滤。
func (r *ApproveRepo) ListPendingQuotes(ctx context.Context, scope supplier.OwnerScope, q supplier.PendingQuoteQuery, now time.Time) (*supplier.PendingQuoteResult, error) {
	base := func() *gorm.DB {
		tx := r.txOf(ctx).
			Table("quote_sheet qs").
			Joins("JOIN supplier_profile sp ON sp.id = qs.supplier_id").
			Where("qs.status = 'APPROVING'")
		if q.SupplierID != nil {
			tx = tx.Where("qs.supplier_id = ?", *q.SupplierID)
		}
		return applyOwnerScope(tx, scope)
	}

	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count pending quotes: %w", err)
	}

	offset := (q.Page - 1) * q.Size
	var rows []pendingRow
	if err := base().
		Select(`qs.id, qs.supplier_id, ls.legal_name AS supplier_name, qs.version_no,
			(SELECT COUNT(*) FROM quote_item qi WHERE qi.quote_sheet_id = qs.id) AS item_count,
			qs.source, qs.retroactive, qs.valid_from, qs.valid_to, qs.submitted_at,
			EXISTS(SELECT 1 FROM quote_sheet p WHERE p.supplier_id = qs.supplier_id AND p.version_no < qs.version_no) AS has_previous`).
		Joins("JOIN legal_subject ls ON ls.id = sp.subject_id").
		Offset(offset).Limit(q.Size).
		Order("qs.submitted_at ASC"). // FIFO：先提交先批
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list pending quotes: %w", err)
	}

	list := make([]supplier.PendingQuoteItem, 0, len(rows))
	for _, rr := range rows {
		wait := now.Sub(rr.SubmittedAt).Hours()
		list = append(list, supplier.PendingQuoteItem{
			ID:           rr.ID,
			SupplierID:   rr.SupplierID,
			SupplierName: rr.SupplierName,
			VersionNo:    rr.VersionNo,
			ItemCount:    rr.ItemCount,
			Source:       rr.Source,
			Retroactive:  rr.Retroactive,
			ValidFrom:    rr.ValidFrom,
			ValidTo:      rr.ValidTo,
			SubmittedAt:  rr.SubmittedAt,
			WaitHours:    float64(int(wait*10)) / 10, // 1 位小数（§7 SLA 口径）
			HasPrevious:  rr.HasPrevious,
		})
	}
	return &supplier.PendingQuoteResult{List: list, Total: total, Page: q.Page, Size: q.Size}, nil
}

// ---- 归属校验 ----

// CheckQuoteScope 归属校验：单是否存在 + 供应商 owner 是否在数据域内。
func (r *ApproveRepo) CheckQuoteScope(ctx context.Context, quoteID int64, scope supplier.OwnerScope) (bool, bool, error) {
	base := func() *gorm.DB {
		return r.txOf(ctx).
			Table("quote_sheet qs").
			Joins("JOIN supplier_profile sp ON sp.id = qs.supplier_id").
			Where("qs.id = ?", quoteID)
	}

	var n int64
	if err := base().Count(&n).Error; err != nil {
		return false, false, fmt.Errorf("check quote scope: %w", err)
	}
	if n == 0 {
		return false, false, nil
	}

	var m int64
	if err := applyOwnerScope(base(), scope).Count(&m).Error; err != nil {
		return true, false, fmt.Errorf("check quote scope filter: %w", err)
	}
	return true, m > 0, nil
}

// ---- §8 审批通过 ----

// ApproveQuote 审批通过（单事务，幂等中间件注入的 tx 经 txOf 复用）。
func (r *ApproveRepo) ApproveQuote(ctx context.Context, p supplier.ApproveParams) (*supplier.ApproveResult, error) {
	tx := r.txOf(ctx)

	// 条件更新做状态占位：0 行 → 前置非法（区分 404 由 service 层 checkScope 先查）
	res := tx.Table("quote_sheet").
		Where("id = ? AND status = 'APPROVING'", p.QuoteID).
		Updates(map[string]interface{}{
			"status":      "APPROVED_PENDING",
			"approved_by": p.OperatorID,
			"approved_at": p.Now,
			"updated_at":  p.Now,
			"updated_by":  p.OperatorID,
		})
	if res.Error != nil {
		if isUniqueViolation(res.Error, "uk_quote_pending") {
			return nil, supplier.ErrQuoteConflict
		}
		return nil, fmt.Errorf("approve quote_sheet: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, supplier.ErrQuoteNotApprovable
	}

	// todo_task → DONE
	if err := tx.Table("todo_task").
		Where("biz_type = 'QUOTE' AND biz_id = ? AND status = 'OPEN'", p.QuoteID).
		Updates(map[string]interface{}{"status": "DONE", "updated_at": p.Now, "updated_by": p.OperatorID}).Error; err != nil {
		return nil, fmt.Errorf("close todo_task: %w", err)
	}

	// task_job(ACTIVATE_QUOTE) 入队，next_run_at = valid_from
	var sheet quoteSheetRow
	if err := tx.Where("id = ?", p.QuoteID).Take(&sheet).Error; err != nil {
		return nil, fmt.Errorf("reload quote_sheet: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{"quote_sheet_id": p.QuoteID})
	job := taskJobRow{
		JobType:   "ACTIVATE_QUOTE",
		Payload:   payload,
		Status:    "PENDING",
		NextRunAt: sheet.ValidFrom,
		CreatedAt: p.Now,
		UpdatedAt: p.Now,
		RequestID: strPtr(p.RequestID),
		CreatedBy: int64Ptr(p.OperatorID),
		UpdatedBy: int64Ptr(p.OperatorID),
	}
	if err := tx.Create(&job).Error; err != nil {
		return nil, fmt.Errorf("insert task_job ACTIVATE_QUOTE: %w", err)
	}

	if err := r.audit.Record(ctx, AuditEntry{
		OperatorID:   p.OperatorID,
		OperatorRole: p.OperatorRole,
		Action:       "QUOTE_APPROVE",
		TargetType:   "QUOTE_SHEET",
		TargetID:     p.QuoteID,
		AfterValue: map[string]any{
			"status": "APPROVED_PENDING", "approved_by": p.OperatorID, "approved_at": p.Now,
		},
		SourceType: "HUMAN",
		RequestID:  p.RequestID,
	}); err != nil {
		return nil, err
	}

	return &supplier.ApproveResult{
		ID:         p.QuoteID,
		Status:     "APPROVED_PENDING",
		ApprovedAt: p.Now,
		ActivateAt: sheet.ValidFrom,
		Immediate:  !sheet.ValidFrom.After(p.Now),
	}, nil
}

// ---- §9 审批驳回 ----

// RejectQuote 审批驳回（单事务；路由已挂幂等中间件，tx 经 txOf 复用）。
func (r *ApproveRepo) RejectQuote(ctx context.Context, p supplier.RejectParams) (*supplier.RejectResult, error) {
	tx := r.txOf(ctx)

	res := tx.Table("quote_sheet").
		Where("id = ? AND status = 'APPROVING'", p.QuoteID).
		Updates(map[string]interface{}{
			"status":        "REJECTED",
			"reject_reason": p.Reason,
			"rejected_at":   p.Now,
			"updated_at":    p.Now,
			"updated_by":    p.OperatorID,
		})
	if res.Error != nil {
		return nil, fmt.Errorf("reject quote_sheet: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, supplier.ErrQuoteNotApprovable
	}

	if err := tx.Table("todo_task").
		Where("biz_type = 'QUOTE' AND biz_id = ? AND status = 'OPEN'", p.QuoteID).
		Updates(map[string]interface{}{"status": "DONE", "updated_at": p.Now, "updated_by": p.OperatorID}).Error; err != nil {
		return nil, fmt.Errorf("close todo_task: %w", err)
	}

	if err := r.audit.Record(ctx, AuditEntry{
		OperatorID:   p.OperatorID,
		OperatorRole: p.OperatorRole,
		Action:       "QUOTE_REJECT",
		TargetType:   "QUOTE_SHEET",
		TargetID:     p.QuoteID,
		AfterValue:   map[string]any{"status": "REJECTED", "reject_reason": p.Reason},
		Reason:       p.Reason,
		SourceType:   "HUMAN",
		RequestID:    p.RequestID,
	}); err != nil {
		return nil, err
	}

	return &supplier.RejectResult{ID: p.QuoteID, Status: "REJECTED", RejectedAt: p.Now}, nil
}

// ---- §10 激活 ----

// ListDueQuotes 取到期待激活单。
func (r *ApproveRepo) ListDueQuotes(ctx context.Context, now time.Time) ([]supplier.DueQuote, error) {
	var rows []quoteSheetRow
	if err := r.txOf(ctx).
		Where("status = 'APPROVED_PENDING' AND valid_from <= ?", now).
		Order("valid_from ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list due quotes: %w", err)
	}
	out := make([]supplier.DueQuote, 0, len(rows))
	for _, rr := range rows {
		out = append(out, supplier.DueQuote{ID: rr.ID, SupplierID: rr.SupplierID, VersionNo: rr.VersionNo, ValidFrom: rr.ValidFrom})
	}
	return out, nil
}

// ActivateOne 单条激活（独立事务，契约 §10 同事务动作 + §10.1 SKU 联动）。
func (r *ApproveRepo) ActivateOne(ctx context.Context, quoteID int64, p supplier.ActivateParams) (*supplier.ActivateDueItem, error) {
	tx := r.txOf(ctx)

	var sheet quoteSheetRow
	if err := tx.Where("id = ?", quoteID).Take(&sheet).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, supplier.ErrQuoteNotFound
		}
		return nil, fmt.Errorf("load quote_sheet %d: %w", quoteID, err)
	}

	// 1. 旧 EFFECTIVE → EXPIRED（valid_to = 本单 valid_from；D-04 修复：同一事务）
	var prevID *int64
	var prev quoteSheetRow
	prevErr := tx.Where("supplier_id = ? AND status = 'EFFECTIVE'", sheet.SupplierID).Take(&prev).Error
	switch {
	case prevErr == nil:
		res := tx.Table("quote_sheet").
			Where("id = ? AND status = 'EFFECTIVE'", prev.ID).
			Updates(map[string]interface{}{
				"status": "EXPIRED", "valid_to": sheet.ValidFrom,
				"updated_at": p.Now, "updated_by": p.OperatorID,
			})
		if res.Error != nil {
			return nil, fmt.Errorf("expire previous quote %d: %w", prev.ID, res.Error)
		}
		prevID = &prev.ID
	case errors.Is(prevErr, gorm.ErrRecordNotFound):
		// 无旧版本，正常
	default:
		return nil, fmt.Errorf("load previous effective: %w", prevErr)
	}

	// 2. 本单 → EFFECTIVE（条件更新做幂等与并发占位；uk_quote_effective 兜底）
	res := tx.Table("quote_sheet").
		Where("id = ? AND status = 'APPROVED_PENDING'", quoteID).
		Updates(map[string]interface{}{
			"status": "EFFECTIVE", "activated_at": p.Now,
			"updated_at": p.Now, "updated_by": p.OperatorID,
		})
	if res.Error != nil {
		if isUniqueViolation(res.Error, "uk_quote_effective") {
			return nil, supplier.ErrQuoteConflict
		}
		return nil, fmt.Errorf("activate quote_sheet %d: %w", quoteID, res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, supplier.ErrQuoteNotApprovable // 已被并发激活或状态漂移，幂等跳过
	}

	// 3. §10.1：明细中 PENDING_VERIFY 的 SKU → PURCHASABLE（条件更新，只动该状态的）
	var skuIDs []int64
	if err := tx.Table("quote_item").
		Where("quote_sheet_id = ?", quoteID).
		Pluck("sku_id", &skuIDs).Error; err != nil {
		return nil, fmt.Errorf("list quote skus: %w", err)
	}
	purchasable := make([]int64, 0, len(skuIDs))
	if len(skuIDs) > 0 {
		upd := tx.Table("model_sku").
			Where("id IN ? AND lifecycle_status = 'PENDING_VERIFY'", skuIDs).
			Updates(map[string]interface{}{
				"lifecycle_status": "PURCHASABLE",
				"updated_at":       p.Now,
				"updated_by":       p.OperatorID,
			})
		if upd.Error != nil {
			return nil, fmt.Errorf("promote sku purchasable: %w", upd.Error)
		}
		if upd.RowsAffected > 0 {
			// 找出实际被推进的 SKU（审计逐条）
			var promoted []int64
			if err := tx.Table("model_sku").
				Where("id IN ? AND lifecycle_status = 'PURCHASABLE' AND updated_at = ?", skuIDs, p.Now).
				Pluck("id", &promoted).Error; err != nil {
				return nil, fmt.Errorf("list promoted skus: %w", err)
			}
			purchasable = promoted
		}
	}

	// 4. task_job(COST_RECALC) 入队（M5 阶段消费；补录单 reason='RETRO'）
	reason := "QUOTE_EFFECTIVE"
	if sheet.Retroactive {
		reason = "RETRO"
	}
	recalcPayloadMap := map[string]any{
		"supplier_id": sheet.SupplierID, "quote_sheet_id": quoteID, "reason": reason,
	}
	// 补录单必须携带 effective_time（只影响该时点起之后的版本，绝不向更早回灌——§11）
	if sheet.Retroactive {
		recalcPayloadMap["effective_time"] = sheet.ValidFrom
	}
	recalcPayload, _ := json.Marshal(recalcPayloadMap)
	job := taskJobRow{
		JobType: "COST_RECALC", Payload: recalcPayload, Status: "PENDING", NextRunAt: p.Now,
		CreatedAt: p.Now, UpdatedAt: p.Now,
		RequestID: strPtr(p.RequestID), CreatedBy: int64Ptr(p.OperatorID), UpdatedBy: int64Ptr(p.OperatorID),
	}
	if err := tx.Create(&job).Error; err != nil {
		return nil, fmt.Errorf("insert task_job COST_RECALC: %w", err)
	}

	// 5. event_outbox(quote.effective)
	eventPayload, _ := json.Marshal(map[string]any{
		"quote_sheet_id": quoteID, "supplier_id": sheet.SupplierID,
		"version_no": sheet.VersionNo, "closed_previous_id": prevID,
	})
	ev := eventOutboxRow{
		EventType: "quote.effective", Payload: eventPayload, Status: "PENDING", NextRunAt: p.Now,
		CreatedAt: p.Now, UpdatedAt: p.Now,
		RequestID: strPtr(p.RequestID), CreatedBy: int64Ptr(p.OperatorID), UpdatedBy: int64Ptr(p.OperatorID),
	}
	if err := tx.Create(&ev).Error; err != nil {
		return nil, fmt.Errorf("insert event_outbox: %w", err)
	}

	// 6. ACTIVATE_QUOTE job → DONE
	if err := tx.Table("task_job").
		Where("job_type = 'ACTIVATE_QUOTE' AND status = 'PENDING' AND payload->>'quote_sheet_id' = ?", fmt.Sprint(quoteID)).
		Updates(map[string]interface{}{"status": "DONE", "updated_at": p.Now, "updated_by": p.OperatorID}).Error; err != nil {
		return nil, fmt.Errorf("close ACTIVATE_QUOTE job: %w", err)
	}

	// 7. audit_log：QUOTE_ACTIVATE ×1 + SKU_PURCHASABLE ×N（同一 request_id 串起追溯链）
	if err := r.audit.Record(ctx, AuditEntry{
		OperatorID: p.OperatorID, OperatorRole: p.OperatorRole,
		Action: "QUOTE_ACTIVATE", TargetType: "QUOTE_SHEET", TargetID: quoteID,
		AfterValue: map[string]any{
			"status": "EFFECTIVE", "activated_at": p.Now, "closed_previous_id": prevID,
		},
		SourceType: p.SourceType, RequestID: p.RequestID,
	}); err != nil {
		return nil, err
	}
	for _, skuID := range purchasable {
		if err := r.audit.Record(ctx, AuditEntry{
			OperatorID: p.OperatorID, OperatorRole: p.OperatorRole,
			Action: "SKU_PURCHASABLE", TargetType: "MODEL_SKU", TargetID: skuID,
			AfterValue: map[string]any{
				"lifecycle_status": "PURCHASABLE", "change_reason": "QUOTE_EFFECTIVE",
				"quote_sheet_id": quoteID,
			},
			SourceType: p.SourceType, RequestID: p.RequestID,
		}); err != nil {
			return nil, err
		}
	}

	return &supplier.ActivateDueItem{
		ID: quoteID, SupplierID: sheet.SupplierID, VersionNo: sheet.VersionNo, ClosedPreviousID: prevID,
	}, nil
}

// MarkActivateError 把单条激活失败写回 task_job(ACTIVATE_QUOTE).last_error（尽力而为）。
func (r *ApproveRepo) MarkActivateError(ctx context.Context, quoteID int64, cause string) error {
	return r.txOf(ctx).Table("task_job").
		Where("job_type = 'ACTIVATE_QUOTE' AND status = 'PENDING' AND payload->>'quote_sheet_id' = ?", fmt.Sprint(quoteID)).
		Updates(map[string]interface{}{"last_error": cause, "updated_at": time.Now().UTC()}).Error
}

// ---- §14 diff ----

// LoadQuoteDiff 取 diff 原始数据（固定 5 次查询：单头 / 明细组件 / 上一版 / 官方价 / 市场最低）。
func (r *ApproveRepo) LoadQuoteDiff(ctx context.Context, quoteID int64) (*supplier.QuoteDiffRaw, error) {
	tx := r.txOf(ctx)

	var sheet quoteSheetRow
	err := tx.Where("id = ?", quoteID).Take(&sheet).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load quote_sheet: %w", err)
	}

	// 明细 + 组件（含 sku_code / currency）
	type itemRow struct {
		ItemID        int64   `gorm:"column:item_id"`
		SkuID         int64   `gorm:"column:sku_id"`
		SkuCode       string  `gorm:"column:sku_code"`
		Currency      string  `gorm:"column:currency"`
		ComponentType string  `gorm:"column:component_type"`
		UnitPrice     string  `gorm:"column:unit_price"`
		Multiplier    *string `gorm:"column:multiplier"`
	}
	var itemRows []itemRow
	if err := tx.Table("quote_item qi").
		Select(`qi.id AS item_id, qi.sku_id, ms.sku_code, qi.currency,
			qc.component_type, qc.unit_price::text AS unit_price, qc.multiplier::text AS multiplier`).
		Joins("JOIN model_sku ms ON ms.id = qi.sku_id").
		Joins("JOIN quote_component qc ON qc.quote_item_id = qi.id").
		Where("qi.quote_sheet_id = ?", quoteID).
		Order("qi.id, qc.component_type").
		Scan(&itemRows).Error; err != nil {
		return nil, fmt.Errorf("load quote items: %w", err)
	}

	raw := &supplier.QuoteDiffRaw{SheetID: sheet.ID, SupplierID: sheet.SupplierID, VersionNo: sheet.VersionNo}
	itemIdx := make(map[int64]int)  // itemID → raw.Items 下标
	compIdx := make(map[string]int) // "skuID|componentType" → Components 下标
	skuIDs := make([]int64, 0, 8)
	for _, ir := range itemRows {
		idx, ok := itemIdx[ir.ItemID]
		if !ok {
			raw.Items = append(raw.Items, supplier.DiffRawItem{SKUID: ir.SkuID, SKUCode: ir.SkuCode, Currency: ir.Currency})
			idx = len(raw.Items) - 1
			itemIdx[ir.ItemID] = idx
			skuIDs = append(skuIDs, ir.SkuID)
		}
		price, perr := decimal.NewFromString(ir.UnitPrice)
		if perr != nil {
			return nil, fmt.Errorf("parse unit_price item=%d: %w", ir.ItemID, perr)
		}
		rc := supplier.DiffRawComponent{ComponentType: ir.ComponentType, UnitPrice: price}
		if ir.Multiplier != nil {
			m, merr := decimal.NewFromString(*ir.Multiplier)
			if merr != nil {
				return nil, fmt.Errorf("parse multiplier item=%d: %w", ir.ItemID, merr)
			}
			rc.Multiplier = &m
		}
		raw.Items[idx].Components = append(raw.Items[idx].Components, rc)
		compIdx[fmt.Sprintf("%d|%s", ir.SkuID, ir.ComponentType)] = len(raw.Items[idx].Components) - 1
	}
	if len(raw.Items) == 0 {
		return raw, nil
	}

	// 上一版本（同供应商 version_no 小于当前的最大版本，不限状态——5b 裁决）
	prevByKey := make(map[string]decimal.Decimal)
	var prevSheet quoteSheetRow
	prevErr := tx.Where("supplier_id = ? AND version_no < ?", sheet.SupplierID, sheet.VersionNo).
		Order("version_no DESC").Take(&prevSheet).Error
	if prevErr != nil && !errors.Is(prevErr, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("load previous quote: %w", prevErr)
	}
	if prevErr == nil {
		type prevRow struct {
			SkuID         int64  `gorm:"column:sku_id"`
			ComponentType string `gorm:"column:component_type"`
			UnitPrice     string `gorm:"column:unit_price"`
		}
		var prevRows []prevRow
		if err := tx.Table("quote_item qi").
			Select("qi.sku_id, qc.component_type, qc.unit_price::text AS unit_price").
			Joins("JOIN quote_component qc ON qc.quote_item_id = qi.id").
			Where("qi.quote_sheet_id = ?", prevSheet.ID).
			Scan(&prevRows).Error; err != nil {
			return nil, fmt.Errorf("load previous components: %w", err)
		}
		for _, pr := range prevRows {
			if v, perr := decimal.NewFromString(pr.UnitPrice); perr == nil {
				prevByKey[fmt.Sprintf("%d|%s", pr.SkuID, pr.ComponentType)] = v
			}
		}
	}

	// 官方价当前版本（is_current 且同币种）
	officialByKey := make(map[string]decimal.Decimal)
	type offRow struct {
		SkuID         int64  `gorm:"column:sku_id"`
		ComponentType string `gorm:"column:component_type"`
		UnitPrice     string `gorm:"column:unit_price"`
	}
	var offRows []offRow
	if err := tx.Table("price_component pc").
		Select("pv.sku_id, pc.component_type, pc.unit_price::text AS unit_price").
		Joins("JOIN price_version pv ON pv.id = pc.price_version_id").
		Joins("JOIN model_sku ms ON ms.id = pv.sku_id").
		Where("pv.is_current = true AND pv.currency = ms.native_currency AND pv.sku_id IN ?", skuIDs).
		Scan(&offRows).Error; err != nil {
		return nil, fmt.Errorf("load official components: %w", err)
	}
	for _, or := range offRows {
		if v, perr := decimal.NewFromString(or.UnitPrice); perr == nil {
			officialByKey[fmt.Sprintf("%d|%s", or.SkuID, or.ComponentType)] = v
		}
	}

	// 市场最低价（全市场 EFFECTIVE 报价，**不做归属过滤**——放开比价决议 §3.2）
	marketByKey := make(map[string]decimal.Decimal)
	type mktRow struct {
		SkuID         int64  `gorm:"column:sku_id"`
		ComponentType string `gorm:"column:component_type"`
		Best          string `gorm:"column:best"`
	}
	var mktRows []mktRow
	if err := tx.Table("quote_component qc").
		Select("qi.sku_id, qc.component_type, MIN(qc.unit_price)::text AS best").
		Joins("JOIN quote_item qi ON qi.id = qc.quote_item_id").
		Joins("JOIN quote_sheet qs ON qs.id = qi.quote_sheet_id").
		Where("qs.status = 'EFFECTIVE' AND qi.sku_id IN ?", skuIDs).
		Group("qi.sku_id, qc.component_type").
		Scan(&mktRows).Error; err != nil {
		return nil, fmt.Errorf("load market best: %w", err)
	}
	for _, mr := range mktRows {
		if v, perr := decimal.NewFromString(mr.Best); perr == nil {
			marketByKey[fmt.Sprintf("%d|%s", mr.SkuID, mr.ComponentType)] = v
		}
	}

	// 回填三方基准
	for i := range raw.Items {
		for j := range raw.Items[i].Components {
			key := fmt.Sprintf("%d|%s", raw.Items[i].SKUID, raw.Items[i].Components[j].ComponentType)
			if v, ok := prevByKey[key]; ok {
				raw.Items[i].Components[j].PrevPrice = &v
			}
			if v, ok := officialByKey[key]; ok {
				raw.Items[i].Components[j].OfficialPrice = &v
			}
			if v, ok := marketByKey[key]; ok {
				raw.Items[i].Components[j].MarketBest = &v
			}
		}
	}
	return raw, nil
}

// GetSysConfigDecimal 读 sys_config 数值配置。
func (r *ApproveRepo) GetSysConfigDecimal(ctx context.Context, key string) (decimal.Decimal, error) {
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
