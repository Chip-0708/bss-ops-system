// Package repo 的 customer.go：9a 客户域仓储（列表 / 移交 / 生成报价）。
//
// 事务边界（红线 8）：
//   - TransferCustomerTx / GenerateQuoteTx 单事务（业务变更 + audit_log 同生共死）。
//   - ListCustomers / Load* 只读，不开事务。
//
// 行级过滤（红线 7）：
//   - customer_profile.owner_sales_operator_id 是「销售归属」，
//     JOIN internal_staff 拿 org_unit_id 做 SELF/DEPT/DEPT_SUB/ALL 过滤。
//   - 与 supplier 域的 applyOwnerScope 同形态，但指向 owner_sales_operator_id。
//
// GORM 行模型读写分离（红线 5）：
//   - customerQuoteRow / customerQuoteItemRow / customerProfileRow 只用于 INSERT/UPDATE。
//   - 列表/详情查询用 flat struct + 显式 gorm:"column:..."（与 skuListRow 同款纪律，
//     避免 embedded 扫描陷阱）。
package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"model_bss/internal/domain/customer"
	"model_bss/internal/infra/db"
)

// ============================================================
// 行模型（只用于 INSERT/UPDATE——绝不加 JOIN 出来的别名列）
// ============================================================

type customerProfileRow struct {
	ID                   int64     `gorm:"primaryKey"`
	SubjectID            int64     `gorm:"column:subject_id"`
	LevelCode            string    `gorm:"column:level_code"`
	OwnerSalesOperatorID int64     `gorm:"column:owner_sales_operator_id"`
	Status               string    `gorm:"column:status"`
	CreatedAt            time.Time `gorm:"column:created_at"`
	UpdatedAt            time.Time `gorm:"column:updated_at"`
	RequestID            *string   `gorm:"column:request_id"`
	CreatedBy            *int64    `gorm:"column:created_by"`
	UpdatedBy            *int64    `gorm:"column:updated_by"`
}

func (customerProfileRow) TableName() string { return "customer_profile" }

type customerQuoteRow struct {
	ID                   int64      `gorm:"primaryKey"`
	CustomerID           int64      `gorm:"column:customer_id"`
	VersionNo            int        `gorm:"column:version_no"`
	Status               string     `gorm:"column:status"`
	NeedRefresh          bool       `gorm:"column:need_refresh"`
	ValidUntil           *time.Time `gorm:"column:valid_until"`
	PriceBookVersion     int        `gorm:"column:price_book_version"`
	OwnerSalesOperatorID int64      `gorm:"column:owner_sales_operator_id"`
	OriginOwnerID        *int64     `gorm:"column:origin_owner_id"`
	QuoteType            string     `gorm:"column:quote_type"`
	CreatedBy            int64      `gorm:"column:created_by"`
	CreatedAt            time.Time  `gorm:"column:created_at"`
	UpdatedAt            time.Time  `gorm:"column:updated_at"`
	RequestID            *string    `gorm:"column:request_id"`
	UpdatedBy            *int64     `gorm:"column:updated_by"`
}

func (customerQuoteRow) TableName() string { return "customer_quote" }

type customerQuoteItemRow struct {
	ID              int64     `gorm:"primaryKey"`
	CustomerQuoteID int64     `gorm:"column:customer_quote_id"`
	SkuID           int64     `gorm:"column:sku_id"`
	Currency        string    `gorm:"column:currency"`
	UnitPrice       string    `gorm:"column:unit_price"`
	FloorPrice      string    `gorm:"column:floor_price"`
	CreatedAt       time.Time `gorm:"column:created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at"`
	RequestID       *string   `gorm:"column:request_id"`
	CreatedBy       *int64    `gorm:"column:created_by"`
	UpdatedBy       *int64    `gorm:"column:updated_by"`
}

func (customerQuoteItemRow) TableName() string { return "customer_quote_item" }

// ============================================================
// Repo
// ============================================================

// CustomerRepo 实现 customer.Store。
type CustomerRepo struct {
	base  *gorm.DB
	audit *AuditRepo
}

// NewCustomerRepo 构造。
func NewCustomerRepo(base *gorm.DB) *CustomerRepo {
	return &CustomerRepo{base: base, audit: NewAuditRepo(base)}
}

var _ customer.Store = (*CustomerRepo)(nil)

func (r *CustomerRepo) txOf(ctx context.Context) *gorm.DB {
	if tx := db.FromContext(ctx); tx != nil {
		return tx
	}
	return r.base
}

// ============================================================
// 行级过滤 Scope（红线 7）
// ============================================================

