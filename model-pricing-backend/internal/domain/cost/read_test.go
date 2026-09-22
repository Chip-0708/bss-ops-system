package cost

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeReadStore 是 BaselineReadStore 的内存实现（读侧单测：只 fake 三个方法，不碰重算 65 条）。
type fakeReadStore struct {
	listItems []BaselineListItem
	listTotal int64
	listErr   error

	resolveID   int64
	resolveOK   bool
	resolveErr  error
	resolveGot  string
	resolveCall int

	histItems []BaselineHistoryItem
	histErr   error
	histGotID int64
	histAsOf  *time.Time

	margin    decimal.Decimal
	marginErr error
}

func (f *fakeReadStore) ListBaselines(_ context.Context, q BaselineListQuery) ([]BaselineListItem, int64, error) {
	// 分页行为不进 fake——handler 才是规整的边界；fake 原样回存储内容。
	_ = q
	return f.listItems, f.listTotal, f.listErr
}

func (f *fakeReadStore) ResolveSKUID(_ context.Context, sku string) (int64, bool, error) {
	f.resolveCall++
	f.resolveGot = sku
	return f.resolveID, f.resolveOK, f.resolveErr
}

func (f *fakeReadStore) ListBaselineHistory(_ context.Context, skuID int64, asOf *time.Time) ([]BaselineHistoryItem, error) {
	f.histGotID = skuID
	f.histAsOf = asOf
	return f.histItems, f.histErr
}

func (f *fakeReadStore) LoadMinGrossMargin(_ context.Context) (decimal.Decimal, error) {
	return f.margin, f.marginErr
}

// ---- 6d-4 追加的 BaselineReadStore 方法（fake 默认零值实现——6d-4 单测另用 fakeCompareStore） ----
// 接口长胖是 6d-4 契约（§4/§5 都要走 BaselineReadStore），这里保持编译通过即可；
// 本测试文件的老用例不碰 trend/bargain 路径，不于此铺 fake 逻辑。

func (f *fakeReadStore) ListBaselineTrend(_ context.Context, skuID int64, days int) ([]TrendPoint, error) {
	return nil, nil
}

func (f *fakeReadStore) ListSKUIDs(_ context.Context) ([]int64, error) {
	return nil, nil
}

func (f *fakeReadStore) LoadSysConfigDecimal(_ context.Context, key string) (decimal.Decimal, error) {
	return decimal.Zero, nil
}

func (f *fakeReadStore) ListPrimarySupplierNames(_ context.Context, skuIDs []int64) (map[int64]string, error) {
	return map[int64]string{}, nil
}

func (f *fakeReadStore) ListPrimarySupplierIDs(_ context.Context, skuIDs []int64) (map[int64]int64, error) {
	return map[int64]int64{}, nil
}

func TestReadService_ListBaselines_FloorComputed(t *testing.T) {
	store := &fakeReadStore{
		listItems: []BaselineListItem{
			{SKUID: 40, SKUCode: "model-a", UnitCost: "2.60075000", UnitCostBasis: "input"},
			{SKUID: 41, SKUCode: "model-b", UnitCost: "62.41800000", UnitCostBasis: "output"},
		},
		listTotal: 2,
		margin:    decimal.RequireFromString("0.15"),
	}
	svc := NewReadService(store)
	res, err := svc.ListBaselines(context.Background(), BaselineListQuery{Page: 1, Size: 20})
	require.NoError(t, err)
	require.Len(t, res.List, 2)

	// floor = unit_cost / (1-0.15)，与写侧同一 Floor 纯函数（裁决 1）
	// 2.60075 / 0.85 = 3.05970588235… → StringFixed(8)
	require.NotNil(t, res.List[0].FloorPrice)
	assert.Equal(t, "3.05970588", *res.List[0].FloorPrice)
	require.NotNil(t, res.List[1].FloorPrice)
	assert.Equal(t, "73.43294118", *res.List[1].FloorPrice)
	assert.Equal(t, int64(2), res.Total)
	assert.Equal(t, 1, res.Page)
	assert.Equal(t, 20, res.Size)
}

