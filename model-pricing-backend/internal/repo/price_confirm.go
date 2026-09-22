package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"model_bss/internal/domain/price"
)

// ============================================================
// 7b 确认入正式版本 + 生效连锁（契约 07 §7/§8/§9）
//
// 事务纪律（红线 3/8）：
//   - ConfirmStaging 单事务：change_request + approval_steps + staging.processed=true。
//   - ApplyOfficialPriceChange 单事务 6 连写：price_version 关旧开新 + quote_sheet 整单静默跟随
//     + task_job(COST_RECALC) + event_outbox(official_price.changed) + cache_version+1 + audit。
//     由 DecideApproval 全步 APPROVED 时回调触发（同事务，与审批状态推进一致）。
//   - 静默跟随按整张生效单翻版（quote_sheet 是供应商级整单版本，非 SKU 级）：
//     倍率组件换新价 = 新官方价 × multiplier；绝对价组件原样保留（不跟随）。
// ============================================================

// PriceConfirmRepo 实现 price.ConfirmStore + 生效连锁写侧。
// audit 独立持有（SupplierRepo 无该字段；与 ApproveRepo/QuoteRepo 同模式）。
type PriceConfirmRepo struct {
	*SupplierRepo
	audit *AuditRepo
}

// NewPriceConfirmRepo 构造仓储。
func NewPriceConfirmRepo(base *SupplierRepo) *PriceConfirmRepo {
	return &PriceConfirmRepo{SupplierRepo: base, audit: NewAuditRepo(base.base)}
}

var _ price.ConfirmStore = (*PriceConfirmRepo)(nil)

// ---- 行模型（复用 supplier_quote.go 的 quoteSheetRow/quoteItemRow/quoteComponentRow） ----

// priceVersionWriteRow 是 price_version 的写模型（supplier.go 里的 priceVersionRow 是缺列扫描行，不可 INSERT）。
type priceVersionWriteRow struct {
	ID              int64      `gorm:"primaryKey"`
	SkuID           int64      `gorm:"column:sku_id"`
	VersionNo       int        `gorm:"column:version_no"`
	Currency        string     `gorm:"column:currency"`
	TaxBasis        string     `gorm:"column:tax_basis"`
	Source          string     `gorm:"column:source"`
	ChangeRequestID *int64     `gorm:"column:change_request_id"`
	EffectiveFrom   time.Time  `gorm:"column:effective_from"`
	EffectiveTo     *time.Time `gorm:"column:effective_to"`
	IsCurrent       bool       `gorm:"column:is_current"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
	RequestID       *string    `gorm:"column:request_id"`
	CreatedBy       *int64     `gorm:"column:created_by"`
	UpdatedBy       *int64     `gorm:"column:updated_by"`
}

func (priceVersionWriteRow) TableName() string { return "price_version" }

// priceComponentWriteRow 是 price_component 的写模型。
type priceComponentWriteRow struct {
	ID             int64     `gorm:"primaryKey"`
	PriceVersionID int64     `gorm:"column:price_version_id"`
	ComponentType  string    `gorm:"column:component_type"`
	UnitPrice      string    `gorm:"column:unit_price"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
	RequestID      *string   `gorm:"column:request_id"`
	CreatedBy      *int64    `gorm:"column:created_by"`
	UpdatedBy      *int64    `gorm:"column:updated_by"`
}

func (priceComponentWriteRow) TableName() string { return "price_component" }

// ConfirmStore 实现
// ============================================================

// LoadStagings 批量按 id 加载 staging。
func (r *PriceConfirmRepo) LoadStagings(ctx context.Context, ids []int64) ([]price.ConfirmStagingRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []stagingPriceRow
	if err := r.txOf(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load stagings: %w", err)
	}
	out := make([]price.ConfirmStagingRow, 0, len(rows))
	for _, rr := range rows {
		payload := map[string]string{}
		if len(rr.Payload) > 0 {
			_ = json.Unmarshal(rr.Payload, &payload)
		}
		out = append(out, price.ConfirmStagingRow{
			ID: rr.ID, SyncJobID: rr.SyncJobID, SKUID: rr.SKUID, Processed: rr.Processed,
			Currency: rr.Currency, Payload: payload, SourceType: rr.Source,
		})
	}
	return out, nil
}

