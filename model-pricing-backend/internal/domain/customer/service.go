// Package customer 客户域（09-customer-quote.md §1 客户列表 + §2 生成报价）。
//
// 与 supplier 域的差异：
//   - customer_profile.owner_sales_operator_id 是「销售归属」（不是采购归属），
//     行级过滤 Join internal_staff 取 org_unit_id。
//   - 客户移交**历史报价随迁**（owner_sales_operator_id 改为新归属）+ 原归属留档
//     （customer_quote.origin_owner_id = 原 owner）——与供应商移交「历史不随迁」相反
//     （设计文档 1070 行：避免 E7「克隆上次报价」失效）。
//
// 生成报价（§2）三种类型：
//   - APPLY  套用当前生效价目表（按 customer.level_code 定位 price_book），
//     items 可省略（全量带出）；floor 校验。
//   - CLONE  克隆 source_quote_id 的价格（须同 customer_id），不重新计算；
//     floor 校验（克隆时成本可能已上涨）。
//   - TEMP   手工填写，valid_to 必填（valid_until = valid_to）；floor 校验。
//
// floor 硬性校验（三类型同口径）：任一 item.unit_price < Floor(cost_baseline.unit_cost,
// min_gross_margin) → 409（必须走特价审批 §3）。
//
// 裁决登记（与真实 DDL 的偏离）：
//   - 9a-① quote_type 是 000023 追加列（契约 0.1 提到但 000005 遗漏）。
//   - 9a-② customer_quote 无 level_code 列（在 customer_profile 上），以表结构为准。
//   - 9a-③ price_book_version 是版本号（不是 ID），以表结构为准。
//   - 9a-④ 无 cost_baseline_id 列，从 price_book_item.baseline_version 关联。
//   - 9a-⑤ floor_price/below_floor 在 customer_quote_item 上（聚合值计算）。
package customer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"model_bss/internal/domain/auth"
	"model_bss/internal/domain/cost"
)

// ============================================================
// 常量与错误
// ============================================================

// quote_type 枚举（与 ck_customer_quote_type 对齐——红线 4 禁止自创）。
const (
	QuoteTypeApply    = "APPLY"
	QuoteTypeClone    = "CLONE"
	QuoteTypeTemp     = "TEMP"
	QuoteTypeSpecial  = "SPECIAL"  // 9b 用
	QuoteTypeContract = "CONTRACT" // 9b/9c 用
)

// 客户报价状态（customer_quote.status，000005 DDL 注释 §73-89）。
const (
	QuoteStatusDraft     = "DRAFT"
	QuoteStatusPending   = "PENDING"
	QuoteStatusApproved  = "APPROVED"
	QuoteStatusEffective = "EFFECTIVE"
	QuoteStatusExpired   = "EXPIRED"
	QuoteStatusRejected  = "REJECTED"
)

// 领域错误。
var (
	ErrCustomerNotFound         = errors.New("客户不存在")
	ErrCustomerOutOfScope       = errors.New("客户不在当前操作员的数据域内")
	ErrNoEffectivePriceBook     = errors.New("该客户等级暂无生效价目表")
	ErrSourceQuoteNotFound      = errors.New("源报价不存在")
	ErrSourceQuoteCrossCustomer = errors.New("不能克隆其他客户的报价")
	ErrTempValidToRequired      = errors.New("临时报价必须提供 valid_to")
	ErrBelowFloor               = errors.New("存在低于 floor 的报价，必须走特价审批")
	ErrInvalidQuoteType         = errors.New("quote_type 非法（9a 仅支持 APPLY/CLONE/TEMP）")
	ErrInvalidTransferTarget    = errors.New("移交目标必须是 ACTIVE 销售")
	ErrTransferToSelf           = errors.New("移交目标就是当前归属（no-op）")
	ErrTransferNotAllowed       = errors.New("仅主管（OPS_ADMIN / PLATFORM_ADMIN）可移交客户")
	ErrConfirmRequired          = errors.New("请带 confirm=true 二次确认")
)

// ============================================================
// DTO
// ============================================================

// OwnerScope 是行级过滤所需的操作员数据域视图（与 supplier.OwnerScope 对齐）。
// ScopePaths 元素形如 "/1/3/"（与 org_unit.path 一致，含首尾斜杠）。
type OwnerScope struct {
	DataScope  auth.DataScope
	StaffID    int64
	MyOrgID    int64
	ScopePaths []string
}

