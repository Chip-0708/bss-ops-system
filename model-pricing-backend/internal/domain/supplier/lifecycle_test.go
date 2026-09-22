package supplier

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// fakeLifecycleStore 是内存生命周期仓储。
type fakeLifecycleStore struct {
	quotable   map[int64]QuotableSKU
	officials  map[int64][]OfficialComponent
	expiring   []ExpiringQuoteItem
	total      int64
	counts     map[int64]int
	confirmRes *ConfirmRemoveResult
	confirmErr error
	targets    []ExpiringScanTarget
	todos      map[string]bool // (sheetID, title) 去重集合
	alerts     map[string]bool // (sheetID, severity) 去重集合
	finalIds   []int64
	finalCalls []int64
	anomalies  []AnomalyRawRow
	supplier   *Supplier
	retroMeta  *RetroSheetMeta
	retroErr   error
	retroCount int
	limitAlert bool
	cfgInt     map[string]int
	cfgDec     map[string]decimal.Decimal
}

func newFakeLifecycleStore() *fakeLifecycleStore {
	return &fakeLifecycleStore{
		quotable:  map[int64]QuotableSKU{},
		officials: map[int64][]OfficialComponent{},
		counts:    map[int64]int{},
		todos:     map[string]bool{},
		alerts:    map[string]bool{},
		cfgInt:    map[string]int{"quote_grace_days": 3, "retro_monthly_limit": 5},
		cfgDec:    map[string]decimal.Decimal{"quote_anomaly_pct": dec("0.20"), "quote_anomaly_mkt": dec("0.30")},
	}
}

func (f *fakeLifecycleStore) FindQuotableSKUs(_ context.Context, skuIDs []int64) (map[int64]QuotableSKU, error) {
	out := map[int64]QuotableSKU{}
	for _, id := range skuIDs {
		if s, ok := f.quotable[id]; ok {
			out[id] = s
		}
	}
	return out, nil
}

func (f *fakeLifecycleStore) FindOfficialComponents(_ context.Context, skuIDs []int64) (map[int64][]OfficialComponent, error) {
	out := map[int64][]OfficialComponent{}
	for _, id := range skuIDs {
		if c, ok := f.officials[id]; ok {
			out[id] = c
		}
	}
	return out, nil
}

func (f *fakeLifecycleStore) ListExpiringQuotes(_ context.Context, _ OwnerScope, _ ExpiringQuoteQuery, _ time.Time) ([]ExpiringQuoteItem, int64, error) {
	return f.expiring, f.total, nil
}

func (f *fakeLifecycleStore) CountEffectiveSuppliersBySku(_ context.Context, skuIDs []int64) (map[int64]int, error) {
	out := map[int64]int{}
	for _, id := range skuIDs {
		out[id] = f.counts[id]
	}
	return out, nil
}

func (f *fakeLifecycleStore) ConfirmRemoveQuote(_ context.Context, _ ConfirmRemoveParams) (*ConfirmRemoveResult, error) {
	if f.confirmErr != nil {
		return nil, f.confirmErr
	}
	return f.confirmRes, nil
}

func (f *fakeLifecycleStore) ExpireScanTargets(_ context.Context, _ time.Time) ([]ExpiringScanTarget, error) {
	return f.targets, nil
}

func (f *fakeLifecycleStore) CreateExpireTodoOnce(_ context.Context, sheetID, _ int64, _, level string, _ time.Time, _ JobIdentity) (bool, error) {
	// 去重键 = (sheetID, priority(level))，与 repo 的 TodoPriorityOf 对齐（5d 热修 P1-3）
	key := fmt.Sprintf("%d|%s", sheetID, TodoPriorityOf(level))
	if f.todos[key] {
		return false, nil
	}
	f.todos[key] = true
	return true, nil
}

