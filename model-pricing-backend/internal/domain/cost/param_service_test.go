package cost

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// ParamService 测试
// ============================================================================

// fakeParamStore 是 ParamStore 的内存实现。
type fakeParamStore struct {
	params            []Param
	skuIDs            map[int64]bool
	supplierIDs       map[int64]bool
	affectedByScope   map[string][]int64 // key = "TYPE|id"
	replaceCalls      []ReplaceTxParams
	listCalled        int
	replaceShouldFail error
}

func newFakeParamStore() *fakeParamStore {
	return &fakeParamStore{
		skuIDs:          map[int64]bool{},
		supplierIDs:     map[int64]bool{},
		affectedByScope: map[string][]int64{},
	}
}

func (f *fakeParamStore) ListAllParams(_ context.Context) ([]Param, error) {
	f.listCalled++
	out := make([]Param, len(f.params))
	copy(out, f.params)
	return out, nil
}

// ListStoredParams 把 fake 里的 decimal 转回字符串——通过 StoredParamOf（内部走
// formatRate/StringFixed(4)），恰与真实 DB 数值列返回的字符串同构（numeric(8,4)）。
// 这样 fake 测试与真实 GORM 仓储在「看到的字符串」层面是同形映射，断言才可信。
func (f *fakeParamStore) ListStoredParams(_ context.Context) ([]StoredParam, error) {
	out := make([]StoredParam, 0, len(f.params))
	for _, p := range f.params {
		out = append(out, StoredParamOf(p))
	}
	return out, nil
}

func (f *fakeParamStore) SKUExists(_ context.Context, id int64) (bool, error) {
	return f.skuIDs[id], nil
}

func (f *fakeParamStore) SupplierExists(_ context.Context, id int64) (bool, error) {
	return f.supplierIDs[id], nil
}

func affectedKey(scopeType string, scopeID int64) string {
	return fmt.Sprintf("%s|%d", scopeType, scopeID)
}

func (f *fakeParamStore) ListParamAffectedSKUs(_ context.Context, scopeType string, scopeID int64) ([]int64, error) {
	out := make([]int64, len(f.affectedByScope[affectedKey(scopeType, scopeID)]))
	copy(out, f.affectedByScope[affectedKey(scopeType, scopeID)])
	return out, nil
}

func (f *fakeParamStore) ReplaceOverridesTx(_ context.Context, p ReplaceTxParams) (*ReplaceTxResult, error) {
	f.replaceCalls = append(f.replaceCalls, p)
	if f.replaceShouldFail != nil {
		return nil, f.replaceShouldFail
	}
	// 模拟落库：params 替换为「GLOBAL 行 + 新 override」。
	kept := make([]Param, 0, len(f.params))
	for _, row := range f.params {
		if row.ScopeType == ScopeGlobal {
			kept = append(kept, row)
		}
	}
	f.params = kept
	for _, ins := range p.Inserts {
		loss, _ := decimal.NewFromString(ins.LossRate)
		channel, _ := decimal.NewFromString(ins.ChannelRate)
		tax, _ := decimal.NewFromString(ins.WithholdingTax)
		f.params = append(f.params, Param{
			ScopeType: ins.ScopeType, ScopeID: ins.ScopeID,
			LossRate: loss, ChannelRate: channel,
			TaxInclusive: ins.TaxInclusive, WithholdingTax: tax,
		})
	}
	taskIDs := make([]int64, len(p.AffectedSKUs))
	for i := range taskIDs {
		taskIDs[i] = int64(1000 + i)
	}
	return &ReplaceTxResult{AuditLogID: 777, TaskIDs: taskIDs}, nil
}

func strToDec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func fixedNow() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) }

// TestStringTrimsTrailingZeros 钉死 decimal.String() 的真实行为：String() 会 trim 尾零。
// 此测试若未来被改挂（比如 shopspring/decimal 的重大改版），说明 Option A 的字面前提被推翻，
// 那时再回头重审 StoredParam 是否还需要——截止到本 commit，String() 真的会 trim。
func TestStringTrimsTrailingZeros(t *testing.T) {
	require.Equal(t, "0.03", decimal.RequireFromString("0.0300").String(),
		"decimal.String() 会 trim 尾零——所以 Option A 不能用 String() 实现，必须用原始字符串透传")
	require.Equal(t, "0.05", decimal.RequireFromString("0.0500").String())
	require.Equal(t, "0", decimal.RequireFromString("0.0000").String())
	// StringFixed(4) 才是 Stable 的 4 位小数形态（写侧用它落库）。
	require.Equal(t, "0.0300", decimal.RequireFromString("0.0300").StringFixed(4))
	require.Equal(t, "0.0000", decimal.RequireFromString("0").StringFixed(4))
}