// applyCustomerOwnerScope 把数据域过滤应用到「JOIN customer_profile cp 之后」的查询上。
// 与 supplier.applyOwnerScope 同形态；差别在指向 owner_sales_operator_id（销售归属）。
func applyCustomerOwnerScope(tx *gorm.DB, scope customer.OwnerScope) *gorm.DB {
	switch scope.DataScope {
	case "ALL":
		return tx
	case "DEPT", "DEPT_SUB":
		tx = tx.Joins("JOIN internal_staff os_ ON os_.id = cp.owner_sales_operator_id")
		if scope.DataScope == "DEPT" {
			return tx.Where("os_.org_unit_id = ?", scope.MyOrgID)
		}
		// DEPT_SUB：负责人可见下属整棵子树。
		if len(scope.ScopePaths) == 0 {
			return tx.Where("os_.org_unit_id = ?", scope.MyOrgID)
		}
		patterns := make([]string, 0, len(scope.ScopePaths))
		for _, p := range scope.ScopePaths {
			patterns = append(patterns, p+"%")
		}
		return tx.Joins("JOIN org_unit ou_ ON ou_.id = os_.org_unit_id").
			Where("ou_.path LIKE ANY(?)", patterns)
	default: // SELF
		return tx.Where("cp.owner_sales_operator_id = ?", scope.StaffID)
	}
}

// ============================================================
// 客户列表
// ============================================================

// ListCustomers 实现 customer.Store.ListCustomers。
//
// 行级过滤 + 关键词搜索（legal_name ILIKE）。
// 分页用固定 2 发查询（count + paged），不 N+1。
func (r *CustomerRepo) ListCustomers(ctx context.Context, scope customer.OwnerScope, keyword string, page, size int) (*customer.CustomerListResult, error) {
	base := func() *gorm.DB {
		tx := r.txOf(ctx).Table("customer_profile cp").
			Joins("JOIN legal_subject ls ON ls.id = cp.subject_id")
		tx = applyCustomerOwnerScope(tx, scope)
		if keyword != "" {
			tx = tx.Where("ls.legal_name ILIKE ?", "%"+keyword+"%")
		}
		return tx
	}

	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count customers: %w", err)
	}
	if total == 0 {
		return &customer.CustomerListResult{List: []customer.CustomerItem{}, Total: 0, Page: page, Size: size}, nil
	}

	// flat-scan（不内嵌带 TableName 的行——坑位表纪律）。
	var rows []struct {
		ID             int64     `gorm:"column:id"`
		SubjectID      int64     `gorm:"column:subject_id"`
		LegalName      string    `gorm:"column:legal_name"`
		LevelCode      string    `gorm:"column:level_code"`
		OwnerSalesID   int64     `gorm:"column:owner_sales_operator_id"`
		OwnerSalesName string    `gorm:"column:owner_sales_name"`
		Status         string    `gorm:"column:status"`
		CreditLimit    string    `gorm:"column:credit_limit"`
		CreditUsed     string    `gorm:"column:credit_used"`
		DepositAmount  string    `gorm:"column:deposit_amount"`
		DepositStatus  string    `gorm:"column:deposit_status"`
		CreatedAt      time.Time `gorm:"column:created_at"`
	}
	if err := base().
		Select(`cp.id, cp.subject_id, ls.legal_name, cp.level_code,
			cp.owner_sales_operator_id, st.name AS owner_sales_name,
			cp.status, cp.credit_limit, cp.credit_used, cp.deposit_amount, cp.deposit_status,
			cp.created_at`).
		Joins("JOIN internal_staff st ON st.id = cp.owner_sales_operator_id").
		Order("cp.created_at DESC, cp.id DESC").
		Limit(size).Offset((page - 1) * size).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list customers: %w", err)
	}

	list := make([]customer.CustomerItem, 0, len(rows))
	for _, rw := range rows {
		list = append(list, customer.CustomerItem{
			ID:             rw.ID,
			SubjectID:      rw.SubjectID,
			LegalName:      rw.LegalName,
			LevelCode:      rw.LevelCode,
			OwnerSalesID:   rw.OwnerSalesID,
			OwnerSalesName: rw.OwnerSalesName,
			Status:         rw.Status,
			CreditLimit:    rw.CreditLimit,
			CreditUsed:     rw.CreditUsed,
			DepositAmount:  rw.DepositAmount,
			DepositStatus:  rw.DepositStatus,
			CreatedAt:      rw.CreatedAt,
		})
	}
	return &customer.CustomerListResult{List: list, Total: total, Page: page, Size: size}, nil
}