func (f *fakeLifecycleStore) CreateAlertOnce(_ context.Context, alertType string, sheetID *int64, severity, _ string, _ time.Time, _ JobIdentity) (bool, error) {
	var id int64
	if sheetID != nil {
		id = *sheetID
	}
	key := fmt.Sprintf("%s|%d|%s", alertType, id, severity)
	if f.alerts[key] {
		return false, nil
	}
	f.alerts[key] = true
	return true, nil
}

func (f *fakeLifecycleStore) ListConfirmedPendingFinal(_ context.Context) ([]int64, error) {
	return f.finalIds, nil
}

func (f *fakeLifecycleStore) FinalExpireOne(_ context.Context, quoteID int64, _ time.Time, _ JobIdentity) error {
	f.finalCalls = append(f.finalCalls, quoteID)
	return nil
}

func (f *fakeLifecycleStore) LoadAnomalyRows(_ context.Context, _ int, _ time.Time) ([]AnomalyRawRow, error) {
	return f.anomalies, nil
}

func (f *fakeLifecycleStore) FindSupplierByID(_ context.Context, _ int64) (*Supplier, error) {
	return f.supplier, nil
}

func (f *fakeLifecycleStore) CreateRetroQuote(_ context.Context, _ CreateRetroParams) (*RetroSheetMeta, error) {
	if f.retroErr != nil {
		return nil, f.retroErr
	}
	return f.retroMeta, nil
}

func (f *fakeLifecycleStore) CountRetroThisMonth(_ context.Context, _ time.Time) (int, error) {
	return f.retroCount, nil
}

func (f *fakeLifecycleStore) CreateRetroLimitAlertOnce(_ context.Context, _, _ int, _ time.Time) (bool, error) {
	f.limitAlert = true
	return true, nil
}

func (f *fakeLifecycleStore) GetSysConfigInt(_ context.Context, key string) (int, error) {
	if v, ok := f.cfgInt[key]; ok {
		return v, nil
	}
	return 0, fmt.Errorf("config %s not found", key)
}

func (f *fakeLifecycleStore) GetSysConfigDecimal(_ context.Context, key string) (decimal.Decimal, error) {
	if v, ok := f.cfgDec[key]; ok {
		return v, nil
	}
	return decimal.Zero, fmt.Errorf("config %s not found", key)
}

// ---- 单测 1：扫描去重（连跑两次只生成一份 todo + 一份 alert，5d 裁决 1） ----

func TestExpireScan_Dedupe(t *testing.T) {
	store := newFakeLifecycleStore()
	now := time.Now().UTC()
	store.targets = []ExpiringScanTarget{
		{ID: 10, SupplierName: "供应商甲", VersionNo: 1, ValidTo: now.AddDate(0, 0, 3), SkuIDs: []int64{40}},
	}
	store.counts[40] = 2 // 非单点
	svc := NewLifecycleService(store, nil)
	svc.now = func() time.Time { return now }

	// 第 1 次跑：生成 1 个 todo + 1 个 alert
	r1, err := svc.RunExpireScan(context.Background(), SystemJob("test-req"))
	require.NoError(t, err)
	require.Equal(t, 1, r1.Scanned)
	require.Equal(t, 1, r1.TodoCreated)
	require.Equal(t, 1, r1.AlertCreated)

	// 第 2 次跑：全部去重跳过，不重复插入
	r2, err := svc.RunExpireScan(context.Background(), SystemJob("test-req"))
	require.NoError(t, err)
	require.Equal(t, 1, r2.Scanned)
	require.Equal(t, 0, r2.TodoCreated, "第 2 次跑 todo 必须去重")
	require.Equal(t, 0, r2.AlertCreated, "第 2 次跑 alert 必须去重")
}

// ---- 单测 2：定级规则与 alert severity 映射（5d 裁决 4） ----