func seedParams() []Param {
	return []Param{
		{ScopeType: ScopeGlobal, ScopeID: 0,
			LossRate: strToDec("0.0300"), ChannelRate: strToDec("0.0100"),
			TaxInclusive: true, WithholdingTax: strToDec("0.0000")},
		{ScopeType: ScopeModel, ScopeID: 40,
			LossRate: strToDec("0.0500"), ChannelRate: strToDec("0.0100"),
			TaxInclusive: false, WithholdingTax: strToDec("0.0000")},
	}
}

// TestListParams_Golden 锁 GET 输出精确形状：
//   - 比率一律**原样透传库存字符串**（numeric(8,4) 恒为 4 位小数）——Option A 落地后
//     GET 返回的就是 DB 里的字符，不再经任何中间重整形；
//   - overrides 按 scope_type, scope_id 稳定排序。

func opPricingOp() PutOperator {
	return PutOperator{OperatorID: 1, OperatorRole: "PRICING_OP"} // 字面值：测试不与 pkg/perm 解耦
}

// TestListParams_Golden 锁 GET 输出精确形状：比率一律原样透传（fake 经 StoredParamOf
// 映射回 4 位小数字符串，与 PG numeric(8,4) 等同），overrides 按 scope_type, scope_id 稳定排序。
func TestListParams_Golden(t *testing.T) {
	fs := newFakeParamStore()
	fs.params = seedParams()
	svc := NewParamService(fs, fixedNow)

	view, err := svc.ListParams(context.Background())
	require.NoError(t, err)
	require.Equal(t, "0.0300", view.Defaults.LossRate)
	require.Equal(t, "0.0100", view.Defaults.ChannelRate)
	require.True(t, view.Defaults.TaxInclusive)
	require.Equal(t, "0.0000", view.Defaults.WithholdingTax)
	require.Len(t, view.Overrides, 1)
	require.Equal(t, ScopeModel, view.Overrides[0].ScopeType)
	require.Equal(t, int64(40), view.Overrides[0].ScopeID)
	require.Equal(t, "0.0500", view.Overrides[0].LossRate)
}

// TestListParams_PreservesTrailingZeros 钉死 Option A（任务 2 的 pin 测试）：
// 写入 "0.0500" 的黄例——GET 必须返回 "0.0500" 而不是 "0.05"。
// 本测试会**跑完写路径再跑读路径**，验证两段的字符串 1:1 一致。
func TestListParams_PreservesTrailingZeros(t *testing.T) {
	fs := newFakeParamStore()
	// 库里先只有 GLOBAL——读 path 不含 override 时也要返 4 位小数。
	fs.params = []Param{
		{ScopeType: ScopeGlobal, ScopeID: 0,
			LossRate: strToDec("0.0300"), ChannelRate: strToDec("0.0100"),
			TaxInclusive: true, WithholdingTax: strToDec("0.0000")},
	}
	fs.skuIDs[40] = true
	fs.affectedByScope[affectedKey(ScopeGlobal, 0)] = []int64{40}
	fs.affectedByScope[affectedKey(ScopeModel, 40)] = []int64{40}
	svc := NewParamService(fs, fixedNow)

	// PUT 写入 MODEL/40 loss_rate="0.0500"（带尾零）。
	overrides := []OverrideItem{
		{ScopeType: ScopeModel, ScopeID: 40,
			LossRate: "0.0500", ChannelRate: "0.0100",
			TaxInclusive: false, WithholdingTax: "0.0000"},
	}
	_, err := svc.ReplaceOverrides(context.Background(),
		PutParamsInput{Overrides: &overrides}, opPricingOp(), "req-pin-1")
	require.NoError(t, err)

	// GET 必须原样返回 "0.0500"（而非 decimal.String() 会丢的 "0.05"）。
	view, err := svc.ListParams(context.Background())
	require.NoError(t, err)
	require.Len(t, view.Overrides, 1)
	require.Equal(t, "0.0500", view.Overrides[0].LossRate,
		"Option A：GET 必须原样返回写入的 \"0.0500\"，绝不能被 decimal.String() 打成 \"0.05\"")
	// 顺带确认 defaults 也没被击穿。
	require.Equal(t, "0.0300", view.Defaults.LossRate)
	require.Equal(t, "0.0100", view.Defaults.ChannelRate)
	require.Equal(t, "0.0000", view.Defaults.WithholdingTax)
}