func TestReadService_ListBaselines_MarginMissing_FloorNullNotError(t *testing.T) {
	store := &fakeReadStore{
		listItems: []BaselineListItem{{SKUID: 40, UnitCost: "2.60075000"}},
		listTotal: 1,
		marginErr: errors.New("sys_config not found"),
	}
	svc := NewReadService(store)
	res, err := svc.ListBaselines(context.Background(), BaselineListQuery{Page: 1, Size: 20})
	require.NoError(t, err)
	require.Len(t, res.List, 1)
	assert.Nil(t, res.List[0].FloorPrice, "读 min_gross_margin 失败 → floor_price=null，不 500")
}

func TestReadService_ListBaselines_MarginOne_FloorNull(t *testing.T) {
	store := &fakeReadStore{
		listItems: []BaselineListItem{{SKUID: 40, UnitCost: "2.60075000"}},
		listTotal: 1,
		margin:    decimal.NewFromInt(1), // 除零保护
	}
	svc := NewReadService(store)
	res, err := svc.ListBaselines(context.Background(), BaselineListQuery{Page: 1, Size: 20})
	require.NoError(t, err)
	assert.Nil(t, res.List[0].FloorPrice)
}

func TestReadService_History_AsOfPassthrough(t *testing.T) {
	asOf := time.Date(2026, 9, 14, 15, 0, 0, 0, time.UTC)
	store := &fakeReadStore{
		resolveID: 40, resolveOK: true,
		histItems: []BaselineHistoryItem{{Version: 1, UnitCost: "2.60075000"}},
	}
	svc := NewReadService(store)
	items, ok, err := svc.History(context.Background(), "40", &asOf)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, items, 1)
	assert.Equal(t, int64(40), store.histGotID)
	require.NotNil(t, store.histAsOf)
	assert.Equal(t, asOf, *store.histAsOf)
	assert.Equal(t, "40", store.resolveGot)
}

func TestReadService_History_SKUNotFound(t *testing.T) {
	store := &fakeReadStore{resolveOK: false}
	svc := NewReadService(store)
	_, ok, err := svc.History(context.Background(), "model-no-such", nil)
	require.NoError(t, err)
	assert.False(t, ok, "sku 解析不到 → handler 回 404")
}

func TestReadService_History_EmptyIs200EmptyList(t *testing.T) {
	store := &fakeReadStore{resolveID: 40, resolveOK: true, histItems: []BaselineHistoryItem{}}
	svc := NewReadService(store)
	items, ok, err := svc.History(context.Background(), "model-a", nil)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Empty(t, items, "asOf 查不到 → 200 + 空 list，不是 404")
}

func TestReadService_History_ResolvedByCode(t *testing.T) {
	store := &fakeReadStore{resolveID: 40, resolveOK: true}
	svc := NewReadService(store)
	_, ok, err := svc.History(context.Background(), "model-a", nil)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "model-a", store.resolveGot, "非纯数字段按 sku_code 解析")
}

func TestBaselineListItem_JSONKeysForMask(t *testing.T) {
	// 000017 的 mask 精确命中 json key——这里锁死字段名防漂移（mask 改名的凭据）。
	fp := "1.00000000"
	item := BaselineListItem{
		SKUID: 40, SKUCode: "model-a", UnitCost: "2.60075000", UnitCostBasis: "input",
		FloorPrice: &fp,
	}
	b, err := json.Marshal(item)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	assert.Contains(t, m, "unit_cost")
	assert.Contains(t, m, "unit_cost_basis")
	assert.Contains(t, m, "floor_price")
	assert.Contains(t, m, "supplier_count")
	assert.Contains(t, m, "single_point")
}

func TestBaselineHistoryItem_ContainsCalcSnapshotAndSupplierCost(t *testing.T) {
	// history 的 calc_snapshot 必须整体出现在响应里（SUPPLIER 剔整块）；
	// supplier_cost 在快照 scores[] 内（被 mask 命中靠快照整体剔除，不再单独出现顶层 key）。
	item := BaselineHistoryItem{
		Version: 1, UnitCost: "2.60075000", UnitCostBasis: "input",
		CalcSnapshot: json.RawMessage(`{"formula_version":"6b-v1","scores":[{"supplier_cost":"2.50000000"}]}`),
	}
	b, err := json.Marshal(item)
	require.NoError(t, err)
	assert.Contains(t, string(b), "calc_snapshot")
	assert.Contains(t, string(b), "supplier_cost") // 在 calc_snapshot 内部，被整块剔除一起带走
}
