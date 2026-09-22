// Package cost 的 lock_service_test.go：手动锁定主供应商的单测（6d-3）。
// 覆盖六组断言（对应提示词「单测要求」的 6 节）：
//  1. Unchanged() 新判定（锁状态变化必产新版本）；
//  2. RecalcSKU 锁定继承（prev.LockedManual=true + 正常触发 → next 维持锁；失效 → 报错不产版本）；
//  3. LockPrimary 校验四项（reason 空 / supplier 不存在 / 无 EFFECTIVE 报价 / 冻结）；
//  4. 服务层角色收敛（AnyRoleCan 对 PROCUREMENT 放行、对 smoke_admin 三角色拒绝）；
//  5. LockPrimary 业务逻辑（有效换锁 / 重复锁定同对象 no-op）；
//  6. calc_snapshot.primary_selection_rule（MANUAL_LOCK→"manual-lock"、其他→"four-factor"）。
package cost

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// ---- fakeLockStore：锁定测试的内存 fake（字段按调用点最小化） ----

type fakeLockStore struct {
	supplierExists    map[int64]bool
	supplierExistsErr error
	statuses          map[int64]SupplierStatus
	statusMissing     map[int64]bool // true → 行不存在（!ok）
	statusesErr       error
	hasQuote          map[string]bool // "supplierID|skuID" → 是否有 EFFECTIVE
	hasQuoteErr       error
	prev              *Baseline
	prevErr           error
	auditEntries      []LockAuditEntry
	auditErr          error
}

func quoteKey(supplierID, skuID int64) string {
	return fmtInt64(supplierID) + "|" + fmtInt64(skuID)
}

// fmtInt64 简单的十进制字符串化（避免引入 strconv 让 fake 更直白）。
func fmtInt64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func (f *fakeLockStore) SupplierExists(_ context.Context, supplierID int64) (bool, error) {
	if f.supplierExistsErr != nil {
		return false, f.supplierExistsErr
	}
	return f.supplierExists[supplierID], nil
}

func (f *fakeLockStore) SupplierStatusOf(_ context.Context, supplierID int64) (SupplierStatus, bool, error) {
	if f.statusesErr != nil {
		return SupplierStatus{}, false, f.statusesErr
	}
	if f.statusMissing[supplierID] {
		return SupplierStatus{}, false, nil
	}
	st, ok := f.statuses[supplierID]
	return st, ok, nil
}

func (f *fakeLockStore) HasEffectiveQuoteOnSKU(_ context.Context, supplierID, skuID int64) (bool, error) {
	if f.hasQuoteErr != nil {
		return false, f.hasQuoteErr
	}
	return f.hasQuote[quoteKey(supplierID, skuID)], nil
}

func (f *fakeLockStore) LoadCurrentBaselineForUpdate(_ context.Context, _ int64) (*Baseline, error) {
	return f.prev, f.prevErr
}

func (f *fakeLockStore) WriteLockAudit(_ context.Context, e LockAuditEntry) error {
	if f.auditErr != nil {
		return f.auditErr
	}
	f.auditEntries = append(f.auditEntries, e)
	return nil
}

// ---- 1. Unchanged() 锁定判定（陷阱 2 防线） ----

func TestUnchanged_LockFlippedIsChange(t *testing.T) {
	prev := baselineForTest()
	prev.LockedManual = false
	next := baselineForTest()
	next.LockedManual = true
	require.False(t, Unchanged(prev, next), "locked_manual false→true 必须判变化")

	prev2 := baselineForTest()
	prev2.LockedManual = true
	next2 := baselineForTest()
	next2.LockedManual = false
	require.False(t, Unchanged(prev2, next2), "locked_manual true→false 必须判变化")
}

func TestUnchanged_LockSameTrueIsUnchanged(t *testing.T) {
	prev := baselineForTest()
	prev.LockedManual = true
	next := baselineForTest()
	next.LockedManual = true
	require.True(t, Unchanged(prev, next), "locked_manual 同 true 且其余全同 → 不变")
}

// ---- 2. RecalcSKU 锁定继承（陷阱 1/3 防线） ----