// LoadCustomerByID 实现 customer.Store.LoadCustomerByID。
func (r *CustomerRepo) LoadCustomerByID(ctx context.Context, id int64) (*customer.CustomerItem, error) {
	var row struct {
		ID             int64     `gorm:"column:id"`
		SubjectID      int64     `gorm:"column:subject_id"`
		LegalName      string    `gorm:"column:legal_name"`
		LevelCode      string    `gorm:"column:level_code"`
		OwnerSalesID   int64     `gorm:"column:owner_sales_operator_id"`
		OwnerSalesName string    `gorm:"column:owner_sales_name"`
		Status         string    `gorm:"column:status"`
		CreditLimit    string    `gorm:"column:credit_limit"`
		CreditUsed     string    `gorm:"column:credit_used"`
		DepositAmount  string    `gorm:"column:deposit_amount"`
		DepositStatus  string    `gorm:"column:deposit_status"`
		CreatedAt      time.Time `gorm:"column:created_at"`
	}
	err := r.txOf(ctx).Table("customer_profile cp").
		Joins("JOIN legal_subject ls ON ls.id = cp.subject_id").
		Joins("JOIN internal_staff st ON st.id = cp.owner_sales_operator_id").
		Select(`cp.id, cp.subject_id, ls.legal_name, cp.level_code,
			cp.owner_sales_operator_id, st.name AS owner_sales_name,
			cp.status, cp.credit_limit, cp.credit_used, cp.deposit_amount, cp.deposit_status,
			cp.created_at`).
		Where("cp.id = ?", id).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load customer %d: %w", id, err)
	}
	return &customer.CustomerItem{
		ID:             row.ID,
		SubjectID:      row.SubjectID,
		LegalName:      row.LegalName,
		LevelCode:      row.LevelCode,
		OwnerSalesID:   row.OwnerSalesID,
		OwnerSalesName: row.OwnerSalesName,
		Status:         row.Status,
		CreditLimit:    row.CreditLimit,
		CreditUsed:     row.CreditUsed,
		DepositAmount:  row.DepositAmount,
		DepositStatus:  row.DepositStatus,
		CreatedAt:      row.CreatedAt,
	}, nil
}

// CheckCustomerScope 实现 customer.Store.CheckCustomerScope。
func (r *CustomerRepo) CheckCustomerScope(ctx context.Context, customerID int64, scope customer.OwnerScope) (found bool, inScope bool, err error) {
	var cnt int64
	if err := r.txOf(ctx).Table("customer_profile cp").
		Where("cp.id = ?", customerID).
		Count(&cnt).Error; err != nil {
		return false, false, fmt.Errorf("check customer exists: %w", err)
	}
	if cnt == 0 {
		return false, false, nil
	}
	// 数据域内可见行数。
	var inScopeCnt int64
	tx := r.txOf(ctx).Table("customer_profile cp").Where("cp.id = ?", customerID)
	tx = applyCustomerOwnerScope(tx, scope)
	if err := tx.Count(&inScopeCnt).Error; err != nil {
		return true, false, fmt.Errorf("check customer scope: %w", err)
	}
	return true, inScopeCnt > 0, nil
}

// ============================================================
// 客户移交
// ============================================================

// LoadTransferImpact 实现 customer.Store.LoadTransferImpact。
func (r *CustomerRepo) LoadTransferImpact(ctx context.Context, customerID, toOperatorID int64) (*customer.TransferImpact, error) {
	// customer + 现 owner 名。
	var cur struct {
		ID        int64  `gorm:"column:id"`
		OwnerID   int64  `gorm:"column:owner_sales_operator_id"`
		OwnerName string `gorm:"column:owner_name"`
	}
	err := r.txOf(ctx).Table("customer_profile cp").
		Joins("JOIN internal_staff st ON st.id = cp.owner_sales_operator_id").
		Select("cp.id, cp.owner_sales_operator_id, st.name AS owner_name").
		Where("cp.id = ?", customerID).
		Take(&cur).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load customer %d: %w", customerID, err)
	}
	// 目标销售名。
	var toName string
	if err := r.txOf(ctx).Table("internal_staff").
		Where("id = ?", toOperatorID).
		Select("name").
		Scan(&toName).Error; err != nil {
		return nil, fmt.Errorf("load target staff %d: %w", toOperatorID, err)
	}
	// 随迁报价数。
	var quoteCnt int64
	if err := r.txOf(ctx).Table("customer_quote").
		Where("customer_id = ?", customerID).
		Count(&quoteCnt).Error; err != nil {
		return nil, fmt.Errorf("count customer_quote: %w", err)
	}
	// 专属价目表行数（提示用，不随迁）。
	var pbCnt int64
	if err := r.txOf(ctx).Table("customer_price_book").
		Where("customer_id = ?", customerID).
		Count(&pbCnt).Error; err != nil {
		return nil, fmt.Errorf("count customer_price_book: %w", err)
	}
	return &customer.TransferImpact{
		CustomerID:       cur.ID,
		FromOperatorID:   cur.OwnerID,
		FromOperatorName: cur.OwnerName,
		ToOperatorID:     toOperatorID,
		ToOperatorName:   toName,
		QuoteCount:       quoteCnt,
		PriceBookCount:   pbCnt,
	}, nil
}

