// Package supplier 的 lifecycle.go：报价到期闭环 + 异常检测 + 特权补录
// （05-quotes.md §11/§12/§13，按 5d 十条裁决口径）。
// 金额一律 decimal；grace_until 实时算（valid_to + quote_grace_days，不写列，5d 裁决 2）。
package supplier

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

// ---- 领域错误 ----

var (
	// ErrQuoteNotEffective confirm-remove 前置：非 EFFECTIVE。
	ErrQuoteNotEffective = errors.New("报价单不在生效状态，无法确认剔除")
	// ErrQuoteInGracePeriod confirm-remove 前置：未过宽限期。
	ErrQuoteInGracePeriod = errors.New("仍在宽限期内，暂不可剔除")
	// ErrQuoteAlreadyRemoved confirm=false 但已执行剔除。
	ErrQuoteAlreadyRemoved = errors.New("该报价单已执行剔除，无法撤销")
	// ErrRetroTimeNotPast effective_time 必须是过去时间。
	ErrRetroTimeNotPast = errors.New("补录的生效时间必须是过去时间（未来时间请用正常提交流程）")
	// ErrRetroReasonInvalid audit_reason 必填且 ≥10 字（按 rune）。
	ErrRetroReasonInvalid = errors.New("审计理由必填，至少 10 字")
	// ErrRetroSupplierNotFound 补录对象供应商不存在。
	ErrRetroSupplierNotFound = errors.New("供应商不存在")
)

// ---- DTO ----

// ExpiringQuoteQuery 是到期清单查询条件（§12.1）。
type ExpiringQuoteQuery struct {
	Days            int
	OnlySinglePoint bool
	Page            int
	Size            int
}

// ExpiringQuoteResult 是到期清单分页结果。
type ExpiringQuoteResult struct {
	List  []ExpiringQuoteItem `json:"list"`
	Total int64               `json:"total"`
	Page  int                 `json:"page"`
	Size  int                 `json:"size"`
}

// ExpiringQuoteItem 是到期清单的一行（§12.1 data.list[]）。
// grace_until 实时算（valid_to + quote_grace_days），不读列（5d 裁决 2）。
// SkuIDs 不参与序列化（仅 service 单点依赖判定用，repo 查询后直接填进对应行——
// 替代此前的包级 map，消除并发写 fatal error 与内存泄漏，见 5d 热修 P0-1）。
type ExpiringQuoteItem struct {
	ID              int64     `json:"id"`
	SupplierID      int64     `json:"supplier_id"`
	SupplierName    string    `json:"supplier_name"`
	VersionNo       int       `json:"version_no"`
	ValidTo         time.Time `json:"valid_to"`
	DaysLeft        int       `json:"days_left"`
	GraceUntil      time.Time `json:"grace_until"`
	InGrace         bool      `json:"in_grace"`
	RemoveConfirmed bool      `json:"remove_confirmed"`
	SinglePoint     bool      `json:"single_point"`
	AlertLevel      string    `json:"alert_level"` // NORMAL / HIGH / URGENT
	PendingRemove   bool      `json:"pending_remove"`
	SkuIDs          []int64   `json:"-"` // 不序列化；仅 service 层单点依赖判定用
}

// ConfirmRemoveInput 是确认剔除请求体（§12.2）。
type ConfirmRemoveInput struct {
	Confirm bool   `json:"confirm"`
	Reason  string `json:"reason"`
}

// ConfirmRemoveResult 是确认剔除响应。
type ConfirmRemoveResult struct {
	ID              int64     `json:"id"`
	Status          string    `json:"status"`
	RemoveConfirmed bool      `json:"remove_confirmed"`
	ExecutedAt      time.Time `json:"executed_at"`
}

// ScanResult 是扫描任务的响应。
type ScanResult struct {
	Type         string `json:"type"` // expire / expire-final / anomaly
	Scanned      int    `json:"scanned"`
	TodoCreated  int    `json:"todo_created"`
	AlertCreated int    `json:"alert_created"`
	Processed    int    `json:"processed"` // expire-final 用
}

// AnomalyQuery 是异常检测查询条件（§13）。
type AnomalyQuery struct {
	Days int
	Page int
	Size int
}

// AnomalyResult 是异常检测分页结果。
type AnomalyResult struct {
	List  []AnomalyItem `json:"list"`
	Total int64         `json:"total"`
	Page  int           `json:"page"`
	Size  int           `json:"size"`
}

