// Package cost 的 lock_service.go：手动锁定主供应商（06-cost.md §6，6d-3）。
//
// 语义裁决（全部钉死在本文件与单测，改动前先读）：
//  1. 入口单行职责：本服务处理「一个 SKU 换/定锁」这**一个动作**，不塞进 ParamService
//     （那是全局参数级）、也不塞进重算 Service（那是所有触发的通用编排）。
//  2. 校验全部在事务开启前完成（陷阱 4 四项：reason 非空 → 供应商存在 → 未被任何形式的
//     冻结/失效 → 该 SKU 有 EFFECTIVE 报价），任一失败 → 不重算、不写库、不写审计。
//  3. 重复锁定同一家 = 合法 no-op（Unchanged=true、返回当前 version、不产生新版本、
//     不重复写冗余审计——写一条变化为零的审计等于噪音，重试请走幂等回放）；
//     是否要改成 409 见 CLAUDE.md 遗留项 6d-3-④（本批冻结为 no-op）。
//  4. 换锁动作 = 复用 RecalcSKU 的不可变版本切换（MANUAL_LOCK + manualLockSupplierID）——
//     版本链、并发兜底（uk_cost_current / ex_cost_no_overlap）、值未变判定全部与其他触发
//     同源，不会悄悄长出第二套写入路径。
//  5. 审计单条 COST_BASELINE_LOCK_PRIMARY，before/after 分别带 primary/locked/version 三键
//     （红线 10：价格相关操作 100% 写审计且前后值齐全），target_id = 新版本基线行 id。
package cost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LockStore 是锁定专属仓储的窄接口（不重定义 Store——CalcSKU 的 LoadRecalcInput 是
// 重算引擎的装配口径，这里的 HasEffectiveQuoteOnSKU 只需要「存不存在」这个 bool）。
// GORM 实现见 internal/repo/cost_lock.go。
type LockStore interface {
	// SupplierExists 校验 supplier_profile.id 存在（不存在 → 404）。
	SupplierExists(ctx context.Context, supplierID int64) (bool, error)
	// SupplierStatusOf 读供应商当前冻结/失效状态（qual_status / settle_status / status）。
	// 行不存在 → (零值, false, nil)——调用方必须先 SupplierExists。
	SupplierStatusOf(ctx context.Context, supplierID int64) (SupplierStatus, bool, error)
	// HasEffectiveQuoteOnSKU 该供应商在该 SKU 上是否有当前 EFFECTIVE 报价（quote_item 维度）。
	HasEffectiveQuoteOnSKU(ctx context.Context, supplierID, skuID int64) (bool, error)
	// LoadCurrentBaselineForUpdate 与 cost.Store 同语义（FOR UPDATE 行锁），
	// 这里只做「重复锁定判定」一次读——新版本 id 由 RecalcOutcome.BaselineID 回填，
	// 不再二次反查。
	LoadCurrentBaselineForUpdate(ctx context.Context, skuID int64) (*Baseline, error)
	// WriteLockAudit 写锁定专属审计行（action=COST_BASELINE_LOCK_PRIMARY）。
	// 与版本切换同事务（调用方保证——幂等中间件注入的事务在此被消费）。
	WriteLockAudit(ctx context.Context, e LockAuditEntry) error
}

// LockAuditEntry 是 COST_BASELINE_LOCK_PRIMARY 审计行的全部字段（红线 10）。
type LockAuditEntry struct {
	Now          time.Time
	OperatorID   int64
	OperatorRole string
	TargetID     int64          // 新版本 cost_baseline.id
	Before       map[string]any // {"primary_supplier_id","locked_manual","version"}；首个版本时版本=0/locked=false/primary=0
	After        map[string]any
	Reason       string // 调用方必填（已 trim）
	RequestID    string
}