// LoadCurrentPriceMap 加载指定 SKU 当前官方价组件 map（is_current=true）。
func (r *PriceConfirmRepo) LoadCurrentPriceMap(ctx context.Context, skuIDs []int64) (map[int64]map[string]decimal.Decimal, error) {
	out := make(map[int64]map[string]decimal.Decimal, len(skuIDs))
	if len(skuIDs) == 0 {
		return out, nil
	}
	var vers []priceVersionRow
	if err := r.txOf(ctx).Table("price_version").
		Select("id, sku_id").
		Where("sku_id IN ? AND is_current", skuIDs).
		Scan(&vers).Error; err != nil {
		return nil, fmt.Errorf("load current price versions: %w", err)
	}
	if len(vers) == 0 {
		return out, nil
	}
	vid2sku := make(map[int64]int64, len(vers))
	vids := make([]int64, 0, len(vers))
	for _, v := range vers {
		vid2sku[v.ID] = v.SkuID
		vids = append(vids, v.ID)
	}
	var comps []priceComponentRow
	if err := r.txOf(ctx).Table("price_component").
		Select("price_version_id, component_type, unit_price::text AS unit_price").
		Where("price_version_id IN ?", vids).
		Scan(&comps).Error; err != nil {
		return nil, fmt.Errorf("load current price components: %w", err)
	}
	for _, c := range comps {
		skuID := vid2sku[c.PriceVersionID]
		v, err := decimal.NewFromString(c.UnitPrice)
		if err != nil {
			return nil, fmt.Errorf("parse price sku=%d comp=%s val=%q: %w", skuID, c.ComponentType, c.UnitPrice, err)
		}
		if out[skuID] == nil {
			out[skuID] = map[string]decimal.Decimal{}
		}
		out[skuID][c.ComponentType] = v
	}
	return out, nil
}

// LoadCostParams 加载 GLOBAL + 指定 MODEL(scope_id=sku_id) 的成本参数。
func (r *PriceConfirmRepo) LoadCostParams(ctx context.Context, skuIDs []int64) ([]price.CostParamRow, error) {
	type row struct {
		ScopeType   string `gorm:"column:scope_type"`
		ScopeID     int64  `gorm:"column:scope_id"`
		LossRate    string `gorm:"column:loss_rate"`
		ChannelRate string `gorm:"column:channel_rate"`
	}
	var rows []row
	q := r.txOf(ctx).Table("cost_param").
		Select("scope_type, scope_id, loss_rate::text AS loss_rate, channel_rate::text AS channel_rate").
		Where("scope_type = 'GLOBAL'")
	if len(skuIDs) > 0 {
		q = q.Or("(scope_type = 'MODEL' AND scope_id IN ?)", skuIDs)
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load cost params: %w", err)
	}
	out := make([]price.CostParamRow, 0, len(rows))
	for _, rr := range rows {
		lr, err1 := decimal.NewFromString(rr.LossRate)
		cr, err2 := decimal.NewFromString(rr.ChannelRate)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("parse cost param scope=%s/%d: %w", rr.ScopeType, rr.ScopeID, errors.Join(err1, err2))
		}
		out = append(out, price.CostParamRow{ScopeType: rr.ScopeType, ScopeID: rr.ScopeID, LossRate: lr, ChannelRate: cr})
	}
	return out, nil
}