// AnomalyItem 是一条异常（逐组件）。
type AnomalyItem struct {
	QuoteSheetID  int64     `json:"quote_sheet_id"`
	SupplierName  string    `json:"supplier_name"`
	SkuID         int64     `json:"sku_id"`
	SkuCode       string    `json:"sku_code"`
	ComponentType string    `json:"component_type"`
	UnitPrice     string    `json:"unit_price"`
	PrevPrice     *string   `json:"prev_price"`
	DeltaPct      *string   `json:"delta_pct"`
	MarketBest    *string   `json:"market_best"`
	MktDeltaPct   *string   `json:"mkt_delta_pct"`
	Reason        string    `json:"reason"` // PREV_DEVIATION / MARKET_DEVIATION / BOTH
	DetectedAt    time.Time `json:"detected_at"`
}

// RetroEffectiveInput 是特权补录请求体（§11，集合路径 + supplier_id 入体，5d 裁决 1）。
type RetroEffectiveInput struct {
	SupplierID    int64             `json:"supplier_id" binding:"required"`
	EffectiveTime time.Time         `json:"effective_time" binding:"required"`
	ValidTo       time.Time         `json:"valid_to" binding:"required"`
	AuditReason   string            `json:"audit_reason" binding:"required"`
	Items         []SubmitQuoteItem `json:"items" binding:"required"`
}

// RetroEffectiveResult 是补录响应（§11 data）。
type RetroEffectiveResult struct {
	ID                  int64  `json:"id"`
	Status              string `json:"status"`
	Retroactive         bool   `json:"retroactive"`
	ClosedPreviousID    *int64 `json:"closed_previous_id"`
	CostRecalcQueued    bool   `json:"cost_recalc_queued"`
	RetroCountThisMonth int    `json:"retro_count_this_month"`
	AlertCreated        bool   `json:"alert_created"`
}

// JobIdentity 是一次后台任务执行的统一身份（6a 补充要求 3）。
// worker 无人值守：OperatorID=0 / OperatorRole="SYSTEM" / SourceType="WORKER"，
// 每次执行生成一个 request_id（UUID）注入，写进 audit_log / task_job / todo_task 的
// request_id，把一次 job 跑出来的所有改动串起来（否则审计无法追溯）。
// 手动端点触发时传 HUMAN 身份（operator=当前操作员）。
type JobIdentity struct {
	OperatorID   int64
	OperatorRole string
	SourceType   string // HUMAN / WORKER
	RequestID    string
}

// SystemJob 返回 worker 默认身份（OperatorID=0 / SYSTEM / WORKER）。
func SystemJob(requestID string) JobIdentity {
	return JobIdentity{OperatorID: 0, OperatorRole: "SYSTEM", SourceType: "WORKER", RequestID: requestID}
}

// ---- 仓储接口 ----

