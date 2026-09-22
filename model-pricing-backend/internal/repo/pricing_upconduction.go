// Package repo 的 pricing_upconduction.go：8b-2 涨价传导决策队列（契约 08-pricing.md §5）。
//
// 事务边界（红线 8）：
//   - GenerateQueue 不强制同事务（每行独立 commit，单条失败不拖整批——与 COST_RECALC 同原理）。
//     每行 InsertQueueRow 自己开事务：INSERT + audit_log。
//   - Decide 单事务：UPDATE status/decided_* + audit_log。前置校验在 Service 层。
//
// 幂等设计：
//   - 生成侧：(sku_id, level_code, cost_baseline_version) 已存在 PENDING/FOLLOWED 行即跳过
//     （ok=false），防手工重复触发产生双份队列；NOT_FOLLOWED 行不阻塞新版本（那是历史）。
//   - Decide 侧：由 API 层 middleware.Idempotency 拦截同 key 重放。
package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"model_bss/internal/domain/pricing"
	"model_bss/internal/infra/db"
)

// ============================================================
// 行模型
// ============================================================

type priceUpconductionRow struct {
	ID             int64          `gorm:"primaryKey"`
	SkuID          int64          `gorm:"column:sku_id"`
	LevelCode      string         `gorm:"column:level_code"`
	CostBefore     string         `gorm:"column:cost_before"`
	CostAfter      string         `gorm:"column:cost_after"`
	CostDeltaPct   string         `gorm:"column:cost_delta_pct"`
	PriceCurrent   string         `gorm:"column:price_current"`
	PriceSuggested string         `gorm:"column:price_suggested"`
	FloorPrice     string         `gorm:"column:floor_price"`
	MarginBefore   sql.NullString `gorm:"column:margin_before"`
	MarginAfter    sql.NullString `gorm:"column:margin_after"`
	Status         string         `gorm:"column:status"`
	FrozenUntil    *time.Time     `gorm:"column:frozen_until"`
	Reason         *string        `gorm:"column:reason"`
	DecidedBy      *int64         `gorm:"column:decided_by"`
	DecidedAt      *time.Time     `gorm:"column:decided_at"`
	OverridePrice  sql.NullString `gorm:"column:override_price"`
	CreatedAt      time.Time      `gorm:"column:created_at"`
	UpdatedAt      time.Time      `gorm:"column:updated_at"`
	RequestID      *string        `gorm:"column:request_id"`
	CreatedBy      *int64         `gorm:"column:created_by"`
	UpdatedBy      *int64         `gorm:"column:updated_by"`
}

func (priceUpconductionRow) TableName() string { return "price_upconduction" }

// ============================================================
// Repo
// ============================================================

// PricingUpconductionRepo 实现 pricing.UpconductionStore。
type PricingUpconductionRepo struct {
	base  *gorm.DB
	audit *AuditRepo
}

// NewPricingUpconductionRepo 构造。
func NewPricingUpconductionRepo(base *gorm.DB) *PricingUpconductionRepo {
	return &PricingUpconductionRepo{base: base, audit: NewAuditRepo(base)}
}

var _ pricing.UpconductionStore = (*PricingUpconductionRepo)(nil)

func (r *PricingUpconductionRepo) txOf(ctx context.Context) *gorm.DB {
	if tx := db.FromContext(ctx); tx != nil {
		return tx
	}
	return r.base
}

// ============================================================
// LoadRisingCostSKUs：找"当前版 unit_cost > 上一版 unit_cost"的 SKU
// ============================================================

