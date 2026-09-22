// Package customer 的 refresh_service.go：9b §4 客户报价刷新。
//
// 语义（裁决 3）：
//   - **unit_price 不动**（价格是用户定的），只重算 floor_price（成本变了时更新快照）。
//   - **不可变版本**：floor_price 变了才生成新版本（version_no+1），旧版本 EXPIRED；
//     值未变（floor_price 全同）→ 不产生新版本，changed_items=[]。
//   - 与 model 的 Unchanged 语义对齐（值未变不产新版，排除 reason/operator 等运行字段）。
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
	ErrQuoteForRefreshNotFound = errors.New("客户报价不存在")
	ErrQuoteTerminal           = errors.New("仅 DRAFT/PENDING 可刷新；EFFECTIVE/EXPIRED/REJECTED 不可刷新")
)

// RefreshInput 刷新入参。
type RefreshInput struct {
	QuoteID int64
	Reason  string
}

// RefreshResult 刷新返回。
type RefreshResult struct {
	QuoteID      int64                `json:"quote_id"`
	OldVersionNo int                  `json:"old_version_no"`
	NewVersionNo int                  `json:"new_version_no"` // 未变时 = OldVersionNo
	ChangedItems []RefreshChangedItem `json:"changed_items"`  // 空切片 = 未变
	Unchanged    bool                 `json:"unchanged"`
	Status       string               `json:"status"` // DRAFT（未变） / 新版本状态
}

// RefreshChangedItem 是一行的变化。
type RefreshChangedItem struct {
	SKUID         int64  `json:"sku_id"`
	OldFloorPrice string `json:"old_floor_price"`
	NewFloorPrice string `json:"new_floor_price"`
}

// RefreshService 刷新客户报价。
type RefreshService struct {
	store RefreshStore
	now   func() time.Time
}

// NewRefreshService 构造；now=nil 用 time.Now。
func NewRefreshService(store RefreshStore, now func() time.Time) *RefreshService {
	if now == nil {
		now = time.Now
	}
	return &RefreshService{store: store, now: now}
}

// RefreshStore 是刷新所需仓储。
type RefreshStore interface {
	// LoadQuote 同 SpecialPriceStore 的读取口径。
	LoadQuote(ctx context.Context, quoteID int64) (*QuoteRow, error)
	// LoadQuoteItemsDetail 读明细。
	LoadQuoteItemsDetail(ctx context.Context, quoteID int64) ([]QuoteItemDetail, error)
	// LoadCurrentUnitCosts 读 cost_baseline（与 9a customer.Store.LoadCurrentUnitCosts 语义相同）。
	LoadCurrentUnitCosts(ctx context.Context, skuIDs []int64) (map[int64]UnitCostInfo, error)
	// LoadMinGrossMargin 读 sys_config。
	LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error)
	// RefreshTx 单事务：旧版本 status='EXPIRED' + 新版本 INSERT items + audit QUOTE_REFRESHED。
	// version_no = MAX+1。
	RefreshTx(ctx context.Context, quote QuoteRow, items []QuoteItemDetail, changed []RefreshChangedItem, reason string, operatorID int64, requestID string, now time.Time) (*RefreshResult, error)
}