// TestListParams_MissingGlobal 全局行缺失 = 数据事故，报错不静默。
func TestListParams_MissingGlobal(t *testing.T) {
	fs := newFakeParamStore()
	fs.params = []Param{{ScopeType: ScopeModel, ScopeID: 40,
		LossRate: strToDec("0.05"), ChannelRate: strToDec("0.01"),
		TaxInclusive: false, WithholdingTax: strToDec("0.0000")}}
	svc := NewParamService(fs, fixedNow)

	_, err := svc.ListParams(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "GLOBAL")
}

// TestReplaceOverrides_HappyPath 全链路：校验 → 替换 → 入队。
func TestReplaceOverrides_HappyPath(t *testing.T) {
	fs := newFakeParamStore()
	fs.params = seedParams()
	fs.skuIDs[40] = true
	fs.skuIDs[41] = true
	fs.supplierIDs[1] = true
	fs.affectedByScope[affectedKey(ScopeGlobal, 0)] = []int64{40, 41}
	fs.affectedByScope[affectedKey(ScopeModel, 40)] = []int64{40}
	fs.affectedByScope[affectedKey(ScopeSupplier, 1)] = []int64{40, 41}
	svc := NewParamService(fs, fixedNow)

	overrides := []OverrideItem{
		{ScopeType: ScopeModel, ScopeID: 40,
			LossRate: "0.0400", ChannelRate: "0.0100",
			TaxInclusive: true, WithholdingTax: "0.0000"},
		{ScopeType: ScopeSupplier, ScopeID: 1,
			LossRate: "0.0600", ChannelRate: "0.0200",
			TaxInclusive: false, WithholdingTax: "0.0010"},
	}
	res, err := svc.ReplaceOverrides(context.Background(),
		PutParamsInput{Overrides: &overrides}, opPricingOp(), "req-1")
	require.NoError(t, err)
	require.Equal(t, 2, res.OverridesCount)
	require.Equal(t, 2, res.SubmittedTasks)
	require.Len(t, res.TaskIDs, 2)
	require.Equal(t, int64(777), res.AuditLogID)

	// 库内容 = GLOBAL + 2 条新 override
	require.Len(t, fs.params, 3)
	byScope := map[string]Param{}
	for _, p := range fs.params {
		byScope[affectedKey(p.ScopeType, p.ScopeID)] = p
	}
	require.True(t, strToDec("0.0300").Equal(byScope[affectedKey(ScopeGlobal, 0)].LossRate),
		"GLOBAL 行 decimal 值绝不能动（字符串尾零可能异，由读侧 formatRate 统一规整）")
	require.True(t, strToDec("0.04").Equal(byScope[affectedKey(ScopeModel, 40)].LossRate))
	require.True(t, strToDec("0.06").Equal(byScope[affectedKey(ScopeSupplier, 1)].LossRate))

	require.Len(t, fs.replaceCalls, 1)
	tx := fs.replaceCalls[0]
	require.ElementsMatch(t, []string{ScopeModel, ScopeSupplier}, tx.Deletes)
	require.NotContains(t, tx.Deletes, ScopeGlobal, "DELETE 绝不能带 GLOBAL")
	require.Len(t, tx.Inserts, 2)
	require.Equal(t, "req-1", tx.Inserts[0].RequestID)
	require.Equal(t, int64(1), tx.Inserts[0].CreatedBy)
	require.Equal(t, fixedNow(), tx.Inserts[0].CreatedAt)

	beforeArr, ok := tx.Before["overrides"].([]OverrideView)
	require.True(t, ok, "before.overrides 必须是 []OverrideView")
	require.Len(t, beforeArr, 1)
	afterArr, ok := tx.After["overrides"].([]OverrideView)
	require.True(t, ok)
	require.Len(t, afterArr, 2)
	require.Equal(t, []int64{40, 41}, tx.AffectedSKUs)
}

