// Package repo 的 supplier_quote.go：报价链路（05-quotes.md §3/§4/§5）的 GORM 实现。
// 写操作统一经 txOf(ctx) 取事务——幂等中间件会经 db.FromContext 注入同一事务，
// 直接用 r.base 会导致幂等记录与业务变更不在同一事务（红线 5）。
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

// QuoteRepo 是 supplier.QuoteStore 的 GORM 实现。
// 内嵌 *SupplierRepo 复用 txOf 约定（幂等中间件注入的事务被优先使用）。
type QuoteRepo struct {
	*SupplierRepo
	audit *AuditRepo
}

// NewQuoteRepo 构造报价仓储。
func NewQuoteRepo(base *gorm.DB) *QuoteRepo {
	return &QuoteRepo{
		SupplierRepo: NewSupplierRepo(base),
		audit:        NewAuditRepo(base),
	}
}

var _ supplier.QuoteStore = (*QuoteRepo)(nil)

// ---- 行模型（写模型只含真实列，绝不包含 JOIN 别名列——与 skuRow 同一纪律） ----

type quoteSheetRow struct {
	ID              int64      `gorm:"primaryKey"`
	SupplierID      int64      `gorm:"column:supplier_id"`
	VersionNo       int        `gorm:"column:version_no"`
	Status          string     `gorm:"column:status"`
	ValidFrom       time.Time  `gorm:"column:valid_from"`
	ValidTo         time.Time  `gorm:"column:valid_to"`
	Source          string     `gorm:"column:source"`
	Retroactive     bool       `gorm:"column:retroactive"`
	AuditReason     *string    `gorm:"column:audit_reason"`
	ChangeRequestID *int64     `gorm:"column:change_request_id"`
	SubmittedBy     *int64     `gorm:"column:submitted_by"`
	SubmittedAt     *time.Time `gorm:"column:submitted_at"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
	RequestID       *string    `gorm:"column:request_id"`
	CreatedBy       *int64     `gorm:"column:created_by"`
	UpdatedBy       *int64     `gorm:"column:updated_by"`
	// 000012 扩展列
	RejectReason    *string    `gorm:"column:reject_reason"`
	ApprovedBy      *int64     `gorm:"column:approved_by"`
	ApprovedAt      *time.Time `gorm:"column:approved_at"`
	ActivatedAt     *time.Time `gorm:"column:activated_at"`
	RemoveConfirmed bool       `gorm:"column:remove_confirmed"`
	GraceUntil      *time.Time `gorm:"column:grace_until"`
	// 000015 扩展列
	RejectedAt *time.Time `gorm:"column:rejected_at"`
}

func (quoteSheetRow) TableName() string { return "quote_sheet" }

type quoteItemRow struct {
	ID           int64     `gorm:"primaryKey"`
	QuoteSheetID int64     `gorm:"column:quote_sheet_id"`
	SkuID        int64     `gorm:"column:sku_id"`
	Currency     string    `gorm:"column:currency"`
	FxTier       *string   `gorm:"column:fx_tier"`
	Constraints  []byte    `gorm:"column:constraints_"`
	CreatedAt    time.Time `gorm:"column:created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
	RequestID    *string   `gorm:"column:request_id"`
	CreatedBy    *int64    `gorm:"column:created_by"`
	UpdatedBy    *int64    `gorm:"column:updated_by"`
}

func (quoteItemRow) TableName() string { return "quote_item" }

type quoteComponentRow struct {
	ID            int64     `gorm:"primaryKey"`
	QuoteItemID   int64     `gorm:"column:quote_item_id"`
	ComponentType string    `gorm:"column:component_type"`
	Multiplier    *string   `gorm:"column:multiplier"`
	UnitPrice     string    `gorm:"column:unit_price"`
	CreatedAt     time.Time `gorm:"column:created_at"`
	UpdatedAt     time.Time `gorm:"column:updated_at"`
	RequestID     *string   `gorm:"column:request_id"`
	CreatedBy     *int64    `gorm:"column:created_by"`
	UpdatedBy     *int64    `gorm:"column:updated_by"`
}

func (quoteComponentRow) TableName() string { return "quote_component" }