// LifecycleStore 是到期闭环/异常/补录的仓储接口（GORM 实现见 internal/repo/supplier_lifecycle.go）。
type LifecycleStore interface {
	// FindQuotableSKUs 与 FindOfficialComponents 供 validateRetroItems 校验明细使用
	FindQuotableSKUs(ctx context.Context, skuIDs []int64) (map[int64]QuotableSKU, error)
	FindOfficialComponents(ctx context.Context, skuIDs []int64) (map[int64][]OfficialComponent, error)

	// ListExpiringQuotes 到期清单（EFFECTIVE 且 valid_to<=now+days），按 OwnerScope 行级过滤。
	ListExpiringQuotes(ctx context.Context, scope OwnerScope, q ExpiringQuoteQuery, now time.Time) ([]ExpiringQuoteItem, int64, error)
	// CountEffectiveSuppliersBySku 批量算单点依赖：每个 SKU 的 EFFECTIVE 报价涉及的**不同 supplier_id 数**
	// （跨供应商计数，不是跨报价单；同一供应商多版本只算 1——5d 裁决 3）。一次 GROUP BY。
	CountEffectiveSuppliersBySku(ctx context.Context, skuIDs []int64) (map[int64]int, error)
	// ConfirmRemoveQuote 确认剔除（同事务：EXPIRED + remove_confirmed + COST_RECALC(EXPIRE_REMOVE)
	// + event_outbox(quote.expired) + audit）。宽限期判定在 service 层（实时算）。
	ConfirmRemoveQuote(ctx context.Context, p ConfirmRemoveParams) (*ConfirmRemoveResult, error)
	// ExpireScanTargets 取扫描目标（EFFECTIVE 且 valid_to<=now+14d，14=单点依赖最大提前阈值）。
	ExpireScanTargets(ctx context.Context, now time.Time) ([]ExpiringScanTarget, error)
	// CreateExpireTodoOnce 去重写待办（同 biz_type+biz_id+级别 已有 OPEN 则跳过，5d 裁决 1）。
	// 返回是否新创建。ident 写入 request_id/created_by（6a 补充要求 3）。
	CreateExpireTodoOnce(ctx context.Context, sheetID, assigneeID int64, title, level string, now time.Time, ident JobIdentity) (bool, error)
	// CreateAlertOnce 去重写告警（同 alert_type+target+severity 已有 OPEN/HANDLING 则跳过）。
	// alertType 由调用方传入（5d 热修 P1-2）：到期 'QUOTE_EXPIRE'、异常 'QUOTE_ANOMALY'、
	// 补录激活失败 'QUOTE_ACTIVATE_FAILED'——否则不同类型告警同 target 同 severity 会互相吞掉。
	// ident 写入 request_id/created_by。
	CreateAlertOnce(ctx context.Context, alertType string, sheetID *int64, severity, message string, now time.Time, ident JobIdentity) (bool, error)
	// ListConfirmedPendingFinal 兜底：remove_confirmed=true AND status='EFFECTIVE'
	// （confirm-remove 已立即执行剔除，正常情况下查不到行——5d 裁决 2，勿"修复"）。
	ListConfirmedPendingFinal(ctx context.Context) ([]int64, error)
	// FinalExpireOne 兜底执行剔除（同事务 EXPIRED + COST_RECALC + event_outbox + audit）。
	// ident 统一身份（worker=SYSTEM/WORKER，手动端点=HUMAN）。
	FinalExpireOne(ctx context.Context, quoteID int64, now time.Time, ident JobIdentity) error
	// LoadAnomalyRows 取近 days 天 status ∈ (APPROVING,APPROVED_PENDING,EFFECTIVE) 的报价组件，
	// 附上一版价与市场最低价。**不做归属过滤**（§3.2 放开比价决议，5d 裁决 8）。
	LoadAnomalyRows(ctx context.Context, days int, now time.Time) ([]AnomalyRawRow, error)
	// FindSupplierByID 按 supplier_profile.id 取供应商（补录入参校验）。
	FindSupplierByID(ctx context.Context, supplierID int64) (*Supplier, error)
	// CreateRetroQuote 补录落库（source='RETRO', retroactive=true, status='APPROVED_PENDING'）。
	// 撞 uk_quote_pending 时返回带阻塞单 id/版本号的 ErrQuoteConflict（5d 裁决 3）。
	CreateRetroQuote(ctx context.Context, p CreateRetroParams) (*RetroSheetMeta, error)
	// CountRetroThisMonth 当月 retroactive=true 的条数。
	CountRetroThisMonth(ctx context.Context, now time.Time) (int, error)
	// CreateRetroLimitAlertOnce 当月已有 OPEN 的 RETRO_LIMIT 告警则跳过（去重）。
	CreateRetroLimitAlertOnce(ctx context.Context, count, limit int, now time.Time) (bool, error)
	// GetSysConfigInt 读 sys_config 整数配置（retro_monthly_limit / quote_grace_days）。
	GetSysConfigInt(ctx context.Context, key string) (int, error)
	// GetSysConfigDecimal 读 sys_config 数值配置（quote_anomaly_pct / quote_anomaly_mkt）。
	GetSysConfigDecimal(ctx context.Context, key string) (decimal.Decimal, error)
}

// RetroSheetMeta 是补录落库后的单头最小视图（供激活与响应）。
type RetroSheetMeta struct {
	ID         int64
	SupplierID int64
	VersionNo  int
	ValidFrom  time.Time
}

// ConfirmRemoveParams 是 ConfirmRemoveQuote 的参数。
type ConfirmRemoveParams struct {
	QuoteID      int64
	Confirm      bool
	Reason       string
	OperatorID   int64
	OperatorRole string
	RequestID    string
	Now          time.Time
}

// ExpiringScanTarget 是到期扫描目标的最小视图。
type ExpiringScanTarget struct {
	ID              int64
	SupplierID      int64
	SupplierName    string
	OwnerID         int64 // supplier.owner_procurement_operator_id（todo assignee）
	VersionNo       int
	ValidTo         time.Time
	RemoveConfirmed bool
	SkuIDs          []int64
}

// AnomalyRawRow 是异常检测的原始行（计算在 domain 层）。
type AnomalyRawRow struct {
	QuoteSheetID  int64
	SupplierName  string
	SkuID         int64
	SkuCode       string
	ComponentType string
	UnitPrice     decimal.Decimal
	PrevPrice     *decimal.Decimal
	MarketBest    *decimal.Decimal
}

// CreateRetroParams 是 CreateRetroQuote 的参数。
type CreateRetroParams struct {
	Supplier     *Supplier
	OperatorID   int64 // internal_staff.id（submitted_by，非供应商）
	OperatorRole string
	RequestID    string
	ValidFrom    time.Time // = effective_time（过去，不钳制）
	ValidTo      time.Time
	AuditReason  string
	Items        []CreateQuoteItem
	SubmittedAt  time.Time
}