// CustomerItem 客户列表的一行。
//
//nolint:revive // Customer 前缀与本包语义一致，改名破坏性大（沿用 8b-2 对齐既有命名风格）。
type CustomerItem struct {
	ID             int64     `json:"id"`
	SubjectID      int64     `json:"subject_id"`
	LegalName      string    `json:"legal_name"` // 联 legal_subject.legal_name
	LevelCode      string    `json:"level_code"`
	OwnerSalesID   int64     `json:"owner_sales_operator_id"`
	OwnerSalesName string    `json:"owner_sales_name"` // 联 internal_staff.name
	Status         string    `json:"status"`
	CreditLimit    string    `json:"credit_limit"`
	CreditUsed     string    `json:"credit_used"`
	DepositAmount  string    `json:"deposit_amount"`
	DepositStatus  string    `json:"deposit_status"`
	CreatedAt      time.Time `json:"created_at"`
}

// CustomerListResult 分页结果。
//
//nolint:revive // Customer 前缀与本包语义一致，改名破坏性大。
type CustomerListResult struct {
	List  []CustomerItem `json:"list"`
	Total int64          `json:"total"`
	Page  int            `json:"page"`
	Size  int            `json:"size"`
}

// TransferInput 移交输入。
type TransferInput struct {
	CustomerID   int64
	ToOperatorID int64
	Reason       string
	Confirm      bool
}

// TransferImpact 首次 confirm=false 返回的影响面。
type TransferImpact struct {
	CustomerID       int64  `json:"customer_id"`
	FromOperatorID   int64  `json:"from_operator_id"`
	FromOperatorName string `json:"from_operator_name"`
	ToOperatorID     int64  `json:"to_operator_id"`
	ToOperatorName   string `json:"to_operator_name"`
	QuoteCount       int64  `json:"quote_count"`      // 将随迁的 customer_quote 行数
	PriceBookCount   int64  `json:"price_book_count"` // 该客户的 customer_price_book 行数（提示）
	Reason           string `json:"reason"`
}

// TransferResult 二次 confirm=true 的执行结果。
type TransferResult struct {
	CustomerID     int64     `json:"customer_id"`
	FromOperatorID int64     `json:"from_operator_id"`
	ToOperatorID   int64     `json:"to_operator_id"`
	QuotesMigrated int64     `json:"quotes_migrated"`
	TransferredAt  time.Time `json:"transferred_at"`
}

// QuoteItemInput 生成报价的明细输入。
type QuoteItemInput struct {
	SKUID     int64  `json:"sku_id"`
	UnitPrice string `json:"unit_price"` // decimal 字符串
}

// GenerateQuoteInput 生成客户报价的输入。
type GenerateQuoteInput struct {
	CustomerID    int64            `json:"customer_id"`
	QuoteType     string           `json:"quote_type"`
	SourceQuoteID *int64           `json:"source_quote_id,omitempty"`
	ValidTo       *time.Time       `json:"valid_to,omitempty"`
	Items         []QuoteItemInput `json:"items,omitempty"`
}

// FloorViolation floor 校验失败的一行。
type FloorViolation struct {
	SKUID      int64  `json:"sku_id"`
	SKUCode    string `json:"sku_code"`
	UnitPrice  string `json:"unit_price"`
	FloorPrice string `json:"floor_price"`
}

// GeneratedQuote 生成报价的响应（契约 §2 data）。
type GeneratedQuote struct {
	ID               int64            `json:"id"`
	CustomerID       int64            `json:"customer_id"`
	VersionNo        int              `json:"version_no"`
	Status           string           `json:"status"`
	QuoteType        string           `json:"quote_type"`
	ItemCount        int              `json:"item_count"`
	BelowFloorCount  int              `json:"below_floor_count"`
	FloorViolations  []FloorViolation `json:"floor_violations,omitempty"`
	PriceBookVersion int              `json:"price_book_version"`
	ValidUntil       *time.Time       `json:"valid_until,omitempty"`
	OwnerSalesID     int64            `json:"owner_sales_operator_id"`
	CreatedAt        time.Time        `json:"created_at"`
}

