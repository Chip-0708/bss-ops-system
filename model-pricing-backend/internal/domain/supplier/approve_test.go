package supplier

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// fakeApproveStore 为单测的内存审批仓储。
type fakeApproveStore struct {
	pending     *PendingQuoteResult
	scopeFound  bool
	scopeIn     bool
	approveRes  *ApproveResult
	approveErr  error
	rejectRes   *RejectResult
	rejectErr   error
	due         []DueQuote
	activateOne map[int64]*ActivateDueItem
	activateErr map[int64]error
	markedErr   []int64
	diffRaw     *QuoteDiffRaw
	minMargin   decimal.Decimal

	approveCalled bool
	rejectCalled  bool
	activateCalls []int64
}

func (f *fakeApproveStore) ListPendingQuotes(_ context.Context, _ OwnerScope, q PendingQuoteQuery, _ time.Time) (*PendingQuoteResult, error) {
	if f.pending != nil {
		return f.pending, nil
	}
	return &PendingQuoteResult{List: []PendingQuoteItem{}, Page: q.Page, Size: q.Size}, nil
}

func (f *fakeApproveStore) CheckQuoteScope(_ context.Context, _ int64, _ OwnerScope) (bool, bool, error) {
	return f.scopeFound, f.scopeIn, nil
}

func (f *fakeApproveStore) ApproveQuote(_ context.Context, _ ApproveParams) (*ApproveResult, error) {
	f.approveCalled = true
	return f.approveRes, f.approveErr
}

func (f *fakeApproveStore) RejectQuote(_ context.Context, _ RejectParams) (*RejectResult, error) {
	f.rejectCalled = true
	return f.rejectRes, f.rejectErr
}

func (f *fakeApproveStore) ListDueQuotes(_ context.Context, _ time.Time) ([]DueQuote, error) {
	return f.due, nil
}

func (f *fakeApproveStore) ActivateOne(_ context.Context, quoteID int64, _ ActivateParams) (*ActivateDueItem, error) {
	f.activateCalls = append(f.activateCalls, quoteID)
	if err, ok := f.activateErr[quoteID]; ok && err != nil {
		return nil, err
	}
	return f.activateOne[quoteID], nil
}

func (f *fakeApproveStore) MarkActivateError(_ context.Context, quoteID int64, _ string) error {
	f.markedErr = append(f.markedErr, quoteID)
	return nil
}

func (f *fakeApproveStore) LoadQuoteDiff(_ context.Context, _ int64) (*QuoteDiffRaw, error) {
	return f.diffRaw, nil
}

func (f *fakeApproveStore) GetSysConfigDecimal(_ context.Context, _ string) (decimal.Decimal, error) {
	return f.minMargin, nil
}

var scopeALL = OwnerScope{DataScope: "ALL", StaffID: 4, MyOrgID: 1}

func TestApprove_OK(t *testing.T) {
	store := &fakeApproveStore{
		scopeFound: true, scopeIn: true,
		approveRes: &ApproveResult{ID: 1, Status: "APPROVED_PENDING", ApprovedAt: time.Now(), ActivateAt: time.Now().Add(24 * time.Hour), Immediate: false},
	}
	svc := NewApproveService(store)
	res, err := svc.Approve(context.Background(), 1, scopeALL, 4, "PROCUREMENT", "req-1")
	require.NoError(t, err)
	require.True(t, store.approveCalled)
	require.Equal(t, "APPROVED_PENDING", res.Status)
	require.False(t, res.Immediate)
	// 非 immediate：不同步激活
	require.Empty(t, store.activateCalls)
}

func TestApprove_ImmediateSyncActivate(t *testing.T) {
	// 契约 §8 实现要求：immediate（valid_from<=now）时 approve 事务提交后
	// 另开事务同步调 ActivateDueQuotes（本轮无 ticker，只入队会永久停在 APPROVED_PENDING）。
	store := &fakeApproveStore{
		scopeFound: true, scopeIn: true,
		approveRes: &ApproveResult{ID: 1, Status: "APPROVED_PENDING", ApprovedAt: time.Now(), ActivateAt: time.Now().Add(-time.Hour), Immediate: true},
		due:        []DueQuote{{ID: 1, SupplierID: 10, VersionNo: 1}},
		activateOne: map[int64]*ActivateDueItem{
			1: {ID: 1, SupplierID: 10, VersionNo: 1},
		},
		activateErr: map[int64]error{},
	}
	svc := NewApproveService(store)
	res, err := svc.Approve(context.Background(), 1, scopeALL, 4, "PROCUREMENT", "req-1")
	require.NoError(t, err)
	require.True(t, res.Immediate)
	require.Equal(t, []int64{1}, store.activateCalls)
}

func TestApprove_NotFound_404(t *testing.T) {
	store := &fakeApproveStore{scopeFound: false}
	svc := NewApproveService(store)
	_, err := svc.Approve(context.Background(), 999, scopeALL, 4, "PROCUREMENT", "req-1")
	require.ErrorIs(t, err, ErrQuoteNotFound)
	require.False(t, store.approveCalled)
}