// ---- 领域服务 ----

// LifecycleService 是到期闭环/异常/补录的领域服务。
type LifecycleService struct {
	store    LifecycleStore
	activate *ApproveService // retro 复用 5b 激活（5d 裁决 6）
	now      func() time.Time
}

// NewLifecycleService 构造生命周期服务。
func NewLifecycleService(store LifecycleStore, activate *ApproveService) *LifecycleService {
	return &LifecycleService{store: store, activate: activate, now: func() time.Time { return time.Now().UTC() }}
}

// graceDays 读 quote_grace_days（默认 3）。
func (s *LifecycleService) graceDays(ctx context.Context) int {
	d, err := s.store.GetSysConfigInt(ctx, "quote_grace_days")
	if err != nil || d <= 0 {
		return 3
	}
	return d
}

// ListExpiring 到期清单（§12.1）：行级过滤 + 实时 grace_until + 单点依赖批量判定。
func (s *LifecycleService) ListExpiring(ctx context.Context, scope OwnerScope, q ExpiringQuoteQuery) (*ExpiringQuoteResult, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 || q.Size > 100 {
		q.Size = 20
	}
	if q.Days <= 0 {
		q.Days = 7
	}
	now := s.now()
	graceDays := s.graceDays(ctx)

	items, total, err := s.store.ListExpiringQuotes(ctx, scope, q, now)
	if err != nil {
		return nil, err
	}
	// 拷贝一份再装配：repo 返回的切片可能被并发请求共享底层数组（fake/真实实现都可能复用），
	// 直接写 items[i] 会触发 data race（5d 热修 P0-1 回归单测实测抓到）。
	items = append([]ExpiringQuoteItem(nil), items...)

	// 单点依赖批量判定（一次 GROUP BY，不每行查——5d 裁决 3）
	skuIDSet := map[int64]bool{}
	var skuIDs []int64
	for _, it := range items {
		for _, id := range it.SkuIDs {
			if !skuIDSet[id] {
				skuIDSet[id] = true
				skuIDs = append(skuIDs, id)
			}
		}
	}
	supplierCount, err := s.store.CountEffectiveSuppliersBySku(ctx, skuIDs)
	if err != nil {
		return nil, err
	}

	for i := range items {
		it := &items[i]
		it.DaysLeft = int(it.ValidTo.Sub(now).Hours() / 24)
		it.GraceUntil = it.ValidTo.AddDate(0, 0, graceDays) // 实时算，不写列
		it.InGrace = now.After(it.ValidTo) && !now.After(it.GraceUntil)
		// 单点依赖：该单内任一 SKU 只有一家有效报价
		for _, id := range it.SkuIDs {
			if supplierCount[id] == 1 {
				it.SinglePoint = true
				break
			}
		}
		it.AlertLevel, it.PendingRemove = alertLevelOf(it.DaysLeft, it.InGrace, now.After(it.GraceUntil), it.RemoveConfirmed, it.SinglePoint)
	}

	// only_single_point 过滤（内存过滤，量小；total 跟随过滤后——5d 热修 P3-10 结论 (a)）
	list := items
	if q.OnlySinglePoint {
		filtered := make([]ExpiringQuoteItem, 0, len(items))
		for _, it := range items {
			if it.SinglePoint {
				filtered = append(filtered, it)
			}
		}
		list = filtered
		total = int64(len(filtered))
	}
	return &ExpiringQuoteResult{List: list, Total: total, Page: q.Page, Size: q.Size}, nil
}

// alertLevelOf 定级（契约 §12.1 表 + 5d 热修 P3-9 结论 (a)）。
// 单点依赖：**只提前阈值（14/7 天），不再额外升一级**——契约原文「阈值提前至 14/7 天，
// 且级别升一级」二义（阈值提前 + escalate 双重升级会让 HIGH 档在单点时不可达，highThresh=14
// 变成死逻辑）。结论 (a)：只提前阈值，不额外升级（契约 §12.1 已定稿决策表同步）。
// inGrace 参数保留给未来宽限期内的细分定级（当前 URGENT 分支已覆盖）。
func alertLevelOf(daysLeft int, inGrace, pastGrace, removeConfirmed, singlePoint bool) (level string, pendingRemove bool) {
	_ = inGrace // 宽限期内的判定由 pastGrace+removeConfirmed 覆盖；保留参数签名稳定
	highThresh, urgentThresh := 7, 0
	if singlePoint {
		highThresh, urgentThresh = 14, 7 // 单点依赖：只提前阈值（结论 a），不再额外升一级
	}
	switch {
	case pastGrace && !removeConfirmed:
		return "URGENT", true
	case daysLeft < urgentThresh || (daysLeft < 0 && !pastGrace):
		return "URGENT", false
	case daysLeft <= highThresh:
		return "HIGH", false
	default:
		return "NORMAL", false
	}
}