// QuotePreviewItem 是内部销售在提交前可查看的售价明细；不含成本、floor 或毛利。
type QuotePreviewItem struct {
	SKUID     int64  `json:"sku_id"`
	SKUCode   string `json:"sku_code"`
	Currency  string `json:"currency"`
	UnitPrice string `json:"unit_price"`
}

// PriceBookPreview 是客户等级当前生效价目表的提交前预览。
type PriceBookPreview struct {
	ID        int64              `json:"id"`
	LevelCode string             `json:"level_code"`
	VersionNo int                `json:"version_no"`
	Items     []QuotePreviewItem `json:"items"`
}

// QuoteHistoryPreview 是可供 CLONE 选择的本客户历史报价。
type QuoteHistoryPreview struct {
	ID               int64              `json:"id"`
	VersionNo        int                `json:"version_no"`
	Status           string             `json:"status"`
	QuoteType        string             `json:"quote_type"`
	ValidUntil       *time.Time         `json:"valid_until,omitempty"`
	PriceBookVersion int                `json:"price_book_version"`
	CreatedAt        time.Time          `json:"created_at"`
	Items            []QuotePreviewItem `json:"items"`
}

// QuoteHistoryPage 是客户历史报价分页结果。
type QuoteHistoryPage struct {
	List  []QuoteHistoryPreview `json:"list"`
	Total int64                 `json:"total"`
	Page  int                   `json:"page"`
	Size  int                   `json:"size"`
}

// QuoteContext 是报价创建弹窗所需的最小只读上下文。
type QuoteContext struct {
	CustomerID int64             `json:"customer_id"`
	LevelCode  string            `json:"level_code"`
	PriceBook  *PriceBookPreview `json:"price_book"`
	History    QuoteHistoryPage  `json:"history"`
}

// ============================================================
// 纯函数
// ============================================================

// ValidQuoteType 校验 quote_type（9a 只接受 APPLY/CLONE/TEMP；SPECIAL/CONTRACT 是 9b/9c）。
func ValidQuoteType(t string) bool {
	return t == QuoteTypeApply || t == QuoteTypeClone || t == QuoteTypeTemp
}

// CalcFloor 纯函数：floor = unit_cost / (1 - min_gross_margin)。
// 复用 cost.Floor，与 8a / 8b-1 / 8b-2 同一份口径，不重写。
func CalcFloor(unitCost, minGrossMargin decimal.Decimal) (decimal.Decimal, error) {
	return cost.Floor(unitCost, minGrossMargin)
}

// CheckFloor 逐项校验 unit_price >= floor；返回违规列表（空切片 = 全过）。
// 金额红线：decimal 比较，不用 float64。
func CheckFloor(items []QuoteItemWithFloor) []FloorViolation {
	var out []FloorViolation
	for _, it := range items {
		if it.UnitPrice.LessThan(it.FloorPrice) {
			out = append(out, FloorViolation{
				SKUID:      it.SKUID,
				SKUCode:    it.SKUCode,
				UnitPrice:  it.UnitPrice.StringFixed(8),
				FloorPrice: it.FloorPrice.StringFixed(8),
			})
		}
	}
	return out
}

// QuoteItemWithFloor 是 CheckFloor 的输入视图（已解析成 decimal）。
type QuoteItemWithFloor struct {
	SKUID      int64
	SKUCode    string
	UnitPrice  decimal.Decimal
	FloorPrice decimal.Decimal
}

// QuoteFloorError 是 floor 校验失败的错误（携带违规明细给 409 响应）。
// 用类型承载而不是字符串拼接——前端要渲染 SKU 级违规列表。
type QuoteFloorError struct {
	Violations []FloorViolation
}

// Error 实现 error 接口。
func (e *QuoteFloorError) Error() string { return ErrBelowFloor.Error() }

// Unwrap 让 errors.Is(err, ErrBelowFloor) 命中。
func (e *QuoteFloorError) Unwrap() error { return ErrBelowFloor }

// ============================================================
// Store 接口
// ============================================================