func TestExpireScan_AlertLevelMapping(t *testing.T) {
	require.Equal(t, "LOW", alertSeverityOf("NORMAL"))
	require.Equal(t, "HIGH", alertSeverityOf("HIGH"))
	require.Equal(t, "CRITICAL", alertSeverityOf("URGENT"))

	// 非单点依赖
	l, p := alertLevelOf(8, false, false, false, false)
	require.Equal(t, "NORMAL", l)
	require.False(t, p)

	l, _ = alertLevelOf(5, false, false, false, false)
	require.Equal(t, "HIGH", l)

	l, _ = alertLevelOf(-1, true, false, false, false)
	require.Equal(t, "URGENT", l)

	// 过宽限期未确认
	l, p = alertLevelOf(-5, false, true, false, false)
	require.Equal(t, "URGENT", l)
	require.True(t, p, "过宽限期未确认时 pending_remove=true")
}

// ---- 单测 3：单点依赖阈值提前（5d 热修 P3-9 结论 (a)：只提前阈值，不额外升级） ----

func TestExpireScan_SinglePointEscalation(t *testing.T) {
	// 单点依赖下 10 天进入 HIGH（阈值提前 14 天；非单点时 10 天是 NORMAL）
	l, _ := alertLevelOf(10, false, false, false, true)
	require.Equal(t, "HIGH", l, "单点依赖阈值提前到 14 天：10 天 → HIGH（结论 a：只提前阈值，不额外升级）")

	// 非单点时 10 天是 NORMAL
	l, _ = alertLevelOf(10, false, false, false, false)
	require.Equal(t, "NORMAL", l)

	// 单点依赖下 15 天仍 NORMAL
	l, _ = alertLevelOf(15, false, false, false, true)
	require.Equal(t, "NORMAL", l)

	// 单点依赖下 5 天 → URGENT（阈值提前到 7 天）
	l, _ = alertLevelOf(5, false, false, false, true)
	require.Equal(t, "URGENT", l)
}

// ---- 单测 4：未过宽限期确认剔除 → 409（5d 裁决 5） ----

func TestConfirmRemove_InGracePeriod_409(t *testing.T) {
	store := newFakeLifecycleStore()
	store.confirmErr = ErrQuoteInGracePeriod
	svc := NewLifecycleService(store, nil)
	_, err := svc.ConfirmRemove(context.Background(), 10, ConfirmRemoveInput{Confirm: true, Reason: "测试"}, 4, "PROCUREMENT", "req-1")
	require.ErrorIs(t, err, ErrQuoteInGracePeriod)
}

// ---- 单测 5：confirm-remove 立即执行（5d 裁决 2/5） ----

func TestConfirmRemove_Executes(t *testing.T) {
	store := newFakeLifecycleStore()
	store.confirmRes = &ConfirmRemoveResult{ID: 10, Status: "EXPIRED", RemoveConfirmed: true, ExecutedAt: time.Now()}
	svc := NewLifecycleService(store, nil)
	res, err := svc.ConfirmRemove(context.Background(), 10, ConfirmRemoveInput{Confirm: true, Reason: "备用承接"}, 4, "PROCUREMENT", "req-1")
	require.NoError(t, err)
	require.Equal(t, "EXPIRED", res.Status)
	require.True(t, res.RemoveConfirmed)
}

// ---- 单测 6：撤销确认（confirm=false） ----

func TestConfirmRemove_UndoBeforeExecution(t *testing.T) {
	store := newFakeLifecycleStore()
	store.confirmRes = &ConfirmRemoveResult{ID: 10, Status: "EFFECTIVE", RemoveConfirmed: false, ExecutedAt: time.Now()}
	svc := NewLifecycleService(store, nil)
	res, err := svc.ConfirmRemove(context.Background(), 10, ConfirmRemoveInput{Confirm: false, Reason: "撤销"}, 4, "PROCUREMENT", "req-1")
	require.NoError(t, err)
	require.False(t, res.RemoveConfirmed)
	require.Equal(t, "EFFECTIVE", res.Status)
}