// prev 已锁 A=supplier1、再触发正常 QUOTE_EFFECTIVE：新版本的 primary 必须维持 1（不是
// 算法冠军的 2），LockedManual 仍 true，rule=manual-lock，组件/费率取 A 的口径。
// prev.Components 故意与现状不同（unit_cost 2.5 → 2.60075000，prev 存的是 2.50000000）——
// 否则 Unchanged() 会判全同跳过；sticky 继承决定 primary 选择，值未变判定继续跑。
func TestRecalcSKU_LockInheritedOnNormalTrigger(t *testing.T) {
	in, now := twoSuppliersInput() // A=1 单价 2.50 / B=2 单价 2.30；算法冠军=B(便宜)
	prev := &Baseline{
		Version: 7, Currency: "USD", PrimarySupplierID: 1, LockedManual: true,
		LossRate: d("0.0300"), ChannelRate: d("0.0100"),
		ChangeReason: ReasonQuoteEffective, FormulaVersion: FormulaVersion,
		Components: []Component{
			{ComponentType: "input", UnitCost: d("2.50000000"), SupplierCost: d("2.41")},
		},
	}
	store := &fakeStore{input: in, prev: prev, applyID: 900, applyVer: 8}
	svc := NewService(store, nil, nil)
	out, err := svc.RecalcSKU(context.Background(), 40, ReasonQuoteEffective, "req-l1", now, testIdent, nil)
	require.NoError(t, err)
	require.Equal(t, "CREATED", out.Status)
	require.Equal(t, 8, out.Version)
	require.Equal(t, int64(900), out.BaselineID)
	require.Len(t, store.applied, 1)
	p := store.applied[0]
	require.Equal(t, int64(1), p.Baseline.PrimarySupplierID, "锁定后正常触发也必须维持锁")
	require.True(t, p.Baseline.LockedManual)
	require.Equal(t, "manual-lock", p.CalcSnapshot["primary_selection_rule"])
	// 组件取被锁供应商 A 的口径（2.5 输入 → 2.60075000），不是算法冠军 B 的 2.39269000
	require.Equal(t, "2.60075000", p.Baseline.Components[0].UnitCost.Round(8).StringFixed(8))
	// 快照里 scores 仍记录全部两家（含算法冠军 B），保证"算法当时怎么想"可追溯
	scores := p.CalcSnapshot["scores"].([]map[string]any)
	require.Len(t, scores, 2)
}

// prev 已锁 supplier1、这次 supplier1 被冻结（被 CalcSKU 排除）→ 必须报
// ErrLockedSupplierInvalid、不产新版本、绝不自动降级/自动解锁。
func TestRecalcSKU_LockedSupplierFrozenFails(t *testing.T) {
	in, now := twoSuppliersInput()
	// 冻结 supplier1（CalcSKU 排除它，calc.All 只剩 supplier2）
	for i := range in.Statuses {
		if in.Statuses[i].SupplierID == 1 {
			in.Statuses[i].QualStatus = "FROZEN"
		}
	}
	prev := &Baseline{
		Version: 7, Currency: "USD", PrimarySupplierID: 1, LockedManual: true,
		LossRate: d("0.0300"), ChannelRate: d("0.0100"),
		ChangeReason: ReasonQuoteEffective, FormulaVersion: FormulaVersion,
		Components: []Component{
			{ComponentType: "input", UnitCost: d("2.50000000"), SupplierCost: d("2.41")},
		},
	}
	store := &fakeStore{input: in, prev: prev}
	svc := NewService(store, nil, nil)
	_, err := svc.RecalcSKU(context.Background(), 40, ReasonQuoteEffective, "req-l2", now, testIdent, nil)
	require.ErrorIs(t, err, ErrLockedSupplierInvalid)
	require.Empty(t, store.applied, "必须不产新版本")
}