// LoadRisingCostSKUs 实现 pricing.UpconductionStore.LoadRisingCostSKUs。
//
// 实现思路（单条 SQL 解决，不在 Go 里 N+1）：
//   - cur  = 当前版本 (is_current=true) 取 (sku_id, unit_cost, version)
//   - prev = 上一版本 (同 sku_id，version 第二大) 取 unit_cost
//   - 对比 cur.unit_cost > prev.unit_cost → 输出
//
// 用 LATERAL JOIN 拿前一版（一个 SKU 一行就够，不需要全历史）：
//
//	LEFT JOIN LATERAL (
//	  SELECT cb2.version AS prev_version, rc2.unit_cost AS prev_unit_cost
//	  FROM cost_baseline cb2
//	  LEFT JOIN ...representCompJoin...
//	  WHERE cb2.sku_id = cb.sku_id AND cb2.is_current = false AND cb2.version < cb.version
//	  ORDER BY cb2.version DESC LIMIT 1
//	) prev ON true
func (r *PricingUpconductionRepo) LoadRisingCostSKUs(ctx context.Context) ([]pricing.CostRisePair, error) {
	sql := `
	SELECT
	    cb.sku_id,
	    cb.version                                AS cur_version,
	    prev.version                              AS prev_version,
	    rc.unit_cost                              AS cur_unit_cost,
	    prev.prev_unit_cost                       AS prev_unit_cost
	FROM cost_baseline cb
	` + representCompJoin + `
	LEFT JOIN LATERAL (
	    SELECT cb2.version AS version, (
	        SELECT cc2.unit_cost
	        FROM cost_component cc2
	        WHERE cc2.cost_baseline_id = cb2.id
	        ORDER BY CASE WHEN cc2.component_type = 'input' THEN 0 ELSE 1 END, cc2.component_type
	        LIMIT 1
	    ) AS prev_unit_cost
	    FROM cost_baseline cb2
	    WHERE cb2.sku_id = cb.sku_id
	      AND cb2.is_current = false
	      AND cb2.version < cb.version
	    ORDER BY cb2.version DESC
	    LIMIT 1
	) prev ON true
	WHERE cb.is_current = true
	  AND prev.prev_unit_cost IS NOT NULL
	  AND rc.unit_cost IS NOT NULL
	  AND rc.unit_cost > prev.prev_unit_cost
	ORDER BY cb.sku_id`
	var rows []struct {
		SkuID        int64  `gorm:"column:sku_id"`
		CurVersion   int    `gorm:"column:cur_version"`
		PrevVersion  int    `gorm:"column:prev_version"`
		CurUnitCost  string `gorm:"column:cur_unit_cost"`
		PrevUnitCost string `gorm:"column:prev_unit_cost"`
	}
	if err := r.txOf(ctx).Raw(sql).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load rising cost skus: %w", err)
	}

	out := make([]pricing.CostRisePair, 0, len(rows))
	for _, rw := range rows {
		cur, err := decimal.NewFromString(rw.CurUnitCost)
		if err != nil {
			return nil, fmt.Errorf("sku %d cur unit_cost parse: %w", rw.SkuID, err)
		}
		prev, err := decimal.NewFromString(rw.PrevUnitCost)
		if err != nil {
			return nil, fmt.Errorf("sku %d prev unit_cost parse: %w", rw.SkuID, err)
		}
		out = append(out, pricing.CostRisePair{
			SKUID:           rw.SkuID,
			CostBefore:      prev,
			CostAfter:       cur,
			CurrentVersion:  rw.CurVersion,
			PreviousVersion: rw.PrevVersion,
		})
	}
	return out, nil
}

// ============================================================
// LoadEffectivePrice：按 (sku_id, level_code) 取当前 EFFECTIVE 价目表的代表组件售价
// ============================================================

// LoadEffectivePrice 实现 pricing.UpconductionStore.LoadEffectivePrice。
// 找：当前 EFFECTIVE price_book + price_book_item(sku_id) + price_book_component（代表组件同口径）。
// 无价目表 → ok=false；有但行结构脏 → error。
func (r *PricingUpconductionRepo) LoadEffectivePrice(ctx context.Context, skuID int64, levelCode string) (decimal.Decimal, bool, error) {
	sql := `
	SELECT pbc.unit_price
	FROM price_book pb
	JOIN price_book_item pbi ON pbi.price_book_id = pb.id
	JOIN LATERAL (
	    SELECT unit_price
	    FROM price_book_component
	    WHERE price_book_item_id = pbi.id
	    ORDER BY CASE WHEN component_type = 'input' THEN 0 ELSE 1 END, component_type
	    LIMIT 1
	) pbc ON true
	WHERE pb.level_code = ?
	  AND pb.status = 'EFFECTIVE'
	  AND pbi.sku_id = ?
	LIMIT 1`
	var unitPrice string
	if err := r.txOf(ctx).Raw(sql, levelCode, skuID).Scan(&unitPrice).Error; err != nil {
		return decimal.Zero, false, fmt.Errorf("load effective price sku=%d level=%s: %w", skuID, levelCode, err)
	}
	if unitPrice == "" {
		return decimal.Zero, false, nil
	}
	d, err := decimal.NewFromString(unitPrice)
	if err != nil {
		return decimal.Zero, false, fmt.Errorf("parse unit_price %q: %w", unitPrice, err)
	}
	return d, true, nil
}

