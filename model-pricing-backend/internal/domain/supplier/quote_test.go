package supplier

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// fakeQuoteStore 为单测的内存报价仓储。
type fakeQuoteStore struct {
	quotable    map[int64]QuotableSKU
	officials   map[int64][]OfficialComponent
	nonTerminal bool

	createParams *CreateQuoteParams
	createErr    error
	created      *SubmitQuoteResult
}

func (f *fakeQuoteStore) FindQuotableSKUs(_ context.Context, skuIDs []int64) (map[int64]QuotableSKU, error) {
	out := make(map[int64]QuotableSKU, len(skuIDs))
	for _, id := range skuIDs {
		if s, ok := f.quotable[id]; ok {
			out[id] = s
		}
	}
	return out, nil
}

func (f *fakeQuoteStore) FindOfficialComponents(_ context.Context, skuIDs []int64) (map[int64][]OfficialComponent, error) {
	out := make(map[int64][]OfficialComponent, len(skuIDs))
	for _, id := range skuIDs {
		if comps, ok := f.officials[id]; ok {
			out[id] = comps
		}
	}
	return out, nil
}

func (f *fakeQuoteStore) FindNonTerminalQuote(_ context.Context, _ int64) (*NonTerminalQuote, error) {
	if !f.nonTerminal {
		return nil, nil
	}
	return &NonTerminalQuote{ID: 321, SKUCount: 2}, nil
}

func (f *fakeQuoteStore) CreateQuoteWithTodo(_ context.Context, p CreateQuoteParams) (*SubmitQuoteResult, error) {
	f.createParams = &p
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.created != nil {
		return f.created, nil
	}
	return &SubmitQuoteResult{
		ID: 1, SupplierID: p.Supplier.ID, VersionNo: 1, Status: "APPROVING",
		ValidFrom: p.ValidFrom, ValidTo: p.ValidTo, Source: "MANUAL",
		Clamped: p.Clamped, ItemCount: len(p.Items), SubmittedAt: p.SubmittedAt,
	}, nil
}

func (f *fakeQuoteStore) ListQuoteHistory(_ context.Context, _ int64, q QuoteHistoryQuery) (*QuoteHistoryResult, error) {
	return &QuoteHistoryResult{List: []QuoteHistoryItem{}, Page: q.Page, Size: q.Size}, nil
}

func (f *fakeQuoteStore) GetQuoteDetail(_ context.Context, _, _ int64) (*QuoteDetail, error) {
	return nil, nil
}

// dec 是 decimal 字面量的便捷构造。
func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func strP(s string) *string { return &s }

var testSupplier = &Supplier{ID: 10, SubjectID: 100, LegalName: "供应商甲", OwnerProcurementOperatorID: 4}

// baseStore 返回一套可用的默认仓储：1 个 USD SKU（有官方价 input/output）。
func baseStore() *fakeQuoteStore {
	return &fakeQuoteStore{
		quotable: map[int64]QuotableSKU{
			// ModelName 与 FindQuotableSKUs 的真库拼接规则同源：family_name + " " + sku_code
			// （supplier_import.go:110）——LI-005 的 model_name 校验依赖这个拼接值。
			12: {ID: 12, SkuCode: "gpt-5-2026-04-11", NativeCurrency: "USD", ModelName: "OpenAI gpt-5-2026-04-11"},
			13: {ID: 13, SkuCode: "qwen3-max-2026-06", NativeCurrency: "CNY", ModelName: "Qwen qwen3-max-2026-06"},
			14: {ID: 14, SkuCode: "qwen3-embedding-2026-03", NativeCurrency: "CNY", ModelName: "Qwen qwen3-embedding-2026-03"}, // 无官方价
		},
		officials: map[int64][]OfficialComponent{
			12: {
				{ComponentType: "input", UnitPrice: dec("2.5")},
				{ComponentType: "output", UnitPrice: dec("10")},
			},
			13: {
				{ComponentType: "input", UnitPrice: dec("8")},
				{ComponentType: "output", UnitPrice: dec("32")},
			},
		},
	}
}

// baseInput 返回一份合法输入（未来生效，USD 行倍率自洽）。
func baseInput() SubmitQuoteInput {
	from := time.Now().UTC().Add(24 * time.Hour)
	to := from.Add(90 * 24 * time.Hour)
	return SubmitQuoteInput{
		ValidFrom: from,
		ValidTo:   to,
		Remark:    "季度续报",
		Items: []SubmitQuoteItem{
			{
				SKUID:  12,
				FxTier: strP("6.8"),
				Components: []SubmitQuoteComponent{
					{ComponentType: "input", Multiplier: strP("0.8"), UnitPrice: "2.00000000"},
					{ComponentType: "output", Multiplier: strP("0.75"), UnitPrice: "7.50000000"},
				},
			},
		},
	}
}

func TestSubmitQuote_OK(t *testing.T) {
	svc := NewQuoteService(baseStore())
	res, err := svc.SubmitQuote(context.Background(), testSupplier, baseInput(), 77, "req-1")
	require.NoError(t, err)
	require.Equal(t, "APPROVING", res.Status)
	require.False(t, res.Clamped)
	require.Equal(t, 1, res.ItemCount)
	require.Equal(t, 1, res.VersionNo)
}