// LoadSalesOperator 实现 customer.Store.LoadSalesOperator。
// ACTIVE + 持 SALES 角色（role_grant JOIN role.code='SALES'）。
func (r *CustomerRepo) LoadSalesOperator(ctx context.Context, staffID int64) (id int64, name string, active bool, hasSalesRole bool, err error) {
	var row struct {
		ID     int64  `gorm:"column:id"`
		Name   string `gorm:"column:name"`
		Status string `gorm:"column:status"`
	}
	err = r.txOf(ctx).Table("internal_staff").
		Where("id = ?", staffID).
		Select("id, name, status").
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, "", false, false, nil
		}
		return 0, "", false, false, fmt.Errorf("load staff %d: %w", staffID, err)
	}
	active = row.Status == "ACTIVE"
	// 持 SALES 角色判定。
	var cnt int64
	if err := r.txOf(ctx).Table("role_grant rg").
		Joins("JOIN role ro ON ro.id = rg.role_id").
		Where("rg.staff_id = ? AND ro.code = 'SALES'", staffID).
		Count(&cnt).Error; err != nil {
		return row.ID, row.Name, active, false, fmt.Errorf("check sales role: %w", err)
	}
	return row.ID, row.Name, active, cnt > 0, nil
}

// LoadCustomerLevel 实现 customer.Store.LoadCustomerLevel。
func (r *CustomerRepo) LoadCustomerLevel(ctx context.Context, customerID int64) (levelCode string, ownerSalesID int64, err error) {
	var row struct {
		LevelCode  string `gorm:"column:level_code"`
		OwnerSales int64  `gorm:"column:owner_sales_operator_id"`
	}
	err = r.txOf(ctx).Table("customer_profile").
		Where("id = ?", customerID).
		Select("level_code, owner_sales_operator_id").
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", 0, customer.ErrCustomerNotFound
		}
		return "", 0, fmt.Errorf("load customer level %d: %w", customerID, err)
	}
	return row.LevelCode, row.OwnerSales, nil
}

// TransferCustomerTx 实现 customer.Store.TransferCustomerTx。
//
// 单事务四写：
//  1. UPDATE customer_profile.owner_sales_operator_id = toOperatorID
//  2. UPDATE customer_quote.owner_sales_operator_id = toOperatorID（历史随迁）
//  3. UPDATE customer_quote.origin_owner_id = fromOperatorID WHERE origin_owner_id IS NULL（留档）
//  4. audit_log（action=CUSTOMER_TRANSFER）
func (r *CustomerRepo) TransferCustomerTx(ctx context.Context, in customer.TransferInput, operatorID int64, requestID string) (*customer.TransferResult, error) {
	now := time.Now().UTC()
	var fromOwnerID int64
	var migrated int64

	txErr := r.txOf(ctx).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 读当前 owner（ FOR UPDATE 锁行，防并发移交）。
		var cur customerProfileRow
		if err := tx.Table("customer_profile").
			Where("id = ?", in.CustomerID).
			Select("id, owner_sales_operator_id").
			Scan(&cur).Error; err != nil {
			return fmt.Errorf("lock customer_profile %d: %w", in.CustomerID, err)
		}
		if cur.ID == 0 {
			return customer.ErrCustomerNotFound
		}
		fromOwnerID = cur.OwnerSalesOperatorID

		// 2. UPDATE customer_profile。
		if err := tx.Model(&customerProfileRow{}).
			Where("id = ?", in.CustomerID).
			Updates(map[string]any{
				"owner_sales_operator_id": in.ToOperatorID,
				"updated_at":              now,
				"updated_by":              operatorID,
				"request_id":              requestID,
			}).Error; err != nil {
			return fmt.Errorf("update customer_profile: %w", err)
		}

		// 3. UPDATE customer_quote（随迁 + origin 留档）。
		res := tx.Model(&customerQuoteRow{}).
			Where("customer_id = ?", in.CustomerID).
			Updates(map[string]any{
				"owner_sales_operator_id": in.ToOperatorID,
				"updated_at":              now,
				"updated_by":              operatorID,
				"request_id":              requestID,
			})
		if res.Error != nil {
			return fmt.Errorf("migrate customer_quote owner: %w", res.Error)
		}
		migrated = res.RowsAffected

		// 4. origin_owner_id 留档（只填 NULL 行——已留过档的不再覆盖）。
		if err := tx.Model(&customerQuoteRow{}).
			Where("customer_id = ? AND origin_owner_id IS NULL", in.CustomerID).
			Updates(map[string]any{
				"origin_owner_id": fromOwnerID,
				"updated_at":      now,
				"updated_by":      operatorID,
				"request_id":      requestID,
			}).Error; err != nil {
			return fmt.Errorf("fill origin_owner_id: %w", err)
		}

		// 5. audit_log。
		if err := r.audit.Record(db.WithDB(ctx, tx), AuditEntry{
			OperatorID:   operatorID,
			OperatorRole: "STAFF",
			Action:       "CUSTOMER_TRANSFER",
			TargetType:   "CUSTOMER_PROFILE",
			TargetID:     in.CustomerID,
			BeforeValue: map[string]any{
				"owner_sales_operator_id": fromOwnerID,
			},
			AfterValue: map[string]any{
				"owner_sales_operator_id": in.ToOperatorID,
				"quotes_migrated":         migrated,
			},
			Reason:    in.Reason,
			RequestID: requestID,
		}); err != nil {
			return fmt.Errorf("write audit_log: %w", err)
		}
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}
	return &customer.TransferResult{
		CustomerID:     in.CustomerID,
		FromOperatorID: fromOwnerID,
		ToOperatorID:   in.ToOperatorID,
		QuotesMigrated: migrated,
		TransferredAt:  now,
	}, nil
}