// ============================================================
// ListEffectiveLevels：该 SKU 在哪些 level 有 EFFECTIVE 价目表
// ============================================================

// ListEffectiveLevels 实现 pricing.UpconductionStore.ListEffectiveLevels。
func (r *PricingUpconductionRepo) ListEffectiveLevels(ctx context.Context, skuID int64) ([]string, error) {
	sql := `
	SELECT DISTINCT pb.level_code
	FROM price_book pb
	JOIN price_book_item pbi ON pbi.price_book_id = pb.id
	WHERE pb.status = 'EFFECTIVE'
	  AND pbi.sku_id = ?
	ORDER BY pb.level_code`
	var levels []string
	if err := r.txOf(ctx).Raw(sql, skuID).Scan(&levels).Error; err != nil {
		return nil, fmt.Errorf("list effective levels sku=%d: %w", skuID, err)
	}
	return levels, nil
}

// ============================================================
// LoadSKUCode：sku_id → sku_code
// ============================================================

// LoadSKUCode 实现 pricing.UpconductionStore.LoadSKUCode。
func (r *PricingUpconductionRepo) LoadSKUCode(ctx context.Context, skuID int64) (string, error) {
	var code string
	if err := r.txOf(ctx).Table("model_sku").Where("id = ?", skuID).
		Select("sku_code").Scan(&code).Error; err != nil {
		return "", fmt.Errorf("load sku_code %d: %w", skuID, err)
	}
	if code == "" {
		return "", fmt.Errorf("sku_id %d 不存在", skuID)
	}
	return code, nil
}

// ============================================================
// LoadMinGrossMargin：sys_config.min_gross_margin
// ============================================================

// LoadMinGrossMargin 实现 pricing.UpconductionStore.LoadMinGrossMargin。
func (r *PricingUpconductionRepo) LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error) {
	var val string
	if err := r.txOf(ctx).Table("sys_config").
		Where("config_key = ?", "min_gross_margin").
		Select("config_value").Scan(&val).Error; err != nil {
		return decimal.Zero, fmt.Errorf("read min_gross_margin: %w", err)
	}
	if val == "" {
		return decimal.Zero, fmt.Errorf("sys_config min_gross_margin 缺失")
	}
	d, err := decimal.NewFromString(val)
	if err != nil {
		return decimal.Zero, fmt.Errorf("parse min_gross_margin %q: %w", val, err)
	}
	return d, nil
}

// ============================================================
// InsertQueueRow：插入 queue 行 + audit_log（同事务），幂等去重
// ============================================================