// alertSeverityOf alert_level → alert.severity 映射（5d 裁决 4：不直接写 NORMAL 进 severity）。
func alertSeverityOf(level string) string {
	switch level {
	case "URGENT":
		return "CRITICAL"
	case "HIGH":
		return "HIGH"
	default:
		return "LOW"
	}
}

// TodoPriorityOf alert_level → todo_task.priority 映射（5d 热修 P1-3）：
// HIGH → 'MID'、URGENT → 'HIGH'（NORMAL 不生成待办，不受影响）。
// 去重键用 (biz_type, biz_id, priority) 而非 title——title 里含「剩余 N 天」，
// 天数变化会产生重复待办；级别升级（HIGH→URGENT）时 priority 变化会再生成一条，正是映射的意义。
func TodoPriorityOf(level string) string {
	if level == "URGENT" {
		return "HIGH"
	}
	return "MID"
}

// ConfirmRemove 人工确认剔除（§12.2）：宽限期在 repo 层实时算（valid_to + quote_grace_days），
// 立即执行剔除（5d 裁决 2：本阶段无 ticker，只打标等于永远不执行）。
// 归属校验在 handler 层（复用 ApproveService 的 checkScope）。
func (s *LifecycleService) ConfirmRemove(ctx context.Context, quoteID int64, in ConfirmRemoveInput, operatorID int64, operatorRole, requestID string) (*ConfirmRemoveResult, error) {
	return s.store.ConfirmRemoveQuote(ctx, ConfirmRemoveParams{
		QuoteID: quoteID, Confirm: in.Confirm, Reason: in.Reason,
		OperatorID: operatorID, OperatorRole: operatorRole, RequestID: requestID, Now: s.now(),
	})
}

// RunExpireScan 到期扫描（§12.3，每日 07:00）：T-7/T-3 生成 todo + alert（去重）。
func (s *LifecycleService) RunExpireScan(ctx context.Context, ident JobIdentity) (*ScanResult, error) {
	now := s.now()
	graceDays := s.graceDays(ctx)
	targets, err := s.store.ExpireScanTargets(ctx, now)
	if err != nil {
		return nil, err
	}

	// 单点依赖批量判定
	skuIDSet := map[int64]bool{}
	var skuIDs []int64
	for _, t := range targets {
		for _, id := range t.SkuIDs {
			if !skuIDSet[id] {
				skuIDSet[id] = true
				skuIDs = append(skuIDs, id)
			}
		}
	}
	supplierCount, err := s.store.CountEffectiveSuppliersBySku(ctx, skuIDs)
	if err != nil {
		return nil, err
	}

	res := &ScanResult{Type: "expire", Scanned: len(targets)}
	for _, t := range targets {
		daysLeft := int(t.ValidTo.Sub(now).Hours() / 24)
		graceUntil := t.ValidTo.AddDate(0, 0, graceDays)
		inGrace := now.After(t.ValidTo) && !now.After(graceUntil)
		singlePoint := false
		for _, id := range t.SkuIDs {
			if supplierCount[id] == 1 {
				singlePoint = true
				break
			}
		}
		level, pendingRemove := alertLevelOf(daysLeft, inGrace, now.After(graceUntil), t.RemoveConfirmed, singlePoint)
		if level == "NORMAL" {
			continue // 超 7 天（非单点）不生成待办
		}

		title := fmt.Sprintf("报价到期预警：%s v%d（剩余 %d 天%s）", t.SupplierName, t.VersionNo, daysLeft, map[bool]string{true: "，单点依赖", false: ""}[singlePoint])
		if pendingRemove {
			title = fmt.Sprintf("报价待剔除确认：%s v%d（已过宽限期）", t.SupplierName, t.VersionNo)
		}
		created, err := s.store.CreateExpireTodoOnce(ctx, t.ID, t.OwnerID, title, level, now, ident)
		if err != nil {
			return nil, err
		}
		if created {
			res.TodoCreated++
		}

		msg := fmt.Sprintf("报价单 #%d（%s v%d）%s", t.ID, t.SupplierName, t.VersionNo, title)
		created, err = s.store.CreateAlertOnce(ctx, "QUOTE_EXPIRE", &t.ID, alertSeverityOf(level), msg, now, ident)
		if err != nil {
			return nil, err
		}
		if created {
			res.AlertCreated++
		}
	}
	return res, nil
}