// Refresh 刷新客户报价。
//
// 步骤：
//  1. LoadQuote 校验存在 + 状态可刷新（DRAFT/PENDING 允许）。
//  2. LoadQuoteItemsDetail + LoadCurrentUnitCosts + LoadMinGrossMargin。
//  3. 逐 item 算 newFloorPrice = CalcFloor(unit_cost, margin)；与 oldFloorPrice 比较（decimal.Equal）。
//  4. 全 Equal → 返回 Unchanged=true + changed_items=[]（不产新版本）。
//  5. 任一不同 → RefreshTx 生成新版本（items 仅更新 floor_price；unit_price 保留）。
func (s *RefreshService) Refresh(ctx context.Context, in RefreshInput, operatorID int64, requestID string) (*RefreshResult, error) {
	quote, err := s.store.LoadQuote(ctx, in.QuoteID)
	if err != nil {
		return nil, fmt.Errorf("load quote %d: %w", in.QuoteID, err)
	}
	if quote == nil {
		return nil, ErrQuoteForRefreshNotFound
	}
	switch quote.Status {
	case QuoteStatusDraft, QuoteStatusPending:
		// OK 可刷新
	case QuoteStatusApproved, QuoteStatusEffective, QuoteStatusExpired, QuoteStatusRejected:
		return nil, ErrQuoteTerminal
	default:
		return nil, fmt.Errorf("未知报价状态：%s", quote.Status)
	}

	items, err := s.store.LoadQuoteItemsDetail(ctx, in.QuoteID)
	if err != nil {
		return nil, fmt.Errorf("load quote items: %w", err)
	}
	skuIDs := make([]int64, 0, len(items))
	for _, it := range items {
		skuIDs = append(skuIDs, it.SKUID)
	}
	unitCosts, err := s.store.LoadCurrentUnitCosts(ctx, skuIDs)
	if err != nil {
		return nil, fmt.Errorf("load unit costs: %w", err)
	}
	margin, err := s.store.LoadMinGrossMargin(ctx)
	if err != nil {
		return nil, fmt.Errorf("load min_gross_margin: %w", err)
	}

	// 计算新 floor + diff。
	//
	// **比较口径**：floor_price 在 customer_quote_item 表里以 numeric(20,8) 落库，
	// 读出时已是 8 位十进制。重算的 CalcFloor 是 division（可能 unbounded），
	// 与 8 位值直接 Equal 必然 false。显式按 StringFixed(8) 字符串比较 ——
	// 这样 altered "3.0/0.85=3.5294117647"（17 位）对 "3.52941176"（8 位）不会误判 changed。
	var changed []RefreshChangedItem
	allSame := true
	newItems := make([]QuoteItemDetail, 0, len(items))
	for _, it := range items {
		uc, ok := unitCosts[it.SKUID]
		if !ok {
			return nil, fmt.Errorf("sku=%d 无当前成本基线", it.SKUID)
		}
		newFloor, ferr := CalcFloor(uc.UnitCost, margin)
		if ferr != nil {
			return nil, fmt.Errorf("floor sku=%d: %w", it.SKUID, ferr)
		}
		// 截断到 8 位（与落库口径一致）
		newFloorTrunc, terr := decimal.NewFromString(newFloor.StringFixed(8))
		if terr != nil {
			return nil, fmt.Errorf("floor sku=%d parse: %w", it.SKUID, terr)
		}
		if !newFloorTrunc.Equal(it.FloorPrice) {
			allSame = false
			changed = append(changed, RefreshChangedItem{
				SKUID:         it.SKUID,
				OldFloorPrice: it.FloorPrice.StringFixed(8),
				NewFloorPrice: newFloorTrunc.StringFixed(8),
			})
		}
		// 新版本 items：unit_price 不动；floor_price 用新值（即使没变——保持快照最新）。
		newItems = append(newItems, QuoteItemDetail{
			SKUID:      it.SKUID,
			Currency:   it.Currency,
			UnitPrice:  it.UnitPrice,
			FloorPrice: newFloorTrunc,
		})
	}

	if allSame {
		return &RefreshResult{
			QuoteID:      in.QuoteID,
			OldVersionNo: quote.VersionNo,
			NewVersionNo: quote.VersionNo,
			ChangedItems: []RefreshChangedItem{},
			Unchanged:    true,
			Status:       quote.Status,
		}, nil
	}
	res, err := s.store.RefreshTx(ctx, *quote, newItems, changed, in.Reason, operatorID, requestID, s.now())
	if err != nil {
		return nil, fmt.Errorf("refresh tx: %w", err)
	}
	return res, nil
}