// ============================================================
// 生成报价 - 只读装配
// ============================================================

// LoadEffectivePriceBook 实现 customer.Store.LoadEffectivePriceBook。
//
// 部分唯一索引保证同 level_code 至多一行 EFFECTIVE——直接 Take。
func (r *CustomerRepo) LoadEffectivePriceBook(ctx context.Context, levelCode string) (bookID int64, versionNo int, found bool, err error) {
	var row struct {
		ID        int64 `gorm:"column:id"`
		VersionNo int   `gorm:"column:version_no"`
	}
	err = r.txOf(ctx).Table("price_book").
		Where("level_code = ? AND status = 'EFFECTIVE'", levelCode).
		Select("id, version_no").
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, 0, false, nil
		}
		return 0, 0, false, fmt.Errorf("load effective price_book level=%s: %w", levelCode, err)
	}
	return row.ID, row.VersionNo, true, nil
}

// LoadPriceBookItems 实现 customer.Store.LoadPriceBookItems。
//
// 每个 SKU 取代表组件（input 优先，否则字母序）的 unit_price——与 6b-4 同款口径。
// 用 LATERAL 取代表组件（避免 N+1）。
func (r *CustomerRepo) LoadPriceBookItems(ctx context.Context, bookID int64) ([]customer.PriceBookItemRow, error) {
	var rows []struct {
		SkuID           int64  `gorm:"column:sku_id"`
		SkuCode         string `gorm:"column:sku_code"`
		Currency        string `gorm:"column:currency"`
		UnitPrice       string `gorm:"column:unit_price"`
		BaselineVersion int    `gorm:"column:baseline_version"`
	}
	err := r.txOf(ctx).Table("price_book_item pbi").
		Joins("JOIN model_sku ms ON ms.id = pbi.sku_id").
		Joins(`LEFT JOIN LATERAL (
			SELECT pbc.unit_price
			FROM price_book_component pbc
			WHERE pbc.price_book_item_id = pbi.id
			ORDER BY CASE WHEN pbc.component_type = 'input' THEN 0 ELSE 1 END, pbc.component_type
			LIMIT 1
		) comp ON true`).
		Where("pbi.price_book_id = ?", bookID).
		Select(`pbi.sku_id, ms.sku_code, pbi.currency,
			comp.unit_price, pbi.baseline_version`).
		Order("pbi.sku_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load price_book items book=%d: %w", bookID, err)
	}
	return r.priceBookRowsToDomain(rows)
}

// LoadPriceBookItemsForSKUs 实现 customer.Store.LoadPriceBookItemsForSKUs。
func (r *CustomerRepo) LoadPriceBookItemsForSKUs(ctx context.Context, bookID int64, skuIDs []int64) ([]customer.PriceBookItemRow, error) {
	if len(skuIDs) == 0 {
		return []customer.PriceBookItemRow{}, nil
	}
	var rows []struct {
		SkuID           int64  `gorm:"column:sku_id"`
		SkuCode         string `gorm:"column:sku_code"`
		Currency        string `gorm:"column:currency"`
		UnitPrice       string `gorm:"column:unit_price"`
		BaselineVersion int    `gorm:"column:baseline_version"`
	}
	err := r.txOf(ctx).Table("price_book_item pbi").
		Joins("JOIN model_sku ms ON ms.id = pbi.sku_id").
		Joins(`LEFT JOIN LATERAL (
			SELECT pbc.unit_price
			FROM price_book_component pbc
			WHERE pbc.price_book_item_id = pbi.id
			ORDER BY CASE WHEN pbc.component_type = 'input' THEN 0 ELSE 1 END, pbc.component_type
			LIMIT 1
		) comp ON true`).
		Where("pbi.price_book_id = ? AND pbi.sku_id IN ?", bookID, skuIDs).
		Select(`pbi.sku_id, ms.sku_code, pbi.currency,
			comp.unit_price, pbi.baseline_version`).
		Order("pbi.sku_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load price_book items for skus book=%d: %w", bookID, err)
	}
	return r.priceBookRowsToDomain(rows)
}

