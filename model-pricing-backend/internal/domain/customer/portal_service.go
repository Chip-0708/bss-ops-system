// Package customer 的 portal_service.go：9c §6 客户门户 6 接口领域服务。
//
// 设计裁决（全部与 stage9c 提示词对齐）：
//  1. **不挂模块权限点**——靠 AuthN（会话有效）+ owner_id 行级过滤
//     （customer_quote.customer_id = account.owner_id）。
//  2. **field_mask**：客户门户所有 DTO 在序列化前剔除
//     {unit_cost, floor_price, margin, baseline, cost_before, cost_after, cost_delta_pct}——
//     本服务在 DTO 层**就不返回这些字段**（物理剔除）。
//  3. `/billing` 无计费系统：从 customer_profile 读 credit/deposit 字段；
//     账单明细 bills=[] 占位（登记 9c-①）。
//  4. `/quotes/{id}/accept` 立即转合同价（无内部确认，登记 9c-③）。
//  5. `/home` 单聚合接口（不拆并行请求，登记 9c-④）。
//  6. `/notifications` 不自动生成（登记 9c-⑤）；标记已读接口登记 9c-②。
package customer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// 领域错误。
var (
	// ErrPortalQuoteNotFound 报价不存在 / 不属于当前客户（行级过滤命中）。
	ErrPortalQuoteNotFound = errors.New("报价不存在")
	// ErrPortalQuoteNotApprovable 报价非 APPROVED 状态，不能接受。
	ErrPortalQuoteNotApprovable = errors.New("报价非 APPROVED 状态，不能接受")
	// ErrPortalQuoteExpired 报价已过 valid_until，不能接受。
	ErrPortalQuoteExpired = errors.New("报价已过期，不能接受")
	// ErrPortalNoEffectivePriceBook 客户等级无生效价目表。
	ErrPortalNoEffectivePriceBook = errors.New("当前等级暂无生效价目表")
	// ErrPortalCustomerNotFound 客户不存在。
	ErrPortalCustomerNotFound = errors.New("客户不存在")
)

// ============================================================
// DTO
// ============================================================

// PortalPriceBookItem 当前生效价目表的单行（已剔除成本/毛利）。
type PortalPriceBookItem struct {
	SKUID     int64  `json:"sku_id"`
	SKUCode   string `json:"sku_code"`
	Currency  string `json:"currency"`
	UnitPrice string `json:"unit_price"` // 售价，不是成本
}

// PortalPriceBookView 当前生效价目表（分币种）。
type PortalPriceBookView struct {
	LevelCode string                `json:"level_code"`
	VersionNo int                   `json:"version_no"`
	Currency  string                `json:"currency"` // 价目表主币种
	Items     []PortalPriceBookItem `json:"items"`
}

// PortalQuoteItem 报价/合同联合视图的单行。
type PortalQuoteItem struct {
	ID           int64      `json:"id"`
	VersionNo    int        `json:"version_no"`
	Status       string     `json:"status"` // DRAFT/PENDING/APPROVED/EFFECTIVE/EXPIRED/REJECTED
	QuoteType    string     `json:"quote_type"`
	ValidUntil   *time.Time `json:"valid_until"`
	ItemCount    int        `json:"item_count"`
	TotalAmount  string     `json:"total_amount"` // Σ unit_price（按行）
	Currency     string     `json:"currency"`
	SourceKind   string     `json:"source_kind"` // QUOTE / CONTRACT（customer_price_book 行）
	ContractFrom *time.Time `json:"contract_from,omitempty"`
	ContractTo   *time.Time `json:"contract_to,omitempty"`
}

// PortalQuoteListResult 报价与合同联合列表。
type PortalQuoteListResult struct {
	List  []PortalQuoteItem `json:"list"`
	Total int               `json:"total"`
	Page  int               `json:"page"`
	Size  int               `json:"size"`
}

// PortalQuoteQuery 报价查询。
type PortalQuoteQuery struct {
	Page int
	Size int
}

// PortalAcceptResult 接受报价的结果。
type PortalAcceptResult struct {
	QuoteID     int64  `json:"quote_id"`
	NewStatus   string `json:"new_status"`   // EFFECTIVE
	ContractCnt int    `json:"contract_cnt"` // 写入 customer_price_book 的行数
}