// Store 是 customer 域的仓储接口（GORM 实现见 internal/repo/customer.go）。
type Store interface {
	// ---- 客户列表 ----
	ListCustomers(ctx context.Context, scope OwnerScope, keyword string, page, size int) (*CustomerListResult, error)
	LoadCustomerByID(ctx context.Context, id int64) (*CustomerItem, error)
	// CheckCustomerScope 归属校验（移交/生成报价前）。
	// found=false → 404；inScope=false → 403。
	CheckCustomerScope(ctx context.Context, customerID int64, scope OwnerScope) (found bool, inScope bool, err error)

	// ---- 客户移交 ----
	// LoadTransferImpact 首次 confirm=false 用：读 customer + 关联 staff 名 + 随迁行数。
	LoadTransferImpact(ctx context.Context, customerID, toOperatorID int64) (*TransferImpact, error)
	// TransferCustomerTx 二次 confirm=true 用：单事务 UPDATE customer_profile.owner_sales_operator_id +
	// UPDATE customer_quote.owner_sales_operator_id + UPDATE customer_quote.origin_owner_id（若 NULL）+
	// audit_log。
	TransferCustomerTx(ctx context.Context, in TransferInput, operatorID int64, requestID string) (*TransferResult, error)
	// LoadSalesOperator 校验 to_operator_id 是 ACTIVE staff 且持 SALES 角色。
	LoadSalesOperator(ctx context.Context, staffID int64) (id int64, name string, active bool, hasSalesRole bool, err error)

	// ---- 生成报价 ----
	// LoadCustomerLevel 读 customer_profile.level_code（生成报价时定位价目表）。
	LoadCustomerLevel(ctx context.Context, customerID int64) (levelCode string, ownerSalesID int64, err error)
	// LoadEffectivePriceBook 按 level_code 查当前 EFFECTIVE 价目表。
	// found=false → 调用方返回 ErrNoEffectivePriceBook。
	LoadEffectivePriceBook(ctx context.Context, levelCode string) (bookID int64, versionNo int, found bool, err error)
	// LoadPriceBookItems 读 price_book_item + price_book_component（APPLY 全量带出）。
	LoadPriceBookItems(ctx context.Context, bookID int64) ([]PriceBookItemRow, error)
	// LoadPriceBookItemsForSKUs 读指定 SKU 的 price_book_item + component（APPLY 指定 items 用）。
	LoadPriceBookItemsForSKUs(ctx context.Context, bookID int64, skuIDs []int64) ([]PriceBookItemRow, error)
	// LoadQuoteItems 读既有 customer_quote_item（CLONE 用）。
	LoadQuoteItems(ctx context.Context, quoteID int64) ([]QuoteItemRow, error)
	// LoadQuoteOwner 读 customer_quote 的 customer_id + owner_sales_operator_id（CLONE 跨客户校验）。
	LoadQuoteOwner(ctx context.Context, quoteID int64) (customerID int64, ownerSalesID int64, found bool, err error)
	// LoadCurrentUnitCosts 读 cost_baseline 的 unit_cost（floor 校验）；
	// 返回 map[skuID]UnitCostInfo，缺基线的 SKU 不在 map 里。
	LoadCurrentUnitCosts(ctx context.Context, skuIDs []int64) (map[int64]UnitCostInfo, error)
	// LoadMinGrossMargin 读 sys_config.min_gross_margin。
	LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error)
	// LoadSKUCode 读 sku_code（floor violation 响应构造）。
	LoadSKUCode(ctx context.Context, skuID int64) (string, error)
	// ListCustomerQuotePreviews 返回同一客户的历史报价及售价项，供 CLONE 选择和确认。
	ListCustomerQuotePreviews(ctx context.Context, customerID int64, page, size int) (*QuoteHistoryPage, error)

	// GenerateQuoteTx 单事务：INSERT customer_quote + customer_quote_item + audit_log。
	// version_no = MAX(version_no)+1（同客户内单调递增）。
	GenerateQuoteTx(ctx context.Context, in GenerateQuoteTxInput, operatorID int64, requestID string) (*GeneratedQuote, error)
}

// PriceBookItemRow 是 price_book_item + price_book_component 的一行（代表组件）。
type PriceBookItemRow struct {
	SKUID           int64
	SKUCode         string
	Currency        string
	UnitPrice       decimal.Decimal // 代表组件 unit_price
	BaselineVersion int
}

// QuoteItemRow 是 customer_quote_item 的一行（CLONE 复制用）。
type QuoteItemRow struct {
	SKUID      int64
	Currency   string
	UnitPrice  decimal.Decimal
	FloorPrice decimal.Decimal
}

// UnitCostInfo 是 cost_baseline 的 unit_cost 信息（floor 校验）。
type UnitCostInfo struct {
	UnitCost        decimal.Decimal
	Currency        string
	BaselineVersion int
}