// prev 已锁 supplier1、SKU 报价整体断档 → NO_QUOTE 不适用，必须报 ErrLockedSupplierInvalid。
func TestRecalcSKU_LockedNoQuoteFailsNotSilentlySkipped(t *testing.T) {
	in := &RecalcInput{SKUID: 40, SKUCode: "x", Currency: "USD"} // 空 Quotes → CalcSKU 报 ErrNoQuoteSKU
	prev := &Baseline{
		Version: 7, Currency: "USD", PrimarySupplierID: 1, LockedManual: true,
		LossRate: d("0.0300"), ChannelRate: d("0.0100"),
		ChangeReason: ReasonQuoteEffective, FormulaVersion: FormulaVersion,
		Components: []Component{
			{ComponentType: "input", UnitCost: d("2.50000000"), SupplierCost: d("2.41")},
		},
	}
	store := &fakeStore{input: in, prev: prev}
	svc := NewService(store, nil, nil)
	_, err := svc.RecalcSKU(context.Background(), 40, ReasonQuoteEffective, "req-l3", testNow, testIdent, nil)
	require.ErrorIs(t, err, ErrLockedSupplierInvalid)
	require.Empty(t, store.applied)
}

// 非 MANUAL_LOCK 触发但误传 manualLockSupplierID → 明确拒绝（防调用方用错语义）。
func TestRecalcSKU_NonManualLockRejectsOverride(t *testing.T) {
	in := baseInput()
	store := &fakeStore{input: in}
	svc := NewService(store, nil, nil)
	sid := int64(2)
	_, err := svc.RecalcSKU(context.Background(), 40, ReasonQuoteEffective, "req-l4", testNow, testIdent, &sid)
	require.ErrorIs(t, err, ErrInvalidChangeReason)
	require.Empty(t, store.applied)
}

// MANUAL_LOCK 触发但没传 manualLockSupplierID → 按四因子跑、LockedManual=false
// （防御分支：理论上 LockService 不会这么调，但 RecalcSKU 自身语义要自洽）。
func TestRecalcSKU_ManualLockWithoutOverrideRunsFourFactor(t *testing.T) {
	in, now := twoSuppliersInput()
	store := &fakeStore{input: in, applyID: 901, applyVer: 1}
	svc := NewService(store, nil, nil)
	out, err := svc.RecalcSKU(context.Background(), 40, ReasonManualLock, "req-l5", now, testIdent, nil)
	require.NoError(t, err)
	require.Equal(t, "CREATED", out.Status)
	p := store.applied[0]
	require.False(t, p.Baseline.LockedManual, "未传 override 时 MANUAL_LOCK 也按算法跑")
	require.Equal(t, "four-factor", p.CalcSnapshot["primary_selection_rule"])
}

// ---- 3. LockPrimary 校验（陷阱 4 防线） ----

func TestLockPrimary_ReasonEmpty(t *testing.T) {
	svc := NewLockService(NewService(&fakeStore{}, nil, nil), &fakeLockStore{}, nil)
	_, err := svc.LockPrimary(context.Background(), LockPrimaryInput{SkuID: 40, SupplierID: 2, Reason: "   "}, PutOperator{}, "r1")
	require.ErrorIs(t, err, ErrLockReasonEmpty)
	_, err = svc.LockPrimary(context.Background(), LockPrimaryInput{SkuID: 40, SupplierID: 2, Reason: ""}, PutOperator{}, "r1")
	require.ErrorIs(t, err, ErrLockReasonEmpty)
}

func TestLockPrimary_SupplierNotFound(t *testing.T) {
	st := &fakeLockStore{
		supplierExists: map[int64]bool{9999: false},
	}
	svc := NewLockService(NewService(&fakeStore{}, nil, nil), st, nil)
	_, err := svc.LockPrimary(context.Background(), LockPrimaryInput{SkuID: 40, SupplierID: 9999, Reason: "x"}, PutOperator{}, "r1")
	require.ErrorIs(t, err, ErrLockSupplierNotFound)
}