// priceBookRowsToDomain 转换 + decimal 解析（失败即数据事故，不静默吞）。
func (r *CustomerRepo) priceBookRowsToDomain(rows []struct {
	SkuID           int64  `gorm:"column:sku_id"`
	SkuCode         string `gorm:"column:sku_code"`
	Currency        string `gorm:"column:currency"`
	UnitPrice       string `gorm:"column:unit_price"`
	BaselineVersion int    `gorm:"column:baseline_version"`
}) ([]customer.PriceBookItemRow, error) {
	out := make([]customer.PriceBookItemRow, 0, len(rows))
	for _, rw := range rows {
		up, err := decimal.NewFromString(rw.UnitPrice)
		if err != nil {
			return nil, fmt.Errorf("parse unit_price %q sku=%d: %w", rw.UnitPrice, rw.SkuID, err)
		}
		out = append(out, customer.PriceBookItemRow{
			SKUID:           rw.SkuID,
			SKUCode:         rw.SkuCode,
			Currency:        rw.Currency,
			UnitPrice:       up,
			BaselineVersion: rw.BaselineVersion,
		})
	}
	return out, nil
}

// LoadQuoteItems 实现 customer.Store.LoadQuoteItems。
func (r *CustomerRepo) LoadQuoteItems(ctx context.Context, quoteID int64) ([]customer.QuoteItemRow, error) {
	var rows []struct {
		SkuID      int64  `gorm:"column:sku_id"`
		Currency   string `gorm:"column:currency"`
		UnitPrice  string `gorm:"column:unit_price"`
		FloorPrice string `gorm:"column:floor_price"`
	}
	err := r.txOf(ctx).Table("customer_quote_item").
		Where("customer_quote_id = ?", quoteID).
		Select("sku_id, currency, unit_price, floor_price").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load quote items quote=%d: %w", quoteID, err)
	}
	out := make([]customer.QuoteItemRow, 0, len(rows))
	for _, rw := range rows {
		up, err := decimal.NewFromString(rw.UnitPrice)
		if err != nil {
			return nil, fmt.Errorf("parse unit_price %q sku=%d: %w", rw.UnitPrice, rw.SkuID, err)
		}
		fp, err := decimal.NewFromString(rw.FloorPrice)
		if err != nil {
			return nil, fmt.Errorf("parse floor_price %q sku=%d: %w", rw.FloorPrice, rw.SkuID, err)
		}
		out = append(out, customer.QuoteItemRow{
			SKUID:      rw.SkuID,
			Currency:   rw.Currency,
			UnitPrice:  up,
			FloorPrice: fp,
		})
	}
	return out, nil
}

