// Package repo 的 customer_special.go：9b 特价审批 + 刷新 + 导出（SQL 读取与事务写入）。
//
// 红线：
//   - 不可变版本（红线 3）：customer_quote 刷新 = 同事务新版本 INSERT + 旧版本 EXPIRED。
//     禁止原地 UPDATE unit_price / floor_price。
//   - 事务边界（红线 8）：单事务内 DML + audit_log，跨表；事件出站用 event_outbox。
//   - 行级过滤（红线 7）：本批是后台接口，**不做 scope 过滤**——9a customer_quote 的
//     owner 字段用于列表过滤，本批 detail 访问由鉴权 + M9 权限点 + customer_id 一致性
//     保证。若需要做 owner 过滤在 9c 补。
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"model_bss/internal/domain/customer"
	"model_bss/internal/infra/db"
)

// CustomerSpecialRepo 提供 9b 的 SQL 能力。
//
// 命名特意与 9a 的 customer.Store 隔离（不并入既有接口——防止一个接口拖太多方法
// 破坏可测性），在 cmd/server/main.go 装配时把 repo 同时挂到 customer.NewService
// 与 customer.NewSpecialPriceService/NewRefreshService/NewExportService（同一进程
// 共享 *CustomerRepo 实例——它实现了所有所需接口的最小集）。
type CustomerSpecialRepo struct {
	base  *gorm.DB
	audit *AuditRepo
}

// NewCustomerSpecialRepo 构造。
func NewCustomerSpecialRepo(base *gorm.DB) *CustomerSpecialRepo {
	return &CustomerSpecialRepo{base: base, audit: NewAuditRepo(base)}
}

func (r *CustomerSpecialRepo) txOf(ctx context.Context) *gorm.DB {
	if tx := db.FromContext(ctx); tx != nil {
		return tx
	}
	return r.base
}

// ------------------------------------------------------------
// customer.SpecialPriceStore / RefreshStore / ExportStore 共用读取
// ------------------------------------------------------------

// quoteRowFull 是 customer_quote 的读侧行模型（含 special_price_status / refresh_diff）。
type quoteRowFull struct {
	ID                 int64      `gorm:"column:id"`
	CustomerID         int64      `gorm:"column:customer_id"`
	VersionNo          int        `gorm:"column:version_no"`
	Status             string     `gorm:"column:status"`
	QuoteType          string     `gorm:"column:quote_type"`
	SpecialPriceStatus *string    `gorm:"column:special_price_status"`
	ValidUntil         *time.Time `gorm:"column:valid_until"`
	PriceBookVersion   int        `gorm:"column:price_book_version"`
	OwnerSalesID       int64      `gorm:"column:owner_sales_operator_id"`
	OriginOwnerID      *int64     `gorm:"column:origin_owner_id"`
}

func (quoteRowFull) TableName() string { return "customer_quote" }

// LoadQuote 读一行。
func (r *CustomerSpecialRepo) LoadQuote(ctx context.Context, quoteID int64) (*customer.QuoteRow, error) {
	var row quoteRowFull
	if err := r.txOf(ctx).Table("customer_quote").
		Select("id", "customer_id", "version_no", "status", "quote_type",
			"special_price_status", "valid_until", "price_book_version",
			"owner_sales_operator_id", "origin_owner_id").
		Where("id = ?", quoteID).
		Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load customer_quote %d: %w", quoteID, err)
	}
	return &customer.QuoteRow{
		ID:                 row.ID,
		CustomerID:         row.CustomerID,
		VersionNo:          row.VersionNo,
		Status:             row.Status,
		QuoteType:          row.QuoteType,
		SpecialPriceStatus: row.SpecialPriceStatus,
		ValidUntil:         row.ValidUntil,
		PriceBookVersion:   row.PriceBookVersion,
		OwnerSalesID:       row.OwnerSalesID,
	}, nil
}

// quoteItemDetailRow 是 customer_quote_item 的读侧。
type quoteItemDetailRow struct {
	SKUID      int64  `gorm:"column:sku_id"`
	Currency   string `gorm:"column:currency"`
	UnitPrice  string `gorm:"column:unit_price"`
	FloorPrice string `gorm:"column:floor_price"`
}

func (quoteItemDetailRow) TableName() string { return "customer_quote_item" }