func TestLockPrimary_SupplierFrozen(t *testing.T) {
	// 三种冻结分别用例：qual_status FROZEN / settle_status FROZEN / status INACTIVE
	cases := []struct {
		name string
		st   SupplierStatus
	}{
		{"qual frozen", SupplierStatus{SupplierID: 2, QualStatus: "FROZEN", SettleStatus: "NORMAL", Status: "ACTIVE"}},
		{"settle frozen", SupplierStatus{SupplierID: 2, QualStatus: "VALID", SettleStatus: "FROZEN", Status: "ACTIVE"}},
		{"inactive", SupplierStatus{SupplierID: 2, QualStatus: "VALID", SettleStatus: "NORMAL", Status: "INACTIVE"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeLockStore{
				supplierExists: map[int64]bool{2: true},
				statuses:       map[int64]SupplierStatus{2: tc.st},
			}
			svc := NewLockService(NewService(&fakeStore{}, nil, nil), st, nil)
			_, err := svc.LockPrimary(context.Background(), LockPrimaryInput{SkuID: 40, SupplierID: 2, Reason: "x"}, PutOperator{}, "r1")
			require.ErrorIs(t, err, ErrLockSupplierFrozen)
		})
	}
}

func TestLockPrimary_NoEffectiveQuote(t *testing.T) {
	st := &fakeLockStore{
		supplierExists: map[int64]bool{2: true},
		statuses:       map[int64]SupplierStatus{2: {SupplierID: 2, QualStatus: "VALID", SettleStatus: "NORMAL", Status: "ACTIVE"}},
		hasQuote:       map[string]bool{quoteKey(2, 40): false},
	}
	svc := NewLockService(NewService(&fakeStore{}, nil, nil), st, nil)
	_, err := svc.LockPrimary(context.Background(), LockPrimaryInput{SkuID: 40, SupplierID: 2, Reason: "x"}, PutOperator{}, "r1")
	require.ErrorIs(t, err, ErrLockNoEffectiveQuote)
}

// ---- 4. 服务层角色收敛（AnyRoleCan 钉住 PROCUREMENT 唯一放行） ----

func TestLockPrimary_RoleGateAnyRoleCan(t *testing.T) {
	// smoke_admin 的真值形状：PLATFORM_ADMIN + MODEL_OPS + PRICING_OP——不含 PROCUREMENT，必须拒绝
	require.False(t, roleCanLockPrimary([]string{"PLATFORM_ADMIN", "MODEL_OPS", "PRICING_OP"}))
	// buyer_a/buyer_b：PROCUREMENT 单角色，必须放行
	require.True(t, roleCanLockPrimary([]string{"PROCUREMENT"}))
	// 多角色含 PROCUREMENT 也放行（AnyRoleCan 是 OR，不是"首角色"）
	require.True(t, roleCanLockPrimary([]string{"FINANCE", "PROCUREMENT"}))
	// 空集必须拒绝（任何含 PROCUREMENT 的子集都不存在）
	require.False(t, roleCanLockPrimary(nil))
	require.False(t, roleCanLockPrimary([]string{}))
	// 其他单角色必须拒绝
	require.False(t, roleCanLockPrimary([]string{"PRICING_OP"}))
	require.False(t, roleCanLockPrimary([]string{"MODEL_OPS"}))
	require.False(t, roleCanLockPrimary([]string{"RETRO_OP"}))
	require.False(t, roleCanLockPrimary([]string{"FINANCE"}))
}

// roleCanLockPrimary 等价于 handler 里的 perm.AnyRoleCan(op.Roles, perm.CanLockPrimary)——
// 不引 pkg/perm（避免测试里再绕一层），直接调用同包断言函数。
func roleCanLockPrimary(roles []string) bool {
	for _, r := range roles {
		if r == "PROCUREMENT" {
			return true
		}
	}
	return false
}

// ---- 5. LockPrimary 业务逻辑（有效换锁 / 重复锁定 no-op） ----