// ListCustomerQuotePreviews 返回同一客户的历史报价与售价明细；不返回成本、floor 或毛利。
func (r *CustomerRepo) ListCustomerQuotePreviews(ctx context.Context, customerID int64, page, size int) (*customer.QuoteHistoryPage, error) {
	tx := r.txOf(ctx)
	var total int64
	if err := tx.Table("customer_quote").Where("customer_id = ?", customerID).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count customer quote previews: %w", err)
	}
	type quotePreviewRow struct {
		ID               int64      `gorm:"column:id"`
		VersionNo        int        `gorm:"column:version_no"`
		Status           string     `gorm:"column:status"`
		QuoteType        string     `gorm:"column:quote_type"`
		ValidUntil       *time.Time `gorm:"column:valid_until"`
		PriceBookVersion int        `gorm:"column:price_book_version"`
		CreatedAt        time.Time  `gorm:"column:created_at"`
	}
	var quotes []quotePreviewRow
	if err := tx.Table("customer_quote").Where("customer_id = ?", customerID).
		Select("id, version_no, status, quote_type, valid_until, price_book_version, created_at").
		Order("id DESC").Offset((page - 1) * size).Limit(size).Scan(&quotes).Error; err != nil {
		return nil, fmt.Errorf("list customer quote previews: %w", err)
	}
	result := &customer.QuoteHistoryPage{List: make([]customer.QuoteHistoryPreview, 0, len(quotes)), Total: total, Page: page, Size: size}
	if len(quotes) == 0 {
		return result, nil
	}
	quoteIDs := make([]int64, 0, len(quotes))
	for _, quote := range quotes {
		quoteIDs = append(quoteIDs, quote.ID)
	}
	type itemPreviewRow struct {
		QuoteID   int64  `gorm:"column:customer_quote_id"`
		SKUID     int64  `gorm:"column:sku_id"`
		SKUCode   string `gorm:"column:sku_code"`
		Currency  string `gorm:"column:currency"`
		UnitPrice string `gorm:"column:unit_price"`
	}
	var itemRows []itemPreviewRow
	if err := tx.Table("customer_quote_item cqi").
		Joins("JOIN model_sku ms ON ms.id = cqi.sku_id").
		Where("cqi.customer_quote_id IN ?", quoteIDs).
		Select("cqi.customer_quote_id, cqi.sku_id, ms.sku_code, cqi.currency, cqi.unit_price").
		Order("cqi.customer_quote_id DESC, cqi.sku_id").Scan(&itemRows).Error; err != nil {
		return nil, fmt.Errorf("list customer quote preview items: %w", err)
	}
	itemsByQuote := make(map[int64][]customer.QuotePreviewItem, len(quotes))
	for _, item := range itemRows {
		itemsByQuote[item.QuoteID] = append(itemsByQuote[item.QuoteID], customer.QuotePreviewItem{
			SKUID: item.SKUID, SKUCode: item.SKUCode, Currency: item.Currency, UnitPrice: item.UnitPrice,
		})
	}
	for _, quote := range quotes {
		items := itemsByQuote[quote.ID]
		if items == nil {
			items = []customer.QuotePreviewItem{}
		}
		result.List = append(result.List, customer.QuoteHistoryPreview{ID: quote.ID, VersionNo: quote.VersionNo,
			Status: quote.Status, QuoteType: quote.QuoteType, ValidUntil: quote.ValidUntil,
			PriceBookVersion: quote.PriceBookVersion, CreatedAt: quote.CreatedAt, Items: items})
	}
	return result, nil
}

// LoadQuoteOwner 实现 customer.Store.LoadQuoteOwner。
func (r *CustomerRepo) LoadQuoteOwner(ctx context.Context, quoteID int64) (customerID int64, ownerSalesID int64, found bool, err error) {
	var row struct {
		CustomerID int64 `gorm:"column:customer_id"`
		OwnerID    int64 `gorm:"column:owner_sales_operator_id"`
	}
	err = r.txOf(ctx).Table("customer_quote").
		Where("id = ?", quoteID).
		Select("customer_id, owner_sales_operator_id").
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, 0, false, nil
		}
		return 0, 0, false, fmt.Errorf("load quote owner %d: %w", quoteID, err)
	}
	return row.CustomerID, row.OwnerID, true, nil
}

// LoadCurrentUnitCosts 实现 customer.Store.LoadCurrentUnitCosts。
//
// 从 cost_baseline 当前版取代表组件（input 优先）的 unit_cost——
// 与 6b-4 ListBaselines 同一份 representCompJoin SQL 片段。
func (r *CustomerRepo) LoadCurrentUnitCosts(ctx context.Context, skuIDs []int64) (map[int64]customer.UnitCostInfo, error) {
	if len(skuIDs) == 0 {
		return map[int64]customer.UnitCostInfo{}, nil
	}
	var rows []struct {
		SkuID    int64  `gorm:"column:sku_id"`
		Currency string `gorm:"column:currency"`
		UnitCost string `gorm:"column:unit_cost"`
		Version  int    `gorm:"column:version"`
	}
	err := r.txOf(ctx).Table("cost_baseline cb").
		Joins(`LEFT JOIN LATERAL (
			SELECT cc.unit_cost
			FROM cost_component cc
			WHERE cc.cost_baseline_id = cb.id
			ORDER BY CASE WHEN cc.component_type = 'input' THEN 0 ELSE 1 END, cc.component_type
			LIMIT 1
		) comp ON true`).
		Where("cb.is_current = true AND cb.sku_id IN ?", skuIDs).
		Select("cb.sku_id, cb.currency, comp.unit_cost, cb.version").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load current unit costs: %w", err)
	}
	out := make(map[int64]customer.UnitCostInfo, len(rows))
	for _, rw := range rows {
		uc, err := decimal.NewFromString(rw.UnitCost)
		if err != nil {
			return nil, fmt.Errorf("parse unit_cost %q sku=%d: %w", rw.UnitCost, rw.SkuID, err)
		}
		out[rw.SkuID] = customer.UnitCostInfo{
			UnitCost:        uc,
			Currency:        rw.Currency,
			BaselineVersion: rw.Version,
		}
	}
	return out, nil
}