// 领域错误（handler 按 errors.Is 映射 HTTP 码；消息透传可见即可，别编码敏感信息）。
var (
	// ErrLockStoreNil 未接仓储时的防御错误。
	ErrLockStoreNil = errors.New("cost.LockService 未接 LockStore")
	// ErrLockReasonEmpty reason trim 后为空（必填字段，绝不静默吞输入）。
	ErrLockReasonEmpty = errors.New("reason 必填且不能为空白")
	// ErrLockSupplierNotFound supplier_id 在 supplier_profile 不存在。
	ErrLockSupplierNotFound = errors.New("供应商不存在")
	// ErrLockNoEffectiveQuote 该供应商在该 SKU 上没有当前 EFFECTIVE 报价（无法锁其为主）。
	ErrLockNoEffectiveQuote = errors.New("该供应商在此 SKU 上没有生效报价，无法锁定为主供应商")
	// ErrLockSupplierFrozen 供应商处于任何一种冻结/失效（资质/结算/停用）；锁定无意义且放大事故面。
	ErrLockSupplierFrozen = errors.New("供应商处于冻结/失效状态，无法锁定")
)

// LockPrimaryInput 是锁定的全部入参（SkuID 在 handler 已按 {sku} 路径解析完，这里不再做解析）。
type LockPrimaryInput struct {
	SkuID      int64
	SupplierID int64
	Reason     string
}

// LockPrimaryResult 是锁定结果的响应素材。Unchanged=true 时 Version 是**旧版本号**、
// 其余字段与当前版本一致（区分于「新版本已产生」——前端/审计需要明确是哪种语义）。
type LockPrimaryResult struct {
	SkuID             int64
	Version           int
	PrimarySupplierID int64
	ChangeReason      string // 本接口恒 MANUAL_LOCK
	Locked            bool   // 恒 true（本接口只产生锁；解锁见遗留 6d-3-①）
	Unchanged         bool   // true = 重复锁定同对象，被直接跳过
}

// LockService 是锁定的业务编排（无状态，可并发使用）。时钟可注入（测试用）。
// 与 ParamService 并列级别：一个做参数，一个做锁定——都是边缘写路径，不是重算引擎主体
// （重算引擎只管被触发后该怎么算，锁的语义在这里聚合）。
type LockService struct {
	costSvc *Service // 不可变版本切换的唯一入口（与其他触发共用同一管道）
	store   LockStore
	now     func() time.Time
}

// NewLockService 构造锁定服务。now 为 nil 用 time.Now().UTC。
func NewLockService(costSvc *Service, store LockStore, now func() time.Time) *LockService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &LockService{costSvc: costSvc, store: store, now: now}
}