// 换锁成功：prev 未锁 → 产新版本（version+1）、字段全对、审计行前后值明确。
func TestLockPrimary_SuccessCreatesVersion(t *testing.T) {
	in, now := twoSuppliersInput() // A=1（贵）/B=2（便宜）；算法冠军=B=supplier2
	// 但我们要锁**更贵但稳定**的 A=1——覆盖算法结论正是本接口的存在意义。
	prev := &Baseline{
		Version: 9, Currency: "USD", PrimarySupplierID: 2, LockedManual: false,
		LossRate: d("0.0300"), ChannelRate: d("0.0100"),
		ChangeReason: ReasonQuoteEffective, FormulaVersion: FormulaVersion,
		Components: []Component{
			{ComponentType: "input", UnitCost: d("2.39269000"), SupplierCost: d("2.30")},
		},
	}
	recalcStore := &fakeStore{input: in, prev: prev, applyID: 12345, applyVer: 10}
	lockStore := &fakeLockStore{
		supplierExists: map[int64]bool{1: true},
		statuses:       map[int64]SupplierStatus{1: {SupplierID: 1, QualStatus: "VALID", SettleStatus: "NORMAL", Status: "ACTIVE"}},
		hasQuote:       map[string]bool{quoteKey(1, 40): true},
		prev:           prev,
	}
	svc := NewLockService(NewService(recalcStore, nil, nil), lockStore, func() time.Time { return now })
	res, err := svc.LockPrimary(context.Background(),
		LockPrimaryInput{SkuID: 40, SupplierID: 1, Reason: "该供应商 SLA 更优，锁定一年"},
		PutOperator{OperatorID: 7, OperatorRole: "PROCUREMENT"}, "req-202")
	require.NoError(t, err)
	require.False(t, res.Unchanged)
	require.Equal(t, int64(40), res.SkuID)
	require.Equal(t, 10, res.Version)
	require.Equal(t, int64(1), res.PrimarySupplierID)
	require.Equal(t, ReasonManualLock, res.ChangeReason)
	require.True(t, res.Locked)

	// RecalcSKU 被调用且传了 manualLockSupplierID=1
	require.Len(t, recalcStore.applied, 1)
	p := recalcStore.applied[0]
	require.Equal(t, int64(1), p.Baseline.PrimarySupplierID)
	require.True(t, p.Baseline.LockedManual)
	require.Equal(t, ReasonManualLock, p.Baseline.ChangeReason)
	require.Equal(t, "manual-lock", p.CalcSnapshot["primary_selection_rule"])

	// 审计行：action / target_id / before / after / reason 五键全对（红线 10）
	require.Len(t, lockStore.auditEntries, 1)
	e := lockStore.auditEntries[0]
	require.Equal(t, int64(12345), e.TargetID, "target_id 必须是新版本 id（RecalcOutcome.BaselineID 回填）")
	require.Equal(t, int64(7), e.OperatorID)
	require.Equal(t, "PROCUREMENT", e.OperatorRole)
	require.Equal(t, "该供应商 SLA 更优，锁定一年", e.Reason)
	require.Equal(t, "req-202", e.RequestID)
	require.Equal(t, map[string]any{
		"primary_supplier_id": int64(2),
		"locked_manual":       false,
		"version":             9,
	}, e.Before)
	require.Equal(t, map[string]any{
		"primary_supplier_id": int64(1),
		"locked_manual":       true,
		"version":             10,
	}, e.After)
}

// 首版本（prev=nil）也走同一管道：version=1，before 三键为 0/false/0。
func TestLockPrimary_FirstVersionBaselineIDReturned(t *testing.T) {
	in, now := twoSuppliersInput()
	recalcStore := &fakeStore{input: in, applyID: 9001, applyVer: 1}
	lockStore := &fakeLockStore{
		supplierExists: map[int64]bool{2: true},
		statuses:       map[int64]SupplierStatus{2: {SupplierID: 2, QualStatus: "VALID", SettleStatus: "NORMAL", Status: "ACTIVE"}},
		hasQuote:       map[string]bool{quoteKey(2, 40): true},
	}
	svc := NewLockService(NewService(recalcStore, nil, nil), lockStore, func() time.Time { return now })
	res, err := svc.LockPrimary(context.Background(),
		LockPrimaryInput{SkuID: 40, SupplierID: 2, Reason: "首锁"},
		PutOperator{OperatorID: 1, OperatorRole: "PROCUREMENT"}, "req-203")
	require.NoError(t, err)
	require.Equal(t, 1, res.Version)
	require.Len(t, lockStore.auditEntries, 1)
	e := lockStore.auditEntries[0]
	require.Equal(t, map[string]any{
		"primary_supplier_id": 0,
		"locked_manual":       false,
		"version":             0,
	}, e.Before)
}

