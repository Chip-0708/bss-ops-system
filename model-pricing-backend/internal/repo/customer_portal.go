// Package repo 的 customer_portal.go：9c 客户门户 6 接口的只读装配 + AcceptQuote 单事务。
//
// 红线对齐：
//   - **行级过滤**：所有查询强制 `WHERE customer_id = ?`（当前登录账号 owner_id）；
//     报价接受前再校验一次（应用层 + DB 层双保险）。
//   - **field_mask**：DTO 层就不 SELECT 成本/毛利字段（unit_cost / floor_price / margin / baseline），
//     物理上不存在于响应。
//   - **幂等**：AcceptQuote 挂 Idempotency-Key（见 handler），应用层再校验状态。
//   - **金额字符串**：unit_price / credit_limit / credit_used / deposit_amount 全部 StringFixed。
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"model_bss/internal/domain/customer"
	"model_bss/internal/infra/db"
)

// CustomerPortalRepo 客户门户仓储。
type CustomerPortalRepo struct {
	base  *gorm.DB
	audit *AuditRepo
}

// NewCustomerPortalRepo 构造。
func NewCustomerPortalRepo(base *gorm.DB) *CustomerPortalRepo {
	return &CustomerPortalRepo{base: base, audit: NewAuditRepo(base)}
}

func (r *CustomerPortalRepo) txOf(ctx context.Context) *gorm.DB {
	if tx := db.FromContext(ctx); tx != nil {
		return tx
	}
	return r.base
}

// ------------------------------------------------------------
// 1. LoadCustomerLevelCode
// ------------------------------------------------------------

// LoadCustomerLevelCode 查客户 level_code。found=false 表示客户不存在。
func (r *CustomerPortalRepo) LoadCustomerLevelCode(ctx context.Context, customerID int64) (string, bool, error) {
	var row struct {
		LevelCode string `gorm:"column:level_code"`
	}
	err := r.txOf(ctx).Table("customer_profile").
		Where("id = ?", customerID).
		Select("level_code").
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("load customer_profile id=%d: %w", customerID, err)
	}
	return row.LevelCode, true, nil
}

// ------------------------------------------------------------
// 2. LoadEffectivePriceBookView
// ------------------------------------------------------------

// LoadEffectivePriceBookView 查 level_code 当前生效价目表（含 items，联 model_sku + LATERAL 代表组件）。
func (r *CustomerPortalRepo) LoadEffectivePriceBookView(ctx context.Context, levelCode string) (*customer.PortalPriceBookView, error) {
	// 书头
	var book struct {
		ID        int64 `gorm:"column:id"`
		VersionNo int   `gorm:"column:version_no"`
	}
	err := r.txOf(ctx).Table("price_book").
		Where("level_code = ? AND status = 'EFFECTIVE'", levelCode).
		Select("id, version_no").
		Take(&book).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load price_book level=%s: %w", levelCode, err)
	}
	// items：联 model_sku 拿 sku_code；联 price_book_component 拿代表组件 unit_price。
	// **不 SELECT floor_price / baseline_version / policy_id**——field_mask 物理剔除。
	var rows []struct {
		SKUID     int64  `gorm:"column:sku_id"`
		SKUCode   string `gorm:"column:sku_code"`
		Currency  string `gorm:"column:currency"`
		UnitPrice string `gorm:"column:unit_price"`
	}
	err = r.txOf(ctx).Table("price_book_item pbi").
		Joins("JOIN model_sku ms ON ms.id = pbi.sku_id").
		Joins(`LEFT JOIN LATERAL (
			SELECT pbc.unit_price
			FROM price_book_component pbc
			WHERE pbc.price_book_item_id = pbi.id
			ORDER BY CASE WHEN pbc.component_type = 'input' THEN 0 ELSE 1 END, pbc.component_type
			LIMIT 1
		) comp ON true`).
		Where("pbi.price_book_id = ?", book.ID).
		Select("pbi.sku_id, ms.sku_code, pbi.currency, comp.unit_price").
		Order("pbi.sku_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load price_book items book=%d: %w", book.ID, err)
	}
	items := make([]customer.PortalPriceBookItem, 0, len(rows))
	currency := ""
	for _, rw := range rows {
		if currency == "" {
			currency = rw.Currency
		}
		// unit_price 是 numeric(20,8)，转字符串标准化。
		up, perr := decimal.NewFromString(rw.UnitPrice)
		if perr != nil {
			return nil, fmt.Errorf("parse unit_price sku=%d %q: %w", rw.SKUID, rw.UnitPrice, perr)
		}
		items = append(items, customer.PortalPriceBookItem{
			SKUID:     rw.SKUID,
			SKUCode:   rw.SKUCode,
			Currency:  rw.Currency,
			UnitPrice: up.StringFixed(8),
		})
	}
	return &customer.PortalPriceBookView{
		LevelCode: levelCode,
		VersionNo: book.VersionNo,
		Currency:  currency,
		Items:     items,
	}, nil
}

