// Package repo 的 workbench.go：10a §2 待办聚合 + §3 指标卡 仓储实现。
//
// 关键裁决（与 stage10a 提示词一致）：
//   - 销售/采购/财务 角色：行级过滤 operator_id（采购 assignee_id=staff_id / 销售 owner_sales_operator_id=staff_id）。
//   - **PLATFORM_ADMIN 看全部**（不按 assignee 过滤），其他角色按 ownerScope=true 过滤。
//   - 成本/毛利字段（unit_cost/floor_price/margin）**SQL 层就不 SELECT**（裁决 9）。
//   - 破 floor 计算所需字段（unit_cost/loss_rate/channel_rate/min_gross_margin）是**内部计算数据**，
//     只在 repo → service 间流动，**绝不透到 API DTO**。
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"model_bss/internal/domain/workbench"
	"model_bss/internal/infra/db"
)

// WorkbenchRepo 工作台仓储。
type WorkbenchRepo struct {
	base *gorm.DB
}

// NewWorkbenchRepo 构造。
func NewWorkbenchRepo(base *gorm.DB) *WorkbenchRepo {
	return &WorkbenchRepo{base: base}
}

func (r *WorkbenchRepo) txOf(ctx context.Context) *gorm.DB {
	if tx := db.FromContext(ctx); tx != nil {
		return tx
	}
	return r.base
}

// ------------------------------------------------------------
// ListTodos：待办列表（行级过滤可选 + JOIN internal_staff 取 assignee_name）
// ------------------------------------------------------------

// ListTodos 待办分页列表。ownerScope=true 时按 assignee_id=operatorID 过滤（PLATFORM_ADMIN 除外）。
// 联 internal_staff 取 name；SERVICE 层负责按 (biz_type, biz_id, priority) 去重和 deeplink。
func (r *WorkbenchRepo) ListTodos(ctx context.Context, q workbench.TodoQuery, ownerScope bool, operatorID int64) ([]workbench.TodoItem, int64, error) {
	tx := r.txOf(ctx).Table("todo_task tt").
		Joins("LEFT JOIN internal_staff s ON s.id = tt.assignee_id")
	if q.BizType != "" {
		tx = tx.Where("tt.biz_type = ?", q.BizType)
	}
	if q.Status != "" {
		tx = tx.Where("tt.status = ?", q.Status)
	}
	if ownerScope {
		tx = tx.Where("tt.assignee_id = ?", operatorID)
	}

	// 统计总数（在过滤后）
	var total int64
	cntTx := tx.Session(&gorm.Session{})
	if err := cntTx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count todos: %w", err)
	}

	// 数据行
	type row struct {
		ID           int64     `gorm:"column:id"`
		BizType      string    `gorm:"column:biz_type"`
		BizID        int64     `gorm:"column:biz_id"`
		Title        string    `gorm:"column:title"`
		Priority     string    `gorm:"column:priority"`
		Status       string    `gorm:"column:status"`
		CreatedAt    time.Time `gorm:"column:created_at"`
		AssigneeName string    `gorm:"column:assignee_name"`
	}
	var rows []row
	offset := (q.Page - 1) * q.Size
	if err := tx.
		Select("tt.id, tt.biz_type, tt.biz_id, tt.title, tt.priority, tt.status, tt.created_at, COALESCE(s.name,'') AS assignee_name").
		Order("tt.id").
		Offset(offset).Limit(q.Size).
		Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list todos: %w", err)
	}

	out := make([]workbench.TodoItem, 0, len(rows))
	for _, rw := range rows {
		out = append(out, workbench.TodoItem{
			ID:           rw.ID,
			BizType:      rw.BizType,
			BizID:        rw.BizID,
			Title:        rw.Title,
			Priority:     rw.Priority,
			Status:       rw.Status,
			CreatedAt:    rw.CreatedAt,
			AssigneeName: rw.AssigneeName,
			Deeplink:     "", // 由 service 层填
		})
	}
	return out, total, nil
}

// ------------------------------------------------------------
// 采购卡片
// ------------------------------------------------------------

// CountPendingQuotes 待审批报价数。采购行级过滤：
// supplier_profile.owner_procurement_operator_id = operatorID → 该 buyer 的供应商 → 其 quote_sheet 待审批数。
// ownerScope=false（PLATFORM_ADMIN）时查全库待审批。
func (r *WorkbenchRepo) CountPendingQuotes(ctx context.Context, operatorID int64, ownerScope bool) (int64, error) {
	tx := r.txOf(ctx).Table("quote_sheet qs").
		Where("qs.status IN ('SUBMITTED_PENDING','APPROVED_PENDING')")
	if ownerScope {
		tx = tx.Joins("JOIN supplier_profile sp ON sp.id = qs.supplier_id").
			Where("sp.owner_procurement_operator_id = ?", operatorID)
	}
	var n int64
	if err := tx.Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count pending quotes: %w", err)
	}
	return n, nil
}