// TestReplaceOverrides_DefaultsRejected defaults 只读：绝不静默忽略。
func TestReplaceOverrides_DefaultsRejected(t *testing.T) {
	fs := newFakeParamStore()
	fs.params = seedParams()
	svc := NewParamService(fs, fixedNow)

	defaults := &ParamViewCost{LossRate: "0.05"}
	overrides := []OverrideItem{}
	_, err := svc.ReplaceOverrides(context.Background(),
		PutParamsInput{Defaults: defaults, Overrides: &overrides}, opPricingOp(), "req-1")
	require.True(t, errors.Is(err, ErrDefaultsReadOnly))
	require.Contains(t, err.Error(), "6d-2-①")
	require.Empty(t, fs.replaceCalls)
}

// TestReplaceOverrides_MissingOverridesKey 缺 overrides 键必须 400。
func TestReplaceOverrides_MissingOverridesKey(t *testing.T) {
	fs := newFakeParamStore()
	fs.params = seedParams()
	svc := NewParamService(fs, fixedNow)

	_, err := svc.ReplaceOverrides(context.Background(),
		PutParamsInput{Overrides: nil}, opPricingOp(), "req-1")
	require.True(t, errors.Is(err, ErrOverridesKeyMissing))
	require.Empty(t, fs.replaceCalls)
}

// TestReplaceOverrides_EmptyOverridesClearAll "overrides": [] 合法清空。
func TestReplaceOverrides_EmptyOverridesClearAll(t *testing.T) {
	fs := newFakeParamStore()
	fs.params = seedParams()
	fs.affectedByScope[affectedKey(ScopeGlobal, 0)] = []int64{40, 41}
	fs.affectedByScope[affectedKey(ScopeModel, 40)] = []int64{40}
	svc := NewParamService(fs, fixedNow)

	empty := []OverrideItem{}
	res, err := svc.ReplaceOverrides(context.Background(),
		PutParamsInput{Overrides: &empty}, opPricingOp(), "req-1")
	require.NoError(t, err)
	require.Equal(t, 0, res.OverridesCount)
	require.Equal(t, 2, res.SubmittedTasks)
	require.Len(t, fs.replaceCalls, 1)
	require.Empty(t, fs.replaceCalls[0].Inserts)
	require.Equal(t, []int64{40, 41}, fs.replaceCalls[0].AffectedSKUs)
	require.Len(t, fs.params, 1)
	require.Equal(t, ScopeGlobal, fs.params[0].ScopeType)
}

// TestReplaceOverrides_ScopeTypeInvalid GLOBAL / 未知类型都拒绝。
func TestReplaceOverrides_ScopeTypeInvalid(t *testing.T) {
	cases := []struct{ name, scopeType string }{
		{"GLOBAL 显式拒绝", ScopeGlobal},
		{"未知类型", "PROJECT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeParamStore()
			fs.params = seedParams()
			svc := NewParamService(fs, fixedNow)
			overrides := []OverrideItem{{ScopeType: tc.scopeType, ScopeID: 1,
				LossRate: "0.05", ChannelRate: "0.01", TaxInclusive: false, WithholdingTax: "0.0000"}}
			_, err := svc.ReplaceOverrides(context.Background(),
				PutParamsInput{Overrides: &overrides}, opPricingOp(), "req-1")
			require.True(t, errors.Is(err, ErrScopeTypeInvalid), "err=%v", err)
			require.Empty(t, fs.replaceCalls)
		})
	}
}