// GenerateQuoteTxInput 是 GenerateQuoteTx 的输入（已 floor 校验通过）。
type GenerateQuoteTxInput struct {
	CustomerID       int64
	QuoteType        string
	SourceQuoteID    *int64
	ValidUntil       *time.Time
	PriceBookVersion int
	OwnerSalesID     int64
	Items            []GenerateQuoteItemRow
}

// GenerateQuoteItemRow 是落库的明细行（价格 + floor 快照）。
type GenerateQuoteItemRow struct {
	SKUID      int64
	Currency   string
	UnitPrice  decimal.Decimal
	FloorPrice decimal.Decimal
}

// ============================================================
// Service
// ============================================================

// Service 是 customer 域的服务（无状态，可并发）。
type Service struct {
	store Store
	now   func() time.Time
}

// NewService 构造。now 为 nil 时用 time.Now。
func NewService(store Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now}
}

// ListCustomers 客户列表（M8:V + 行级过滤）。
func (s *Service) ListCustomers(ctx context.Context, scope OwnerScope, keyword string, page, size int) (*CustomerListResult, error) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 200 {
		size = 20
	}
	res, err := s.store.ListCustomers(ctx, scope, keyword, page, size)
	if err != nil {
		return nil, fmt.Errorf("list customers: %w", err)
	}
	if res == nil {
		return &CustomerListResult{List: []CustomerItem{}, Total: 0, Page: page, Size: size}, nil
	}
	return res, nil
}

// GetQuoteContext 返回 APPLY 当前价目表与 CLONE 历史报价的只读预览。
func (s *Service) GetQuoteContext(ctx context.Context, customerID int64, scope OwnerScope, page, size int) (*QuoteContext, error) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 200 {
		size = 20
	}
	found, inScope, err := s.store.CheckCustomerScope(ctx, customerID, scope)
	if err != nil {
		return nil, fmt.Errorf("check customer scope: %w", err)
	}
	if !found {
		return nil, ErrCustomerNotFound
	}
	if !inScope {
		return nil, ErrCustomerOutOfScope
	}
	levelCode, _, err := s.store.LoadCustomerLevel(ctx, customerID)
	if err != nil {
		return nil, err
	}
	contextView := &QuoteContext{CustomerID: customerID, LevelCode: levelCode}
	bookID, versionNo, bookFound, err := s.store.LoadEffectivePriceBook(ctx, levelCode)
	if err != nil {
		return nil, err
	}
	if bookFound {
		items, loadErr := s.store.LoadPriceBookItems(ctx, bookID)
		if loadErr != nil {
			return nil, loadErr
		}
		previewItems := make([]QuotePreviewItem, 0, len(items))
		for _, item := range items {
			previewItems = append(previewItems, QuotePreviewItem{SKUID: item.SKUID, SKUCode: item.SKUCode,
				Currency: item.Currency, UnitPrice: item.UnitPrice.StringFixed(8)})
		}
		contextView.PriceBook = &PriceBookPreview{ID: bookID, LevelCode: levelCode, VersionNo: versionNo, Items: previewItems}
	}
	history, err := s.store.ListCustomerQuotePreviews(ctx, customerID, page, size)
	if err != nil {
		return nil, err
	}
	if history == nil {
		history = &QuoteHistoryPage{List: []QuoteHistoryPreview{}, Page: page, Size: size}
	}
	contextView.History = *history
	return contextView, nil
}

// TransferPreview 移交影响面（首次 confirm=false 调用）。
func (s *Service) TransferPreview(ctx context.Context, in TransferInput) (*TransferImpact, error) {
	imp, err := s.store.LoadTransferImpact(ctx, in.CustomerID, in.ToOperatorID)
	if err != nil {
		return nil, err
	}
	if imp == nil {
		return nil, ErrCustomerNotFound
	}
	imp.Reason = in.Reason
	return imp, nil
}