// InsertQueueRow 实现 pricing.UpconductionStore.InsertQueueRow。
//
// 去重逻辑：同 (sku_id, level_code, 参考 cost_baseline_current_version) 已有 PENDING/FOLLOWED 行 → 跳过。
// 判重锚点：cost_baseline.version 写在审计 JSON 里（表里没有专门列），所以 SQL 无法直接查"是否同版本"。
// 本批裁决：用 (sku_id, level_code, cost_after) 判重——同 SKU 同 level 同新成本 = 同一次涨价事件。
// NOT_FOLLOWED/已超时的行不阻塞（新的一次涨价应重新入队）。
func (r *PricingUpconductionRepo) InsertQueueRow(ctx context.Context, row *pricing.UpconductionItem, costBaselineVersion int, operatorID int64, requestID string) (int64, bool, error) {
	// 1. 判重：同 sku/level/cost_after 已有 PENDING/FOLLOWED → 跳过。
	var existingID int64
	err := r.txOf(ctx).Table("price_upconduction").
		Where("sku_id = ? AND level_code = ? AND cost_after = ? AND status IN ?",
			row.SKUID, row.LevelCode, row.CostAfter,
			[]string{pricing.UpconductionStatusPending, pricing.UpconductionStatusFollowed}).
		Select("id").Limit(1).Scan(&existingID).Error
	if err != nil {
		return 0, false, fmt.Errorf("dedup check: %w", err)
	}
	if existingID > 0 {
		return 0, false, nil // 已存在，幂等跳过
	}

	// 2. 单事务：INSERT 行 + audit_log。
	var insertedID int64
	txErr := r.txOf(ctx).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		r := priceUpconductionRow{
			SkuID:          row.SKUID,
			LevelCode:      row.LevelCode,
			CostBefore:     row.CostBefore,
			CostAfter:      row.CostAfter,
			CostDeltaPct:   row.CostDeltaPct,
			PriceCurrent:   row.PriceCurrent,
			PriceSuggested: row.PriceSuggested,
			FloorPrice:     row.FloorPrice,
			MarginBefore:   sql.NullString{String: derefOrEmpty(row.MarginBefore), Valid: row.MarginBefore != nil},
			MarginAfter:    sql.NullString{String: derefOrEmpty(row.MarginAfter), Valid: row.MarginAfter != nil},
			Status:         row.Status,
			FrozenUntil:    row.FrozenUntil,
			CreatedAt:      now,
			UpdatedAt:      now,
			RequestID:      strPtr(requestID),
			CreatedBy:      int64Ptr(operatorID),
			UpdatedBy:      int64Ptr(operatorID),
		}
		if err := tx.Create(&r).Error; err != nil {
			return fmt.Errorf("insert price_upconduction: %w", err)
		}
		insertedID = r.ID

		// 3. audit_log（红线 10：价格相关操作）。
		auditPayload := map[string]any{
			"sku_id":                row.SKUID,
			"level_code":            row.LevelCode,
			"cost_before":           row.CostBefore,
			"cost_after":            row.CostAfter,
			"cost_delta_pct":        row.CostDeltaPct,
			"price_current":         row.PriceCurrent,
			"price_suggested":       row.PriceSuggested,
			"floor_price":           row.FloorPrice,
			"cost_baseline_version": costBaselineVersion,
		}
		b, err := json.Marshal(auditPayload)
		if err != nil {
			return fmt.Errorf("marshal audit: %w", err)
		}
		arow := auditLogRow{
			OperatorID:   operatorID,
			OperatorRole: "PRICING_OP", // 手工触发时操作员持 M7:E 的默认角色（PRICING_OP）
			Action:       "PRICE_UPCONDUCTION_GENERATE",
			TargetType:   "PRICE_UPCONDUCTION",
			TargetID:     insertedID,
			AfterValue:   b,
			SourceType:   "HUMAN",
			CreatedAt:    now,
			UpdatedAt:    now,
			RequestID:    strPtr(requestID),
			CreatedBy:    int64Ptr(operatorID),
			UpdatedBy:    int64Ptr(operatorID),
		}
		if err := tx.Create(&arow).Error; err != nil {
			return fmt.Errorf("insert audit_log: %w", err)
		}
		return nil
	})
	if txErr != nil {
		return 0, false, txErr
	}
	return insertedID, true, nil
}

// ============================================================
// ListQueue：分页查询
// ============================================================