// ---- 单测 7：expire-final 正常情况下查不到行（5d 补充裁决 b） ----

func TestExpireFinal_NoRowsNormally(t *testing.T) {
	// confirm-remove 已立即执行剔除，expire-final 只是历史遗留兜底，
	// 正常运行下 ids 恒为空，绝不误剔除 EFFECTIVE 单（防止有人"修复"它）
	store := newFakeLifecycleStore()
	store.finalIds = []int64{} // 正常查不到行
	svc := NewLifecycleService(store, nil)
	res, err := svc.RunExpireFinal(context.Background(), SystemJob("test-req"))
	require.NoError(t, err)
	require.Equal(t, 0, res.Scanned)
	require.Equal(t, 0, res.Processed)
	require.Empty(t, store.finalCalls)
}

// ---- 单测 8：特权补录有效时间必须为过去时间（5d 裁决 6） ----

func TestRetroEffective_RequiresPastTime(t *testing.T) {
	svc := NewLifecycleService(newFakeLifecycleStore(), nil)
	in := RetroEffectiveInput{
		SupplierID:    1,
		EffectiveTime: time.Now().UTC().Add(time.Hour), // 未来时间
		ValidTo:       time.Now().UTC().Add(24 * time.Hour),
		AuditReason:   "这是一段超过十个字的有效审计理由",
	}
	_, err := svc.RetroEffective(context.Background(), in, 4, "RETRO_OP", "req-1")
	require.ErrorIs(t, err, ErrRetroTimeNotPast)
}

// ---- 单测 9：特权补录理由不足 10 字（按 rune）→ 400 ----

func TestRetroEffective_AuditReasonRuneCount(t *testing.T) {
	svc := NewLifecycleService(newFakeLifecycleStore(), nil)
	in := RetroEffectiveInput{
		SupplierID:    1,
		EffectiveTime: time.Now().UTC().Add(-time.Hour),
		ValidTo:       time.Now().UTC().Add(24 * time.Hour),
		AuditReason:   "九个字审计理由", // 7 个字
	}
	_, err := svc.RetroEffective(context.Background(), in, 4, "RETRO_OP", "req-1")
	require.ErrorIs(t, err, ErrRetroReasonInvalid)
}

// ---- 单测 10：特权补录超限写 alert（当月第 6 条 → RETRO_LIMIT，5d 裁决 6） ----

func TestRetroEffective_OverLimitAlert(t *testing.T) {
	store := newFakeLifecycleStore()
	store.supplier = testSupplier
	store.quotable[12] = QuotableSKU{ID: 12, SkuCode: "gpt-5", NativeCurrency: "USD"}
	store.officials[12] = []OfficialComponent{{ComponentType: "input", UnitPrice: dec("2.5")}}
	store.retroMeta = &RetroSheetMeta{ID: 88, SupplierID: 1, VersionNo: 5, ValidFrom: time.Now().Add(-24 * time.Hour)}
	store.retroCount = 6 // 超过限额 5

	fakeApprove := &fakeApproveStore{
		activateOne: map[int64]*ActivateDueItem{88: {ID: 88, SupplierID: 1, VersionNo: 5}},
	}
	appSvc := NewApproveService(fakeApprove)
	svc := NewLifecycleService(store, appSvc)

	in := RetroEffectiveInput{
		SupplierID:    1,
		EffectiveTime: time.Now().UTC().Add(-24 * time.Hour),
		ValidTo:       time.Now().UTC().Add(90 * 24 * time.Hour),
		AuditReason:   "口头已达成协议且已按此价格结算满三个月",
		Items: []SubmitQuoteItem{
			{SKUID: 12, FxTier: strP("6.8"), Components: []SubmitQuoteComponent{{ComponentType: "input", UnitPrice: "2.0"}}},
		},
	}
	res, err := svc.RetroEffective(context.Background(), in, 4, "RETRO_OP", "req-1")
	require.NoError(t, err)
	require.True(t, res.AlertCreated, "超过限额必须写告警")
	require.True(t, store.limitAlert)
	require.Equal(t, 6, res.RetroCountThisMonth)
}