// LoadQuoteItemsDetail 读明细（decimal 解析失败即报错，不静默吞——8a §8a-8 教训）。
func (r *CustomerSpecialRepo) LoadQuoteItemsDetail(ctx context.Context, quoteID int64) ([]customer.QuoteItemDetail, error) {
	var rows []quoteItemDetailRow
	if err := r.txOf(ctx).Table("customer_quote_item").
		Select("sku_id", "currency", "unit_price", "floor_price").
		Where("customer_quote_id = ?", quoteID).
		Order("sku_id ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load customer_quote_item: %w", err)
	}
	out := make([]customer.QuoteItemDetail, 0, len(rows))
	for _, row := range rows {
		up, err := decimal.NewFromString(row.UnitPrice)
		if err != nil {
			return nil, fmt.Errorf("parse unit_price %q sku=%d: %w", row.UnitPrice, row.SKUID, err)
		}
		fp, err := decimal.NewFromString(row.FloorPrice)
		if err != nil {
			return nil, fmt.Errorf("parse floor_price %q sku=%d: %w", row.FloorPrice, row.SKUID, err)
		}
		out = append(out, customer.QuoteItemDetail{
			SKUID: row.SKUID, Currency: row.Currency, UnitPrice: up, FloorPrice: fp,
		})
	}
	return out, nil
}

// LoadCustomerLegalName 读 legal_subject.legal_name。
func (r *CustomerSpecialRepo) LoadCustomerLegalName(ctx context.Context, customerID int64) (string, error) {
	var name string
	if err := r.txOf(ctx).Table("customer_profile AS cp").
		Select("ls.legal_name").
		Joins("JOIN legal_subject ls ON ls.id = cp.subject_id").
		Where("cp.id = ?", customerID).
		Scan(&name).Error; err != nil {
		return "", fmt.Errorf("load customer legal_name %d: %w", customerID, err)
	}
	if name == "" {
		return "", fmt.Errorf("customer %d 无 legal_name（customer_profile 或 legal_subject 缺失）", customerID)
	}
	return name, nil
}

// LoadCurrentUnitCosts 读 cost_baseline.unit_cost（与 9a 的 customer.Store 同语义，独立写本 repo 避免循环）。
func (r *CustomerSpecialRepo) LoadCurrentUnitCosts(ctx context.Context, skuIDs []int64) (map[int64]customer.UnitCostInfo, error) {
	type costRow struct {
		SKUID           int64  `gorm:"column:sku_id"`
		UnitCost        string `gorm:"column:unit_cost"`
		Currency        string `gorm:"column:currency"`
		BaselineVersion int    `gorm:"column:baseline_version"`
	}
	if len(skuIDs) == 0 {
		return map[int64]customer.UnitCostInfo{}, nil
	}
	var rows []costRow
	// currency 从 cost_baseline 取；cost_component 的 baseline 外键是 cost_baseline_id。
	// 与 9a CustomerRepo.LoadCurrentUnitCosts 同口径（representCompJoin 骨架）。
	sql := `
SELECT cb.sku_id,
       c.unit_cost AS unit_cost,
       cb.currency AS currency,
       cb.version AS baseline_version
FROM cost_baseline cb
LEFT JOIN LATERAL (
    SELECT unit_cost FROM cost_component
     WHERE cost_baseline_id = cb.id
     ORDER BY CASE WHEN component_type='input' THEN 0 ELSE 1 END, component_type ASC
     LIMIT 1
) c ON TRUE
WHERE cb.sku_id IN (?) AND cb.is_current`
	if err := r.txOf(ctx).Raw(sql, skuIDs).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load unit costs: %w", err)
	}
	out := make(map[int64]customer.UnitCostInfo, len(rows))
	for _, r2 := range rows {
		uc, err := decimal.NewFromString(r2.UnitCost)
		if err != nil {
			return nil, fmt.Errorf("parse unit_cost sku=%d %q: %w", r2.SKUID, r2.UnitCost, err)
		}
		out[r2.SKUID] = customer.UnitCostInfo{
			UnitCost: uc, Currency: r2.Currency, BaselineVersion: r2.BaselineVersion,
		}
	}
	return out, nil
}

// LoadMinGrossMargin 读 sys_config。
func (r *CustomerSpecialRepo) LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error) {
	var val string
	if err := r.txOf(ctx).Table("sys_config").
		Where("config_key = ?", "min_gross_margin").
		Select("config_value").
		Scan(&val).Error; err != nil {
		return decimal.Zero, fmt.Errorf("load min_gross_margin: %w", err)
	}
	if val == "" {
		return decimal.Zero, errors.New("sys_config min_gross_margin not found")
	}
	d, err := decimal.NewFromString(val)
	if err != nil {
		return decimal.Zero, fmt.Errorf("parse min_gross_margin %q: %w", val, err)
	}
	return d, nil
}