func TestSubmitQuote_CNYFxTierSilentlyNulled(t *testing.T) {
	store := baseStore()
	svc := NewQuoteService(store)
	in := baseInput()
	in.Items = []SubmitQuoteItem{
		{
			SKUID:  13, // CNY 模型
			FxTier: strP("6.8"),
			Components: []SubmitQuoteComponent{
				{ComponentType: "input", Multiplier: strP("0.8"), UnitPrice: "6.40000000"},
			},
		},
	}
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.NoError(t, err)
	// CNY 模型传了 fx_tier → 置 null 不报错
	require.Nil(t, store.createParams.Items[0].FxTier)
}

func TestSubmitQuote_FxTierNumericEquivalent(t *testing.T) {
	store := baseStore()
	svc := NewQuoteService(store)
	in := baseInput()
	in.Items[0].FxTier = strP("6.800") // 数值等价 6.8
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.NoError(t, err)
	// 命中后回显规范形式
	require.NotNil(t, store.createParams.Items[0].FxTier)
	require.Equal(t, "6.8", *store.createParams.Items[0].FxTier)
}

func TestSubmitQuote_PriceInconsistent(t *testing.T) {
	svc := NewQuoteService(baseStore())
	in := baseInput()
	in.Items[0].Components[0].UnitPrice = "9.99999999" // 2.5×0.8=2.0，差 7.99 远超 1e-4
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.ErrorIs(t, err, ErrQuotePriceInconsistent)
	require.Contains(t, err.Error(), "input")
}

func TestSubmitQuote_PriceWithinTolerance(t *testing.T) {
	svc := NewQuoteService(baseStore())
	in := baseInput()
	in.Items[0].Components[0].UnitPrice = "2.00009999" // |2.00009999 − 2.0| = 0.00009999 ≤ 1e-4
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.NoError(t, err)
}

func TestSubmitQuote_MultiplierForbiddenWithoutOfficialPrice(t *testing.T) {
	svc := NewQuoteService(baseStore())
	in := baseInput()
	in.Items = []SubmitQuoteItem{
		{
			SKUID: 14, // 无官方价
			Components: []SubmitQuoteComponent{
				{ComponentType: "embedding", Multiplier: strP("0.5"), UnitPrice: "1.0"},
			},
		},
	}
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.ErrorIs(t, err, ErrQuoteMultiplierForbidden)
}

func TestSubmitQuote_AbsolutePriceWithoutOfficialPriceOK(t *testing.T) {
	store := baseStore()
	svc := NewQuoteService(store)
	in := baseInput()
	in.Items = []SubmitQuoteItem{
		{
			SKUID: 14, // 无官方价，强制绝对价
			Components: []SubmitQuoteComponent{
				{ComponentType: "embedding", UnitPrice: "1.50000000"},
			},
		},
	}
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.NoError(t, err)
	require.Nil(t, store.createParams.Items[0].Components[0].Multiplier)
}

func TestSubmitQuote_ClampPastValidFrom(t *testing.T) {
	store := baseStore()
	svc := NewQuoteService(store)
	in := baseInput()
	in.ValidFrom = time.Now().UTC().Add(-48 * time.Hour) // 过去时间
	in.ValidTo = time.Now().UTC().Add(48 * time.Hour)
	res, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.NoError(t, err)
	require.True(t, res.Clamped)
	// 钳制为提交时刻（近似 now）
	require.WithinDuration(t, time.Now().UTC(), res.ValidFrom, 5*time.Second)
}

func TestSubmitQuote_NonTerminalConflict(t *testing.T) {
	store := baseStore()
	store.nonTerminal = true
	svc := NewQuoteService(store)
	_, err := svc.SubmitQuote(context.Background(), testSupplier, baseInput(), 77, "req-1")
	require.ErrorIs(t, err, ErrQuoteConflict)
	require.Contains(t, err.Error(), "quote_sheet_id=321")
	require.Contains(t, err.Error(), "包含 2 个 SKU")
}

func TestSubmitQuote_VersionConflictOnUniqueIndex(t *testing.T) {
	// uk_quote_ver / uk_quote_effective / uk_quote_pending 兜底命中：
	// 仓储层识别 PG 23505 并映射为领域冲突错误，service 原样透传。
	store := baseStore()
	store.createErr = ErrQuoteConflict
	svc := NewQuoteService(store)
	_, err := svc.SubmitQuote(context.Background(), testSupplier, baseInput(), 77, "req-1")
	require.ErrorIs(t, err, ErrQuoteConflict)
}

func TestSubmitQuote_UnknownComponentType(t *testing.T) {
	svc := NewQuoteService(baseStore())
	in := baseInput()
	in.Items[0].Components[0].ComponentType = "magic_input"
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.ErrorIs(t, err, ErrQuoteComponentTypeUnknown)
}