// ------------------------------------------------------------
// 3. ListPortalQuotes（报价 + 合同联合）
// ------------------------------------------------------------

// ListPortalQuotes 客户报价 + 合同联合分页查询（行级过滤在 SQL 里）。
func (r *CustomerPortalRepo) ListPortalQuotes(ctx context.Context, customerID int64, q customer.PortalQuoteQuery) (*customer.PortalQuoteListResult, error) {
	offset := (q.Page - 1) * q.Size

	// 报价部分：customer_quote + items count + total_amount。
	// **行级过滤**：customer_id = ?。
	var quoteRows []struct {
		ID          int64      `gorm:"column:id"`
		VersionNo   int        `gorm:"column:version_no"`
		Status      string     `gorm:"column:status"`
		QuoteType   string     `gorm:"column:quote_type"`
		ValidUntil  *time.Time `gorm:"column:valid_until"`
		ItemCount   int        `gorm:"column:item_count"`
		TotalAmount string     `gorm:"column:total_amount"`
		Currency    string     `gorm:"column:currency"`
	}
	err := r.txOf(ctx).Table("customer_quote cq").
		Joins(`LEFT JOIN LATERAL (
			SELECT COUNT(*) AS cnt, COALESCE(SUM(cqi.unit_price), 0) AS total, MAX(cqi.currency) AS cur
			FROM customer_quote_item cqi
			WHERE cqi.customer_quote_id = cq.id
		) agg ON true`).
		Where("cq.customer_id = ?", customerID).
		Select(`cq.id, cq.version_no, cq.status, cq.quote_type, cq.valid_until,
			agg.cnt AS item_count, agg.total AS total_amount, agg.cur AS currency`).
		Order("cq.id DESC").
		Offset(offset).Limit(q.Size).
		Scan(&quoteRows).Error
	if err != nil {
		return nil, fmt.Errorf("list quotes customer=%d: %w", customerID, err)
	}

	var totalQ int64
	if err := r.txOf(ctx).Table("customer_quote").
		Where("customer_id = ?", customerID).
		Count(&totalQ).Error; err != nil {
		return nil, fmt.Errorf("count quotes: %w", err)
	}

	// 合同部分：customer_price_book 行（按 sku_id DESC 当附加列表）。
	var contractRows []struct {
		ID           int64     `gorm:"column:id"`
		SKUID        int64     `gorm:"column:sku_id"`
		Currency     string    `gorm:"column:currency"`
		UnitPrice    string    `gorm:"column:unit_price"`
		ContractFrom time.Time `gorm:"column:contract_from"`
		ContractTo   time.Time `gorm:"column:contract_to"`
	}
	err = r.txOf(ctx).Table("customer_price_book").
		Where("customer_id = ?", customerID).
		Select("id, sku_id, currency, unit_price, contract_from, contract_to").
		Order("id DESC").
		Scan(&contractRows).Error
	if err != nil {
		return nil, fmt.Errorf("list contracts customer=%d: %w", customerID, err)
	}

	list := make([]customer.PortalQuoteItem, 0, len(quoteRows)+len(contractRows))
	for _, rw := range quoteRows {
		total, perr := decimal.NewFromString(rw.TotalAmount)
		if perr != nil {
			return nil, fmt.Errorf("parse total quote=%d %q: %w", rw.ID, rw.TotalAmount, perr)
		}
		list = append(list, customer.PortalQuoteItem{
			ID:          rw.ID,
			VersionNo:   rw.VersionNo,
			Status:      rw.Status,
			QuoteType:   rw.QuoteType,
			ValidUntil:  rw.ValidUntil,
			ItemCount:   rw.ItemCount,
			TotalAmount: total.StringFixed(8),
			Currency:    rw.Currency,
			SourceKind:  "QUOTE",
		})
	}
	for _, rw := range contractRows {
		up, perr := decimal.NewFromString(rw.UnitPrice)
		if perr != nil {
			return nil, fmt.Errorf("parse contract unit_price id=%d %q: %w", rw.ID, rw.UnitPrice, perr)
		}
		cf := rw.ContractFrom
		ct := rw.ContractTo
		list = append(list, customer.PortalQuoteItem{
			ID:           rw.ID,
			VersionNo:    0,
			Status:       "EFFECTIVE",
			QuoteType:    "CONTRACT",
			ValidUntil:   &ct,
			ItemCount:    1,
			TotalAmount:  up.StringFixed(8),
			Currency:     rw.Currency,
			SourceKind:   "CONTRACT",
			ContractFrom: &cf,
			ContractTo:   &ct,
		})
	}
	return &customer.PortalQuoteListResult{
		List:  list,
		Total: int(totalQ) + len(contractRows),
		Page:  q.Page,
		Size:  q.Size,
	}, nil
}