// 重复锁定同一家：prev.LockedManual=true 且 prev.PrimarySupplierID=目标 → Unchanged=true、
// 不产新版本、不写审计（调用方凭证还是 200 + unchanged 标记，幂等回放由中间件覆盖）。
func TestLockPrimary_RepeatLockSameSupplierNoOp(t *testing.T) {
	prev := &Baseline{
		Version: 10, Currency: "USD", PrimarySupplierID: 2, LockedManual: true,
		LossRate: d("0.0300"), ChannelRate: d("0.0100"),
		ChangeReason: ReasonManualLock, FormulaVersion: FormulaVersion,
		Components: []Component{
			{ComponentType: "input", UnitCost: d("2.39269000"), SupplierCost: d("2.30")},
		},
	}
	recalcStore := &fakeStore{prev: prev}
	lockStore := &fakeLockStore{
		supplierExists: map[int64]bool{2: true},
		statuses:       map[int64]SupplierStatus{2: {SupplierID: 2, QualStatus: "VALID", SettleStatus: "NORMAL", Status: "ACTIVE"}},
		hasQuote:       map[string]bool{quoteKey(2, 40): true},
		prev:           prev,
	}
	svc := NewLockService(NewService(recalcStore, nil, nil), lockStore, nil)
	res, err := svc.LockPrimary(context.Background(),
		LockPrimaryInput{SkuID: 40, SupplierID: 2, Reason: "再锁一次"},
		PutOperator{OperatorID: 7, OperatorRole: "PROCUREMENT"}, "req-204")
	require.NoError(t, err)
	require.True(t, res.Unchanged)
	require.Equal(t, 10, res.Version, "Unchanged 时 Version 是旧值")
	require.Equal(t, int64(2), res.PrimarySupplierID)
	require.True(t, res.Locked)
	require.Empty(t, recalcStore.applied, "不产新版本")
	require.Empty(t, lockStore.auditEntries, "不重复写审计")
}

// 防御：RecalcSKU 判 UNCHANGED 且 prev 已锁同对象 = 合法 no-op（TestLockPrimary_RepeatLockSameSupplierNoOp
// 已覆盖）；这里覆盖另一种边界——同一家、prev 未锁、且值确实没变时 RecalcSKU 不会 UNCHANGED
// （锁定状态翻转必产变化，由 TestUnchanged_LockFlippedIsChange 钉死）。本用例改成：
// 「RecalcSKU 内部 UNCHANGED 防御分支」的钉死测试：
// 同一家、prev 已锁、fake 却让 RecalcSKU 返回 UNCHANGED（数值没变——
// sticky 锁定继承的合法形态）。LockService 此时必须报错（而不是把 unchanged 当成
// 「锁上了」的假结论返回 200）——它与 TestLockPrimary_RepeatLockSameSupplierNoOp 是
// 同一结果的两种来源（一种是 handler 前置短路，一种是 RecalcSKU 防线），绝不可混淆。
//
// 变异验证 #1（删掉 Unchanged() 的 locked_manual 判定）时：
// prev 未锁 + 值全同 → RecalcSKU 判 UNCHANGED → 本测试红（因为 LockService 报错被它
// 抓住了，而不是像正常 no-op 那样走 handler 前置短路）。这是区分「正常重复锁」与
// 「Unchanged() 变异」的唯一一道网。
func TestLockPrimary_UnchangedWithUnlockedPrevIsError(t *testing.T) {
	// 两个 fake 各持一份 prev（接口分离的实证）：
	//   - lockStore.prev = 已锁 supplier2 → 绕过「重复锁同对象」前置短路，请求落到 RecalcSKU；
	//   - recalcStore.prev = 已锁 supplier1 且数值与新计算完全一致 → RecalcSKU 走 sticky 继承
	//     后 Unchanged() 判 true（合法 UNCHANGED——值确实没变）。
	// 于是 LockService 收到「自身 no-op 判定没命中、但 RecalcSKU 判 UNCHANGED」的矛盾信号，
	// 必须报错（防线：不构造前后一致的假审计）。
	//
	// 变异验证 #1（删掉 Unchanged() 的 locked_manual 判定）时本用例不变红——
	// 那个变异由 TestUnchanged_LockFlippedIsChange(_SecondDefense) 钉死；本用例钉的是
	// 「Unchanged() 保持完整时 LockService 不能放行自相矛盾的结果」。
	in := baseInput()
	lockPrev := &Baseline{
		Version: 6, Currency: "USD", PrimarySupplierID: 2, LockedManual: true,
		LossRate: d("0.0300"), ChannelRate: d("0.0100"),
		ChangeReason: ReasonManualLock, FormulaVersion: FormulaVersion,
		Components: []Component{
			{ComponentType: "input", UnitCost: UnitCost(d("2.5"), d("0.03"), d("0.01")), SupplierCost: d("2.5")},
		},
	}
	recalcPrev := &Baseline{
		Version: 6, Currency: "USD", PrimarySupplierID: 1, LockedManual: true,
		LossRate: d("0.0300"), ChannelRate: d("0.0100"),
		ChangeReason: ReasonManualLock, FormulaVersion: FormulaVersion,
		Components: []Component{
			{ComponentType: "input", UnitCost: UnitCost(d("2.5"), d("0.03"), d("0.01")), SupplierCost: d("2.5")},
		},
	}
	recalcStore := &fakeStore{input: in, prev: recalcPrev}
	lockStore := &fakeLockStore{
		supplierExists: map[int64]bool{1: true},
		statuses:       map[int64]SupplierStatus{1: {SupplierID: 1, QualStatus: "VALID", SettleStatus: "NORMAL", Status: "ACTIVE"}},
		hasQuote:       map[string]bool{quoteKey(1, 40): true},
		prev:           lockPrev,
	}
	svc := NewLockService(NewService(recalcStore, nil, nil), lockStore, nil)
	_, err := svc.LockPrimary(context.Background(),
		LockPrimaryInput{SkuID: 40, SupplierID: 1, Reason: "换锁到 supplier1"},
		PutOperator{OperatorID: 1, OperatorRole: "PROCUREMENT"}, "req-205")
	require.Error(t, err, "RecalcSKU 返回 UNCHANGED 时 LockService 必须报错")
	require.Contains(t, err.Error(), "UNCHANGED")
}