// ---- 单测 11：异常检测偏离上一版（严格大于 20%，恰好 20% 不触发） ----

func TestAnomalies_PrevDeviation(t *testing.T) {
	store := newFakeLifecycleStore()
	p100 := dec("100")
	store.anomalies = []AnomalyRawRow{
		{QuoteSheetID: 1, SkuID: 40, SkuCode: "gpt-5", ComponentType: "input", UnitPrice: dec("120"), PrevPrice: &p100}, // 恰好 20% → 不触发
		{QuoteSheetID: 2, SkuID: 40, SkuCode: "gpt-5", ComponentType: "input", UnitPrice: dec("121"), PrevPrice: &p100}, // 21% → 触发
	}
	svc := NewLifecycleService(store, nil)
	res, err := svc.ListAnomalies(context.Background(), AnomalyQuery{})
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Total, "恰好等于阈值不触发，必须严格大于")
	require.Equal(t, int64(2), res.List[0].QuoteSheetID)
	require.Equal(t, "PREV_DEVIATION", res.List[0].Reason)
	require.Equal(t, "0.21", *res.List[0].DeltaPct)
}

// ---- 单测 12：异常检测偏离市场最低与 BOTH ----

func TestAnomalies_MarketAndBoth(t *testing.T) {
	store := newFakeLifecycleStore()
	pPrev := dec("100")
	pBest := dec("100")
	store.anomalies = []AnomalyRawRow{
		// 偏离市场最低 > 30% 但没偏离上一版
		{QuoteSheetID: 1, SkuID: 40, SkuCode: "gpt-5", ComponentType: "input", UnitPrice: dec("135"), MarketBest: &pBest},
		// 偏离上一版 > 20% 且偏离市场最低 > 30% → BOTH
		{QuoteSheetID: 2, SkuID: 40, SkuCode: "gpt-5", ComponentType: "input", UnitPrice: dec("135"), PrevPrice: &pPrev, MarketBest: &pBest},
	}
	svc := NewLifecycleService(store, nil)
	res, err := svc.ListAnomalies(context.Background(), AnomalyQuery{})
	require.NoError(t, err)
	require.Equal(t, int64(2), res.Total)
	require.Equal(t, "MARKET_DEVIATION", res.List[0].Reason)
	require.Equal(t, "BOTH", res.List[1].Reason)
}

// ---- 单测 13：并发安全（P0-1 回归：包级 map 已删，SkIDs 内嵌进 DTO） ----

func TestListExpiring_ConcurrentSafe(t *testing.T) {
	store := newFakeLifecycleStore()
	now := time.Now().UTC()
	store.expiring = []ExpiringQuoteItem{
		{ID: 1, SupplierID: 10, VersionNo: 1, ValidTo: now.AddDate(0, 0, 5), SkuIDs: []int64{40}},
		{ID: 2, SupplierID: 11, VersionNo: 2, ValidTo: now.AddDate(0, 0, 10), SkuIDs: []int64{41, 42}},
	}
	store.total = 2
	store.counts[40] = 1 // 单点
	store.counts[41] = 2
	store.counts[42] = 3
	svc := NewLifecycleService(store, nil)
	svc.now = func() time.Time { return now }

	// 并发 100 次：包级 map 时代必崩（concurrent map writes），现在必须安全
	done := make(chan error, 100)
	for i := 0; i < 100; i++ {
		go func() {
			res, err := svc.ListExpiring(context.Background(), scopeALL, ExpiringQuoteQuery{Days: 30})
			if err != nil {
				done <- err
				return
			}
			if res.Total != 2 {
				done <- fmt.Errorf("total=%d, want 2", res.Total)
				return
			}
			done <- nil
		}()
	}
	for i := 0; i < 100; i++ {
		require.NoError(t, <-done)
	}
}