// ------------------------------------------------------------
// 4. LoadQuoteForAccept + AcceptQuoteTx
// ------------------------------------------------------------

// LoadQuoteForAccept 读报价接受前校验字段 + 明细（**不读 floor_price**——portal 剔除）。
func (r *CustomerPortalRepo) LoadQuoteForAccept(ctx context.Context, quoteID int64) (*customer.QuoteRow, []customer.QuoteItemDetail, error) {
	var row quoteRowFull
	err := r.txOf(ctx).Table("customer_quote").
		Where("id = ?", quoteID).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("load quote %d: %w", quoteID, err)
	}
	q := &customer.QuoteRow{
		ID:                 row.ID,
		CustomerID:         row.CustomerID,
		VersionNo:          row.VersionNo,
		Status:             row.Status,
		QuoteType:          row.QuoteType,
		SpecialPriceStatus: row.SpecialPriceStatus,
		ValidUntil:         row.ValidUntil,
		PriceBookVersion:   row.PriceBookVersion,
		OwnerSalesID:       row.OwnerSalesID,
	}
	items, err := r.loadQuoteItemsForPortal(ctx, quoteID)
	if err != nil {
		return nil, nil, err
	}
	return q, items, nil
}

// loadQuoteItemsForPortal 读明细。**不 SELECT floor_price**——客户门户 DTO 物理剔除成本红线。
func (r *CustomerPortalRepo) loadQuoteItemsForPortal(ctx context.Context, quoteID int64) ([]customer.QuoteItemDetail, error) {
	var rows []struct {
		SKUID     int64  `gorm:"column:sku_id"`
		Currency  string `gorm:"column:currency"`
		UnitPrice string `gorm:"column:unit_price"`
	}
	if err := r.txOf(ctx).Table("customer_quote_item").
		Select("sku_id", "currency", "unit_price").
		Where("customer_quote_id = ?", quoteID).
		Order("sku_id ASC").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load customer_quote_item: %w", err)
	}
	out := make([]customer.QuoteItemDetail, 0, len(rows))
	for _, row := range rows {
		up, err := decimal.NewFromString(row.UnitPrice)
		if err != nil {
			return nil, fmt.Errorf("parse unit_price %q sku=%d: %w", row.UnitPrice, row.SKUID, err)
		}
		out = append(out, customer.QuoteItemDetail{
			SKUID: row.SKUID, Currency: row.Currency, UnitPrice: up,
			// FloorPrice 留零值——portal 不读
		})
	}
	return out, nil
}