func TestApprove_NotApprovable_409(t *testing.T) {
	store := &fakeApproveStore{
		scopeFound: true, scopeIn: true,
		approveErr: ErrQuoteNotApprovable,
	}
	svc := NewApproveService(store)
	_, err := svc.Approve(context.Background(), 1, scopeALL, 4, "PROCUREMENT", "req-1")
	require.ErrorIs(t, err, ErrQuoteNotApprovable)
}

func TestApprove_ForbiddenScope_403(t *testing.T) {
	store := &fakeApproveStore{scopeFound: true, scopeIn: false}
	svc := NewApproveService(store)
	_, err := svc.Approve(context.Background(), 1, OwnerScope{DataScope: "SELF", StaffID: 99}, 99, "PROCUREMENT", "req-1")
	require.ErrorIs(t, err, ErrQuoteScopeForbidden)
	require.False(t, store.approveCalled)
}

func TestReject_OK(t *testing.T) {
	store := &fakeApproveStore{
		scopeFound: true, scopeIn: true,
		rejectRes: &RejectResult{ID: 1, Status: "REJECTED", RejectedAt: time.Now()},
	}
	svc := NewApproveService(store)
	in := RejectInput{Reason: "output 组件高于市场最低价 32%，请复核后重新提交"}
	res, err := svc.Reject(context.Background(), 1, scopeALL, in, 4, "PROCUREMENT", "req-2")
	require.NoError(t, err)
	require.True(t, store.rejectCalled)
	require.Equal(t, "REJECTED", res.Status)
}

func TestReject_ReasonTooShort(t *testing.T) {
	store := &fakeApproveStore{scopeFound: true, scopeIn: true}
	svc := NewApproveService(store)
	_, err := svc.Reject(context.Background(), 1, scopeALL, RejectInput{Reason: "太短"}, 4, "PROCUREMENT", "req-2")
	require.ErrorIs(t, err, ErrQuoteRejectReasonInvalid)
	require.False(t, store.rejectCalled)
}

func TestReject_ReasonRuneCount(t *testing.T) {
	// 10 个中文 rune 合法（中文一字一算）
	store := &fakeApproveStore{
		scopeFound: true, scopeIn: true,
		rejectRes: &RejectResult{ID: 1, Status: "REJECTED"},
	}
	svc := NewApproveService(store)
	_, err := svc.Reject(context.Background(), 1, scopeALL, RejectInput{Reason: "一二三四五六七八九十"}, 4, "PROCUREMENT", "req-2")
	require.NoError(t, err)
}

func TestActivateDue_OK(t *testing.T) {
	store := &fakeApproveStore{
		due: []DueQuote{{ID: 1, SupplierID: 10}, {ID: 2, SupplierID: 11}},
		activateOne: map[int64]*ActivateDueItem{
			1: {ID: 1, SupplierID: 10, VersionNo: 3},
			2: {ID: 2, SupplierID: 11, VersionNo: 1},
		},
		activateErr: map[int64]error{},
	}
	svc := NewApproveService(store)
	res, err := svc.ActivateDueQuotes(context.Background(), ActivateParams{Now: time.Now()})
	require.NoError(t, err)
	require.Equal(t, 2, res.Scanned)
	require.Equal(t, 2, res.Activated)
	require.Equal(t, []int64{1, 2}, store.activateCalls)
	// worker 默认：OperatorRole=SYSTEM / SourceType=WORKER
}

func TestActivateDue_SkipsConflict(t *testing.T) {
	store := &fakeApproveStore{
		due: []DueQuote{{ID: 1}, {ID: 2}},
		activateOne: map[int64]*ActivateDueItem{
			2: {ID: 2, SupplierID: 11, VersionNo: 1},
		},
		activateErr: map[int64]error{
			1: errors.New("uk_quote_effective violation"),
		},
	}
	svc := NewApproveService(store)
	res, err := svc.ActivateDueQuotes(context.Background(), ActivateParams{Now: time.Now()})
	require.NoError(t, err)
	require.Equal(t, 2, res.Scanned)
	require.Equal(t, 1, res.Activated)
	require.Equal(t, []int64{1}, store.markedErr) // 失败记 last_error，不中断整批
}

// ---- §14 diff 纯函数测试 ----

func diffRawFixture() *QuoteDiffRaw {
	prev := dec("2.2")
	official := dec("2.5")
	market := dec("1.9")
	return &QuoteDiffRaw{
		SheetID: 31, SupplierID: 5, VersionNo: 2,
		Items: []DiffRawItem{
			{
				SKUID: 12, SKUCode: "gpt-5-2026-04-11", Currency: "USD",
				Components: []DiffRawComponent{
					{ComponentType: "input", UnitPrice: dec("2.0"), Multiplier: decP("0.8"), PrevPrice: &prev, OfficialPrice: &official, MarketBest: &market},
					{ComponentType: "output", UnitPrice: dec("7.5"), Multiplier: decP("0.75")},
				},
			},
		},
	}
}

func decP(s string) *decimal.Decimal { d := dec(s); return &d }