type todoTaskRow struct {
	ID           int64      `gorm:"primaryKey"`
	BizType      string     `gorm:"column:biz_type"`
	BizID        int64      `gorm:"column:biz_id"`
	AssigneeID   *int64     `gorm:"column:assignee_id"`
	AssigneeRole *string    `gorm:"column:assignee_role"`
	Title        string     `gorm:"column:title"`
	Priority     string     `gorm:"column:priority"`
	Status       string     `gorm:"column:status"`
	DueAt        *time.Time `gorm:"column:due_at"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
	RequestID    *string    `gorm:"column:request_id"`
	CreatedBy    *int64     `gorm:"column:created_by"`
	UpdatedBy    *int64     `gorm:"column:updated_by"`
}

func (todoTaskRow) TableName() string { return "todo_task" }

// ---- 查询 ----

// FindQuotableSKUs 按 ID 批量取回可报价 SKU。
// ModelName 与 FindTemplateSKUs 同源（family_name + " " + sku_code，supplier_import.go:110）——
// LI-005 校验 CSV 里的 model_name 列需要库内基准；用同一份拼接规则保证
// 「模板下载时的 model_name」与「导入校验的库内 model_name」恒等。
func (r *QuoteRepo) FindQuotableSKUs(ctx context.Context, skuIDs []int64) (map[int64]supplier.QuotableSKU, error) {
	out := make(map[int64]supplier.QuotableSKU, len(skuIDs))
	if len(skuIDs) == 0 {
		return out, nil
	}
	type row struct {
		ID             int64  `gorm:"column:id"`
		SkuCode        string `gorm:"column:sku_code"`
		NativeCurrency string `gorm:"column:native_currency"`
		FamilyName     string `gorm:"column:family_name"`
	}
	var rows []row
	if err := r.txOf(ctx).
		Table("model_sku s").
		Select("s.id, s.sku_code, s.native_currency, f.name AS family_name").
		Joins("LEFT JOIN model_family f ON f.id = s.family_id").
		Where("s.id IN ? AND s.lifecycle_status IN ?", skuIDs,
			[]string{"PUBLISHED", "PURCHASABLE", "PENDING_VERIFY"}).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("find quotable skus: %w", err)
	}
	for _, rr := range rows {
		out[rr.ID] = supplier.QuotableSKU{
			ID: rr.ID, SkuCode: rr.SkuCode, NativeCurrency: rr.NativeCurrency,
			ModelName: rr.FamilyName + " " + rr.SkuCode,
		}
	}
	return out, nil
}

// FindOfficialComponents 按 SKU 批量取回当前官方价组件（is_current=true 且 currency=native）。
// unit_price 走 ::text 再由 decimal 解析，避免 numeric → float 丢精度。
func (r *QuoteRepo) FindOfficialComponents(ctx context.Context, skuIDs []int64) (map[int64][]supplier.OfficialComponent, error) {
	out := make(map[int64][]supplier.OfficialComponent, len(skuIDs))
	if len(skuIDs) == 0 {
		return out, nil
	}
	type row struct {
		SkuID         int64  `gorm:"column:sku_id"`
		ComponentType string `gorm:"column:component_type"`
		UnitPrice     string `gorm:"column:unit_price"`
	}
	var rows []row
	if err := r.txOf(ctx).
		Table("price_component AS pc").
		Select("pv.sku_id, pc.component_type, pc.unit_price::text AS unit_price").
		Joins("JOIN price_version pv ON pv.id = pc.price_version_id").
		Joins("JOIN model_sku ms ON ms.id = pv.sku_id").
		Where("pv.is_current = true AND pv.currency = ms.native_currency AND pv.sku_id IN ?", skuIDs).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("find official components: %w", err)
	}
	for _, rr := range rows {
		price, err := decimal.NewFromString(rr.UnitPrice)
		if err != nil {
			return nil, fmt.Errorf("parse official unit_price sku=%d %s: %w", rr.SkuID, rr.ComponentType, err)
		}
		out[rr.SkuID] = append(out[rr.SkuID], supplier.OfficialComponent{
			ComponentType: rr.ComponentType,
			UnitPrice:     price,
		})
	}
	return out, nil
}

// FindNonTerminalQuote 返回阻塞新提交的报价及其 SKU 数。
func (r *QuoteRepo) FindNonTerminalQuote(ctx context.Context, supplierID int64) (*supplier.NonTerminalQuote, error) {
	var row struct {
		ID       int64 `gorm:"column:id"`
		SKUCount int   `gorm:"column:sku_count"`
	}
	err := r.txOf(ctx).Table("quote_sheet qs").
		Select("qs.id, (SELECT COUNT(*) FROM quote_item qi WHERE qi.quote_sheet_id = qs.id) AS sku_count").
		Where("qs.supplier_id = ? AND qs.status IN ?", supplierID, []string{"SUBMITTED", "APPROVING", "APPROVED_PENDING"}).
		Order("qs.created_at DESC").Limit(1).Scan(&row).Error
	if err != nil {
		return nil, fmt.Errorf("find non-terminal quote: %w", err)
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &supplier.NonTerminalQuote{ID: row.ID, SKUCount: row.SKUCount}, nil
}

// ---- 落库 ----

// CreateQuoteWithTodo 单事务落库：报价单 + 明细 + 组件 + 审批待办 + 审计日志。
// version_no 在事务内取 max(同供应商)+1；uk_quote_ver / uk_quote_effective / uk_quote_pending
// 唯一索引兜底命中时映射为 supplier.ErrQuoteConflict（409）。
func (r *QuoteRepo) CreateQuoteWithTodo(ctx context.Context, p supplier.CreateQuoteParams) (*supplier.SubmitQuoteResult, error) {
	tx := r.txOf(ctx)
	now := p.SubmittedAt

	// version_no = max(同供应商)+1（互斥前置已挡并发，uk_quote_ver 兜底）
	var maxVer *int
	if err := tx.Table("quote_sheet").
		Where("supplier_id = ?", p.Supplier.ID).
		Select("MAX(version_no)").
		Scan(&maxVer).Error; err != nil {
		return nil, fmt.Errorf("compute next version_no: %w", err)
	}
	versionNo := 1
	if maxVer != nil {
		versionNo = *maxVer + 1
	}

	source := p.Source
	if source == "" {
		source = "MANUAL" // 零值兜底，5a-2 调用方不传时行为不变
	}
	sheet := quoteSheetRow{
		SupplierID:  p.Supplier.ID,
		VersionNo:   versionNo,
		Status:      "APPROVING", // SUBMITTED→APPROVING 同事务瞬时完成，无中间态
		ValidFrom:   p.ValidFrom,
		ValidTo:     p.ValidTo,
		Source:      source,
		Retroactive: false,
		SubmittedBy: int64Ptr(p.OperatorID),
		SubmittedAt: &now,
		CreatedAt:   now,
		UpdatedAt:   now,
		RequestID:   strPtr(p.RequestID),
		CreatedBy:   int64Ptr(p.OperatorID),
		UpdatedBy:   int64Ptr(p.OperatorID),
	}
	if p.Remark != "" {
		sheet.AuditReason = &p.Remark // remark 写入 audit_reason（§3 请求体说明）
	}
	if err := tx.Create(&sheet).Error; err != nil {
		if isUniqueViolation(err, "uk_quote_ver") ||
			isUniqueViolation(err, "uk_quote_effective") ||
			isUniqueViolation(err, "uk_quote_pending") {
			return nil, supplier.ErrQuoteConflict
		}
		return nil, fmt.Errorf("insert quote_sheet: %w", err)
	}

	for _, it := range p.Items {
		var constraintsJSON []byte
		if it.Constraints != nil {
			b, err := json.Marshal(it.Constraints)
			if err != nil {
				return nil, fmt.Errorf("marshal constraints: %w", err)
			}
			constraintsJSON = b
		}
		item := quoteItemRow{
			QuoteSheetID: sheet.ID,
			SkuID:        it.SKUID,
			Currency:     it.Currency,
			FxTier:       it.FxTier,
			Constraints:  constraintsJSON,
			CreatedAt:    now,
			UpdatedAt:    now,
			RequestID:    strPtr(p.RequestID),
			CreatedBy:    int64Ptr(p.OperatorID),
			UpdatedBy:    int64Ptr(p.OperatorID),
		}
		if err := tx.Create(&item).Error; err != nil {
			if isUniqueViolation(err, "uk_quote_item") {
				return nil, supplier.ErrQuoteConflict
			}
			return nil, fmt.Errorf("insert quote_item: %w", err)
		}

		for _, c := range it.Components {
			comp := quoteComponentRow{
				QuoteItemID:   item.ID,
				ComponentType: c.ComponentType,
				UnitPrice:     c.UnitPrice.String(),
				CreatedAt:     now,
				UpdatedAt:     now,
				RequestID:     strPtr(p.RequestID),
				CreatedBy:     int64Ptr(p.OperatorID),
				UpdatedBy:     int64Ptr(p.OperatorID),
			}
			if c.Multiplier != nil {
				s := c.Multiplier.String()
				comp.Multiplier = &s
			}
			if err := tx.Create(&comp).Error; err != nil {
				if isUniqueViolation(err, "uk_qc") {
					return nil, supplier.ErrQuoteConflict
				}
				return nil, fmt.Errorf("insert quote_component: %w", err)
			}
		}
	}

	// 审批待办：归属采购 = 该供应商引入人（§3 落库清单）
	todo := todoTaskRow{
		BizType:    "QUOTE",
		BizID:      sheet.ID,
		AssigneeID: int64Ptr(p.Supplier.OwnerProcurementOperatorID),
		Title:      fmt.Sprintf("报价待审批：%s v%d", p.Supplier.LegalName, versionNo),
		Priority:   "MID",
		Status:     "OPEN",
		CreatedAt:  now,
		UpdatedAt:  now,
		RequestID:  strPtr(p.RequestID),
		CreatedBy:  int64Ptr(p.OperatorID),
		UpdatedBy:  int64Ptr(p.OperatorID),
	}
	if err := tx.Create(&todo).Error; err != nil {
		return nil, fmt.Errorf("insert todo_task: %w", err)
	}

	// 审计日志（红线 10：价格相关操作 100% 写 audit_log）
	// action 与 source 对齐：MANUAL→QUOTE_SUBMIT，IMPORT→QUOTE_IMPORT（渠道是审计维度）
	auditAction := "QUOTE_SUBMIT"
	if source == "IMPORT" {
		auditAction = "QUOTE_IMPORT"
	}
	if err := r.audit.Record(ctx, AuditEntry{
		OperatorID:   p.OperatorID,
		OperatorRole: "SUPPLIER",
		Action:       auditAction,
		TargetType:   "QUOTE_SHEET",
		TargetID:     sheet.ID,
		AfterValue: map[string]any{
			"id": sheet.ID, "supplier_id": sheet.SupplierID, "version_no": versionNo,
			"status": sheet.Status, "valid_from": sheet.ValidFrom, "valid_to": sheet.ValidTo,
			"source": sheet.Source, "item_count": len(p.Items), "clamped": p.Clamped,
		},
		SourceType: "HUMAN",
		RequestID:  p.RequestID,
	}); err != nil {
		return nil, err
	}

	return &supplier.SubmitQuoteResult{
		ID:          sheet.ID,
		SupplierID:  sheet.SupplierID,
		VersionNo:   versionNo,
		Status:      sheet.Status,
		ValidFrom:   sheet.ValidFrom,
		ValidTo:     sheet.ValidTo,
		Source:      sheet.Source,
		Retroactive: sheet.Retroactive,
		Clamped:     p.Clamped,
		ItemCount:   len(p.Items),
		SubmittedAt: now,
	}, nil
}

// ---- 历史与详情 ----

// historyRow 是历史列表的扫描行（列表查询专用，含聚合计数列）。
type historyRow struct {
	ID           int64      `gorm:"column:id"`
	VersionNo    int        `gorm:"column:version_no"`
	Status       string     `gorm:"column:status"`
	ValidFrom    time.Time  `gorm:"column:valid_from"`
	ValidTo      time.Time  `gorm:"column:valid_to"`
	Source       string     `gorm:"column:source"`
	SubmittedAt  *time.Time `gorm:"column:submitted_at"`
	ItemCount    int        `gorm:"column:item_count"`
	RejectReason *string    `gorm:"column:reject_reason"`
	ApprovedAt   *time.Time `gorm:"column:approved_at"`
	RejectedAt   *time.Time `gorm:"column:rejected_at"`
}

// ListQuoteHistory 按供应商硬过滤的分页历史（§4）。
// item_count 用相关子查询聚合计数，不 N+1。
func (r *QuoteRepo) ListQuoteHistory(ctx context.Context, supplierID int64, q supplier.QuoteHistoryQuery) (*supplier.QuoteHistoryResult, error) {
	applyFilters := func(tx *gorm.DB) *gorm.DB {
		tx = tx.Where("qs.supplier_id = ?", supplierID)
		if q.Status != "" {
			tx = tx.Where("qs.status = ?", q.Status)
		}
		if q.SkuID != nil {
			tx = tx.Where("EXISTS (SELECT 1 FROM quote_item qi WHERE qi.quote_sheet_id = qs.id AND qi.sku_id = ?)", *q.SkuID)
		}
		if q.From != nil {
			tx = tx.Where("qs.valid_from >= ?", *q.From)
		}
		if q.To != nil {
			tx = tx.Where("qs.valid_from <= ?", *q.To)
		}
		return tx
	}

	var total int64
	if err := applyFilters(r.txOf(ctx).Table("quote_sheet AS qs")).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count quote history: %w", err)
	}

	offset := (q.Page - 1) * q.Size
	var rows []historyRow
	if err := applyFilters(r.txOf(ctx).
		Table("quote_sheet AS qs").
		Select(`qs.id, qs.version_no, qs.status, qs.valid_from, qs.valid_to, qs.source,
			qs.submitted_at, qs.reject_reason, qs.approved_at, qs.rejected_at,
			(SELECT COUNT(*) FROM quote_item qi WHERE qi.quote_sheet_id = qs.id) AS item_count`)).
		Offset(offset).Limit(q.Size).Order("qs.id DESC").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list quote history: %w", err)
	}

	list := make([]supplier.QuoteHistoryItem, 0, len(rows))
	for _, rr := range rows {
		list = append(list, supplier.QuoteHistoryItem{
			ID:          rr.ID,
			VersionNo:   rr.VersionNo,
			Status:      rr.Status,
			ValidFrom:   rr.ValidFrom,
			ValidTo:     rr.ValidTo,
			Source:      rr.Source,
			ItemCount:   rr.ItemCount,
			SubmittedAt: rr.SubmittedAt,
			Decision:    buildDecision(rr.Status, rr.RejectReason, rr.ApprovedAt, rr.RejectedAt),
		})
	}
	return &supplier.QuoteHistoryResult{List: list, Total: total, Page: q.Page, Size: q.Size}, nil
}

// buildDecision 组装审批结论（§4 decision）。
// APPROVED → decided_at=approved_at；REJECTED → decided_at=rejected_at（000015 新列，5b 写入）。
func buildDecision(status string, rejectReason *string, approvedAt, rejectedAt *time.Time) *supplier.QuoteDecision {
	switch status {
	case "REJECTED":
		return &supplier.QuoteDecision{Result: "REJECTED", Reason: rejectReason, DecidedAt: rejectedAt}
	case "APPROVED_PENDING", "EFFECTIVE", "EXPIRED":
		if approvedAt != nil {
			return &supplier.QuoteDecision{Result: "APPROVED", DecidedAt: approvedAt}
		}
	}
	return nil
}

// detailSheetRow 是详情的单头扫描行（占位说明：直接用 quoteSheetRow 即可，别名仅提高可读性）。

// detailItemRow 是详情明细的扫描行（列表查询专用：平铺列 + JOIN 别名，绝不用于写入）。
// 注意不能内嵌 quoteItemRow：内嵌结构带 primaryKey 标签会让 GORM 按模型展开 Select，
// 与手写 Select 列冲突导致扫描错位。
type detailItemRow struct {
	ID           int64   `gorm:"column:id"`
	QuoteSheetID int64   `gorm:"column:quote_sheet_id"`
	SkuID        int64   `gorm:"column:sku_id"`
	Currency     string  `gorm:"column:currency"`
	FxTier       *string `gorm:"column:fx_tier"`
	Constraints  []byte  `gorm:"column:constraints_"`
	SkuCode      string  `gorm:"column:sku_code"`
	FamilyName   string  `gorm:"column:family_name"`
}

// GetQuoteDetail 按 id + supplier_id 取详情；不属于该供应商时返回 (nil, nil)。
// 查询次数固定为 3（单头 / 明细 / 组件）。
func (r *QuoteRepo) GetQuoteDetail(ctx context.Context, supplierID, quoteID int64) (*supplier.QuoteDetail, error) {
	tx := r.txOf(ctx)

	var sheet quoteSheetRow
	err := tx.Where("id = ? AND supplier_id = ?", quoteID, supplierID).Take(&sheet).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get quote_sheet: %w", err)
	}

	var items []detailItemRow
	if err := tx.Table("quote_item AS qi").
		Select("qi.*, ms.sku_code, mf.name AS family_name").
		Joins("JOIN model_sku ms ON ms.id = qi.sku_id").
		Joins("LEFT JOIN model_family mf ON mf.id = ms.family_id").
		Where("qi.quote_sheet_id = ?", sheet.ID).
		Order("qi.id").
		Scan(&items).Error; err != nil {
		return nil, fmt.Errorf("list quote_item: %w", err)
	}

	itemIDs := make([]int64, 0, len(items))
	for _, it := range items {
		itemIDs = append(itemIDs, it.ID)
	}
	type compScanRow struct {
		QuoteItemID   int64   `gorm:"column:quote_item_id"`
		ComponentType string  `gorm:"column:component_type"`
		Multiplier    *string `gorm:"column:multiplier"`
		UnitPrice     string  `gorm:"column:unit_price"`
	}
	compsByItem := make(map[int64][]supplier.QuoteDetailComponent, len(itemIDs))
	if len(itemIDs) > 0 {
		var comps []compScanRow
		if err := tx.Table("quote_component").
			Select("quote_item_id, component_type, multiplier::text AS multiplier, unit_price::text AS unit_price").
			Where("quote_item_id IN ?", itemIDs).
			Order("quote_item_id, component_type").
			Scan(&comps).Error; err != nil {
			return nil, fmt.Errorf("list quote_component: %w", err)
		}
		for _, c := range comps {
			compsByItem[c.QuoteItemID] = append(compsByItem[c.QuoteItemID], supplier.QuoteDetailComponent{
				ComponentType: c.ComponentType,
				Multiplier:    c.Multiplier,
				UnitPrice:     c.UnitPrice,
			})
		}
	}

	detail := &supplier.QuoteDetail{
		ID:          sheet.ID,
		SupplierID:  sheet.SupplierID,
		VersionNo:   sheet.VersionNo,
		Status:      sheet.Status,
		ValidFrom:   sheet.ValidFrom,
		ValidTo:     sheet.ValidTo,
		Source:      sheet.Source,
		Retroactive: sheet.Retroactive,
		Remark:      sheet.AuditReason,
		SubmittedAt: sheet.SubmittedAt,
		Decision:    buildDecision(sheet.Status, sheet.RejectReason, sheet.ApprovedAt, sheet.RejectedAt),
	}
	for _, it := range items {
		var constraints map[string]any
		if len(it.Constraints) > 0 {
			if err := json.Unmarshal(it.Constraints, &constraints); err != nil {
				return nil, fmt.Errorf("unmarshal constraints item=%d: %w", it.ID, err)
			}
		}
		detail.Items = append(detail.Items, supplier.QuoteDetailItem{
			ID:          it.ID,
			SKUID:       it.SkuID,
			SKUCode:     it.SkuCode,
			ModelName:   it.FamilyName + " " + it.SkuCode,
			Currency:    it.Currency,
			FxTier:      supplier.CanonicalFxTier(it.FxTier), // §0.4：回显规范形式（"6.800"→"6.8"）
			Constraints: constraints,
			Components:  compsByItem[it.ID],
		})
	}
	return detail, nil
}