// RunExpireFinal 兜底执行剔除（§12.3，每日 07:10）。
// 注意（5d 裁决 2）：confirm-remove 已立即执行剔除，本任务筛
// remove_confirmed=true AND status='EFFECTIVE'，**正常情况下查不到任何行**——
// 这是兜底语义，不是 bug，勿"修复"成把 EFFECTIVE 的单误剔除。
func (s *LifecycleService) RunExpireFinal(ctx context.Context, ident JobIdentity) (*ScanResult, error) {
	now := s.now()
	ids, err := s.store.ListConfirmedPendingFinal(ctx)
	if err != nil {
		return nil, err
	}
	res := &ScanResult{Type: "expire-final", Scanned: len(ids)}
	for _, id := range ids {
		if err := s.store.FinalExpireOne(ctx, id, now, ident); err != nil {
			continue // 单条失败不中断整批
		}
		res.Processed++
	}
	return res, nil
}

// RunAnomalyScan 异常扫描（§13，每日 07:20）：实时计算不落表，写 alert。
func (s *LifecycleService) RunAnomalyScan(ctx context.Context, days int, ident JobIdentity) (*ScanResult, error) {
	items, err := s.detectAnomalies(ctx, days)
	if err != nil {
		return nil, err
	}
	res := &ScanResult{Type: "anomaly", Scanned: len(items)}
	now := s.now()
	for _, it := range items {
		msg := fmt.Sprintf("报价异常：%s sku=%s %s 本次 %s（%s）",
			it.SupplierName, it.SkuCode, it.ComponentType, it.UnitPrice, it.Reason)
		created, err := s.store.CreateAlertOnce(ctx, "QUOTE_ANOMALY", &it.QuoteSheetID, "HIGH", msg, now, ident)
		if err != nil {
			return nil, err
		}
		if created {
			res.AlertCreated++
		}
	}
	return res, nil
}

// ListAnomalies 异常清单（§13，实时计算，**不做归属过滤**——5d 裁决 8）。
func (s *LifecycleService) ListAnomalies(ctx context.Context, q AnomalyQuery) (*AnomalyResult, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 || q.Size > 100 {
		q.Size = 20
	}
	if q.Days <= 0 {
		q.Days = 30
	}
	items, err := s.detectAnomalies(ctx, q.Days)
	if err != nil {
		return nil, err
	}
	total := int64(len(items))
	start := (q.Page - 1) * q.Size
	start = min(start, len(items))
	end := min(start+q.Size, len(items))
	return &AnomalyResult{List: items[start:end], Total: total, Page: q.Page, Size: q.Size}, nil
}

// detectAnomalies 逐组件判定（§13：偏离上一版 > quote_anomaly_pct 或偏离市场最低 > quote_anomaly_mkt）。
func (s *LifecycleService) detectAnomalies(ctx context.Context, days int) ([]AnomalyItem, error) {
	now := s.now()
	rows, err := s.store.LoadAnomalyRows(ctx, days, now)
	if err != nil {
		return nil, err
	}
	prevThresh, err := s.store.GetSysConfigDecimal(ctx, "quote_anomaly_pct")
	if err != nil {
		prevThresh = decimal.NewFromFloat(0.20)
	}
	mktThresh, err := s.store.GetSysConfigDecimal(ctx, "quote_anomaly_mkt")
	if err != nil {
		mktThresh = decimal.NewFromFloat(0.30)
	}

	var out []AnomalyItem
	for _, r := range rows {
		var prevDelta, mktDelta *decimal.Decimal
		prevHit, mktHit := false, false
		if r.PrevPrice != nil && !r.PrevPrice.IsZero() {
			d := r.UnitPrice.Sub(*r.PrevPrice).Div(*r.PrevPrice).Abs()
			prevDelta = &d
			prevHit = d.GreaterThan(prevThresh) // 严格大于（恰好等于阈值不触发）
		}
		if r.MarketBest != nil && !r.MarketBest.IsZero() {
			d := r.UnitPrice.Sub(*r.MarketBest).Div(*r.MarketBest)
			if d.GreaterThan(mktThresh) {
				mktDelta = &d
				mktHit = true
			}
		}
		if !prevHit && !mktHit {
			continue
		}
		reason := "PREV_DEVIATION"
		if prevHit && mktHit {
			reason = "BOTH"
		} else if mktHit {
			reason = "MARKET_DEVIATION"
		}
		out = append(out, AnomalyItem{
			QuoteSheetID:  r.QuoteSheetID,
			SupplierName:  r.SupplierName,
			SkuID:         r.SkuID,
			SkuCode:       r.SkuCode,
			ComponentType: r.ComponentType,
			UnitPrice:     r.UnitPrice.StringFixed(8), // 与报价详情/diff 口径一致（5d 热修 P2-8）
			PrevPrice:     decPtrToStrPtrFixed8(r.PrevPrice),
			DeltaPct:      decPtrRounded(prevDelta),
			MarketBest:    decPtrToStrPtrFixed8(r.MarketBest),
			MktDeltaPct:   decPtrRounded(mktDelta),
			Reason:        reason,
			DetectedAt:    now,
		})
	}
	return out, nil
}