// ------------------------------------------------------------
// 特价：Request / Apply
// ------------------------------------------------------------

// RequestSpecialPriceTx 单事务：change_request + 2 步 approval_step + customer_quote.special_price_status='PENDING' + audit。
func (r *CustomerSpecialRepo) RequestSpecialPriceTx(ctx context.Context, quote customer.QuoteRow, impact *customer.MarginImpact, reason, expectedMargin string, operatorID int64, requestID string) (*customer.SpecialPriceResult, error) {
	now := time.Now().UTC()
	var res *customer.SpecialPriceResult
	err := r.txOf(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 干记得读一遍：update 要确保是当前 PENDING 状态期望（防并发双审批）。
		var cur quoteRowFull
		if err := tx.Table("customer_quote").
			Select("id", "special_price_status", "status").
			Where("id = ?", quote.ID).Take(&cur).Error; err != nil {
			return fmt.Errorf("reload quote %d: %w", quote.ID, err)
		}
		if cur.Status != customer.QuoteStatusDraft {
			return customer.ErrQuoteNotDraft
		}
		if cur.SpecialPriceStatus != nil && *cur.SpecialPriceStatus == customer.SpecialPricePending {
			return customer.ErrQuoteAlreadyPending
		}

		// 2. 设 special_price_status='PENDING'。
		if err := tx.Model(&quoteRowFull{}).Where("id = ?", quote.ID).
			Updates(map[string]any{
				"special_price_status": customer.SpecialPricePending,
				"updated_at":           now,
				"updated_by":           operatorID,
				"request_id":           requestID,
			}).Error; err != nil {
			return fmt.Errorf("update quote special_price_status=PENDING: %w", err)
		}

		// 3. change_request：sku_id=NULL / change_type=SPECIAL_PRICE / payload margin_impact。
		payload := map[string]any{
			"customer_quote_id": quote.ID,
			"current_price":     impact.CurrentPrice,
			"target_price":      impact.TargetPrice,
			"target_margin":     impact.TargetMargin,
			"reason":            reason,
			"expected_margin":   expectedMargin,
		}
		payloadJSON, _ := json.Marshal(payload)
		marginPreview, _ := json.Marshal(impact) // 冗余到 margin_preview（与 7b 的位置约定一致）
		crMap := map[string]any{
			"change_type":    customer.ChangeSpecialPrice,
			"sku_id":         nil,
			"risk_level":     "MID",
			"payload":        payloadJSON,
			"margin_preview": marginPreview,
			"status":         "PENDING",
			"created_by":     fmt.Sprintf("staff:%d", operatorID),
			"created_at":     now,
			"updated_at":     now,
			"request_id":     requestID,
		}
		if err := tx.Model(&changeRequestRow{}).Create(crMap).Error; err != nil {
			return fmt.Errorf("insert change_request: %w", err)
		}
		var crID int64
		if err := tx.Raw(`SELECT id FROM change_request WHERE request_id = ? ORDER BY id DESC LIMIT 1`, requestID).
			Row().Scan(&crID); err != nil {
			return fmt.Errorf("load change_request id: %w", err)
		}

		// 4. 2 步 approval_step（PRICING_OP → FINANCE，biz_type=change_type，裁决 7）。
		for i, role := range []string{"PRICING_OP", "FINANCE"} {
			step := approvalStepRow{
				BizType:      customer.ChangeSpecialPrice,
				BizID:        crID,
				StepNo:       i + 1,
				RequiredRole: role,
				CreatedAt:    now,
				UpdatedAt:    now,
				RequestID:    strPtr(requestID),
				CreatedBy:    int64Ptr(operatorID),
			}
			if err := tx.Create(&step).Error; err != nil {
				return fmt.Errorf("insert approval_step %d: %w", i+1, err)
			}
		}

		// 5. audit_log SPECIAL_PRICE_REQUEST（红线 10）。
		if err := r.audit.Record(db.WithDB(ctx, tx), AuditEntry{
			OperatorID: operatorID, OperatorRole: "STAFF",
			Action: "SPECIAL_PRICE_REQUEST", TargetType: "CUSTOMER_QUOTE", TargetID: quote.ID,
			SourceType: "HUMAN", RequestID: requestID,
			AfterValue: map[string]any{
				"quote_id":           quote.ID,
				"current_price":      impact.CurrentPrice,
				"current_margin":     impact.CurrentMargin,
				"target_price":       impact.TargetPrice,
				"target_margin":      impact.TargetMargin,
				"delta_gap_distance": impact.DeltaGapDistance,
				"reason":             reason,
				"expected_margin":    expectedMargin,
				"change_request_id":  crID,
			},
		}); err != nil {
			return fmt.Errorf("audit special_price_request: %w", err)
		}

		res = &customer.SpecialPriceResult{
			QuoteID:         quote.ID,
			ChangeRequestID: crID,
			StepCount:       2,
			Status:          customer.SpecialPricePending,
			MarginImpact:    impact,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// ApplySpecialPriceApproved 审批全步通过后调用（main.go ApprovedHook 分发）。
// 单事务：customer_quote.special_price_status='APPROVED' + event_outbox('customer.special_price.approved') + audit。
// 不改 unit_price（价格是用户定的，审批只是"允许以这个价格报出去"的许可）。
func (r *CustomerSpecialRepo) ApplySpecialPriceApproved(ctx context.Context, changeRequestID int64, operatorID int64, requestID string) error {
	now := time.Now().UTC()
	return r.txOf(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 查 payload 拿 customer_quote_id。
		//    用 struct Take（与 ModelRepo.LoadChangePayload 同款）：
		//    GORM Scan(&[]byte) 对 jsonb 类型处理有 bug——
		//    会把 JSON 数据按 uint8 单字节解析报 "converting driver.Value type []uint8 to a uint8"。
		var cr struct {
			Payload []byte `gorm:"column:payload"`
		}
		if err := tx.Table("change_request").Where("id = ?", changeRequestID).
			Select("payload").Take(&cr).Error; err != nil {
			return fmt.Errorf("load payload cr=%d: %w", changeRequestID, err)
		}
		payloadRaw := cr.Payload
		if len(payloadRaw) == 0 {
			return fmt.Errorf("change_request %d payload 为空", changeRequestID)
		}
		var payload map[string]any
		if err := json.Unmarshal(payloadRaw, &payload); err != nil {
			return fmt.Errorf("parse payload: %w", err)
		}
		quoteIDRaw, ok := payload["customer_quote_id"].(float64)
		if !ok {
			return fmt.Errorf("change_request %d payload.customer_quote_id 缺失/类型错", changeRequestID)
		}
		quoteID := int64(quoteIDRaw)

		// 2. 改 quote。
		if err := tx.Model(&quoteRowFull{}).Where("id = ?", quoteID).
			Updates(map[string]any{
				"special_price_status": customer.SpecialPriceApproved,
				"updated_at":           now,
				"updated_by":           operatorID,
				"request_id":           requestID,
			}).Error; err != nil {
			return fmt.Errorf("update quote special_price_status=APPROVED: %w", err)
		}

		// 3. event_outbox。
		evPayload, _ := json.Marshal(map[string]any{
			"customer_quote_id": quoteID,
			"change_request_id": changeRequestID,
		})
		if err := tx.Create(&eventOutboxRow{
			EventType: "customer.special_price.approved", Payload: evPayload,
			Status: "PENDING", NextRunAt: now,
			CreatedAt: now, UpdatedAt: now,
			RequestID: strPtr(requestID), CreatedBy: int64Ptr(operatorID), UpdatedBy: int64Ptr(operatorID),
		}).Error; err != nil {
			return fmt.Errorf("enqueue customer.special_price.approved: %w", err)
		}

		// 4. audit。
		if err := r.audit.Record(db.WithDB(ctx, tx), AuditEntry{
			OperatorID: operatorID, OperatorRole: "STAFF",
			Action: "SPECIAL_PRICE_APPROVED", TargetType: "CUSTOMER_QUOTE", TargetID: quoteID,
			SourceType: "HUMAN", RequestID: requestID,
			AfterValue: map[string]any{
				"quote_id":             quoteID,
				"change_request_id":    changeRequestID,
				"special_price_status": customer.SpecialPriceApproved,
			},
		}); err != nil {
			return fmt.Errorf("audit special_price_approved: %w", err)
		}
		return nil
	})
}

// ------------------------------------------------------------
// 刷新：RefreshTx
// ------------------------------------------------------------

// RefreshTx 单事务：旧版本 EXPIRED + 新版本 INSERT（unit_price 保留、floor_price 新值）+ items + audit。
//
// 调用方已保证「不是 allSame」——至少一行的 floor_price 变了。
func (r *CustomerSpecialRepo) RefreshTx(ctx context.Context, old customer.QuoteRow, newItems []customer.QuoteItemDetail, changed []customer.RefreshChangedItem, reason string, operatorID int64, requestID string, now time.Time) (*customer.RefreshResult, error) {
	var result *customer.RefreshResult
	err := r.txOf(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. MAX(version_no)+1 同客户内单调递增（与 9a GenerateQuoteTx 同口径）。
		var maxVer int
		if err := tx.Raw(`SELECT COALESCE(MAX(version_no),0) FROM customer_quote WHERE customer_id = ?`, old.CustomerID).
			Row().Scan(&maxVer); err != nil {
			return fmt.Errorf("max version_no: %w", err)
		}
		newVer := maxVer + 1

		// 2. 旧版本 EXPIRED。
		if err := tx.Model(&quoteRowFull{}).Where("id = ?", old.ID).
			Updates(map[string]any{
				"status":     customer.QuoteStatusExpired,
				"updated_at": now,
				"updated_by": operatorID,
				"request_id": requestID,
			}).Error; err != nil {
			return fmt.Errorf("expire old quote %d: %w", old.ID, err)
		}

		// 3. 新版本 INSERT。
		refreshDiff, _ := json.Marshal(map[string]any{
			"reason":         reason,
			"refreshed_at":   now,
			"changed_items":  changed,
			"old_version_no": old.VersionNo,
		})
		newRow := map[string]any{
			"customer_id":             old.CustomerID,
			"version_no":              newVer,
			"status":                  string(customer.QuoteStatusDraft),
			"need_refresh":            false,
			"refresh_diff":            refreshDiff,
			"special_price_status":    nil,                  // 新版本重新归零
			"valid_until":             old.ValidUntil,       // 沿用
			"price_book_version":      old.PriceBookVersion, // 沿用
			"owner_sales_operator_id": old.OwnerSalesID,
			"origin_owner_id":         nil,
			"quote_type":              old.QuoteType,
			"created_by":              operatorID,
			"created_at":              now,
			"updated_at":              now,
			"request_id":              requestID,
		}
		if err := tx.Table("customer_quote").Create(newRow).Error; err != nil {
			return fmt.Errorf("insert new quote version: %w", err)
		}
		var newID int64
		if err := tx.Raw(`SELECT id FROM customer_quote WHERE request_id = ? ORDER BY id DESC LIMIT 1`, requestID).
			Row().Scan(&newID); err != nil {
			return fmt.Errorf("load new quote id: %w", err)
		}

		// 4. 新版本 items。
		for _, it := range newItems {
			row2 := customerQuoteItemRow{
				CustomerQuoteID: newID,
				SkuID:           it.SKUID,
				Currency:        it.Currency,
				UnitPrice:       it.UnitPrice.StringFixed(8),
				FloorPrice:      it.FloorPrice.StringFixed(8),
				CreatedAt:       now,
				UpdatedAt:       now,
				RequestID:       strPtr(requestID),
				CreatedBy:       int64Ptr(operatorID),
				UpdatedBy:       int64Ptr(operatorID),
			}
			if err := tx.Create(&row2).Error; err != nil {
				return fmt.Errorf("insert item sku=%d: %w", it.SKUID, err)
			}
		}

		// 5. audit QUOTE_REFRESHED。
		if err := r.audit.Record(db.WithDB(ctx, tx), AuditEntry{
			OperatorID: operatorID, OperatorRole: "STAFF",
			Action: "QUOTE_REFRESHED", TargetType: "CUSTOMER_QUOTE", TargetID: old.ID,
			Reason: reason, SourceType: "HUMAN", RequestID: requestID,
			BeforeValue: map[string]any{
				"quote_id": old.ID, "version_no": old.VersionNo, "status": old.Status,
			},
			AfterValue: map[string]any{
				"quote_id": newID, "version_no": newVer, "status": customer.QuoteStatusDraft,
				"refresh_diff": string(refreshDiff),
			},
		}); err != nil {
			return fmt.Errorf("audit quote_refreshed: %w", err)
		}

		result = &customer.RefreshResult{
			QuoteID:      newID,
			OldVersionNo: old.VersionNo,
			NewVersionNo: newVer,
			ChangedItems: changed,
			Unchanged:    false,
			Status:       customer.QuoteStatusDraft,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