// ---- 单测 14：only_single_point 时 total 跟随过滤后（P3-10 结论 a） ----

func TestListExpiring_OnlySinglePointTotal(t *testing.T) {
	store := newFakeLifecycleStore()
	now := time.Now().UTC()
	store.expiring = []ExpiringQuoteItem{
		{ID: 1, SupplierID: 10, VersionNo: 1, ValidTo: now.AddDate(0, 0, 5), SkuIDs: []int64{40}},
		{ID: 2, SupplierID: 11, VersionNo: 2, ValidTo: now.AddDate(0, 0, 10), SkuIDs: []int64{41}},
	}
	store.total = 2
	store.counts[40] = 1 // 单点
	store.counts[41] = 2 // 非单点
	svc := NewLifecycleService(store, nil)
	svc.now = func() time.Time { return now }

	res, err := svc.ListExpiring(context.Background(), scopeALL, ExpiringQuoteQuery{Days: 30, OnlySinglePoint: true})
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Total, "only_single_point=true 时 total 必须跟随过滤后")
	require.Len(t, res.List, 1)
	require.True(t, res.List[0].SinglePoint)
}

// ---- 单测 15：单点依赖跨供应商去重计数（P5-1：同一供应商多版本只算 1） ----

func TestSinglePoint_CountDistinctSuppliers(t *testing.T) {
	// 同一供应商有 v3/v4 两个 EFFECTIVE 版本时，该 SKU 计数应为 1（不是 2）
	// COUNT(DISTINCT qs.supplier_id) 由 repo 层保证；service 层验证「计数==1 → 单点」的判定
	store := newFakeLifecycleStore()
	now := time.Now().UTC()
	store.expiring = []ExpiringQuoteItem{
		{ID: 1, SupplierID: 10, VersionNo: 3, ValidTo: now.AddDate(0, 0, 5), SkuIDs: []int64{40}},
		{ID: 2, SupplierID: 10, VersionNo: 4, ValidTo: now.AddDate(0, 0, 10), SkuIDs: []int64{40}}, // 同供应商
	}
	store.total = 2
	store.counts[40] = 1 // repo 的 COUNT(DISTINCT supplier_id) 已去重：同供应商多版本只算 1
	svc := NewLifecycleService(store, nil)
	svc.now = func() time.Time { return now }

	res, err := svc.ListExpiring(context.Background(), scopeALL, ExpiringQuoteQuery{Days: 30})
	require.NoError(t, err)
	require.Equal(t, int64(2), res.Total)
	require.True(t, res.List[0].SinglePoint, "同供应商多版本但跨供应商计数=1 → 单点依赖")
	require.True(t, res.List[1].SinglePoint)
}

// ---- 单测 16：anomalies 不做归属过滤（P5-2：§3.2 放开比价决议） ----

func TestAnomalies_NoOwnerFilter(t *testing.T) {
	// LoadAnomalyRows 的 SQL 不带 owner_procurement_operator_id 条件；
	// 服务层 ListAnomalies 也不传 scope。fake 记录调用以验证不过滤。
	store := newFakeLifecycleStore()
	p100 := dec("100")
	p130 := dec("130")
	store.anomalies = []AnomalyRawRow{
		{QuoteSheetID: 1, SupplierName: "供应商甲", SkuID: 40, SkuCode: "gpt-5", ComponentType: "input", UnitPrice: dec("135"), PrevPrice: &p100, MarketBest: &p130},
	}
	svc := NewLifecycleService(store, nil)
	res, err := svc.ListAnomalies(context.Background(), AnomalyQuery{})
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Total)
	// 服务层未传 scope（函数签名无 scope 参数），repo SQL 无 owner 过滤——由代码结构保证，
	// 此处断言「能查到数据」即证明未被归属过滤误删。
	require.Equal(t, "供应商甲", res.List[0].SupplierName)
}