// Transfer 客户移交（二次 confirm=true 调用；M8:E + 主管 + 双确认 + 历史随迁）。
//
// 步骤（红线 8：最小同步事务）：
//  1. 校验目标：ACTIVE + 持 SALES 角色 + 不是当前归属。
//  2. TransferCustomerTx 单事务：
//     UPDATE customer_profile.owner_sales_operator_id = to +
//     UPDATE customer_quote.owner_sales_operator_id = to（随迁）+
//     UPDATE customer_quote.origin_owner_id = from WHERE origin_owner_id IS NULL（留档）+
//     audit_log。
func (s *Service) Transfer(ctx context.Context, in TransferInput, operatorID int64, requestID string) (*TransferResult, error) {
	if !in.Confirm {
		return nil, ErrConfirmRequired
	}
	// 校验目标销售。
	toID, _, active, hasSales, err := s.store.LoadSalesOperator(ctx, in.ToOperatorID)
	if err != nil {
		return nil, fmt.Errorf("load sales operator %d: %w", in.ToOperatorID, err)
	}
	if !active || !hasSales || toID == 0 {
		return nil, ErrInvalidTransferTarget
	}
	// 读当前归属。
	_, ownerID, err := s.store.LoadCustomerLevel(ctx, in.CustomerID)
	if err != nil {
		return nil, err
	}
	if ownerID == in.ToOperatorID {
		return nil, ErrTransferToSelf
	}
	res, err := s.store.TransferCustomerTx(ctx, in, operatorID, requestID)
	if err != nil {
		return nil, fmt.Errorf("transfer customer: %w", err)
	}
	return res, nil
}