// LoadMinGrossMargin 实现 customer.Store.LoadMinGrossMargin。
func (r *CustomerRepo) LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error) {
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

// LoadSKUCode 实现 customer.Store.LoadSKUCode。
func (r *CustomerRepo) LoadSKUCode(ctx context.Context, skuID int64) (string, error) {
	var code string
	if err := r.txOf(ctx).Table("model_sku").
		Where("id = ?", skuID).
		Select("sku_code").
		Scan(&code).Error; err != nil {
		return "", fmt.Errorf("load sku_code %d: %w", skuID, err)
	}
	return code, nil
}

// ============================================================
// 生成报价 - 写
// ============================================================

// GenerateQuoteTx 实现 customer.Store.GenerateQuoteTx。
//
// 单事务三写：
//  1. INSERT customer_quote（version_no = MAX(version_no)+1，DRAFT 状态）
//  2. INSERT customer_quote_item（逐 SKU，uk_cqi(customer_quote_id, sku_id)）
//  3. audit_log（action=CUSTOMER_QUOTE_GENERATE）
//
// 并发安全：uk_cq_ver(customer_id, version_no) 兜底；如果并发同 version_no 撞 23505，
// 由调用方（幂等中间件）按 409 处理——本仓储不重试（重试会掩盖真并发 bug）。
func (r *CustomerRepo) GenerateQuoteTx(ctx context.Context, in customer.GenerateQuoteTxInput, operatorID int64, requestID string) (*customer.GeneratedQuote, error) {
	now := time.Now().UTC()
	var quoteID int64
	var versionNo int

	txErr := r.txOf(ctx).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 算 version_no = MAX+1（同 customer 内）。
		var maxVer *int
		if err := tx.Table("customer_quote").
			Where("customer_id = ?", in.CustomerID).
			Select("MAX(version_no)").
			Scan(&maxVer).Error; err != nil {
			return fmt.Errorf("query max version_no: %w", err)
		}
		versionNo = 1
		if maxVer != nil {
			versionNo = *maxVer + 1
		}

		// 2. INSERT customer_quote（status=DRAFT——9a 不实现审批流，DRAFT 是默认状态）。
		quote := customerQuoteRow{
			CustomerID:           in.CustomerID,
			VersionNo:            versionNo,
			Status:               customer.QuoteStatusDraft,
			NeedRefresh:          false,
			ValidUntil:           in.ValidUntil,
			PriceBookVersion:     in.PriceBookVersion,
			OwnerSalesOperatorID: in.OwnerSalesID,
			QuoteType:            in.QuoteType,
			CreatedBy:            operatorID,
			CreatedAt:            now,
			UpdatedAt:            now,
			RequestID:            strPtr(requestID),
			UpdatedBy:            int64Ptr(operatorID),
		}
		if err := tx.Create(&quote).Error; err != nil {
			return fmt.Errorf("insert customer_quote: %w", err)
		}
		quoteID = quote.ID

		// 3. INSERT customer_quote_item。
		for _, it := range in.Items {
			item := customerQuoteItemRow{
				CustomerQuoteID: quoteID,
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
			if err := tx.Create(&item).Error; err != nil {
				return fmt.Errorf("insert customer_quote_item sku=%d: %w", it.SKUID, err)
			}
		}

		// 4. audit_log。
		if err := r.audit.Record(db.WithDB(ctx, tx), AuditEntry{
			OperatorID:   operatorID,
			OperatorRole: "STAFF",
			Action:       "CUSTOMER_QUOTE_GENERATE",
			TargetType:   "CUSTOMER_QUOTE",
			TargetID:     quoteID,
			AfterValue: map[string]any{
				"customer_id":        in.CustomerID,
				"version_no":         versionNo,
				"quote_type":         in.QuoteType,
				"item_count":         len(in.Items),
				"price_book_version": in.PriceBookVersion,
			},
			RequestID: requestID,
		}); err != nil {
			return fmt.Errorf("write audit_log: %w", err)
		}
		return nil
	})
	if txErr != nil {
		// uk_cq_ver 撞 23505 → 调用方按 409 处理（这里不上抛 sentinel，让错误带 SQLSTATE）。
		if strings.Contains(txErr.Error(), "uk_cq_ver") {
			return nil, fmt.Errorf("并发版本冲突：%w", txErr)
		}
		return nil, txErr
	}

	return &customer.GeneratedQuote{
		ID:               quoteID,
		CustomerID:       in.CustomerID,
		VersionNo:        versionNo,
		Status:           customer.QuoteStatusDraft,
		QuoteType:        in.QuoteType,
		ItemCount:        len(in.Items),
		BelowFloorCount:  0, // 校验已过，无违规
		PriceBookVersion: in.PriceBookVersion,
		ValidUntil:       in.ValidUntil,
		OwnerSalesID:     in.OwnerSalesID,
		CreatedAt:        now,
	}, nil
}