// CountEffectiveQuotesThisMonth 本月生效报价数（裁决 4）。
func (r *WorkbenchRepo) CountEffectiveQuotesThisMonth(ctx context.Context, operatorID int64, ownerScope bool, now time.Time) (int64, error) {
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	tx := r.txOf(ctx).Table("quote_sheet qs").
		Where("qs.status = 'EFFECTIVE'").
		Where("qs.valid_from <= ?", now).
		Where("(qs.valid_to IS NULL OR qs.valid_to > ?)", now).
		Where("qs.created_at >= ?", monthStart)
	if ownerScope {
		tx = tx.Joins("JOIN supplier_profile sp ON sp.id = qs.supplier_id").
			Where("sp.owner_procurement_operator_id = ?", operatorID)
	}
	var n int64
	if err := tx.Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count effective quotes month: %w", err)
	}
	return n, nil
}

// CountQuotesExpiringIn30Days 30 天内到期（裁决 5）。
func (r *WorkbenchRepo) CountQuotesExpiringIn30Days(ctx context.Context, operatorID int64, ownerScope bool, now time.Time) (int64, error) {
	thirty := now.Add(30 * 24 * time.Hour)
	tx := r.txOf(ctx).Table("quote_sheet qs").
		Where("qs.status = 'EFFECTIVE'").
		Where("qs.valid_to > ?", now).
		Where("qs.valid_to <= ?", thirty)
	if ownerScope {
		tx = tx.Joins("JOIN supplier_profile sp ON sp.id = qs.supplier_id").
			Where("sp.owner_procurement_operator_id = ?", operatorID)
	}
	var n int64
	if err := tx.Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count quotes expiring 30d: %w", err)
	}
	return n, nil
}

// CountSupplierDepsForBuyer 单点依赖数：该 buyer 拥有的供应商中，按 SKU 去重后只有 1 个有效报价的 SKU 数。
//
// 实现：quote_sheet → quote_item → quote_component；对每个 (sku_id) 数 distinct supplier_id
// where status='EFFECTIVE'；只保留 distinct_supplier_count=1 的；再按 buyer 的供应商归属过滤。
func (r *WorkbenchRepo) CountSupplierDepsForBuyer(ctx context.Context, operatorID int64, ownerScope bool) (int64, error) {
	// 简化为：该 buyer 负责的 SKU 集合中，每个 SKU 当前只有 1 个 EFFECTIVE 报价的 SKU 数量。
	sub := r.txOf(ctx).Table("quote_sheet qs").
		Joins("JOIN quote_item qi ON qi.quote_sheet_id = qs.id").
		Joins("JOIN quote_component qc ON qc.quote_item_id = qi.id").
		Where("qs.status = 'EFFECTIVE'")
	if ownerScope {
		sub = sub.Joins("JOIN supplier_profile sp ON sp.id = qs.supplier_id").
			Where("sp.owner_procurement_operator_id = ?", operatorID)
	}
	sub = sub.Select("qi.sku_id").
		Group("qi.sku_id").
		Having("COUNT(DISTINCT qs.supplier_id) = 1")
	var n int64
	if err := r.txOf(ctx).Table("(?) AS t", sub).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count single deps: %w", err)
	}
	return n, nil
}

// ------------------------------------------------------------
// 定价运营卡片
// ------------------------------------------------------------

// CountPriceBooksPending 待发布价目表数（DRAFT/APPROVING）。
func (r *WorkbenchRepo) CountPriceBooksPending(ctx context.Context) (int64, error) {
	var n int64
	if err := r.txOf(ctx).Table("price_book").
		Where("status IN ('DRAFT','APPROVING')").
		Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count price books pending: %w", err)
	}
	return n, nil
}

// LoadEffectivePriceBookItemsWithFloor 读当前生效价目表条目（sku_id + 代表组件 unit_price）。
// **不 SELECT floor_price**：破 floor 判定在 service 层算（floor 是成本侧概念，不应外泄）。
func (r *WorkbenchRepo) LoadEffectivePriceBookItemsWithFloor(ctx context.Context) ([]workbench.PriceBookItemForFloor, error) {
	type row struct {
		SKUID     int64  `gorm:"column:sku_id"`
		UnitPrice string `gorm:"column:unit_price"`
		Currency  string `gorm:"column:currency"`
	}
	var rows []row
	err := r.txOf(ctx).Table("price_book_item pbi").
		Joins("JOIN price_book pb ON pb.id = pbi.price_book_id").
		Joins(`LEFT JOIN LATERAL (
			SELECT pbc.unit_price
			FROM price_book_component pbc
			WHERE pbc.price_book_item_id = pbi.id
			ORDER BY CASE WHEN pbc.component_type = 'input' THEN 0 ELSE 1 END, pbc.component_type
			LIMIT 1
		) rep ON true`).
		Where("pb.status = 'EFFECTIVE'").
		Select("pbi.sku_id, COALESCE(rep.unit_price::text,'') AS unit_price, pbi.currency").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load price book items: %w", err)
	}
	out := make([]workbench.PriceBookItemForFloor, 0, len(rows))
	for _, rw := range rows {
		out = append(out, workbench.PriceBookItemForFloor{
			SKUID:     rw.SKUID,
			UnitPrice: rw.UnitPrice,
			Currency:  rw.Currency,
		})
	}
	return out, nil
}