// LoadMinGrossMargin 读 sys_config.min_gross_margin（与 6b-4 同口径）。
func (r *PriceConfirmRepo) LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error) {
	var val string
	err := r.txOf(ctx).Table("sys_config").
		Select("config_value").
		Where("config_key = ?", "min_gross_margin").
		Take(&val).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return decimal.Zero, errors.New("sys_config min_gross_margin not found")
		}
		return decimal.Zero, fmt.Errorf("read sys_config min_gross_margin: %w", err)
	}
	d, err := decimal.NewFromString(val)
	if err != nil {
		return decimal.Zero, fmt.Errorf("sys_config min_gross_margin %q 非法: %w", val, err)
	}
	return d, nil
}

// ConfirmStaging 单事务：change_request + approval_steps + staging.processed=true。
func (r *PriceConfirmRepo) ConfirmStaging(ctx context.Context, p price.ConfirmStagingParams) (int64, error) {
	var changeRequestID int64
	err := r.txOf(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		// 1. change_request（NOT NULL 列全部显式初始化——CLAUDE.md 编码约定）。
		//    created_by 是 varchar(24)：存操作员标识（角色码），与 M4 退役流程 "SYNC_JOB" 同列口径。
		cr := changeRequestRow{
			ChangeType:    p.ChangeType,
			SkuID:         p.SKUID,
			RiskLevel:     riskOf(p.ChangeType),
			Payload:       p.Payload,
			MarginPreview: p.MarginPreview,
			Status:        "PENDING",
			CreatedBy:     p.OperatorRole,
			CreatedAt:     now, UpdatedAt: now,
			RequestID: strPtr(p.RequestID), UpdatedBy: strPtr(p.OperatorRole),
		}
		if err := tx.Create(&cr).Error; err != nil {
			return fmt.Errorf("insert change_request: %w", err)
		}
		changeRequestID = cr.ID
		// 2. approval_steps：涨价 2 步（MODEL_OPS→PRICING_OP）、降价 1 步（MODEL_OPS）。
		steps := approvalStepsFor(p.ChangeType, changeRequestID, now, p.OperatorID, p.RequestID)
		if err := tx.Create(&steps).Error; err != nil {
			return fmt.Errorf("insert approval_steps: %w", err)
		}
		// 3. staging.processed=true（条件更新：只允许 processed=false → true；0 行 = 并发已确认）。
		res := tx.Table("staging_price").
			Where("id IN ? AND processed = false", p.StagingIDs).
			Updates(map[string]any{
				"processed": true, "updated_at": now, "updated_by": p.OperatorID,
			})
		if res.Error != nil {
			return fmt.Errorf("mark staging processed: %w", res.Error)
		}
		if res.RowsAffected != int64(len(p.StagingIDs)) {
			return fmt.Errorf("mark staging processed: 影响 %d 行，期望 %d（并发已确认？）", res.RowsAffected, len(p.StagingIDs))
		}
		// 4. audit（红线 10）。
		if err := r.audit.Record(ctx, AuditEntry{
			OperatorID: p.OperatorID, OperatorRole: p.OperatorRole,
			Action: "PRICE_CHANGE_CONFIRM", TargetType: "CHANGE_REQUEST", TargetID: changeRequestID,
			SourceType: "INTERNAL", RequestID: p.RequestID,
			AfterValue: map[string]any{
				"change_type": p.ChangeType, "sku_id": p.SKUID, "step_count": p.StepCount,
				"staging_ids": p.StagingIDs,
			},
		}); err != nil {
			return fmt.Errorf("audit change_request=%d: %w", changeRequestID, err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return changeRequestID, nil
}

// riskOf 涨跌风险等级（涨价 HIGH、降价 MID——与设计 §7.3 risk_level 枚举对齐）。
func riskOf(changeType string) string {
	if changeType == price.ChangePriceUp {
		return "HIGH"
	}
	return "MID"
}

// approvalStepsFor 按涨跌方向生成审批步（涨价 2 步 MODEL_OPS→PRICING_OP、降价 1 步 MODEL_OPS）。
// approval_step.biz_type 与 change_request.change_type 同值（PRICE_UP/PRICE_DOWN）——
// 与 M4 退役的 "DEPRECATE" 同款口径（DecideApproval 按 change_request.change_type 查步骤）。
func approvalStepsFor(changeType string, changeRequestID int64, now time.Time, operatorID int64, requestID string) []approvalStepRow {
	roles := []string{"MODEL_OPS"}
	if changeType == price.ChangePriceUp {
		roles = []string{"MODEL_OPS", "PRICING_OP"}
	}
	steps := make([]approvalStepRow, 0, len(roles))
	for i, role := range roles {
		steps = append(steps, approvalStepRow{
			BizType: changeType, BizID: changeRequestID, StepNo: i + 1, RequiredRole: role,
			Decision:  nil, // 待审批
			CreatedAt: now, UpdatedAt: now,
			RequestID: strPtr(requestID), CreatedBy: int64Ptr(operatorID), UpdatedBy: int64Ptr(operatorID),
		})
	}
	return steps
}

// ============================================================
// 生效连锁（DecideApproval 全步 APPROVED 时回调）
// ============================================================

// ApplyOfficialPriceChangeParams 生效连锁入参。
type ApplyOfficialPriceChangeParams struct {
	ChangeRequestID int64
	Payload         []byte // change_request.payload（含 sku_ids/prices/effective_time/direction）
	OperatorID      int64
	OperatorRole    string
	RequestID       string
}

// changePayload 是 change_request.payload 的解码结构（与 domain buildChangePayload 对齐）。
type changePayload struct {
	Direction     string                       `json:"direction"`
	EffectiveTime string                       `json:"effective_time"`
	StagingIDs    []int64                      `json:"staging_ids"`
	SKUIDs        []int64                      `json:"sku_ids"`
	Prices        map[string]map[string]string `json:"prices"` // sku_id → component_type → unit_price
}

// ApplyOfficialPriceChange 审批全通过后执行生效连锁（6 连写单事务）。
// 由 model_deprecate.go DecideApproval 的 onApproved 回调触发（同事务——审批状态推进与连锁一致）。
func (r *PriceConfirmRepo) ApplyOfficialPriceChange(ctx context.Context, p ApplyOfficialPriceChangeParams) error {
	var cp changePayload
	if err := json.Unmarshal(p.Payload, &cp); err != nil {
		return fmt.Errorf("decode change_request payload: %w", err)
	}
	effectiveTime, err := time.Parse(time.RFC3339, cp.EffectiveTime)
	if err != nil {
		return fmt.Errorf("decode effective_time %q: %w", cp.EffectiveTime, err)
	}
	now := time.Now().UTC()
	return r.txOf(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. price_version 关旧开新（逐 SKU）。
		for _, skuID := range cp.SKUIDs {
			if err := r.applyPriceVersion(ctx, tx, skuID, cp.Prices[fmt.Sprint(skuID)], effectiveTime, now, p); err != nil {
				return err
			}
		}
		// 2. 静默跟随：涉及 SKU 的所有 EFFECTIVE 报价单整单翻版。
		followed, err := r.silentFollowQuotes(ctx, tx, cp, effectiveTime, now, p)
		if err != nil {
			return err
		}
		// 3. task_job(COST_RECALC) 入队（逐 SKU，reason=OFFICIAL_PRICE_CHANGE）。
		for _, skuID := range cp.SKUIDs {
			payload, _ := json.Marshal(map[string]any{
				"sku_id": skuID, "reason": "OFFICIAL_PRICE_CHANGE",
				"change_request_id": p.ChangeRequestID,
			})
			if err := tx.Create(&taskJobRow{
				JobType: "COST_RECALC", Payload: payload, Status: "PENDING", NextRunAt: now,
				CreatedAt: now, UpdatedAt: now,
				RequestID: strPtr(p.RequestID), CreatedBy: int64Ptr(p.OperatorID), UpdatedBy: int64Ptr(p.OperatorID),
			}).Error; err != nil {
				return fmt.Errorf("enqueue COST_RECALC sku=%d: %w", skuID, err)
			}
		}
		// 4. event_outbox(official_price.changed)。
		eventPayload, _ := json.Marshal(map[string]any{
			"change_request_id": p.ChangeRequestID, "sku_ids": cp.SKUIDs,
			"direction": cp.Direction, "effective_time": cp.EffectiveTime,
			"followed_quote_sheet_ids": followed,
		})
		if err := tx.Create(&eventOutboxRow{
			EventType: "official_price.changed", Payload: eventPayload, Status: "PENDING", NextRunAt: now,
			CreatedAt: now, UpdatedAt: now,
			RequestID: strPtr(p.RequestID), CreatedBy: int64Ptr(p.OperatorID), UpdatedBy: int64Ptr(p.OperatorID),
		}).Error; err != nil {
			return fmt.Errorf("enqueue official_price.changed: %w", err)
		}
		// 5. cache_version('official_price') +1（upsert）。
		if err := tx.Exec(`
			INSERT INTO cache_version (cache_key, version, updated_at)
			VALUES ('official_price', 1, ?)
			ON CONFLICT (cache_key) DO UPDATE SET version = cache_version.version + 1, updated_at = EXCLUDED.updated_at`,
			now).Error; err != nil {
			return fmt.Errorf("bump cache_version: %w", err)
		}
		// 6. audit（红线 10：价格相关操作 100% 写审计）。
		if err := r.audit.Record(ctx, AuditEntry{
			OperatorID: p.OperatorID, OperatorRole: p.OperatorRole,
			Action: "OFFICIAL_PRICE_CHANGE_APPLY", TargetType: "CHANGE_REQUEST", TargetID: p.ChangeRequestID,
			SourceType: "INTERNAL", RequestID: p.RequestID,
			AfterValue: map[string]any{
				"sku_ids": cp.SKUIDs, "direction": cp.Direction,
				"effective_time": cp.EffectiveTime, "followed_quote_sheet_ids": followed,
			},
		}); err != nil {
			return fmt.Errorf("audit apply change_request=%d: %w", p.ChangeRequestID, err)
		}
		return nil
	})
}

// applyPriceVersion 单 SKU 的 price_version 关旧开新。
func (r *PriceConfirmRepo) applyPriceVersion(ctx context.Context, tx *gorm.DB, skuID int64, comps map[string]string, effectiveTime, now time.Time, p ApplyOfficialPriceChangeParams) error {
	// 1. 关旧（条件更新当前版本；0 行 = 该 SKU 无当前版本，允许首次设价）。
	var prev priceVersionWriteRow
	prevErr := tx.Where("sku_id = ? AND is_current", skuID).Take(&prev).Error
	var prevVersionNo int
	var prevCurrency, prevTaxBasis string
	switch {
	case prevErr == nil:
		res := tx.Model(&priceVersionWriteRow{}).
			Where("id = ? AND is_current", prev.ID).
			Updates(map[string]any{
				"is_current": false, "effective_to": effectiveTime,
				"updated_at": now, "updated_by": p.OperatorID,
			})
		if res.Error != nil {
			return fmt.Errorf("close prev price_version sku=%d: %w", skuID, res.Error)
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("close prev price_version sku=%d: 并发已换版", skuID)
		}
		prevVersionNo = prev.VersionNo
		prevCurrency, prevTaxBasis = prev.Currency, prev.TaxBasis
	case errors.Is(prevErr, gorm.ErrRecordNotFound):
		prevCurrency, prevTaxBasis = "USD", "EXCLUSIVE" // 首次设价默认（契约 07 §7 默认值）
	default:
		return fmt.Errorf("load prev price_version sku=%d: %w", skuID, prevErr)
	}
	// 2. 新版本 INSERT。
	row := priceVersionWriteRow{
		SkuID: skuID, VersionNo: prevVersionNo + 1, Currency: prevCurrency, TaxBasis: prevTaxBasis,
		Source: "SYNC", ChangeRequestID: int64Ptr(p.ChangeRequestID),
		EffectiveFrom: effectiveTime, EffectiveTo: nil, IsCurrent: true,
		CreatedAt: now, UpdatedAt: now,
		RequestID: strPtr(p.RequestID), CreatedBy: int64Ptr(p.OperatorID), UpdatedBy: int64Ptr(p.OperatorID),
	}
	if err := tx.Create(&row).Error; err != nil {
		return fmt.Errorf("insert price_version sku=%d ver=%d: %w", skuID, prevVersionNo+1, err)
	}
	// 3. 组件批量 INSERT。
	items := make([]priceComponentWriteRow, 0, len(comps))
	for ct, priceStr := range comps {
		if _, err := decimal.NewFromString(priceStr); err != nil {
			return fmt.Errorf("parse comp price sku=%d comp=%s val=%q: %w", skuID, ct, priceStr, err)
		}
		items = append(items, priceComponentWriteRow{
			PriceVersionID: row.ID, ComponentType: ct, UnitPrice: priceStr,
			CreatedAt: now, UpdatedAt: now,
			RequestID: strPtr(p.RequestID), CreatedBy: int64Ptr(p.OperatorID), UpdatedBy: int64Ptr(p.OperatorID),
		})
	}
	if len(items) > 0 {
		if err := tx.Create(&items).Error; err != nil {
			return fmt.Errorf("insert price_component sku=%d: %w", skuID, err)
		}
	}
	return nil
}

// silentFollowQuotes 静默跟随：涉及 SKU 的所有 EFFECTIVE 报价单整单翻版。
// 返回被翻版的 quote_sheet id 集（供 event/audit）。
func (r *PriceConfirmRepo) silentFollowQuotes(ctx context.Context, tx *gorm.DB, cp changePayload, effectiveTime, now time.Time, p ApplyOfficialPriceChangeParams) ([]int64, error) {
	// 1. 找出涉及 SKU 的所有 EFFECTIVE 报价单（该 SKU 在明细中出现的单）。
	var sheets []quoteSheetRow
	if err := tx.Distinct("quote_sheet.*").
		Table("quote_sheet").
		Joins("JOIN quote_item ON quote_item.quote_sheet_id = quote_sheet.id").
		Where("quote_sheet.status = 'EFFECTIVE' AND quote_item.sku_id IN ?", cp.SKUIDs).
		Find(&sheets).Error; err != nil {
		return nil, fmt.Errorf("load effective quote sheets: %w", err)
	}
	followed := make([]int64, 0, len(sheets))
	for _, sheet := range sheets {
		// 2. 旧单 → EXPIRED（valid_to = effective_time）。
		res := tx.Table("quote_sheet").
			Where("id = ? AND status = 'EFFECTIVE'", sheet.ID).
			Updates(map[string]any{
				"status": "EXPIRED", "valid_to": effectiveTime,
				"updated_at": now, "updated_by": p.OperatorID,
			})
		if res.Error != nil {
			return nil, fmt.Errorf("expire quote_sheet %d: %w", sheet.ID, res.Error)
		}
		if res.RowsAffected == 0 {
			return nil, fmt.Errorf("expire quote_sheet %d: 并发已失效", sheet.ID)
		}
		// 3. 新单 INSERT（version_no+1，source=SILENT_FOLLOW，submitted_by=NULL，valid_from=effective_time，valid_to 继承旧单）。
		newSheet := quoteSheetRow{
			SupplierID: sheet.SupplierID, VersionNo: sheet.VersionNo + 1,
			Status: "EFFECTIVE", ValidFrom: effectiveTime, ValidTo: sheet.ValidTo,
			Source: "SILENT_FOLLOW", Retroactive: false,
			ChangeRequestID: int64Ptr(p.ChangeRequestID),
			SubmittedBy:     nil, SubmittedAt: nil, // 系统跟随，无提交人（契约 07 §8）
			CreatedAt: now, UpdatedAt: now,
			RequestID: strPtr(p.RequestID), CreatedBy: int64Ptr(p.OperatorID), UpdatedBy: int64Ptr(p.OperatorID),
			ActivatedAt: &now,
		}
		if err := tx.Create(&newSheet).Error; err != nil {
			return nil, fmt.Errorf("insert silent-follow quote_sheet sup=%d: %w", sheet.SupplierID, err)
		}
		// 4. 明细 + 组件翻版：倍率组件换新价 = 新官方价 × multiplier；绝对价组件原样保留。
		if err := r.copyQuoteItems(ctx, tx, sheet.ID, newSheet.ID, cp, now, p); err != nil {
			return nil, err
		}
		followed = append(followed, newSheet.ID)
	}
	return followed, nil
}

// copyQuoteItems 翻版一张报价单的明细与组件（倍率组件按新官方价重算，绝对价组件原样）。
func (r *PriceConfirmRepo) copyQuoteItems(ctx context.Context, tx *gorm.DB, oldSheetID, newSheetID int64, cp changePayload, now time.Time, p ApplyOfficialPriceChangeParams) error {
	var items []quoteItemRow
	if err := tx.Where("quote_sheet_id = ?", oldSheetID).Find(&items).Error; err != nil {
		return fmt.Errorf("load quote_items sheet=%d: %w", oldSheetID, err)
	}
	for _, item := range items {
		newItem := quoteItemRow{
			QuoteSheetID: newSheetID, SkuID: item.SkuID, Currency: item.Currency,
			FxTier: item.FxTier, Constraints: item.Constraints,
			CreatedAt: now, UpdatedAt: now,
			RequestID: strPtr(p.RequestID), CreatedBy: int64Ptr(p.OperatorID), UpdatedBy: int64Ptr(p.OperatorID),
		}
		if err := tx.Create(&newItem).Error; err != nil {
			return fmt.Errorf("insert quote_item sku=%d: %w", item.SkuID, err)
		}
		var comps []quoteComponentRow
		if err := tx.Where("quote_item_id = ?", item.ID).Find(&comps).Error; err != nil {
			return fmt.Errorf("load quote_components item=%d: %w", item.ID, err)
		}
		newComps := make([]quoteComponentRow, 0, len(comps))
		for _, c := range comps {
			newPrice := c.UnitPrice
			// 倍率组件：若该 SKU 在本次变价集合内，换新价 = 新官方价 × multiplier。
			if c.Multiplier != nil {
				if newOfficial, ok := cp.Prices[fmt.Sprint(item.SkuID)]; ok {
					if newOfficialComp, ok2 := newOfficial[c.ComponentType]; ok2 {
						mult, err1 := decimal.NewFromString(*c.Multiplier)
						off, err2 := decimal.NewFromString(newOfficialComp)
						if err1 == nil && err2 == nil {
							newPrice = off.Mul(mult).StringFixed(8)
						}
					}
				}
			}
			newComps = append(newComps, quoteComponentRow{
				QuoteItemID: newItem.ID, ComponentType: c.ComponentType,
				Multiplier: c.Multiplier, UnitPrice: newPrice,
				CreatedAt: now, UpdatedAt: now,
				RequestID: strPtr(p.RequestID), CreatedBy: int64Ptr(p.OperatorID), UpdatedBy: int64Ptr(p.OperatorID),
			})
		}
		if len(newComps) > 0 {
			if err := tx.Create(&newComps).Error; err != nil {
				return fmt.Errorf("insert quote_components item=%d: %w", newItem.ID, err)
			}
		}
	}
	return nil
}