// AcceptQuoteTx 接受报价的单事务：
//  1. UPDATE customer_quote.status='EFFECTIVE'（守卫：当前是 APPROVED 或 special_price_status='APPROVED'）
//  2. INSERT customer_price_book 行（每个 quote_item 一行；uk_cpb(customer_id, sku_id, contract_from)）
//  3. audit CUSTOMER_QUOTE_ACCEPTED
//
// **行级过滤**：UPDATE 的 WHERE 子句带 `customer_id = ?`——双保险（应用层已校验，DB 层再校验）。
func (r *CustomerPortalRepo) AcceptQuoteTx(ctx context.Context, quoteID, customerID int64, items []customer.QuoteItemDetail, validUntil *time.Time, operatorID int64, requestID string, now time.Time) (*customer.PortalAcceptResult, error) {
	var res *customer.PortalAcceptResult
	err := r.txOf(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. UPDATE quote：守卫（status='APPROVED' 或 special_price_status='APPROVED'）+ 行级过滤。
		//    注意：uk_cq_formal 是 partial unique on status='FORMAL'——'EFFECTIVE' 不冲突。
		upd := tx.Model(&quoteRowFull{}).
			Where("id = ? AND customer_id = ?", quoteID, customerID).
			Where("status = 'APPROVED' OR special_price_status = 'APPROVED'").
			Updates(map[string]any{
				"status":     "EFFECTIVE",
				"updated_at": now,
				"updated_by": operatorID,
				"request_id": requestID,
			})
		if upd.Error != nil {
			return fmt.Errorf("update quote status: %w", upd.Error)
		}
		if upd.RowsAffected == 0 {
			return customer.ErrPortalQuoteNotApprovable
		}

		// 2. INSERT customer_price_book：每个 quote_item 一行。
		//    contract_from = now；contract_to = valid_until（如果有，否则 now+1 年占位——
		//    设计裁决：APPLY/CLONE 无 valid_until 时 contract_to 给一个远端时间；
		//    更严谨做法应在 docs/api/09 §6 明确，登记 9c-①）。
		contractFrom := now
		contractTo := now.AddDate(1, 0, 0)
		if validUntil != nil {
			contractTo = *validUntil
		}
		inserted := 0
		for _, it := range items {
			row := map[string]any{
				"customer_id":     customerID,
				"sku_id":          it.SKUID,
				"currency":        it.Currency,
				"unit_price":      it.UnitPrice.StringFixed(8),
				"contract_from":   contractFrom,
				"contract_to":     contractTo,
				"source_quote_id": quoteID,
				"created_at":      now,
				"updated_at":      now,
				"request_id":      requestID,
				"created_by":      operatorID,
				"updated_by":      operatorID,
			}
			if err := tx.Table("customer_price_book").Create(&row).Error; err != nil {
				return fmt.Errorf("insert customer_price_book sku=%d: %w", it.SKUID, err)
			}
			inserted++
		}

		// 3. audit。
		if err := r.audit.Record(db.WithDB(ctx, tx), AuditEntry{
			OperatorID: operatorID, OperatorRole: "CUSTOMER",
			Action: "CUSTOMER_QUOTE_ACCEPTED", TargetType: "CUSTOMER_QUOTE", TargetID: quoteID,
			SourceType: "HUMAN", RequestID: requestID,
			AfterValue: map[string]any{
				"quote_id":     quoteID,
				"customer_id":  customerID,
				"new_status":   "EFFECTIVE",
				"contract_cnt": inserted,
			},
		}); err != nil {
			return fmt.Errorf("audit accept: %w", err)
		}
		res = &customer.PortalAcceptResult{
			QuoteID:     quoteID,
			NewStatus:   "EFFECTIVE",
			ContractCnt: inserted,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// ------------------------------------------------------------
// 5. LoadBilling
// ------------------------------------------------------------

// LoadBilling 读 customer_profile 的计费字段（bills=[] 占位，9c-①）。
func (r *CustomerPortalRepo) LoadBilling(ctx context.Context, customerID int64) (*customer.PortalBillingView, error) {
	var row struct {
		CreditLimit   string `gorm:"column:credit_limit"`
		CreditUsed    string `gorm:"column:credit_used"`
		DepositAmount string `gorm:"column:deposit_amount"`
		DepositStatus string `gorm:"column:deposit_status"`
		BillingCycle  int    `gorm:"column:billing_cycle"`
	}
	err := r.txOf(ctx).Table("customer_profile").
		Where("id = ?", customerID).
		Select("credit_limit, credit_used, deposit_amount, deposit_status, billing_cycle").
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customer.ErrPortalCustomerNotFound
		}
		return nil, fmt.Errorf("load billing customer=%d: %w", customerID, err)
	}
	// 金额格式化：credit/deposit 是 numeric(20,2)。
	cl, _ := decimal.NewFromString(row.CreditLimit)
	cu, _ := decimal.NewFromString(row.CreditUsed)
	da, _ := decimal.NewFromString(row.DepositAmount)
	return &customer.PortalBillingView{
		CreditLimit:   cl.StringFixed(2),
		CreditUsed:    cu.StringFixed(2),
		DepositAmount: da.StringFixed(2),
		DepositStatus: row.DepositStatus,
		BillingCycle:  row.BillingCycle,
		Bills:         []interface{}{}, // 9c-① 占位
	}, nil
}

// ------------------------------------------------------------
// 6. ListNotifications
// ------------------------------------------------------------

// ListNotifications 客户通知分页（created_at DESC）。
func (r *CustomerPortalRepo) ListNotifications(ctx context.Context, customerID int64, q customer.PortalPageQuery) (*customer.PortalNotificationListResult, error) {
	offset := (q.Page - 1) * q.Size
	var rows []struct {
		ID        int64      `gorm:"column:id"`
		Type      string     `gorm:"column:type"`
		Title     string     `gorm:"column:title"`
		Content   string     `gorm:"column:content"`
		ReadAt    *time.Time `gorm:"column:read_at"`
		CreatedAt time.Time  `gorm:"column:created_at"`
	}
	err := r.txOf(ctx).Table("customer_notification").
		Where("customer_id = ?", customerID).
		Select("id, type, title, content, read_at, created_at").
		Order("created_at DESC, id DESC").
		Offset(offset).Limit(q.Size).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list notifications customer=%d: %w", customerID, err)
	}
	var total int64
	if err := r.txOf(ctx).Table("customer_notification").
		Where("customer_id = ?", customerID).
		Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count notifications: %w", err)
	}
	list := make([]customer.PortalNotificationItem, 0, len(rows))
	for _, rw := range rows {
		list = append(list, customer.PortalNotificationItem{
			ID:        rw.ID,
			Type:      rw.Type,
			Title:     rw.Title,
			Content:   rw.Content,
			ReadAt:    rw.ReadAt,
			CreatedAt: rw.CreatedAt,
		})
	}
	return &customer.PortalNotificationListResult{
		List:  list,
		Total: int(total),
		Page:  q.Page,
		Size:  q.Size,
	}, nil
}