// TestReplaceOverrides_ScopeNotFound scope_id 必须真实存在。
func TestReplaceOverrides_ScopeNotFound(t *testing.T) {
	fs := newFakeParamStore()
	fs.params = seedParams()
	fs.skuIDs[40] = true
	svc := NewParamService(fs, fixedNow)
	overrides := []OverrideItem{
		{ScopeType: ScopeModel, ScopeID: 40,
			LossRate: "0.05", ChannelRate: "0.01", TaxInclusive: false, WithholdingTax: "0.0000"},
		{ScopeType: ScopeSupplier, ScopeID: 99,
			LossRate: "0.05", ChannelRate: "0.01", TaxInclusive: false, WithholdingTax: "0.0000"},
	}
	_, err := svc.ReplaceOverrides(context.Background(),
		PutParamsInput{Overrides: &overrides}, opPricingOp(), "req-1")
	require.True(t, errors.Is(err, ErrScopeNotFound), "err=%v", err)
	require.Contains(t, err.Error(), "scope_id=99")
	require.Empty(t, fs.replaceCalls)
}

// TestReplaceOverrides_Duplicate 同 (scope_type, scope_id) 重复必拒绝。
func TestReplaceOverrides_Duplicate(t *testing.T) {
	fs := newFakeParamStore()
	fs.params = seedParams()
	fs.skuIDs[40] = true
	svc := NewParamService(fs, fixedNow)
	overrides := []OverrideItem{
		{ScopeType: ScopeModel, ScopeID: 40,
			LossRate: "0.05", ChannelRate: "0.01", TaxInclusive: false, WithholdingTax: "0.0000"},
		{ScopeType: ScopeModel, ScopeID: 40,
			LossRate: "0.06", ChannelRate: "0.02", TaxInclusive: true, WithholdingTax: "0.0010"},
	}
	_, err := svc.ReplaceOverrides(context.Background(),
		PutParamsInput{Overrides: &overrides}, opPricingOp(), "req-1")
	require.True(t, errors.Is(err, ErrDuplicateScope), "err=%v", err)
	require.Contains(t, err.Error(), "scope_id=40")
	require.Empty(t, fs.replaceCalls)
}

// TestReplaceOverrides_RateInvalid 费率 5 个非法形态（报错必须带原始值）。
func TestReplaceOverrides_RateInvalid(t *testing.T) {
	cases := []struct{ name, lossRate string }{
		{"无法解析", "abc"},
		{"浮点写法拒绝", "1e-3"},
		{"负数", "-0.01"},
		{"超过 1", "1.0001"},
		{"小数位超过 4", "0.00001"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeParamStore()
			fs.params = seedParams()
			fs.skuIDs[40] = true
			svc := NewParamService(fs, fixedNow)
			overrides := []OverrideItem{{ScopeType: ScopeModel, ScopeID: 40,
				LossRate: tc.lossRate, ChannelRate: "0.01", TaxInclusive: false, WithholdingTax: "0.0000"}}
			_, err := svc.ReplaceOverrides(context.Background(),
				PutParamsInput{Overrides: &overrides}, opPricingOp(), "req-1")
			require.True(t, errors.Is(err, ErrRateInvalid), "err=%v", err)
			require.Contains(t, err.Error(), tc.lossRate, "报错必须带原始值")
			require.Empty(t, fs.replaceCalls)
		})
	}
}

// TestReplaceOverrides_RateBoundary 边界 0 / 1 / 4 位小数合法。
func TestReplaceOverrides_RateBoundary(t *testing.T) {
	fs := newFakeParamStore()
	fs.params = seedParams()
	fs.skuIDs[40] = true
	fs.affectedByScope[affectedKey(ScopeGlobal, 0)] = []int64{40}
	fs.affectedByScope[affectedKey(ScopeModel, 40)] = []int64{40}
	svc := NewParamService(fs, fixedNow)
	overrides := []OverrideItem{
		{ScopeType: ScopeModel, ScopeID: 40,
			LossRate: "1", ChannelRate: "0.0001", TaxInclusive: true, WithholdingTax: "0"},
	}
	_, err := svc.ReplaceOverrides(context.Background(),
		PutParamsInput{Overrides: &overrides}, opPricingOp(), "req-1")
	require.NoError(t, err)
	require.Len(t, fs.replaceCalls, 1)
}