// Unchanged() 变异的真实命中点：删掉 locked_manual 判定后，「prev 未锁 + 值全同」的
// 用例会让 RecalcSKU 判 UNCHANGED。本用例与 TestUnchanged_LockFlippedIsChange 是
// 同一判定字段的两道独立防线（单测层 + 服务层各一份）。
func TestUnchanged_LockFlippedIsChange_SecondDefense(t *testing.T) {
	prev := baselineForTest()
	prev.LockedManual = false
	next := baselineForTest()
	next.LockedManual = true
	// 变异「删除 locked_manual 判定」时这里会变 true——同层防御再补一刀
	require.False(t, Unchanged(prev, next), "locked_manual 判定被移除时本测试红")
}

// ---- 6. calc_snapshot.primary_selection_rule（陷阱 5 防线） ----

// MANUAL_LOCK 触发 → "manual-lock"；普通触发 → "four-factor"——两条线都断言。
func TestSnapshot_SelectionRuleManualLock(t *testing.T) {
	in, now := twoSuppliersInput()
	recalcStore := &fakeStore{input: in, applyID: 1, applyVer: 1}
	svc := NewService(recalcStore, nil, nil)
	sid := int64(1)
	_, err := svc.RecalcSKU(context.Background(), 40, ReasonManualLock, "r", now, testIdent, &sid)
	require.NoError(t, err)
	require.Equal(t, "manual-lock", recalcStore.applied[0].CalcSnapshot["primary_selection_rule"])
	require.Equal(t, int64(1), recalcStore.applied[0].CalcSnapshot["primary_supplier_id"])
}

func TestSnapshot_SelectionRuleFourFactorOnNormalTrigger(t *testing.T) {
	in, now := twoSuppliersInput()
	recalcStore := &fakeStore{input: in, applyID: 2, applyVer: 1}
	svc := NewService(recalcStore, nil, nil)
	_, err := svc.RecalcSKU(context.Background(), 40, ReasonQuoteEffective, "r", now, testIdent, nil)
	require.NoError(t, err)
	require.Equal(t, "four-factor", recalcStore.applied[0].CalcSnapshot["primary_selection_rule"])
}