// GenerateQuote 生成客户报价（M9:E + 幂等）。
//
// 步骤：
//  1. 校验 quote_type（9a 只支持 APPLY/CLONE/TEMP）。
//  2. 归属校验：客户必须在操作员数据域内。
//  3. 按类型取 items：
//     - APPLY：items 省略 → 全量 price_book_item；items 指定 → 校验都在 price_book 里。
//     - CLONE：复制 source_quote 的 customer_quote_item。
//     - TEMP：请求 items 手工填写。
//  4. 逐 SKU 查 cost_baseline.unit_cost → 算 floor → CheckFloor。
//  5. 任一违规 → *QuoteFloorError（errors.Is 命中 ErrBelowFloor，handler 解包取 violations）。
//  6. GenerateQuoteTx 单事务落库（version_no = MAX+1）。
func (s *Service) GenerateQuote(ctx context.Context, in GenerateQuoteInput, scope OwnerScope, operatorID int64, requestID string) (*GeneratedQuote, error) {
	// 1. quote_type 校验。
	if !ValidQuoteType(in.QuoteType) {
		return nil, ErrInvalidQuoteType
	}
	// 2. 归属校验。
	found, inScope, err := s.store.CheckCustomerScope(ctx, in.CustomerID, scope)
	if err != nil {
		return nil, fmt.Errorf("check customer scope: %w", err)
	}
	if !found {
		return nil, ErrCustomerNotFound
	}
	if !inScope {
		return nil, ErrCustomerOutOfScope
	}
	// 3. 客户 level + owner。
	levelCode, ownerSalesID, err := s.store.LoadCustomerLevel(ctx, in.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("load customer level: %w", err)
	}

	// 4. 按类型取 items（含 floor 快照）。
	var items []GenerateQuoteItemRow
	var priceBookVersion int
	var validUntil *time.Time
	var skuIDs []int64

	switch in.QuoteType {
	case QuoteTypeApply:
		// 查生效价目表。
		bookID, ver, ok, err := s.store.LoadEffectivePriceBook(ctx, levelCode)
		if err != nil {
			return nil, fmt.Errorf("load effective price_book: %w", err)
		}
		if !ok {
			return nil, ErrNoEffectivePriceBook
		}
		priceBookVersion = ver
		var pbItems []PriceBookItemRow
		if len(in.Items) == 0 {
			// 全量带出。
			pbItems, err = s.store.LoadPriceBookItems(ctx, bookID)
			if err != nil {
				return nil, fmt.Errorf("load price_book items: %w", err)
			}
		} else {
			// 指定 SKU：校验都在价目表里。
			ids := make([]int64, 0, len(in.Items))
			for _, it := range in.Items {
				ids = append(ids, it.SKUID)
			}
			pbItems, err = s.store.LoadPriceBookItemsForSKUs(ctx, bookID, ids)
			if err != nil {
				return nil, fmt.Errorf("load price_book items for skus: %w", err)
			}
			if len(pbItems) != len(in.Items) {
				return nil, fmt.Errorf("%w：部分 SKU 不在该等级的生效价目表中", ErrInvalidQuoteType)
			}
		}
		for _, pb := range pbItems {
			items = append(items, GenerateQuoteItemRow{
				SKUID: pb.SKUID, Currency: pb.Currency, UnitPrice: pb.UnitPrice,
			})
			skuIDs = append(skuIDs, pb.SKUID)
		}
		if in.ValidTo != nil {
			validUntil = in.ValidTo
		}

	case QuoteTypeClone:
		if in.SourceQuoteID == nil {
			return nil, fmt.Errorf("%w：CLONE 必须提供 source_quote_id", ErrInvalidQuoteType)
		}
		// 跨客户校验。
		srcCustID, _, srcFound, err := s.store.LoadQuoteOwner(ctx, *in.SourceQuoteID)
		if err != nil {
			return nil, fmt.Errorf("load source quote owner: %w", err)
		}
		if !srcFound {
			return nil, ErrSourceQuoteNotFound
		}
		if srcCustID != in.CustomerID {
			return nil, ErrSourceQuoteCrossCustomer
		}
		// 复制明细。
		quoteItems, err := s.store.LoadQuoteItems(ctx, *in.SourceQuoteID)
		if err != nil {
			return nil, fmt.Errorf("load quote items: %w", err)
		}
		for _, qi := range quoteItems {
			items = append(items, GenerateQuoteItemRow{
				SKUID: qi.SKUID, Currency: qi.Currency, UnitPrice: qi.UnitPrice,
			})
			skuIDs = append(skuIDs, qi.SKUID)
		}
		if in.ValidTo != nil {
			validUntil = in.ValidTo
		}

	case QuoteTypeTemp:
		if in.ValidTo == nil {
			return nil, ErrTempValidToRequired
		}
		validUntil = in.ValidTo
		for _, it := range in.Items {
			up, perr := decimal.NewFromString(it.UnitPrice)
			if perr != nil {
				return nil, fmt.Errorf("解析 sku=%d 的 unit_price: %w", it.SKUID, perr)
			}
			items = append(items, GenerateQuoteItemRow{
				SKUID: it.SKUID, UnitPrice: up, // currency 留空，下面从 cost_baseline 补
			})
			skuIDs = append(skuIDs, it.SKUID)
		}
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("%w：items 不能为空", ErrInvalidQuoteType)
	}

	// 5. 查成本 + min_gross_margin，算 floor 并校验。
	unitCosts, err := s.store.LoadCurrentUnitCosts(ctx, skuIDs)
	if err != nil {
		return nil, fmt.Errorf("load unit costs: %w", err)
	}
	margin, err := s.store.LoadMinGrossMargin(ctx)
	if err != nil {
		return nil, fmt.Errorf("load min_gross_margin: %w", err)
	}
	withFloor := make([]QuoteItemWithFloor, 0, len(items))
	for i, it := range items {
		uc, ok := unitCosts[it.SKUID]
		if !ok {
			return nil, fmt.Errorf("sku=%d 无当前成本基线，无法计算 floor", it.SKUID)
		}
		floor, ferr := CalcFloor(uc.UnitCost, margin)
		if ferr != nil {
			return nil, fmt.Errorf("floor sku=%d: %w", it.SKUID, ferr)
		}
		items[i].FloorPrice = floor
		if items[i].Currency == "" {
			items[i].Currency = uc.Currency
		}
		skuCode, _ := s.store.LoadSKUCode(ctx, it.SKUID) // 失败不阻断（violation 用）
		withFloor = append(withFloor, QuoteItemWithFloor{
			SKUID: it.SKUID, SKUCode: skuCode, UnitPrice: it.UnitPrice, FloorPrice: floor,
		})
	}
	violations := CheckFloor(withFloor)
	if len(violations) > 0 {
		return nil, &QuoteFloorError{Violations: violations}
	}

	// 6. 落库。
	res, err := s.store.GenerateQuoteTx(ctx, GenerateQuoteTxInput{
		CustomerID:       in.CustomerID,
		QuoteType:        in.QuoteType,
		SourceQuoteID:    in.SourceQuoteID,
		ValidUntil:       validUntil,
		PriceBookVersion: priceBookVersion,
		OwnerSalesID:     ownerSalesID,
		Items:            items,
	}, operatorID, requestID)
	if err != nil {
		return nil, fmt.Errorf("generate quote tx: %w", err)
	}
	return res, nil
}
