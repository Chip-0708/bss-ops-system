package pricing

// Package pricing 的 publish_service.go：价目表发布 + 回滚（08-pricing.md §3/§4，阶段 8b-1）。
//
// 裁决（全部来自 stage8b-1 提示词，与真实 DDL 000005/000006 对齐）：
//   - A1：change_request.sku_id DROP NOT NULL（迁移 000021）——发布/回滚是 price_book 级，
//     sku_id=NULL，真实主体写 payload jsonb。
//   - A2：状态机用 §2.6 字典值：DRAFT → APPROVING → EFFECTIVE（原地升格，version_no 不变）；
//     审批驳回回 DRAFT；旧版被替代 → RETIRED + valid_to。
//   - A3：发布 = 草稿行原地升格（不 INSERT 新行，version_no 不变）；只有回滚才复制新行。
//   - B1-2：回滚用当前成本基线重算的 floor 校验（不是历史快照的 floor），违规 → 409。
//   - B2：审批角色链 PRICING_OP → FINANCE（FINANCE 无 M7 权限也能审批——通用审批入口
//     不挂模块权限点，审批权由 approval_step.required_role 决定）。
//   - B3：只支持 IMMEDIATE（effective_time > now → 400）；GRAY → 400。
//   - B4：floor_violation 是 bool + 硬 409（必须重新定价或走特价审批）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"model_bss/internal/domain/cost"
	"time"

	"github.com/shopspring/decimal"
)

// ---- 枚举（8b-1 新增，与 §2.6 状态字典对齐）----

// price_book 状态（§2.6：草稿/审批中/待生效/已生效/已冻结/已替代）。
const (
	BookStatusApproving        = "APPROVING"         // 审批中（8b-1 新增）
	BookStatusPendingEffective = "PENDING_EFFECTIVE" // 待生效（SCHEDULED 用，本批不注册 worker）
	BookStatusRetired          = "RETIRED"           // 已替代（被新版本替换的旧版）
)

// change_type 枚举（8b-1 新增，约定优先于 DDL 注释——遗留 8b-1-②）。
const (
	ChangePriceBookPublish  = "PRICE_BOOK_PUBLISH"
	ChangePriceBookRollback = "PRICE_BOOK_ROLLBACK"
)

// 发布模式（契约 §3）。
const (
	ModeImmediate = "IMMEDIATE"
	ModeScheduled = "SCHEDULED" // 本批不支持（>now → 400）
	ModeGray      = "GRAY"      // 本批不支持（→ 400）
)

// ---- 领域错误（errors.Is 可判定）----

var (
	ErrPriceBookNotFound      = errors.New("价目表不存在")                                          //nolint:revive // 变量组错误，沿用8b-1命名
	ErrPriceBookNotDraft      = errors.New("价目表不是草稿状态（只有 DRAFT 可发布）")                         //nolint:revive // 变量组错误，沿用8b-1命名
	ErrPriceBookNotEffective  = errors.New("目标版本不是当前生效版（回滚目标必须是历史版本）")                        //nolint:revive
	ErrFloorViolation         = errors.New("存在 floor_violation=true 的条目，必须先处理红线")             //nolint:revive
	ErrInvalidMode            = errors.New("mode 非法（本批只支持 IMMEDIATE）")                        //nolint:revive
	ErrScheduledNotSupported  = errors.New("SCHEDULED 预约生效本批不支持（effective_time > now → 400）") //nolint:revive
	ErrGrayNotSupported       = errors.New("GRAY 灰度发布本批不支持（契约 §6 待裁决点 2）")                    //nolint:revive
	ErrRollbackTargetNotFound = errors.New("回滚目标版本不存在")                                       //nolint:revive
	ErrRollbackToSelf         = errors.New("回滚目标就是当前生效版（no-op）")                              //nolint:revive
)

// ---- 领域模型 ----

// PublishInput 是发布的输入。
type PublishInput struct {
	PriceBookID   int64
	EffectiveTime time.Time
	Mode          string // IMMEDIATE / SCHEDULED / GRAY（本批只支持 IMMEDIATE）
	GrayPercent   int    // 本批不用（GRAY 直接 400）
}

// RollbackInput 是回滚的输入。
type RollbackInput struct {
	PriceBookID     int64
	TargetVersionNo int
	Reason          string
}

// PublishResult 是发布/回滚的响应 data。
type PublishResult struct {
	PriceBookID     int64     `json:"price_book_id"`
	VersionNo       int       `json:"version_no"`
	ChangeRequestID int64     `json:"change_request_id"`
	StepCount       int       `json:"step_count"`
	EffectiveTime   time.Time `json:"effective_time"`
	Status          string    `json:"status"` // 发布/回滚后的 price_book.status
}

// PriceBookInfo 是 price_book 的读侧信息（发布/回滚校验用）。
type PriceBookInfo struct {
	ID            int64
	VersionNo     int
	LevelCode     string
	Status        string
	EffectiveTime *time.Time
	ValidTo       *time.Time
	RollbackOf    *int64
	DiffReport    []byte // jsonb
	CreatedBy     int64
}