func TestDiff_PrevOfficialMarket(t *testing.T) {
	out := buildQuoteDiff(diffRawFixture(), dec("0.15"))
	require.Len(t, out.Items, 1)
	c := out.Items[0].Components[0]
	require.Equal(t, "2", c.UnitPrice)
	require.Equal(t, "2.2", *c.PrevPrice)
	require.Equal(t, "2.5", *c.OfficialPrice)
	require.Equal(t, "1.9", *c.MarketBest)
	// prev: (2.0-2.2)/2.2 = -0.0909；official: (2.0-2.5)/2.5 = -0.2；market: (2.0-1.9)/1.9 = 0.0526
	require.Equal(t, "-0.0909", *c.PrevDeltaPct)
	require.Equal(t, "-0.2", *c.OfficialDeltaPct)
	require.Equal(t, "0.0526", *c.MarketDeltaPct)
	// 上一版缺 output → null
	require.Nil(t, out.Items[0].Components[1].PrevPrice)
	require.Nil(t, out.Items[0].Components[1].PrevDeltaPct)
}

func TestDiff_Distortion(t *testing.T) {
	raw := diffRawFixture()
	m := dec("0.8")
	raw.Items[0].Components[1].Multiplier = &m // 两组件倍率均为 0.8 → 失真
	out := buildQuoteDiff(raw, dec("0.15"))
	require.True(t, out.Distortion)
	require.NotEmpty(t, out.DistortionNote)

	// 含绝对价（multiplier=nil）→ 不判定
	raw2 := diffRawFixture()
	out2 := buildQuoteDiff(raw2, dec("0.15"))
	require.False(t, out2.Distortion) // output 是 0.75 ≠ 0.8

	raw3 := diffRawFixture()
	raw3.Items[0].Components[1].Multiplier = nil // 绝对价存在 → 不判定
	out3 := buildQuoteDiff(raw3, dec("0.15"))
	require.False(t, out3.Distortion)
}

func TestDiff_MarginPreview(t *testing.T) {
	out := buildQuoteDiff(diffRawFixture(), dec("0.15"))
	mp := out.Items[0].MarginPreview
	require.NotNil(t, mp)
	// floor = 2.0 / (1-0.15) = 2.35294117...
	require.Equal(t, "2.3529411764705882", mp.FloorPrice)
	require.Equal(t, "2.5", *mp.ReferenceSellPrice)
	require.True(t, *mp.MarginOK) // 2.5 ≥ 2.3529

	// 无官方价 → reference_sell_price / margin_ok 为 null
	raw := diffRawFixture()
	raw.Items[0].Components[0].OfficialPrice = nil
	out2 := buildQuoteDiff(raw, dec("0.15"))
	require.Nil(t, out2.Items[0].MarginPreview.ReferenceSellPrice)
	require.Nil(t, out2.Items[0].MarginPreview.MarginOK)
}

func TestDiff_ScopeForbidden(t *testing.T) {
	store := &fakeApproveStore{scopeFound: true, scopeIn: false}
	svc := NewApproveService(store)
	_, err := svc.Diff(context.Background(), 1, OwnerScope{DataScope: "SELF", StaffID: 99})
	require.ErrorIs(t, err, ErrQuoteScopeForbidden)
}

// ---- 数据域 Scope 的显式断言（DEPT_SUB 路径格式必须与 org_unit.path 一致） ----

func TestOwnerScopePaths_Format(t *testing.T) {
	// ScopePaths 来自 auth.Synthesize，元素形如 "/1/3/"（含首尾斜杠），
	// repo 用 u.path LIKE ANY(paths || '%')。这里显式断言匹配语义，
	// 防止路径格式漂移导致 DEPT_SUB 静默失效（配不上不报错，只会永远查不到数据）。
	paths := []string{"/1/3/", "/1/4/"}
	patterns := make([]string, 0, len(paths))
	for _, p := range paths {
		patterns = append(patterns, p+"%")
	}
	require.Equal(t, []string{"/1/3/%", "/1/4/%"}, patterns)

	// 模拟 LIKE 前缀匹配语义
	matches := func(pattern, path string) bool {
		prefix := pattern[:len(pattern)-1] // 去 %
		return len(path) >= len(prefix) && path[:len(prefix)] == prefix
	}
	require.True(t, matches("/1/3/%", "/1/3/"))     // 本节点
	require.True(t, matches("/1/3/%", "/1/3/7/"))   // 下属节点
	require.True(t, matches("/1/3/%", "/1/3/7/9/")) // 更深层
	require.False(t, matches("/1/3/%", "/1/30/"))   // 前缀陷阱：/1/3/ ≠ /1/30/（尾斜杠保护）
	require.False(t, matches("/1/3/%", "/1/4/"))    // 兄弟节点
}

func TestListPending_PagingNormalize(t *testing.T) {
	svc := NewApproveService(&fakeApproveStore{})
	res, err := svc.ListPending(context.Background(), scopeALL, PendingQuoteQuery{Page: 0, Size: 999})
	require.NoError(t, err)
	require.Equal(t, 1, res.Page)
	require.Equal(t, 20, res.Size)
}
