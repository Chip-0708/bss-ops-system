// Package customer 的 special_price_service.go：9b §3 特价审批申请。
//
// 业务：把 DRAFT 客户报价标记为「特价审批中」，创建 change_request[SPECIAL_PRICE]
// + 2 步 approval_step（PRICING_OP → FINANCE），customer_quote.special_price_status := 'PENDING'。
//
// 关键裁决（与 stage9b 提示词对齐）：
//   - 审批链固定 2 步，不按破线幅度分档（9b-① 登记为遗留）
//   - margin_impact = {current_price, current_margin, target_price, target_margin, delta_gap_distance}（毛利预演）
//   - change_request.sku_id=NULL（按报价单粒度，非 SKU 粒度，与 8b-1 一致）
//   - payload 含 customer_quote_id / current_price / target_price / target_margin / reason / expected_margin
//   - 状态机变化：special_price_status NULL → PENDING → （全批通过→APPROVED / 任一驳回→REJECTED）
package customer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// ============================================================
// 常量与错误
// ============================================================

// SpecialPriceStatus 状态机（裁决 5）。
const (
	// SpecialPricePending customer_quote.special_price_status='PENDING'
	SpecialPricePending = "PENDING"
	// SpecialPriceApproved customer_quote.special_price_status='APPROVED'
	SpecialPriceApproved = "APPROVED"
	// SpecialPriceRejected customer_quote.special_price_status='REJECTED'
	SpecialPriceRejected = "REJECTED"
)

// ChangeSpecialPrice change_request.change_type='SPECIAL_PRICE'（裁决 7）。
const ChangeSpecialPrice = "SPECIAL_PRICE"

// 领域错误。
var (
	ErrQuoteNotFound         = errors.New("客户报价不存在")
	ErrQuoteNotDraft         = errors.New("仅 DRAFT 状态的报价可申请特价")
	ErrQuoteAlreadyPending   = errors.New("已在特价审批中，请勿重复申请")
	ErrReasonRequired        = errors.New("reason 必填")
	ErrExpectedMarginInvalid = errors.New("expected_margin 必须是合法 decimal 字符串")
)

// ============================================================
// DTO
// ============================================================

// SpecialPriceInput 特价申请入参（reason 必填，expected_margin 必填）。
type SpecialPriceInput struct {
	QuoteID        int64
	Reason         string
	ExpectedMargin string
}

// MarginImpact 毛利预演（裁决 2）。
type MarginImpact struct {
	CurrentPrice     string `json:"current_price"`
	CurrentMargin    string `json:"current_margin"`     // 现状整体毛利（items 加权平均或聚合，简化=按 min(items.unit_price-floor)/unit_price）
	TargetPrice      string `json:"target_price"`       // 与 current_price 相同（本批特价是"允许破线价"，不改价格本身）
	TargetMargin     string `json:"target_margin"`      // 期望值：expected_margin 字符串原文
	DeltaGapDistance string `json:"delta_gap_distance"` // 与 floor 的距离：min(items.unit_price - floor_price)
}

// SpecialPriceResult 是 Request 的返回值。
type SpecialPriceResult struct {
	QuoteID         int64         `json:"quote_id"`
	ChangeRequestID int64         `json:"change_request_id"`
	StepCount       int           `json:"step_count"`
	Status          string        `json:"status"` // PENDING / APPROVED / REJECTED
	MarginImpact    *MarginImpact `json:"margin_impact"`
}

// ============================================================
// Service
// ============================================================

// SpecialPriceService 特价审批申请。
type SpecialPriceService struct {
	store SpecialPriceStore
}

// NewSpecialPriceService 构造。
func NewSpecialPriceService(store SpecialPriceStore) *SpecialPriceService {
	return &SpecialPriceService{store: store}
}

// SpecialPriceStore 是特价审批所需的最小仓储。
type SpecialPriceStore interface {
	// LoadQuote 读 customer_quote 主表（含 quote_type / status / special_price_status / customer_id）。
	LoadQuote(ctx context.Context, quoteID int64) (*QuoteRow, error)
	// LoadQuoteItems 读 customer_quote_item（skew_id, unit_price, floor_price）。
	LoadQuoteItemsDetail(ctx context.Context, quoteID int64) ([]QuoteItemDetail, error)
	// RequestSpecialPriceTx 单事务：change_request[SPECIAL_PRICE] + 2 步 approval_step
	// + customer_quote.special_price_status='PENDING' + audit_log。
	RequestSpecialPriceTx(ctx context.Context, in QuoteRow, impact *MarginImpact, reason, expectedMargin string, operatorID int64, requestID string) (*SpecialPriceResult, error)
}