// PortalBillingView 余额 / 授信 / 押金。
type PortalBillingView struct {
	CreditLimit   string        `json:"credit_limit"`
	CreditUsed    string        `json:"credit_used"`
	DepositAmount string        `json:"deposit_amount"`
	DepositStatus string        `json:"deposit_status"`
	BillingCycle  int           `json:"billing_cycle"`
	Bills         []interface{} `json:"bills"` // 9c-①：账单明细待计费系统接入，当前占位空数组
}

// PortalNotificationItem 通知单行。
type PortalNotificationItem struct {
	ID        int64      `json:"id"`
	Type      string     `json:"type"`
	Title     string     `json:"title"`
	Content   string     `json:"content"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// PortalNotificationListResult 通知分页。
type PortalNotificationListResult struct {
	List  []PortalNotificationItem `json:"list"`
	Total int                      `json:"total"`
	Page  int                      `json:"page"`
	Size  int                      `json:"size"`
}

// PortalPageQuery 通用分页。
type PortalPageQuery struct {
	Page int
	Size int
}

// PortalHomeView H5 首页聚合。
type PortalHomeView struct {
	Balance             PortalHomeBalance       `json:"balance"`
	PendingCount        int                     `json:"pending_count"`
	UnreadNotifications int                     `json:"unread_notifications"`
	CommonModels        []PortalHomeCommonModel `json:"common_models"`
}

// PortalHomeBalance 余额聚合（与 BillingView 同源，字段更少）。
type PortalHomeBalance struct {
	CreditLimit   string `json:"credit_limit"`
	CreditUsed    string `json:"credit_used"`
	DepositAmount string `json:"deposit_amount"`
	DepositStatus string `json:"deposit_status"`
}

// PortalHomeCommonModel 常用模型（已剔除成本/毛利，只留 SKU 标识）。
type PortalHomeCommonModel struct {
	SKUID    int64  `json:"sku_id"`
	SKUCode  string `json:"sku_code"`
	Currency string `json:"currency"`
}

// ============================================================
// Store 接口
// ============================================================

// PortalStore 客户门户所需仓储。
type PortalStore interface {
	// LoadCustomerLevelCode 按 customer_id 查 level_code。
	LoadCustomerLevelCode(ctx context.Context, customerID int64) (levelCode string, found bool, err error)
	// LoadEffectivePriceBookView 查 level_code 当前生效价目表（含 items，已联 model_sku）。
	LoadEffectivePriceBookView(ctx context.Context, levelCode string) (*PortalPriceBookView, error)

	// ListPortalQuotes 客户报价 + 合同联合查询（行级过滤已注入）。
	ListPortalQuotes(ctx context.Context, customerID int64, q PortalQuoteQuery) (*PortalQuoteListResult, error)

	// LoadQuoteForAccept 读报价接受前校验字段（含 customer_id / status / valid_until / items）。
	LoadQuoteForAccept(ctx context.Context, quoteID int64) (*QuoteRow, []QuoteItemDetail, error)
	// AcceptQuoteTx 单事务：customer_quote.status → EFFECTIVE + INSERT customer_price_book 行（items 数量）+ audit。
	AcceptQuoteTx(ctx context.Context, quoteID, customerID int64, items []QuoteItemDetail, validUntil *time.Time, operatorID int64, requestID string, now time.Time) (*PortalAcceptResult, error)

	// LoadBilling 读 customer_profile 的计费字段。
	LoadBilling(ctx context.Context, customerID int64) (*PortalBillingView, error)

	// ListNotifications 客户通知（created_at DESC 分页）。
	ListNotifications(ctx context.Context, customerID int64, q PortalPageQuery) (*PortalNotificationListResult, error)

	// LoadHome 聚合查询：balance + pending_count + unread_count + common_models。
	// **common_models 必须按 levelCode → EFFECTIVE price_book → price_book_item 取**
	// （变异验证 #2 锚点：levelCode 由 service 层显式传入，repo 不得忽略）。
	LoadHome(ctx context.Context, customerID int64, levelCode string) (*PortalHomeView, error)
}

// ============================================================
// Service
// ============================================================

// PortalService 客户门户领域服务。
type PortalService struct {
	store PortalStore
	now   func() time.Time
}

// NewPortalService 构造；now=nil 用 time.Now。
func NewPortalService(store PortalStore, now func() time.Time) *PortalService {
	if now == nil {
		now = time.Now
	}
	return &PortalService{store: store, now: now}
}

// GetPriceBook 当前生效价目表（本等级，分币种）。
func (s *PortalService) GetPriceBook(ctx context.Context, customerID int64) (*PortalPriceBookView, error) {
	levelCode, found, err := s.store.LoadCustomerLevelCode(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("load customer level: %w", err)
	}
	if !found {
		return nil, ErrPortalCustomerNotFound
	}
	view, err := s.store.LoadEffectivePriceBookView(ctx, levelCode)
	if err != nil {
		return nil, fmt.Errorf("load effective price_book level=%s: %w", levelCode, err)
	}
	if view == nil {
		return nil, ErrPortalNoEffectivePriceBook
	}
	return view, nil
}

// ListQuotes 我的报价与合同。
func (s *PortalService) ListQuotes(ctx context.Context, customerID int64, q PortalQuoteQuery) (*PortalQuoteListResult, error) {
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.Size <= 0 || q.Size > 200 {
		q.Size = 20
	}
	return s.store.ListPortalQuotes(ctx, customerID, q)
}

// AcceptQuote 接受报价 → 转合同价（幂等）。
//
// 校验链（任一失败即返回对应错误）：
//  1. 报价存在且属于当前客户（行级过滤）→ 否则 ErrPortalQuoteNotFound
//  2. 报价状态是 APPROVED（含 special_price_status='APPROVED' 的特价单）→ 否则 ErrPortalQuoteNotApprovable
//  3. 若报价有 valid_until，未过期 → 否则 ErrPortalQuoteExpired
func (s *PortalService) AcceptQuote(ctx context.Context, quoteID, customerID int64, operatorID int64, requestID string) (*PortalAcceptResult, error) {
	quote, items, err := s.store.LoadQuoteForAccept(ctx, quoteID)
	if err != nil {
		return nil, fmt.Errorf("load quote %d: %w", quoteID, err)
	}
	if quote == nil || quote.CustomerID != customerID {
		return nil, ErrPortalQuoteNotFound
	}
	// 状态校验：APPROVED 或 special_price_status='APPROVED'（特价单 status 可能是 DRAFT 但 special_price_status=APPROVED）。
	isApproved := quote.Status == QuoteStatusApproved
	if quote.SpecialPriceStatus != nil && *quote.SpecialPriceStatus == SpecialPriceApproved {
		isApproved = true
	}
	if !isApproved {
		return nil, ErrPortalQuoteNotApprovable
	}
	// 过期校验（只对 TEMP 报价；APPLY/CLONE 无 valid_until）。
	if quote.ValidUntil != nil && s.now().After(*quote.ValidUntil) {
		return nil, ErrPortalQuoteExpired
	}
	return s.store.AcceptQuoteTx(ctx, quoteID, customerID, items, quote.ValidUntil, operatorID, requestID, s.now())
}

// GetBilling 余额 / 授信占用 / 押金状态。
func (s *PortalService) GetBilling(ctx context.Context, customerID int64) (*PortalBillingView, error) {
	return s.store.LoadBilling(ctx, customerID)
}

// ListNotifications 通知列表。
func (s *PortalService) ListNotifications(ctx context.Context, customerID int64, q PortalPageQuery) (*PortalNotificationListResult, error) {
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.Size <= 0 || q.Size > 200 {
		q.Size = 20
	}
	return s.store.ListNotifications(ctx, customerID, q)
}

// GetHome H5 首页聚合。
//
// 链路：先取客户 level_code（变异验证 #2 锚点：repo.LoadHome 必须收到该 levelCode，
// 由其内部按 level → EFFECTIVE price_book → price_book_item 过滤 common_models）。
// 客户不存在 → ErrPortalCustomerNotFound。
func (s *PortalService) GetHome(ctx context.Context, customerID int64) (*PortalHomeView, error) {
	levelCode, found, err := s.store.LoadCustomerLevelCode(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("load customer level: %w", err)
	}
	if !found {
		return nil, ErrPortalCustomerNotFound
	}
	return s.store.LoadHome(ctx, customerID, levelCode)
}

// 辅助：格式化 decimal 为字符串（保留 2 位小数，与 billing 字段一致）。
func portalDec2(d decimal.Decimal) string {
	return d.StringFixed(2)
}

var _ = portalDec2 // 备用，当前 PortalBillingView 由 repo 直接 StringFixed(2)