// decPtrRounded 保留 4 位小数的字符串指针（delta_pct 口径与 5b diff 一致）。
func decPtrRounded(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.Round(4).String()
	return &s
}

// decPtrToStrPtrFixed8 金额字符串指针（8 位小数，与报价详情/diff 口径一致——5d 热修 P2-8）。
func decPtrToStrPtrFixed8(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.StringFixed(8)
	return &s
}

// RetroEffective 特权补录（§11，集合路径 + supplier_id 入体，5d 裁决 1）。
// 免审批：创建即 APPROVED_PENDING，事务提交后复用 5b 激活（valid_from≤now → EFFECTIVE）。
// 激活失败时写 alert(QUOTE_ACTIVATE_FAILED) + 返回明确错误（不静默留孤儿——5d 额外裁决）。
func (s *LifecycleService) RetroEffective(ctx context.Context, in RetroEffectiveInput, operatorID int64, operatorRole, requestID string) (*RetroEffectiveResult, error) {
	now := s.now()

	// 校验：过去时间 + audit_reason ≥10 rune
	if !in.EffectiveTime.UTC().Before(now) {
		return nil, ErrRetroTimeNotPast
	}
	if utf8.RuneCountInString(in.AuditReason) < 10 {
		return nil, ErrRetroReasonInvalid
	}
	if !in.ValidTo.UTC().After(in.EffectiveTime.UTC()) {
		return nil, ErrQuoteValidToInvalid
	}

	sup, err := s.store.FindSupplierByID(ctx, in.SupplierID)
	if err != nil {
		return nil, err
	}
	if sup == nil {
		return nil, ErrRetroSupplierNotFound
	}

	// items 校验复用提交链路的组件/约束/自洽规则（valid_from 不钳制——补录特权就是填过去时间）
	normItems, err := s.validateRetroItems(ctx, in.Items)
	if err != nil {
		return nil, err
	}

	meta, err := s.store.CreateRetroQuote(ctx, CreateRetroParams{
		Supplier: sup, OperatorID: operatorID, OperatorRole: operatorRole, RequestID: requestID,
		ValidFrom: in.EffectiveTime.UTC(), ValidTo: in.ValidTo.UTC(),
		AuditReason: in.AuditReason, Items: normItems, SubmittedAt: now,
	})
	if err != nil {
		return nil, err
	}

	// 事务提交后激活（复用 5b；失败写 alert + 返回明确错误，不静默留孤儿）
	actRes, actErr := s.activate.ActivateDueQuotes(ctx, ActivateParams{
		OperatorID: operatorID, OperatorRole: operatorRole, SourceType: "HUMAN", RequestID: requestID, Now: now,
	})

	// 月度计数与超限告警
	count, err := s.store.CountRetroThisMonth(ctx, now)
	if err != nil {
		return nil, err
	}
	limit, err := s.store.GetSysConfigInt(ctx, "retro_monthly_limit")
	if err != nil || limit <= 0 {
		limit = 5
	}
	alertCreated := false
	if count > limit {
		// 禁止静默吞错（5d 热修纪律 2）：告警写不进去必须显式返回错误，
		// 否则线上超限了没人知道。
		created, aerr := s.store.CreateRetroLimitAlertOnce(ctx, count, limit, now)
		if aerr != nil {
			return nil, fmt.Errorf("补录已落库（id=%d）但超限告警写入失败：%w", meta.ID, aerr)
		}
		alertCreated = created
	}

	res := &RetroEffectiveResult{
		ID: meta.ID, Status: "APPROVED_PENDING", Retroactive: true,
		CostRecalcQueued: false, RetroCountThisMonth: count, AlertCreated: alertCreated,
	}

	if actErr != nil {
		// 激活失败：写 alert + 明确错误（事务已提交，不静默留 APPROVED_PENDING 孤儿）
		_, _ = s.store.CreateAlertOnce(ctx, "QUOTE_ACTIVATE_FAILED", &meta.ID, "CRITICAL",
			fmt.Sprintf("补录单 #%d 激活失败：%v", meta.ID, actErr), now,
			JobIdentity{OperatorID: operatorID, OperatorRole: operatorRole, SourceType: "HUMAN", RequestID: requestID})
		return res, fmt.Errorf("补录已落库（id=%d）但激活失败：%w", meta.ID, actErr)
	}
	for _, item := range actRes.Items {
		if item.ID == meta.ID {
			res.Status = "EFFECTIVE"
			res.ClosedPreviousID = item.ClosedPreviousID
			res.CostRecalcQueued = true
		}
	}
	return res, nil
}