// ------------------------------------------------------------
// 7. LoadHome（聚合）
// ------------------------------------------------------------

// LoadHome 聚合查询。levelCode 由 service 层显式传入（变异验证 #2 锁点：
// repo **必须**用这个 levelCode 过滤 common_models，不能忽略）。
func (r *CustomerPortalRepo) LoadHome(ctx context.Context, customerID int64, levelCode string) (*customer.PortalHomeView, error) {
	// 1) balance：与 LoadBilling 同源，但只取 4 个字段。
	var bal struct {
		CreditLimit   string `gorm:"column:credit_limit"`
		CreditUsed    string `gorm:"column:credit_used"`
		DepositAmount string `gorm:"column:deposit_amount"`
		DepositStatus string `gorm:"column:deposit_status"`
	}
	err := r.txOf(ctx).Table("customer_profile").
		Where("id = ?", customerID).
		Select("credit_limit, credit_used, deposit_amount, deposit_status").
		Take(&bal).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customer.ErrPortalCustomerNotFound
		}
		return nil, fmt.Errorf("load home balance: %w", err)
	}
	cl, _ := decimal.NewFromString(bal.CreditLimit)
	cu, _ := decimal.NewFromString(bal.CreditUsed)
	da, _ := decimal.NewFromString(bal.DepositAmount)

	// 2) pending_count：DRAFT/PENDING 报价数。
	var pending int64
	if err := r.txOf(ctx).Table("customer_quote").
		Where("customer_id = ? AND status IN ('DRAFT','PENDING')", customerID).
		Count(&pending).Error; err != nil {
		return nil, fmt.Errorf("count pending quotes: %w", err)
	}

	// 3) unread_notifications：read_at IS NULL。
	var unread int64
	if err := r.txOf(ctx).Table("customer_notification").
		Where("customer_id = ? AND read_at IS NULL", customerID).
		Count(&unread).Error; err != nil {
		return nil, fmt.Errorf("count unread notifications: %w", err)
	}

	// 4) common_models：按 service 传入的 levelCode → EFFECTIVE price_book → price_book_item。
	// **变异验证 #2 锁点**：不得忽略 levelCode，不得不加 status='EFFECTIVE'。
	models := make([]customer.PortalHomeCommonModel, 0)
	if levelCode != "" {
		var rows []struct {
			SKUID    int64  `gorm:"column:sku_id"`
			SKUCode  string `gorm:"column:sku_code"`
			Currency string `gorm:"column:currency"`
		}
		err = r.txOf(ctx).Table("price_book_item pbi").
			Joins("JOIN price_book pb ON pb.id = pbi.price_book_id").
			Joins("JOIN model_sku ms ON ms.id = pbi.sku_id").
			Where("pb.level_code = ? AND pb.status = 'EFFECTIVE'", levelCode).
			Select("pbi.sku_id, ms.sku_code, pbi.currency").
			Order("pbi.sku_id").
			Scan(&rows).Error
		if err != nil {
			return nil, fmt.Errorf("load common models: %w", err)
		}
		for _, rw := range rows {
			models = append(models, customer.PortalHomeCommonModel{
				SKUID:    rw.SKUID,
				SKUCode:  rw.SKUCode,
				Currency: rw.Currency,
			})
		}
	}
	return &customer.PortalHomeView{
		Balance: customer.PortalHomeBalance{
			CreditLimit:   cl.StringFixed(2),
			CreditUsed:    cu.StringFixed(2),
			DepositAmount: da.StringFixed(2),
			DepositStatus: bal.DepositStatus,
		},
		PendingCount:        int(pending),
		UnreadNotifications: int(unread),
		CommonModels:        models,
	}, nil
}
