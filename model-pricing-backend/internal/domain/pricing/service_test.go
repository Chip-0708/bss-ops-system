// Package pricing 的 service_test.go：8a 定价策略 + 生成草稿的单测。
//
// 覆盖（stage8a 提示词单测要求）：
//  1. 策略命中：ALL/FAMILY/SKU/VENDOR 命中域、priority 最小优先、无命中跳过；
//  2. 售价计算 4 种 price_method 手算锚点（MARGIN/COST_UP/OFFICIAL_ANCHOR/FIXED）；
//  3. floor 校验边界（< 违规 / >= 不违规）；
//  4. diff_report：old_price NULL（首次）/ delta_pct 计算；
//  5. 草稿存储：status=DRAFT、version_no=MAX+1、created_by 非空。
package pricing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- fake store ----

type fakePricingStore struct {
	policies     []PricingPolicy
	skus         []SKUContext
	unitCosts    map[int64]UnitCostInfo
	official     map[int64]decimal.Decimal
	oldPrices    map[int64]string
	margin       decimal.Decimal
	savedInput   *SaveDraftInput
	savedDraftID int64
	createErr    error
	policyByID   map[int64]*PricingPolicy
	nextPolicyID int64
}

func (f *fakePricingStore) ListPolicies(ctx context.Context, page, size int) ([]PricingPolicy, int64, error) {
	return f.policies, int64(len(f.policies)), nil
}
func (f *fakePricingStore) CreatePolicy(ctx context.Context, p *PricingPolicy, operatorID int64, requestID string) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.nextPolicyID++
	p.ID = f.nextPolicyID
	f.policies = append(f.policies, *p)
	return nil
}
func (f *fakePricingStore) UpdatePolicy(ctx context.Context, p *PricingPolicy, operatorID int64, requestID string) error {
	for i := range f.policies {
		if f.policies[i].ID == p.ID {
			f.policies[i] = *p
			return nil
		}
	}
	return ErrPolicyNotFound
}
func (f *fakePricingStore) LoadPolicyByID(ctx context.Context, id int64) (*PricingPolicy, error) {
	if f.policyByID != nil {
		if p, ok := f.policyByID[id]; ok {
			return p, nil
		}
	}
	for i := range f.policies {
		if f.policies[i].ID == id {
			return &f.policies[i], nil
		}
	}
	return nil, ErrPolicyNotFound
}
func (f *fakePricingStore) LoadPoliciesByIDs(ctx context.Context, ids []int64) ([]PricingPolicy, error) {
	set := map[int64]bool{}
	for _, id := range ids {
		set[id] = true
	}
	var out []PricingPolicy
	for _, p := range f.policies {
		if set[p.ID] {
			out = append(out, p)
		}
	}
	return out, nil
}
func (f *fakePricingStore) LoadSKUContexts(ctx context.Context, skuIDs []int64) ([]SKUContext, error) {
	if len(skuIDs) == 0 {
		return f.skus, nil
	}
	set := map[int64]bool{}
	for _, id := range skuIDs {
		set[id] = true
	}
	var out []SKUContext
	for _, s := range f.skus {
		if set[s.SKUID] {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f *fakePricingStore) LoadCurrentUnitCosts(ctx context.Context, skuIDs []int64) (map[int64]UnitCostInfo, error) {
	return f.unitCosts, nil
}
func (f *fakePricingStore) LoadCurrentOfficialPrices(ctx context.Context, skuIDs []int64) (map[int64]decimal.Decimal, error) {
	return f.official, nil
}
func (f *fakePricingStore) LoadOldPrices(ctx context.Context, levelCode string, skuIDs []int64) (map[int64]string, error) {
	return f.oldPrices, nil
}
func (f *fakePricingStore) LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error) {
	return f.margin, nil
}
func (f *fakePricingStore) SaveDraft(ctx context.Context, in SaveDraftInput, operatorID int64, requestID string) (int64, error) {
	f.savedInput = &in
	if f.savedDraftID == 0 {
		f.savedDraftID = 1
	}
	return f.savedDraftID, nil
}

func dec(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

func newSvc(f *fakePricingStore) *PricingService {
	return NewPricingService(f, func() time.Time { return time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC) })
}

func i64(v int64) *int64 { return &v }

// ---- 1. 策略命中 ----

func TestMatchPolicy_ScopeAll(t *testing.T) {
	policies := []PricingPolicy{
		{ID: 1, ScopeType: ScopeAll, PriceMethod: MethodCostUp, ParamValue: "0.10", Priority: 1, Status: PolicyStatusActive},
	}
	sku := SKUContext{SKUID: 40, VendorID: 9, FamilyID: 9}
	hit := MatchPolicy(policies, sku, "STANDARD")
	require.NotNil(t, hit)
	assert.Equal(t, int64(1), hit.ID, "ALL 策略应命中所有 SKU")
}

func TestMatchPolicy_ScopeFamily(t *testing.T) {
	policies := []PricingPolicy{
		{ID: 1, ScopeType: ScopeFamily, ScopeID: i64(2), PriceMethod: MethodCostUp, ParamValue: "0.10", Priority: 1, Status: PolicyStatusActive},
	}
	hit := MatchPolicy(policies, SKUContext{SKUID: 40, FamilyID: 2}, "STANDARD")
	require.NotNil(t, hit, "family_id=2 应命中")
	assert.Nil(t, MatchPolicy(policies, SKUContext{SKUID: 41, FamilyID: 3}, "STANDARD"), "family_id=3 不应命中")
}

func TestMatchPolicy_ScopeSKU(t *testing.T) {
	policies := []PricingPolicy{
		{ID: 1, ScopeType: ScopeSKU, ScopeID: i64(40), PriceMethod: MethodCostUp, ParamValue: "0.10", Priority: 1, Status: PolicyStatusActive},
	}
	require.NotNil(t, MatchPolicy(policies, SKUContext{SKUID: 40}, "STANDARD"))
	assert.Nil(t, MatchPolicy(policies, SKUContext{SKUID: 41}, "STANDARD"))
}

func TestMatchPolicy_ScopeVendor(t *testing.T) {
	policies := []PricingPolicy{
		{ID: 1, ScopeType: ScopeVendor, ScopeID: i64(7), PriceMethod: MethodCostUp, ParamValue: "0.10", Priority: 1, Status: PolicyStatusActive},
	}
	require.NotNil(t, MatchPolicy(policies, SKUContext{SKUID: 40, VendorID: 7}, "STANDARD"))
	assert.Nil(t, MatchPolicy(policies, SKUContext{SKUID: 41, VendorID: 8}, "STANDARD"))
}

func TestMatchPolicy_PriorityMinWins(t *testing.T) {
	// 多条命中时取 priority 最小的（不叠加）。
	policies := []PricingPolicy{
		{ID: 1, ScopeType: ScopeAll, PriceMethod: MethodCostUp, ParamValue: "0.50", Priority: 5, Status: PolicyStatusActive},
		{ID: 2, ScopeType: ScopeAll, PriceMethod: MethodCostUp, ParamValue: "0.10", Priority: 1, Status: PolicyStatusActive},
		{ID: 3, ScopeType: ScopeAll, PriceMethod: MethodCostUp, ParamValue: "0.30", Priority: 3, Status: PolicyStatusActive},
	}
	hit := MatchPolicy(policies, SKUContext{SKUID: 40}, "STANDARD")
	require.NotNil(t, hit)
	assert.Equal(t, int64(2), hit.ID, "priority=1 最小应先命中")
	assert.Equal(t, "0.10", hit.ParamValue)
}

func TestMatchPolicy_LevelCodeFilter(t *testing.T) {
	gold := "GOLD"
	policies := []PricingPolicy{
		{ID: 1, ScopeType: ScopeAll, LevelCode: &gold, PriceMethod: MethodCostUp, ParamValue: "0.10", Priority: 1, Status: PolicyStatusActive},
	}
	// 策略指定 GOLD，请求 STANDARD → 不命中。
	assert.Nil(t, MatchPolicy(policies, SKUContext{SKUID: 40}, "STANDARD"))
	// 请求 GOLD → 命中。
	require.NotNil(t, MatchPolicy(policies, SKUContext{SKUID: 40}, "GOLD"))
}

func TestMatchPolicy_InactiveSkipped(t *testing.T) {
	policies := []PricingPolicy{
		{ID: 1, ScopeType: ScopeAll, PriceMethod: MethodCostUp, ParamValue: "0.10", Priority: 1, Status: PolicyStatusArchived},
	}
	assert.Nil(t, MatchPolicy(policies, SKUContext{SKUID: 40}, "STANDARD"), "非 ACTIVE 策略不命中")
}

func TestMatchPolicy_NoMatch(t *testing.T) {
	policies := []PricingPolicy{
		{ID: 1, ScopeType: ScopeSKU, ScopeID: i64(99), PriceMethod: MethodCostUp, ParamValue: "0.10", Priority: 1, Status: PolicyStatusActive},
	}
	assert.Nil(t, MatchPolicy(policies, SKUContext{SKUID: 40}, "STANDARD"), "无命中返回 nil")
}

// ---- 2. 售价计算 4 种 price_method（手算锚点）----

func TestCalculatePrice_Margin(t *testing.T) {
	// cost=2.50, param=0.15 → 2.50/0.85 = 2.94117647...
	got, err := CalculatePrice(MethodMargin, dec("2.50"), decimal.Zero, dec("0.15"))
	require.NoError(t, err)
	assert.Equal(t, "2.94117647", got.StringFixed(8))
}

func TestCalculatePrice_CostUp(t *testing.T) {
	// cost=2.50, param=0.10 → 2.50×1.10 = 2.75000000
	got, err := CalculatePrice(MethodCostUp, dec("2.50"), decimal.Zero, dec("0.10"))
	require.NoError(t, err)
	assert.Equal(t, "2.75000000", got.StringFixed(8))
}

func TestCalculatePrice_OfficialAnchor(t *testing.T) {
	// official=2.50, param=1.2 → 3.00000000
	got, err := CalculatePrice(MethodOfficialAnchor, decimal.Zero, dec("2.50"), dec("1.2"))
	require.NoError(t, err)
	assert.Equal(t, "3.00000000", got.StringFixed(8))
}

func TestCalculatePrice_Fixed(t *testing.T) {
	// param=3.00 → 3.00000000（与成本无关）
	got, err := CalculatePrice(MethodFixed, dec("99"), decimal.Zero, dec("3.00"))
	require.NoError(t, err)
	assert.Equal(t, "3.00000000", got.StringFixed(8))
}

func TestCalculatePrice_MarginParamGE1(t *testing.T) {
	// param>=1 → 除零护栏报错。
	_, err := CalculatePrice(MethodMargin, dec("2.50"), decimal.Zero, dec("1.0"))
	require.Error(t, err)
}

func TestCalculatePrice_InvalidMethod(t *testing.T) {
	_, err := CalculatePrice("TIERED", dec("2.50"), decimal.Zero, dec("0.1"))
	assert.ErrorIs(t, err, ErrInvalidPriceMethod, "TIERED 不在 DDL 枚举，应拒绝")
}

// ---- 3. floor 校验边界（经 GenerateDraft 验证）----

// buildDraftFixture 构造一个 ALL COST_UP 策略 + 单 SKU 的 fake，返回 svc 与 store。
func buildDraftFixture(paramValue, unitCost string) (*PricingService, *fakePricingStore) {
	f := &fakePricingStore{
		policies: []PricingPolicy{
			{ID: 1, ScopeType: ScopeAll, PriceMethod: MethodCostUp, ParamValue: paramValue, Priority: 1, Status: PolicyStatusActive},
		},
		skus: []SKUContext{{SKUID: 40, SKUCode: "gpt-5", VendorID: 1, FamilyID: 2, Currency: "USD"}},
		unitCosts: map[int64]UnitCostInfo{
			40: {UnitCost: dec(unitCost), UnitCostBasis: "input", BaselineVersion: 12, Currency: "USD"},
		},
		official:  map[int64]decimal.Decimal{},
		oldPrices: map[int64]string{},
		margin:    dec("0.15"),
	}
	return newSvc(f), f
}

func TestGenerateDraft_FloorViolation(t *testing.T) {
	// unit_cost=2.50, COST_UP 0.10 → new=2.75；floor=2.50/0.85=2.94117647；2.75<2.94 → 违规。
	svc, f := buildDraftFixture("0.10", "2.50")
	draft, err := svc.GenerateDraft(context.Background(), GenerateDraftInput{LevelCode: "STANDARD", Currency: "USD", SKUIDs: []int64{40}}, 1, "req-1")
	require.NoError(t, err)
	require.Len(t, draft.DiffReport, 1)
	d := draft.DiffReport[0]
	assert.Equal(t, "2.75000000", d.NewPrice)
	assert.Equal(t, "2.94117648", d.FloorPrice, "floor=2.50/0.85=2.941176470... ceil8 向上取整=2.94117648")
	assert.True(t, d.FloorViolation, "new<floor 应违规")
	assert.Equal(t, 1, draft.BlockedCount)
	_ = f
}

func TestGenerateDraft_NoFloorViolation(t *testing.T) {
	// unit_cost=2.50, COST_UP 0.30 → new=3.25；floor=2.94117647；3.25>=2.94 → 不违规。
	svc, _ := buildDraftFixture("0.30", "2.50")
	draft, err := svc.GenerateDraft(context.Background(), GenerateDraftInput{LevelCode: "STANDARD", Currency: "USD", SKUIDs: []int64{40}}, 1, "req-1")
	require.NoError(t, err)
	require.Len(t, draft.DiffReport, 1)
	assert.False(t, draft.DiffReport[0].FloorViolation, "new>=floor 不违规")
	assert.Equal(t, 0, draft.BlockedCount)
}

func TestGenerateDraft_FloorBoundaryEqual(t *testing.T) {
	// 边界：new == floor → 不违规（< 才违规）。
	// unit_cost=0.85, COST_UP 使 new = 0.85/0.85 = 1.00。即 0.85×(1+param)=1.00 → param=0.1764705882...
	// 用 MARGIN 更精确：MARGIN param=0.15 → new = 0.85/0.85 = 1.00 = floor。
	f := &fakePricingStore{
		policies: []PricingPolicy{
			{ID: 1, ScopeType: ScopeAll, PriceMethod: MethodMargin, ParamValue: "0.15", Priority: 1, Status: PolicyStatusActive},
		},
		skus:      []SKUContext{{SKUID: 40, SKUCode: "gpt-5", Currency: "USD"}},
		unitCosts: map[int64]UnitCostInfo{40: {UnitCost: dec("0.85"), UnitCostBasis: "input", BaselineVersion: 1, Currency: "USD"}},
		official:  map[int64]decimal.Decimal{},
		oldPrices: map[int64]string{},
		margin:    dec("0.15"),
	}
	svc := newSvc(f)
	draft, err := svc.GenerateDraft(context.Background(), GenerateDraftInput{LevelCode: "STANDARD", Currency: "USD", SKUIDs: []int64{40}}, 1, "req-1")
	require.NoError(t, err)
	require.Len(t, draft.DiffReport, 1)
	// new = 0.85/0.85 = 1.00000000；floor = 0.85/0.85 = 1.00000000；new==floor → 不违规。
	assert.Equal(t, "1.00000000", draft.DiffReport[0].NewPrice)
	assert.Equal(t, "1.00000000", draft.DiffReport[0].FloorPrice)
	assert.False(t, draft.DiffReport[0].FloorViolation, "new==floor 边界不违规（< 才违规）")
}

// ---- 4. diff_report：old_price / delta_pct ----

func TestGenerateDraft_OldPriceNull(t *testing.T) {
	// 首次生成（无 EFFECTIVE 旧版本）→ old_price=NULL, delta_pct=NULL。
	svc, _ := buildDraftFixture("0.30", "2.50")
	draft, err := svc.GenerateDraft(context.Background(), GenerateDraftInput{LevelCode: "STANDARD", Currency: "USD", SKUIDs: []int64{40}}, 1, "req-1")
	require.NoError(t, err)
	require.Len(t, draft.DiffReport, 1)
	assert.Nil(t, draft.DiffReport[0].OldPrice, "首次生成 old_price 应为 NULL")
	assert.Nil(t, draft.DiffReport[0].DeltaPct, "old 为 NULL 时 delta_pct 应为 NULL")
}

func TestGenerateDraft_DeltaPct(t *testing.T) {
	// 有旧版本售价 2.50，新价 3.25 → delta_pct = (3.25-2.50)/2.50 = 0.300000。
	svc, f := buildDraftFixture("0.30", "2.50")
	f.oldPrices = map[int64]string{40: "2.50000000"}
	draft, err := svc.GenerateDraft(context.Background(), GenerateDraftInput{LevelCode: "STANDARD", Currency: "USD", SKUIDs: []int64{40}}, 1, "req-1")
	require.NoError(t, err)
	require.Len(t, draft.DiffReport, 1)
	d := draft.DiffReport[0]
	require.NotNil(t, d.OldPrice)
	assert.Equal(t, "2.50000000", *d.OldPrice)
	require.NotNil(t, d.DeltaPct)
	assert.Equal(t, "0.300000", *d.DeltaPct, "delta_pct=(3.25-2.50)/2.50=0.300000")
}

// ---- 5. 草稿存储字段 ----

func TestGenerateDraft_SaveDraftFields(t *testing.T) {
	svc, f := buildDraftFixture("0.30", "2.50")
	draft, err := svc.GenerateDraft(context.Background(), GenerateDraftInput{LevelCode: "STANDARD", Currency: "USD", SKUIDs: []int64{40}}, 7, "req-9")
	require.NoError(t, err)
	assert.Equal(t, int64(1), draft.DraftID)
	assert.Equal(t, 1, draft.ItemCount)
	require.NotNil(t, f.savedInput)
	require.Len(t, f.savedInput.Items, 1)
	item := f.savedInput.Items[0]
	assert.Equal(t, int64(40), item.SKUID)
	assert.Equal(t, int64(1), item.PolicyID)
	assert.Equal(t, 12, item.BaselineVersion, "baseline_version 应记录成本基线版本号")
	assert.Equal(t, "input", item.ComponentType, "代表组件类型应透传（不硬编码）")
	assert.Equal(t, "3.25000000", item.NewPrice)
	assert.Equal(t, "2.94117648", item.FloorPrice, "floor ceil8 向上取整")
}

// ---- 跳过逻辑 ----

func TestGenerateDraft_SkipNoPolicy(t *testing.T) {
	// 无命中策略 → 跳过，不进 diff_report。
	f := &fakePricingStore{
		policies:  []PricingPolicy{}, // 无策略
		skus:      []SKUContext{{SKUID: 40, SKUCode: "gpt-5", Currency: "USD"}},
		unitCosts: map[int64]UnitCostInfo{40: {UnitCost: dec("2.50"), UnitCostBasis: "input", BaselineVersion: 1, Currency: "USD"}},
		official:  map[int64]decimal.Decimal{},
		oldPrices: map[int64]string{},
		margin:    dec("0.15"),
	}
	svc := newSvc(f)
	draft, err := svc.GenerateDraft(context.Background(), GenerateDraftInput{LevelCode: "STANDARD", Currency: "USD", SKUIDs: []int64{40}}, 1, "req-1")
	require.NoError(t, err)
	assert.Equal(t, 0, draft.ItemCount, "无命中策略应跳过")
	assert.Empty(t, draft.DiffReport)
}

func TestGenerateDraft_SkipNoCostBaseline(t *testing.T) {
	// 有策略但无成本基线 → 跳过。
	f := &fakePricingStore{
		policies: []PricingPolicy{
			{ID: 1, ScopeType: ScopeAll, PriceMethod: MethodCostUp, ParamValue: "0.10", Priority: 1, Status: PolicyStatusActive},
		},
		skus:      []SKUContext{{SKUID: 42, SKUCode: "qwen3", Currency: "USD"}},
		unitCosts: map[int64]UnitCostInfo{}, // 无成本基线
		official:  map[int64]decimal.Decimal{},
		oldPrices: map[int64]string{},
		margin:    dec("0.15"),
	}
	svc := newSvc(f)
	draft, err := svc.GenerateDraft(context.Background(), GenerateDraftInput{LevelCode: "STANDARD", Currency: "USD", SKUIDs: []int64{42}}, 1, "req-1")
	require.NoError(t, err)
	assert.Equal(t, 0, draft.ItemCount, "无成本基线应跳过")
}

func TestGenerateDraft_SkipOfficialAnchorNoPrice(t *testing.T) {
	// OFFICIAL_ANCHOR 但无官方价 → 跳过。
	f := &fakePricingStore{
		policies: []PricingPolicy{
			{ID: 1, ScopeType: ScopeAll, PriceMethod: MethodOfficialAnchor, ParamValue: "1.2", Priority: 1, Status: PolicyStatusActive},
		},
		skus:      []SKUContext{{SKUID: 40, SKUCode: "gpt-5", Currency: "USD"}},
		unitCosts: map[int64]UnitCostInfo{40: {UnitCost: dec("2.50"), UnitCostBasis: "input", BaselineVersion: 1, Currency: "USD"}},
		official:  map[int64]decimal.Decimal{}, // 无官方价
		oldPrices: map[int64]string{},
		margin:    dec("0.15"),
	}
	svc := newSvc(f)
	draft, err := svc.GenerateDraft(context.Background(), GenerateDraftInput{LevelCode: "STANDARD", Currency: "USD", SKUIDs: []int64{40}}, 1, "req-1")
	require.NoError(t, err)
	assert.Equal(t, 0, draft.ItemCount, "OFFICIAL_ANCHOR 无官方价应跳过")
}

// ---- 策略校验 ----

func TestValidatePolicy(t *testing.T) {
	f := &fakePricingStore{}
	svc := newSvc(f)
	ctx := context.Background()

	// MODEL_TYPE → 400
	err := svc.CreatePolicy(ctx, &PricingPolicy{Code: "x", Name: "x", ScopeType: ScopeModelType, PriceMethod: MethodCostUp, ParamValue: "0.1", Status: PolicyStatusActive}, 1, "r")
	assert.ErrorIs(t, err, ErrModelTypeUnsupported)
	// 非法 scope_type
	err = svc.CreatePolicy(ctx, &PricingPolicy{Code: "x", Name: "x", ScopeType: "GLOBAL", PriceMethod: MethodCostUp, ParamValue: "0.1", Status: PolicyStatusActive}, 1, "r")
	assert.ErrorIs(t, err, ErrInvalidScopeType)
	// 非法 price_method
	err = svc.CreatePolicy(ctx, &PricingPolicy{Code: "x", Name: "x", ScopeType: ScopeAll, PriceMethod: "TIERED", ParamValue: "0.1", Status: PolicyStatusActive}, 1, "r")
	assert.ErrorIs(t, err, ErrInvalidPriceMethod)
	// 非法 param_value
	err = svc.CreatePolicy(ctx, &PricingPolicy{Code: "x", Name: "x", ScopeType: ScopeAll, PriceMethod: MethodCostUp, ParamValue: "abc", Status: PolicyStatusActive}, 1, "r")
	assert.ErrorIs(t, err, ErrInvalidParamValue)
	// 负数 param_value
	err = svc.CreatePolicy(ctx, &PricingPolicy{Code: "x", Name: "x", ScopeType: ScopeAll, PriceMethod: MethodCostUp, ParamValue: "-0.1", Status: PolicyStatusActive}, 1, "r")
	assert.ErrorIs(t, err, ErrInvalidParamValue)
	// 合法
	err = svc.CreatePolicy(ctx, &PricingPolicy{Code: "ok", Name: "ok", ScopeType: ScopeAll, PriceMethod: MethodCostUp, ParamValue: "0.10", Status: PolicyStatusActive}, 1, "r")
	require.NoError(t, err)
}

// TestUpdatePolicy_NotFound 更新不存在的策略 → ErrPolicyNotFound。
func TestUpdatePolicy_NotFound(t *testing.T) {
	f := &fakePricingStore{}
	svc := newSvc(f)
	err := svc.UpdatePolicy(context.Background(), &PricingPolicy{ID: 999, Code: "x", Name: "x", ScopeType: ScopeAll, PriceMethod: MethodCostUp, ParamValue: "0.1", Status: PolicyStatusActive}, 1, "r")
	assert.ErrorIs(t, err, ErrPolicyNotFound)
}

// 确保 errors 包被引用（fake 用）。
var _ = errors.Is