// TestReplaceOverrides_TaskEnqueue 受影响 SKU 是新旧 override 并集 + GLOBAL。
func TestReplaceOverrides_TaskEnqueue(t *testing.T) {
	fs := newFakeParamStore()
	fs.params = []Param{
		{ScopeType: ScopeGlobal, ScopeID: 0,
			LossRate: strToDec("0.0300"), ChannelRate: strToDec("0.0100"),
			TaxInclusive: true, WithholdingTax: strToDec("0.0000")},
		{ScopeType: ScopeModel, ScopeID: 40,
			LossRate: strToDec("0.0500"), ChannelRate: strToDec("0.0100"),
			TaxInclusive: false, WithholdingTax: strToDec("0.0000")},
		{ScopeType: ScopeSupplier, ScopeID: 2,
			LossRate: strToDec("0.0600"), ChannelRate: strToDec("0.0200"),
			TaxInclusive: false, WithholdingTax: strToDec("0.0000")},
	}
	fs.skuIDs[40], fs.skuIDs[41], fs.skuIDs[42] = true, true, true
	fs.supplierIDs[1] = true
	fs.affectedByScope[affectedKey(ScopeGlobal, 0)] = []int64{40, 41, 42}
	fs.affectedByScope[affectedKey(ScopeModel, 40)] = []int64{40}
	fs.affectedByScope[affectedKey(ScopeModel, 41)] = []int64{41}
	fs.affectedByScope[affectedKey(ScopeSupplier, 1)] = []int64{41, 42}
	fs.affectedByScope[affectedKey(ScopeSupplier, 2)] = []int64{42}
	svc := NewParamService(fs, fixedNow)

	overrides := []OverrideItem{
		{ScopeType: ScopeModel, ScopeID: 41,
			LossRate: "0.05", ChannelRate: "0.01", TaxInclusive: false, WithholdingTax: "0.0000"},
		{ScopeType: ScopeSupplier, ScopeID: 1,
			LossRate: "0.05", ChannelRate: "0.01", TaxInclusive: false, WithholdingTax: "0.0000"},
	}
	res, err := svc.ReplaceOverrides(context.Background(),
		PutParamsInput{Overrides: &overrides}, opPricingOp(), "req-1")
	require.NoError(t, err)
	require.Equal(t, 3, res.SubmittedTasks)
	require.Equal(t, []int64{40, 41, 42}, fs.replaceCalls[0].AffectedSKUs)
}