// ListQueue 实现 pricing.UpconductionStore.ListQueue。固定 2 发（count + page），不 N+1。
func (r *PricingUpconductionRepo) ListQueue(ctx context.Context, status string, page, size int) ([]pricing.UpconductionItem, int64, error) {
	base := func() *gorm.DB {
		d := r.txOf(ctx).Table("price_upconduction pu").
			Joins("JOIN model_sku ms ON ms.id = pu.sku_id")
		if status != "" {
			d = d.Where("pu.status = ?", status)
		}
		return d
	}

	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count queue: %w", err)
	}
	if total == 0 {
		return []pricing.UpconductionItem{}, 0, nil
	}

	var rows []struct {
		ID             int64          `gorm:"column:id"`
		SkuID          int64          `gorm:"column:sku_id"`
		SKUCode        string         `gorm:"column:sku_code"`
		LevelCode      string         `gorm:"column:level_code"`
		CostBefore     string         `gorm:"column:cost_before"`
		CostAfter      string         `gorm:"column:cost_after"`
		CostDeltaPct   string         `gorm:"column:cost_delta_pct"`
		PriceCurrent   string         `gorm:"column:price_current"`
		PriceSuggested string         `gorm:"column:price_suggested"`
		FloorPrice     string         `gorm:"column:floor_price"`
		MarginBefore   sql.NullString `gorm:"column:margin_before"`
		MarginAfter    sql.NullString `gorm:"column:margin_after"`
		Status         string         `gorm:"column:status"`
		FrozenUntil    *time.Time     `gorm:"column:frozen_until"`
		Reason         *string        `gorm:"column:reason"`
		DecidedBy      *int64         `gorm:"column:decided_by"`
		DecidedAt      *time.Time     `gorm:"column:decided_at"`
		OverridePrice  sql.NullString `gorm:"column:override_price"`
		CreatedAt      time.Time      `gorm:"column:created_at"`
	}
	if err := base().
		Select(`pu.id, pu.sku_id, ms.sku_code, pu.level_code, pu.cost_before, pu.cost_after,
			pu.cost_delta_pct, pu.price_current, pu.price_suggested, pu.floor_price,
			pu.margin_before, pu.margin_after, pu.status, pu.frozen_until,
			pu.reason, pu.decided_by, pu.decided_at, pu.override_price, pu.created_at`).
		Order("pu.created_at DESC, pu.id DESC").
		Limit(size).Offset((page - 1) * size).
		Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list queue: %w", err)
	}

	items := make([]pricing.UpconductionItem, 0, len(rows))
	for i := range rows {
		rw := &rows[i]
		var marginBefore, marginAfter, override *string
		if rw.MarginBefore.Valid {
			s := rw.MarginBefore.String
			marginBefore = &s
		}
		if rw.MarginAfter.Valid {
			s := rw.MarginAfter.String
			marginAfter = &s
		}
		if rw.OverridePrice.Valid {
			s := rw.OverridePrice.String
			override = &s
		}
		items = append(items, pricing.UpconductionItem{
			ID: rw.ID, SKUID: rw.SkuID, SKUCode: rw.SKUCode, LevelCode: rw.LevelCode,
			CostBefore: rw.CostBefore, CostAfter: rw.CostAfter, CostDeltaPct: rw.CostDeltaPct,
			PriceCurrent: rw.PriceCurrent, PriceSuggested: rw.PriceSuggested, FloorPrice: rw.FloorPrice,
			MarginBefore: marginBefore, MarginAfter: marginAfter,
			Status: rw.Status, FrozenUntil: rw.FrozenUntil, Reason: rw.Reason,
			DecidedBy: rw.DecidedBy, DecidedAt: rw.DecidedAt, OverridePrice: override,
			CreatedAt: rw.CreatedAt,
		})
	}
	return items, total, nil
}

// ============================================================
// LoadQueueRowByID / Decide
// ============================================================

// LoadQueueRowByID 实现 pricing.UpconductionStore.LoadQueueRowByID。
func (r *PricingUpconductionRepo) LoadQueueRowByID(ctx context.Context, id int64) (*pricing.UpconductionItem, error) {
	var row struct {
		ID             int64          `gorm:"column:id"`
		SkuID          int64          `gorm:"column:sku_id"`
		SKUCode        string         `gorm:"column:sku_code"`
		LevelCode      string         `gorm:"column:level_code"`
		CostBefore     string         `gorm:"column:cost_before"`
		CostAfter      string         `gorm:"column:cost_after"`
		CostDeltaPct   string         `gorm:"column:cost_delta_pct"`
		PriceCurrent   string         `gorm:"column:price_current"`
		PriceSuggested string         `gorm:"column:price_suggested"`
		FloorPrice     string         `gorm:"column:floor_price"`
		MarginBefore   sql.NullString `gorm:"column:margin_before"`
		MarginAfter    sql.NullString `gorm:"column:margin_after"`
		Status         string         `gorm:"column:status"`
		FrozenUntil    *time.Time     `gorm:"column:frozen_until"`
		Reason         *string        `gorm:"column:reason"`
		DecidedBy      *int64         `gorm:"column:decided_by"`
		DecidedAt      *time.Time     `gorm:"column:decided_at"`
		OverridePrice  sql.NullString `gorm:"column:override_price"`
		CreatedAt      time.Time      `gorm:"column:created_at"`
	}
	err := r.txOf(ctx).Table("price_upconduction pu").
		Joins("JOIN model_sku ms ON ms.id = pu.sku_id").
		Select(`pu.id, pu.sku_id, ms.sku_code, pu.level_code, pu.cost_before, pu.cost_after,
			pu.cost_delta_pct, pu.price_current, pu.price_suggested, pu.floor_price,
			pu.margin_before, pu.margin_after, pu.status, pu.frozen_until,
			pu.reason, pu.decided_by, pu.decided_at, pu.override_price, pu.created_at`).
		Where("pu.id = ?", id).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, pricing.ErrUpconductionNotFound
		}
		return nil, fmt.Errorf("load queue row %d: %w", id, err)
	}

	var marginBefore, marginAfter, override *string
	if row.MarginBefore.Valid {
		s := row.MarginBefore.String
		marginBefore = &s
	}
	if row.MarginAfter.Valid {
		s := row.MarginAfter.String
		marginAfter = &s
	}
	if row.OverridePrice.Valid {
		s := row.OverridePrice.String
		override = &s
	}
	return &pricing.UpconductionItem{
		ID: row.ID, SKUID: row.SkuID, SKUCode: row.SKUCode, LevelCode: row.LevelCode,
		CostBefore: row.CostBefore, CostAfter: row.CostAfter, CostDeltaPct: row.CostDeltaPct,
		PriceCurrent: row.PriceCurrent, PriceSuggested: row.PriceSuggested, FloorPrice: row.FloorPrice,
		MarginBefore: marginBefore, MarginAfter: marginAfter,
		Status: row.Status, FrozenUntil: row.FrozenUntil, Reason: row.Reason,
		DecidedBy: row.DecidedBy, DecidedAt: row.DecidedAt, OverridePrice: override,
		CreatedAt: row.CreatedAt,
	}, nil
}