// QuoteRow 是 customer_quote 的读取视图（Stage 9b 复用）。
type QuoteRow struct {
	ID                 int64
	CustomerID         int64
	VersionNo          int
	Status             string
	QuoteType          string
	SpecialPriceStatus *string    // 可空
	ValidUntil         *time.Time // 可空
	PriceBookVersion   int
	OwnerSalesID       int64
}

// QuoteItemDetail 是 customer_quote_item 的明细。
type QuoteItemDetail struct {
	SKUID      int64
	Currency   string
	UnitPrice  decimal.Decimal
	FloorPrice decimal.Decimal
}

// ComputeMarginImpact 纯函数：由 items 计算 margin_impact。
//
// 口径（简化版）：
//   - current_price = min(items.unit_price)（代表性，不按组件聚合）
//   - target_price  = current_price（本批特价是"许可"，不修改价格本身）
//   - current_margin = min_item_i(unit_price_i - floor_price_i) / min_item_i(unit_price_i)
//     复杂度 N；对每个 item 算 margin_i = (unit_i - floor_i) / unit_i，取最小；
//     若 unit_price_i == 0 则按 0。整体 margin 粗粒度取最严值（最破线那个 SKU 的 margin）。
//   - target_margin = expectedMargin（用户输入原文）
//   - delta_gap_distance = min_item_i(unit_price_i - floor_price_i)，可能为负（破线越多越负）
//
// 红线 1：全程 decimal 不用 float64。
func ComputeMarginImpact(items []QuoteItemDetail, expectedMargin string) *MarginImpact {
	if len(items) == 0 {
		return &MarginImpact{
			CurrentPrice: "0", CurrentMargin: "0", TargetPrice: "0",
			TargetMargin: expectedMargin, DeltaGapDistance: "0",
		}
	}
	minPrice := items[0].UnitPrice
	minMargin := decimal.Zero
	minGap := decimal.Zero
	first := true
	for _, it := range items {
		if it.UnitPrice.LessThan(minPrice) {
			minPrice = it.UnitPrice
		}
		gap := it.UnitPrice.Sub(it.FloorPrice)
		if first || gap.LessThan(minGap) {
			minGap = gap
		}
		if !it.UnitPrice.IsZero() {
			m := gap.Div(it.UnitPrice)
			if first || m.LessThan(minMargin) {
				minMargin = m
			}
		}
		first = false
	}
	return &MarginImpact{
		CurrentPrice:     minPrice.StringFixed(8),
		CurrentMargin:    minMargin.StringFixed(8),
		TargetPrice:      minPrice.StringFixed(8),
		TargetMargin:     expectedMargin,
		DeltaGapDistance: minGap.StringFixed(8),
	}
}

// Request 申请特价。
//
// 步骤：
//  1. LoadQuote 校验存在 / DRAFT / 当前非 PENDING（防重复申请）。
//  2. LoadQuoteItemsDetail → ComputeMarginImpact。
//  3. RequestSpecialPriceTx 单事务落库。
func (s *SpecialPriceService) Request(ctx context.Context, in SpecialPriceInput, operatorID int64, requestID string) (*SpecialPriceResult, error) {
	if in.Reason == "" {
		return nil, ErrReasonRequired
	}
	// expected_margin 必填且可解析为 decimal（裁决 2 字段）；格式由入库前的 price 校验保证。
	if _, err := decimal.NewFromString(in.ExpectedMargin); err != nil {
		return nil, fmt.Errorf("%w：%s", ErrExpectedMarginInvalid, in.ExpectedMargin)
	}

	quote, err := s.store.LoadQuote(ctx, in.QuoteID)
	if err != nil {
		return nil, fmt.Errorf("load quote %d: %w", in.QuoteID, err)
	}
	if quote == nil {
		return nil, ErrQuoteNotFound
	}
	if quote.Status != QuoteStatusDraft {
		return nil, ErrQuoteNotDraft
	}
	if quote.SpecialPriceStatus != nil && *quote.SpecialPriceStatus == SpecialPricePending {
		return nil, ErrQuoteAlreadyPending
	}
	items, err := s.store.LoadQuoteItemsDetail(ctx, in.QuoteID)
	if err != nil {
		return nil, fmt.Errorf("load quote items: %w", err)
	}
	impact := ComputeMarginImpact(items, in.ExpectedMargin)
	res, err := s.store.RequestSpecialPriceTx(ctx, *quote, impact, in.Reason, in.ExpectedMargin, operatorID, requestID)
	if err != nil {
		return nil, fmt.Errorf("request special price tx: %w", err)
	}
	return res, nil
}