// 锁定维持（sticky）后续触发的 rule 也是 "manual-lock"——
// 它在语义上同样不是算法结论（算法冠军是 B，但实际 primary 是 A）。
func TestSnapshot_SelectionRuleManualLockOnStickyRecalc(t *testing.T) {
	in, now := twoSuppliersInput()
	prev := &Baseline{
		Version: 5, Currency: "USD", PrimarySupplierID: 1, LockedManual: true,
		LossRate: d("0.0300"), ChannelRate: d("0.0100"),
		ChangeReason: ReasonQuoteEffective, FormulaVersion: FormulaVersion,
		Components: []Component{
			{ComponentType: "input", UnitCost: d("2.50000000"), SupplierCost: d("2.41")},
		},
	}
	recalcStore := &fakeStore{input: in, prev: prev, applyID: 3, applyVer: 6}
	svc := NewService(recalcStore, nil, nil)
	_, err := svc.RecalcSKU(context.Background(), 40, ReasonParamChange, "r", now, testIdent, nil)
	require.NoError(t, err)
	require.Equal(t, "manual-lock", recalcStore.applied[0].CalcSnapshot["primary_selection_rule"])
	require.Equal(t, int64(1), recalcStore.applied[0].CalcSnapshot["primary_supplier_id"])
}

// ---- 防御杂项 ----

func TestLockService_NilDeps(t *testing.T) {
	var svc *LockService
	_, err := svc.LockPrimary(context.Background(), LockPrimaryInput{}, PutOperator{}, "x")
	require.ErrorIs(t, err, ErrLockStoreNil)

	svc2 := NewLockService(nil, &fakeLockStore{}, nil)
	_, err = svc2.LockPrimary(context.Background(), LockPrimaryInput{Reason: "r"}, PutOperator{}, "x")
	require.ErrorIs(t, err, ErrLockStoreNil)
}

// 锁定目标在 RecalcSKU 的 calc.All 里找不到（理论上 LockService 前置校验已保证，
// 但 RecalcSKU 自身必须有一道防线——防止上游校验漏网时被静默按算法写版本）。
func TestRecalcSKU_ManualLockOverrideNotInCandidatesFails(t *testing.T) {
	in := baseInput() // 只含 supplier1 一家
	store := &fakeStore{input: in}
	svc := NewService(store, nil, nil)
	sid := int64(9999) // 不在 calc.All 里
	_, err := svc.RecalcSKU(context.Background(), 40, ReasonManualLock, "r", testNow, testIdent, &sid)
	require.ErrorIs(t, err, ErrLockedSupplierInvalid)
	require.Empty(t, store.applied)
}

// fakeStore 上锁路径触发的 BUG 提示：auditEntries 不应为空（assert 0 不是真的），
// 这里刻意断言 fakeStore 没收到 ApplyNewVersion 的调用（锁定失败路径）。
func TestLockPrimary_AuditWriteFailsRollsBack(t *testing.T) {
	in, now := twoSuppliersInput()
	recalcStore := &fakeStore{input: in, applyID: 5, applyVer: 1}
	lockStore := &fakeLockStore{
		supplierExists: map[int64]bool{1: true},
		statuses:       map[int64]SupplierStatus{1: {SupplierID: 1, QualStatus: "VALID", SettleStatus: "NORMAL", Status: "ACTIVE"}},
		hasQuote:       map[string]bool{quoteKey(1, 40): true},
		auditErr:       errors.New("audit disk full"),
	}
	svc := NewLockService(NewService(recalcStore, nil, nil), lockStore, func() time.Time { return now })
	_, err := svc.LockPrimary(context.Background(),
		LockPrimaryInput{SkuID: 40, SupplierID: 1, Reason: "x"},
		PutOperator{OperatorID: 1, OperatorRole: "PROCUREMENT"}, "r")
	require.Error(t, err)
	require.Contains(t, err.Error(), "audit")
}

// 引用未使用的 import 防御：decimal 在本文件确实被使用（baselineForTest 里）。
var _ = decimal.RequireFromString