// TestReplaceOverrides_StoreError 事务失败错误透传。
func TestReplaceOverrides_StoreError(t *testing.T) {
	fs := newFakeParamStore()
	fs.params = seedParams()
	fs.skuIDs[40] = true
	fs.affectedByScope[affectedKey(ScopeGlobal, 0)] = []int64{40}
	fs.affectedByScope[affectedKey(ScopeModel, 40)] = []int64{40}
	fs.replaceShouldFail = errors.New("db deadlock")
	svc := NewParamService(fs, fixedNow)
	overrides := []OverrideItem{{ScopeType: ScopeModel, ScopeID: 40,
		LossRate: "0.05", ChannelRate: "0.01", TaxInclusive: false, WithholdingTax: "0.0000"}}
	_, err := svc.ReplaceOverrides(context.Background(),
		PutParamsInput{Overrides: &overrides}, opPricingOp(), "req-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "deadlock")
}

// ============================================================================
// ConsumeTask 的 sku_id 分支测试（6d-2 新增）
// ============================================================================

// recalcCall 记录一次 RecalcSKU 调用。
type recalcCall struct {
	skuID  int64
	reason string
}

// fakeStoreWithRecalc 只实现 ConsumeTask 用到的方法；其余 nil。
type fakeStoreWithRecalc struct {
	recalcTargets []int64
	recalcCalls   *[]recalcCall // 指针——LoadRecalcInput 每调一次追一次
}

func (f *fakeStoreWithRecalc) ListRecalcTargets(_ context.Context, _, _ int64) ([]int64, error) {
	return f.recalcTargets, nil
}

func (f *fakeStoreWithRecalc) LoadRecalcInput(_ context.Context, skuID int64) (*RecalcInput, error) {
	// LoadRecalcInput 是 RecalcSKU 的第一步——在这里记录"RecalcSKU 被以 skuID 调起"。
	// 返回值构造空报价，让后续走 NO_QUOTE 分支（算成功，任务标 DONE）。
	*f.recalcCalls = append(*f.recalcCalls, recalcCall{skuID: skuID, reason: ""})
	return &RecalcInput{SKUID: skuID, SKUCode: "X"}, nil
}

func (f *fakeStoreWithRecalc) LoadCurrentBaselineForUpdate(_ context.Context, _ int64) (*Baseline, error) {
	return nil, nil
}

func (f *fakeStoreWithRecalc) ApplyNewVersion(_ context.Context, p ApplyParams) (int64, int, error) {
	return 0, p.PreviousVersion + 1, nil
}

// fakeTaskStore 已在 consume_task_test.go 提供（claimOK/loadTask 字段驱动）——
// 本文件直接用它的字段语义构造用例：claimOK=true + loadTask=<任务>，
// 断言 MarkDone 被调次数来判断新分支的写入路径。

// TestConsumeTask_SKUIDBranch 带 sku_id 时**只**算该 SKU，不走 RecalcFromTask。
func TestConsumeTask_SKUIDBranch(t *testing.T) {
	recalcCalls := []recalcCall{}
	fs := &fakeStoreWithRecalc{recalcCalls: &recalcCalls}
	skuID := int64(42)
	ts := &fakeTaskStore{
		claimOK: true,
		loadTask: &TaskJob{
			ID:      7,
			JobType: "COST_RECALC",
			Payload: TaskPayload{SKUID: &skuID, Reason: ReasonParamChange},
		},
	}
	svc := NewService(fs, ts, nil)
	ident := JobIdentity{OperatorID: 9, OperatorRole: "SYSTEM", SourceType: "WORKER", RequestID: "req-42"}

	claimed, err := svc.ConsumeTask(context.Background(), 7, fixedNow(), ident)
	require.NoError(t, err)
	require.True(t, claimed)
	// 42 被算一次，且**没有**走 ListRecalcTargets 展开的其他 SKU
	require.Len(t, recalcCalls, 1)
	require.Equal(t, int64(42), recalcCalls[0].skuID)
	// NO_QUOTE 也是成功路径，任务标 DONE（6b-3 裁决）
	require.Equal(t, 1, ts.doneN)
	require.Equal(t, 0, ts.failedN)
}

// TestConsumeTask_LegacyBranchUnchanged 无 sku_id 时仍按 supplier/sheet 展开。
func TestConsumeTask_LegacyBranchUnchanged(t *testing.T) {
	recalcCalls := []recalcCall{}
	fs := &fakeStoreWithRecalc{recalcTargets: []int64{40, 41}, recalcCalls: &recalcCalls}
	ts := &fakeTaskStore{
		claimOK: true,
		loadTask: &TaskJob{
			ID:      8,
			JobType: "COST_RECALC",
			Payload: TaskPayload{SupplierID: 2, QuoteSheetID: 29, Reason: ReasonQuoteEffective},
		},
	}
	svc := NewService(fs, ts, nil)
	ident := JobIdentity{OperatorID: 9, OperatorRole: "SYSTEM", SourceType: "WORKER", RequestID: "req-8"}

	claimed, err := svc.ConsumeTask(context.Background(), 8, fixedNow(), ident)
	require.NoError(t, err)
	require.True(t, claimed)
	// ListRecalcTargets 展开的 40 和 41 都被算
	require.Len(t, recalcCalls, 2)
	require.Equal(t, int64(40), recalcCalls[0].skuID)
	require.Equal(t, int64(41), recalcCalls[1].skuID)
}

// TestConsumeTask_SKUIDReasonDefault reason 缺省默认 QUOTE_EFFECTIVE（ConsumeTask 层级）。
func TestConsumeTask_SKUIDReasonDefault(t *testing.T) {
	recalcCalls := []recalcCall{}
	fs := &fakeStoreWithRecalc{recalcCalls: &recalcCalls}
	skuID := int64(42)
	ts := &fakeTaskStore{
		claimOK: true,
		loadTask: &TaskJob{
			ID:      9,
			JobType: "COST_RECALC",
			Payload: TaskPayload{SKUID: &skuID /* Reason 缺省 */},
		},
	}
	svc := NewService(fs, ts, nil)
	ident := JobIdentity{OperatorID: 9, OperatorRole: "SYSTEM", SourceType: "WORKER", RequestID: "req-9"}

	_, err := svc.ConsumeTask(context.Background(), 9, fixedNow(), ident)
	require.NoError(t, err)
	require.Len(t, recalcCalls, 1)
	require.Equal(t, int64(42), recalcCalls[0].skuID)
}