// LockPrimary 锁定主供应商的唯一入口（06-cost.md §6）。
// 入参 op/requestID 只用于审计落库；**鉴权已在 handler 用 perm.AnyRoleCan(op.Roles, perm.CanLockPrimary)
// 完成**——本方法不做二次鉴权（CLAUDE.md 6d-2 的同一纪律：operatorRoleOf 绝不参与权限判定）。
//
// 返回值：
//   - Unchanged=true：重复锁定同对象，未产新版本（锁定本来就在那）。
//   - Unchanged=false：产新版本（Version=prev+1，或首版本的 1）。
func (s *LockService) LockPrimary(ctx context.Context, input LockPrimaryInput, op PutOperator, requestID string) (*LockPrimaryResult, error) {
	if s == nil || s.costSvc == nil || s.store == nil {
		return nil, ErrLockStoreNil
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return nil, ErrLockReasonEmpty
	}

	// ---- 校验（陷阱 4 四项，全部只读，失败即返、不开任何东西） ----
	// 1. supplier_id 必须存在（404）。
	exists, err := s.store.SupplierExists(ctx, input.SupplierID)
	if err != nil {
		return nil, fmt.Errorf("check supplier exists id=%d: %w", input.SupplierID, err)
	}
	if !exists {
		return nil, fmt.Errorf("%w：supplier_id=%d", ErrLockSupplierNotFound, input.SupplierID)
	}
	// 2. 供应商必须「真实可达」（不被任何一种冻结/失效）。
	st, ok, err := s.store.SupplierStatusOf(ctx, input.SupplierID)
	if err != nil {
		return nil, fmt.Errorf("load supplier status id=%d: %w", input.SupplierID, err)
	}
	if !ok {
		return nil, fmt.Errorf("%w：supplier_id=%d 行存在但状态行读取缺失（数据事故）", ErrLockSupplierNotFound, input.SupplierID)
	}
	if st.QualStatus == "FROZEN" || st.SettleStatus == "FROZEN" || st.Status == "INACTIVE" {
		return nil, fmt.Errorf("%w：supplier_id=%d qual=%s settle=%s status=%s",
			ErrLockSupplierFrozen, input.SupplierID, st.QualStatus, st.SettleStatus, st.Status)
	}
	// 3. 该供应商在该 SKU 上必须有当前 EFFECTIVE 报价（409）。
	hasQuote, err := s.store.HasEffectiveQuoteOnSKU(ctx, input.SupplierID, input.SkuID)
	if err != nil {
		return nil, fmt.Errorf("check effective quote supplier=%d sku=%d: %w", input.SupplierID, input.SkuID, err)
	}
	if !hasQuote {
		return nil, fmt.Errorf("%w：supplier_id=%d sku_id=%d", ErrLockNoEffectiveQuote, input.SupplierID, input.SkuID)
	}

	// ---- 重复锁定判定（no-op 语义，不是 409——见文件头裁决 3）----
	prev, err := s.store.LoadCurrentBaselineForUpdate(ctx, input.SkuID)
	if err != nil {
		return nil, fmt.Errorf("load current baseline sku=%d: %w", input.SkuID, err)
	}
	now := s.now().UTC()
	if prev != nil && prev.LockedManual && prev.PrimarySupplierID == input.SupplierID {
		return &LockPrimaryResult{
			SkuID: input.SkuID, Version: prev.Version,
			PrimarySupplierID: prev.PrimarySupplierID, ChangeReason: ReasonManualLock,
			Locked: true, Unchanged: true,
		}, nil
	}

	// ---- 换锁重算（复用 RecalcSKU 的不可变版本管道） ----
	supplierID := input.SupplierID
	outcome, err := s.costSvc.RecalcSKU(ctx, input.SkuID, ReasonManualLock, requestID, now,
		JobIdentity{OperatorID: op.OperatorID, OperatorRole: op.OperatorRole, SourceType: "HUMAN", RequestID: requestID},
		&supplierID)
	if err != nil {
		return nil, err
	}
	if outcome.Unchanged {
		// RecalcSKU 判了 UNCHANGED，说明连「锁定状态变」这一层都被值未变吃掉——
		// 这只可能是 Unchanged() 的 locked_manual 判定被移除（陷阱 2 的变异形态）。
		// 不静默构造前后值一致的假审计——直接报错让防线抓住（提示词变异验证 #1 防线）。
		return nil, errors.New("cost: RecalcSKU 判 UNCHANGED 但 prev 未锁定同对象（Unchanged() 锁定判定疑似被移除）")
	}

	// ---- 审计（同一事务；target_id = 新版本基线 id，由 RecalcSKU 直接回填——不再二次反查） ----
	before := map[string]any{"primary_supplier_id": 0, "locked_manual": false, "version": 0}
	if prev != nil {
		before["primary_supplier_id"] = prev.PrimarySupplierID
		before["locked_manual"] = prev.LockedManual
		before["version"] = prev.Version
	}
	if err := s.store.WriteLockAudit(ctx, LockAuditEntry{
		Now:          now,
		OperatorID:   op.OperatorID,
		OperatorRole: op.OperatorRole,
		TargetID:     outcome.BaselineID,
		Before:       before,
		After: map[string]any{
			"primary_supplier_id": input.SupplierID,
			"locked_manual":       true,
			"version":             outcome.Version,
		},
		Reason:    reason,
		RequestID: requestID,
	}); err != nil {
		return nil, fmt.Errorf("write lock audit: %w", err)
	}
	return &LockPrimaryResult{
		SkuID: input.SkuID, Version: outcome.Version,
		PrimarySupplierID: input.SupplierID, ChangeReason: ReasonManualLock,
		Locked: true, Unchanged: false,
	}, nil
}