// Decide 实现 pricing.UpconductionStore.Decide。
// 前置校验（PENDING、floor 校验）在 Service 层完成；这里只负责 UPDATE + audit。
// 用条件 UPDATE（WHERE status='PENDING'）做并发防重——另一个并发请求已成功则此处 0 rows → 报冲突。
func (r *PricingUpconductionRepo) Decide(ctx context.Context, in pricing.DecideInput, operatorRole string) error {
	return r.txOf(ctx).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		// 1. 条件 UPDATE：WHERE status='PENDING' 防并发双写。
		updates := map[string]any{
			"status":     in.Decision, // Service 层已把 Decision 映射为最终 status
			"decided_by": in.OperatorID,
			"decided_at": now,
			"reason":     in.Reason,
			"updated_at": now,
			"updated_by": in.OperatorID,
		}
		if in.OverridePrice.GreaterThan(decimal.Zero) && in.HasOverride {
			updates["override_price"] = in.OverridePrice.StringFixed(8)
		}
		res := tx.Model(&priceUpconductionRow{}).
			Where("id = ? AND status = ?", in.QueueID, pricing.UpconductionStatusPending).
			Updates(updates)
		if res.Error != nil {
			return fmt.Errorf("update price_upconduction %d: %w", in.QueueID, res.Error)
		}
		if res.RowsAffected == 0 {
			return pricing.ErrUpconductionNotPending
		}

		// 2. audit_log。
		afterPayload := map[string]any{
			"decision":       in.Decision,
			"decided_by":     in.OperatorID,
			"decided_at":     now.Format(time.RFC3339),
			"reason":         in.Reason,
			"override_price": nil,
		}
		if in.HasOverride && in.OverridePrice.GreaterThan(decimal.Zero) {
			afterPayload["override_price"] = in.OverridePrice.StringFixed(8)
		}
		b, err := json.Marshal(afterPayload)
		if err != nil {
			return fmt.Errorf("marshal audit: %w", err)
		}
		arow := auditLogRow{
			OperatorID:   in.OperatorID,
			OperatorRole: operatorRole,
			Action:       "PRICE_UPCONDUCTION_DECIDE",
			TargetType:   "PRICE_UPCONDUCTION",
			TargetID:     in.QueueID,
			AfterValue:   b,
			SourceType:   "HUMAN",
			CreatedAt:    now,
			UpdatedAt:    now,
			RequestID:    strPtr(in.RequestID),
			CreatedBy:    int64Ptr(in.OperatorID),
			UpdatedBy:    int64Ptr(in.OperatorID),
		}
		if err := tx.Create(&arow).Error; err != nil {
			return fmt.Errorf("insert audit_log: %w", err)
		}
		return nil
	})
}

// ============================================================
// 转换 helper
// ============================================================
// （列表/详情读取已在各自方法内做 flat-scan + NullString 转换；
//  曾经的 upconductionRowToItem 因 GORM embedded 扫描陷阱（见 model.go 坑位表）
//  已删除，勿再恢复内嵌写法。）

// derefOrEmpty 返回 *string 的值或空串（写侧把 nil 压成 NullString.Invalid 时用）。
func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