// LoadCurrentCostBaseline 读某 SKU 当前生效成本基线：loss_rate/channel_rate + components。
// components 从 cost_component 表（不是 calc_snapshot jsonb，更易读且业务一致）。
//
// 返回 nil,nil 表示无基线。
//
// ⚠️ 本方法返回的 components.unit_cost 是**成本数据**，只能在 service 内部用于 floor 计算，
// 绝不写入任何 API DTO。
func (r *WorkbenchRepo) LoadCurrentCostBaseline(ctx context.Context, skuID int64) (*workbench.CostBaselineForFloor, error) {
	type blRow struct {
		ID          int64  `gorm:"column:id"`
		Currency    string `gorm:"column:currency"`
		LossRate    string `gorm:"column:loss_rate"`
		ChannelRate string `gorm:"column:channel_rate"`
	}
	var bl blRow
	err := r.txOf(ctx).Table("cost_baseline").
		Where("sku_id = ? AND is_current = true", skuID).
		Select("id, currency, loss_rate::text AS loss_rate, channel_rate::text AS channel_rate").
		Take(&bl).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load cost_baseline sku=%d: %w", skuID, err)
	}

	type compRow struct {
		ComponentType string `gorm:"column:component_type"`
		UnitCost      string `gorm:"column:unit_cost"`
	}
	var comps []compRow
	if err := r.txOf(ctx).Table("cost_component").
		Where("cost_baseline_id = ?", bl.ID).
		Select("component_type, unit_cost::text AS unit_cost").
		Order("component_type").
		Scan(&comps).Error; err != nil {
		return nil, fmt.Errorf("load cost_component baseline=%d: %w", bl.ID, err)
	}
	out := &workbench.CostBaselineForFloor{
		SKUID:       skuID,
		Currency:    bl.Currency,
		LossRate:    bl.LossRate,
		ChannelRate: bl.ChannelRate,
		Components:  make([]workbench.CostComponentSnapshot, 0, len(comps)),
	}
	for _, c := range comps {
		out.Components = append(out.Components, workbench.CostComponentSnapshot{
			ComponentType: c.ComponentType,
			UnitCost:      c.UnitCost,
		})
	}
	return out, nil
}

// LoadMinGrossMargin 从 sys_config 读 min_gross_margin（全局，与 cost.CostBaselineRepo 同口径）。
func (r *WorkbenchRepo) LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error) {
	var val string
	if err := r.txOf(ctx).Table("sys_config").
		Where("config_key = 'min_gross_margin'").
		Select("config_value").
		Scan(&val).Error; err != nil {
		return decimal.Zero, fmt.Errorf("read sys_config min_gross_margin: %w", err)
	}
	if val == "" {
		return decimal.Zero, errors.New("sys_config min_gross_margin not found")
	}
	d, err := decimal.NewFromString(val)
	if err != nil {
		return decimal.Zero, fmt.Errorf("sys_config min_gross_margin %q 非法: %w", val, err)
	}
	return d, nil
}

// CountPendingUpconduction 成本上涨待决策数（裁决 6）。
func (r *WorkbenchRepo) CountPendingUpconduction(ctx context.Context) (int64, error) {
	var n int64
	if err := r.txOf(ctx).Table("price_upconduction").
		Where("status = 'PENDING'").
		Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count pending upconduction: %w", err)
	}
	return n, nil
}

// ------------------------------------------------------------
// 销售卡片
// ------------------------------------------------------------

// CountMyCustomers 我的客户数（裁决：按 customer_profile.owner_sales_operator_id=operatorID）。
func (r *WorkbenchRepo) CountMyCustomers(ctx context.Context, operatorID int64) (int64, error) {
	var n int64
	if err := r.txOf(ctx).Table("customer_profile").
		Where("owner_sales_operator_id = ?", operatorID).
		Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count my customers: %w", err)
	}
	return n, nil
}

// CountPendingCustomerQuotes 待确认报价数（裁决 7：customer_quote.status='PENDING' 且归我）。
func (r *WorkbenchRepo) CountPendingCustomerQuotes(ctx context.Context, operatorID int64) (int64, error) {
	var n int64
	if err := r.txOf(ctx).Table("customer_quote").
		Where("status = 'PENDING' AND owner_sales_operator_id = ?", operatorID).
		Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count pending customer quotes: %w", err)
	}
	return n, nil
}

// ------------------------------------------------------------
// 财务卡片
// ------------------------------------------------------------

// CountUnpaidDeposit 待处理押金/授信（裁决 8：customer_profile.deposit_status='UNPAID'）。
func (r *WorkbenchRepo) CountUnpaidDeposit(ctx context.Context) (int64, error) {
	var n int64
	if err := r.txOf(ctx).Table("customer_profile").
		Where("deposit_status = 'UNPAID'").
		Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count unpaid deposit: %w", err)
	}
	return n, nil
}

// suppress unused import warnings（encoding/json 预留，若 calc_snapshot 解析需要再启用）
var _ = json.Marshal