// PriceBookItemInfo 是 price_book_item 的读侧信息（回滚复制用）。
type PriceBookItemInfo struct {
	ID              int64
	PriceBookID     int64
	SkuID           int64
	Currency        string
	FloorPrice      string
	PolicyID        int64
	BaselineVersion int
}

// PriceBookComponentInfo 是 price_book_component 的读侧信息（回滚复制用）。
type PriceBookComponentInfo struct {
	ID              int64
	PriceBookItemID int64
	ComponentType   string
	UnitPrice       string
}

// ---- 服务 ----

// PublishStore 是发布/回滚的仓储接口（GORM 实现见 internal/repo/pricing_publish.go）。
type PublishStore interface {
	// 读侧
	LoadPriceBookByID(ctx context.Context, id int64) (*PriceBookInfo, error)
	LoadPriceBookByVersion(ctx context.Context, levelCode string, versionNo int) (*PriceBookInfo, error)
	LoadCurrentEffective(ctx context.Context, levelCode string) (*PriceBookInfo, error)
	LoadPriceBookItems(ctx context.Context, priceBookID int64) ([]PriceBookItemInfo, error)
	LoadPriceBookComponents(ctx context.Context, priceBookItemIDs []int64) ([]PriceBookComponentInfo, error)
	LoadCurrentUnitCosts(ctx context.Context, skuIDs []int64) (map[int64]UnitCostInfo, error) // 复用 8a
	LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error)                          // 复用 8a

	// 写侧（单事务）
	Publish(ctx context.Context, in PublishTxInput, operatorID int64, requestID string) (*PublishResult, error)
	ApplyPriceBookPublish(ctx context.Context, p ApplyPublishParams) error
}

// PublishTxInput 是 Publish 单事务的输入。
type PublishTxInput struct {
	PriceBookID   int64
	ChangeType    string // PRICE_BOOK_PUBLISH / PRICE_BOOK_ROLLBACK
	Payload       []byte // change_request.payload（jsonb）
	EffectiveTime time.Time
	OperatorID    int64
	RequestID     string
	// 回滚专用（发布时为零值）
	RollbackOf     *int64
	RollbackReason string
}

// ApplyPublishParams 是 ApplyPriceBookPublish 的入参（审批全通过后调用）。
type ApplyPublishParams struct {
	ChangeRequestID int64
	Payload         []byte // change_request.payload
	OperatorID      int64
	OperatorRole    string
	RequestID       string
}

// PublishService 是发布/回滚服务（无状态，可并发）。
type PublishService struct {
	store PublishStore
	now   func() time.Time
}

// NewPublishService 构造服务。now 为 nil 时用 time.Now。
func NewPublishService(store PublishStore, now func() time.Time) *PublishService {
	if now == nil {
		now = time.Now
	}
	return &PublishService{store: store, now: now}
}