func TestSubmitQuote_DuplicateComponentTypeInRow(t *testing.T) {
	svc := NewQuoteService(baseStore())
	in := baseInput()
	in.Items[0].Components = append(in.Items[0].Components,
		SubmitQuoteComponent{ComponentType: "input", Multiplier: strP("0.8"), UnitPrice: "2.0"})
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.ErrorIs(t, err, ErrQuoteComponentEmpty)
}

func TestSubmitQuote_UnknownConstraintKey(t *testing.T) {
	svc := NewQuoteService(baseStore())
	in := baseInput()
	in.Items[0].Constraints = map[string]any{"rpm": 3000, "magic_key": 1}
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.ErrorIs(t, err, ErrQuoteConstraintKeyUnknown)
}

func TestSubmitQuote_FxTierRequiredForUSD(t *testing.T) {
	svc := NewQuoteService(baseStore())
	in := baseInput()
	in.Items[0].FxTier = nil
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.ErrorIs(t, err, ErrQuoteFxTierRequired)
}

func TestSubmitQuote_FxTierInvalid(t *testing.T) {
	svc := NewQuoteService(baseStore())
	in := baseInput()
	in.Items[0].FxTier = strP("6.86")
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.ErrorIs(t, err, ErrQuoteFxTierInvalid)
}

func TestSubmitQuote_SkuNotQuotable(t *testing.T) {
	svc := NewQuoteService(baseStore())
	in := baseInput()
	in.Items[0].SKUID = 999
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.ErrorIs(t, err, ErrQuoteSkuNotQuotable)
}

func TestSubmitQuote_DuplicateSku(t *testing.T) {
	svc := NewQuoteService(baseStore())
	in := baseInput()
	in.Items = append(in.Items, in.Items[0])
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.ErrorIs(t, err, ErrQuoteDuplicateSku)
}

func TestSubmitQuote_EmptyItems(t *testing.T) {
	svc := NewQuoteService(baseStore())
	in := baseInput()
	in.Items = nil
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.ErrorIs(t, err, ErrQuoteItemsEmpty)
}

func TestSubmitQuote_TooManyItems(t *testing.T) {
	svc := NewQuoteService(baseStore())
	in := baseInput()
	in.Items = make([]SubmitQuoteItem, 501)
	for i := range in.Items {
		in.Items[i] = SubmitQuoteItem{
			SKUID:      int64(i + 1),
			Components: []SubmitQuoteComponent{{ComponentType: "input", UnitPrice: "1"}},
		}
	}
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.ErrorIs(t, err, ErrQuoteItemsTooMany)
	require.Contains(t, err.Error(), "500")
}

func TestSubmitQuote_ValidToNotAfterValidFrom(t *testing.T) {
	svc := NewQuoteService(baseStore())
	in := baseInput()
	in.ValidTo = in.ValidFrom.Add(-time.Hour)
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.ErrorIs(t, err, ErrQuoteValidToInvalid)
}

func TestSubmitQuote_PriceInvalid(t *testing.T) {
	svc := NewQuoteService(baseStore())
	in := baseInput()
	in.Items[0].Components[0].UnitPrice = "not-a-number"
	_, err := svc.SubmitQuote(context.Background(), testSupplier, in, 77, "req-1")
	require.ErrorIs(t, err, ErrQuotePriceInvalid)
}

func TestGetQuoteDetail_NotFound(t *testing.T) {
	svc := NewQuoteService(baseStore())
	_, err := svc.GetQuoteDetail(context.Background(), 10, 999)
	require.ErrorIs(t, err, ErrQuoteNotFound)
}

func TestListQuoteHistory_PagingNormalize(t *testing.T) {
	svc := NewQuoteService(baseStore())
	res, err := svc.ListQuoteHistory(context.Background(), 10, QuoteHistoryQuery{Page: 0, Size: 999})
	require.NoError(t, err)
	require.Equal(t, 1, res.Page)
	require.Equal(t, 20, res.Size)
}

// 确保领域错误可被 errors.Is 判定且不互相误判。
func TestQuoteErrors_Distinct(t *testing.T) {
	require.False(t, errors.Is(ErrQuoteConflict, ErrQuoteNotFound))
	require.False(t, errors.Is(ErrQuotePriceInconsistent, ErrQuoteItemsEmpty))
}

// 契约 §0.4：库存 "6.800"/"6.80" → 详情/历史回显规范形式 "6.8"；null 保持 null。
func TestCanonicalFxTier(t *testing.T) {
	require.Equal(t, "6.8", *CanonicalFxTier(strP("6.800")))
	require.Equal(t, "6.8", *CanonicalFxTier(strP("6.80")))
	require.Equal(t, "6.8", *CanonicalFxTier(strP("6.8")))
	require.Equal(t, "6.75", *CanonicalFxTier(strP("6.750")))
	require.Equal(t, "7.0", *CanonicalFxTier(strP("7.000")))
	require.Nil(t, CanonicalFxTier(nil))
	// 非档位值原样返回（不静默改历史数据）
	require.Equal(t, "6.86", *CanonicalFxTier(strP("6.86")))
}