// validateRetroItems 补录明细校验：复用提交链路的组件类型/约束/自洽规则。
// 与 submitQuoteWithSource 的差异：valid_from 不钳制、跳过非终态互斥（补录直接进
// APPROVED_PENDING，uk_quote_pending 唯一索引兜底——撞了由 repo 返回带阻塞单信息的 409）。
func (s *LifecycleService) validateRetroItems(ctx context.Context, items []SubmitQuoteItem) ([]CreateQuoteItem, error) {
	if len(items) == 0 {
		return nil, ErrQuoteItemsEmpty
	}
	if len(items) > maxQuoteItems {
		return nil, ErrQuoteItemsTooMany
	}
	seen := map[int64]bool{}
	skuIDs := make([]int64, 0, len(items))
	for _, it := range items {
		if seen[it.SKUID] {
			return nil, fmt.Errorf("%w：sku_id=%d", ErrQuoteDuplicateSku, it.SKUID)
		}
		seen[it.SKUID] = true
		skuIDs = append(skuIDs, it.SKUID)
		if err := validateComponents(it.Components); err != nil {
			return nil, err
		}
		if err := validateConstraints(it.Constraints); err != nil {
			return nil, err
		}
	}
	skus, err := s.store.FindQuotableSKUs(ctx, skuIDs)
	if err != nil {
		return nil, err
	}
	for _, id := range skuIDs {
		if _, ok := skus[id]; !ok {
			return nil, fmt.Errorf("%w：sku_id=%d", ErrQuoteSkuNotQuotable, id)
		}
	}
	// 官方价与自洽复核：补录可填倍率（按当前官方价）或绝对价
	officials, err := s.store.FindOfficialComponents(ctx, skuIDs)
	if err != nil {
		return nil, err
	}
	norm := make([]CreateQuoteItem, 0, len(items))
	for _, it := range items {
		sku := skus[it.SKUID]
		ni := CreateQuoteItem{SKUID: it.SKUID, SkuCode: sku.SkuCode, Currency: sku.NativeCurrency, Constraints: it.Constraints}
		if sku.NativeCurrency == "USD" {
			if it.FxTier == nil || *it.FxTier == "" {
				return nil, fmt.Errorf("%w：sku=%s", ErrQuoteFxTierRequired, sku.SkuCode)
			}
			canonical, ok := normalizeFxTier(*it.FxTier)
			if !ok {
				return nil, fmt.Errorf("%w：sku=%s fx_tier=%q", ErrQuoteFxTierInvalid, sku.SkuCode, *it.FxTier)
			}
			ni.FxTier = &canonical
		}
		officialByType := map[string]decimal.Decimal{}
		for _, oc := range officials[it.SKUID] {
			officialByType[oc.ComponentType] = oc.UnitPrice
		}
		for _, c := range it.Components {
			nc := CreateQuoteComponent{ComponentType: c.ComponentType}
			price, perr := decimal.NewFromString(c.UnitPrice)
			if perr != nil {
				return nil, fmt.Errorf("%w：sku=%s %s unit_price=%q", ErrQuotePriceInvalid, sku.SkuCode, c.ComponentType, c.UnitPrice)
			}
			nc.UnitPrice = price
			official, hasOfficial := officialByType[c.ComponentType]
			if c.Multiplier != nil && *c.Multiplier != "" {
				if !hasOfficial {
					return nil, fmt.Errorf("%w：sku=%s %s", ErrQuoteMultiplierForbidden, sku.SkuCode, c.ComponentType)
				}
				mult, merr := decimal.NewFromString(*c.Multiplier)
				if merr != nil {
					return nil, fmt.Errorf("%w：sku=%s %s multiplier=%q", ErrQuotePriceInvalid, sku.SkuCode, c.ComponentType, *c.Multiplier)
				}
				if price.Sub(official.Mul(mult)).Abs().Cmp(priceTolerance) > 0 {
					return nil, fmt.Errorf("%w：sku=%s %s", ErrQuotePriceInconsistent, sku.SkuCode, c.ComponentType)
				}
				nc.Multiplier = &mult
			}
			ni.Components = append(ni.Components, nc)
		}
		norm = append(norm, ni)
	}
	return norm, nil
}