// Publish 发布价目表（草稿 → 审批中）。
func (s *PublishService) Publish(ctx context.Context, in PublishInput, operatorID int64, requestID string) (*PublishResult, error) {
	// 1. 校验 mode（B3：只支持 IMMEDIATE）。
	if in.Mode == ModeGray {
		return nil, ErrGrayNotSupported
	}
	if in.Mode == ModeScheduled || in.EffectiveTime.After(s.now()) {
		return nil, ErrScheduledNotSupported
	}
	if in.Mode != ModeImmediate {
		return nil, ErrInvalidMode
	}

	// 2. 加载草稿（A2：只有 DRAFT 可发布）。
	book, err := s.store.LoadPriceBookByID(ctx, in.PriceBookID)
	if err != nil {
		return nil, err
	}
	if book.Status != BookStatusDraft {
		return nil, ErrPriceBookNotDraft
	}

	// 3. 校验 floor_violation（B4：硬 409）。
	var diffReport []DiffItem
	if len(book.DiffReport) > 0 {
		if err := json.Unmarshal(book.DiffReport, &diffReport); err != nil {
			return nil, fmt.Errorf("decode diff_report: %w", err)
		}
	}
	for _, item := range diffReport {
		if item.FloorViolation {
			return nil, ErrFloorViolation
		}
	}

	// 4. 构造 payload（A1：sku_id=NULL，真实主体写 payload）。
	skuIDs := make([]int64, 0, len(diffReport))
	for _, item := range diffReport {
		skuIDs = append(skuIDs, item.SKUID)
	}
	payload, err := json.Marshal(map[string]any{
		"price_book_id":  in.PriceBookID,
		"level_code":     book.LevelCode,
		"version_no":     book.VersionNo,
		"sku_ids":        skuIDs,
		"diff_report":    diffReport,
		"effective_time": in.EffectiveTime.Format(time.RFC3339),
		"mode":           in.Mode,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	// 5. 单事务：change_request + 2 步 approval_step + 草稿 DRAFT → APPROVING。
	return s.store.Publish(ctx, PublishTxInput{
		PriceBookID:   in.PriceBookID,
		ChangeType:    ChangePriceBookPublish,
		Payload:       payload,
		EffectiveTime: in.EffectiveTime,
		OperatorID:    operatorID,
		RequestID:     requestID,
	}, operatorID, requestID)
}

// Rollback 回滚价目表（历史快照重发一个新版本）。
func (s *PublishService) Rollback(ctx context.Context, in RollbackInput, operatorID int64, requestID string) (*PublishResult, error) {
	// 1. 加载当前生效版（回滚的基准）。
	current, err := s.store.LoadPriceBookByID(ctx, in.PriceBookID)
	if err != nil {
		return nil, err
	}
	if current.Status != BookStatusEffective {
		return nil, ErrPriceBookNotEffective
	}

	// 2. 加载目标版本（必须存在且属于同一 level_code）。
	target, err := s.store.LoadPriceBookByVersion(ctx, current.LevelCode, in.TargetVersionNo)
	if err != nil {
		return nil, err
	}
	if target.ID == current.ID {
		return nil, ErrRollbackToSelf
	}

	// 3. 加载目标版本的 items + components（复制用）。
	targetItems, err := s.store.LoadPriceBookItems(ctx, target.ID)
	if err != nil {
		return nil, fmt.Errorf("load target items: %w", err)
	}
	if len(targetItems) == 0 {
		return nil, fmt.Errorf("target version %d has no items", in.TargetVersionNo)
	}
	itemIDs := make([]int64, 0, len(targetItems))
	for _, it := range targetItems {
		itemIDs = append(itemIDs, it.ID)
	}
	targetComponents, err := s.store.LoadPriceBookComponents(ctx, itemIDs)
	if err != nil {
		return nil, fmt.Errorf("load target components: %w", err)
	}

	// 4. 用当前成本基线重算的 floor 校验目标版本的售价（B1-2）。
	skuIDs := make([]int64, 0, len(targetItems))
	for _, it := range targetItems {
		skuIDs = append(skuIDs, it.SkuID)
	}
	unitCosts, err := s.store.LoadCurrentUnitCosts(ctx, skuIDs)
	if err != nil {
		return nil, fmt.Errorf("load current unit costs: %w", err)
	}
	margin, err := s.store.LoadMinGrossMargin(ctx)
	if err != nil {
		return nil, fmt.Errorf("load min_gross_margin: %w", err)
	}

	// 按 sku_id 索引目标版本的售价（代表组件）。
	targetPrices := make(map[int64]string, len(targetComponents))
	for _, comp := range targetComponents {
		// 找该 component 对应的 item 的 sku_id。
		for _, it := range targetItems {
			if it.ID == comp.PriceBookItemID {
				targetPrices[it.SkuID] = comp.UnitPrice
				break
			}
		}
	}

	// 校验每个 SKU 的售价 >= 当前 floor。
	for _, it := range targetItems {
		uc, ok := unitCosts[it.SkuID]
		if !ok {
			return nil, fmt.Errorf("sku=%d 无当前成本基线，无法校验回滚红线", it.SkuID)
		}
		floor, ferr := cost.Floor(uc.UnitCost, margin)
		if ferr != nil {
			return nil, fmt.Errorf("floor sku=%d: %w", it.SkuID, ferr)
		}
		floor = ceil8(floor)
		priceStr, hasPrice := targetPrices[it.SkuID]
		if !hasPrice {
			return nil, fmt.Errorf("sku=%d 在目标版本无售价", it.SkuID)
		}
		price, perr := decimal.NewFromString(priceStr)
		if perr != nil {
			return nil, fmt.Errorf("sku=%d 目标售价 %q 非法: %w", it.SkuID, priceStr, perr)
		}
		if price.LessThan(floor) {
			return nil, fmt.Errorf("sku=%d 回滚价 %s 低于当前红线 %s（floor=cost/(1-%.2f)），请走特价审批或重新定价: %w",
				it.SkuID, priceStr, floor.StringFixed(8), margin.InexactFloat64(), ErrFloorViolation)
		}
	}

	// 5. 构造 payload（回滚专用：含 target_version_no + reason）。
	payload, err := json.Marshal(map[string]any{
		"price_book_id":     in.PriceBookID,
		"target_version_no": in.TargetVersionNo,
		"reason":            in.Reason,
		"effective_time":    s.now().Format(time.RFC3339), // 回滚立即生效
		"mode":              ModeImmediate,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	// 6. 单事务：复制新版本 + change_request + 2 步 approval_step。
	return s.store.Publish(ctx, PublishTxInput{
		PriceBookID:    in.PriceBookID,
		ChangeType:     ChangePriceBookRollback,
		Payload:        payload,
		EffectiveTime:  s.now(),
		OperatorID:     operatorID,
		RequestID:      requestID,
		RollbackOf:     &target.ID,
		RollbackReason: in.Reason,
	}, operatorID, requestID)
}
