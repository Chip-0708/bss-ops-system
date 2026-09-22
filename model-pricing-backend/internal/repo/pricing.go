package repo

import (
	"context"
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
// 8a 定价策略 + 生成价目表草稿（08-pricing.md §1/§2）
//
// 表结构纪律（000005_pricing.up.sql，与 stage8a 提示词裁决对齐）：
//   - pricing_policy：price_method/param_value/rounding_rule（NOT NULL DEFAULT 'CEIL'）。
//   - price_book：无 currency / 无 is_current 列；uk_pb_ver UNIQUE(level_code, version_no)
//     → 草稿 version_no 取 MAX(version_no)+1（不是 0）；created_by NOT NULL。
//   - price_book_item：currency char(3) + floor_price + policy_id + baseline_version。
//   - price_book_component：逐组件售价（本批只落代表组件一行）。
// ============================================================

// PricingRepo 实现 pricing.PricingStore。
type PricingRepo struct {
	base *gorm.DB
}

// NewPricingRepo 构造仓储。
func NewPricingRepo(base *gorm.DB) *PricingRepo { return &PricingRepo{base: base} }

var _ pricing.PricingStore = (*PricingRepo)(nil)

func (r *PricingRepo) txOf(ctx context.Context) *gorm.DB {
	if tx := db.FromContext(ctx); tx != nil {
		return tx
	}
	return r.base
}

// ---- 行模型 ----

type pricingPolicyRow struct {
	ID           int64     `gorm:"primaryKey"`
	Code         string    `gorm:"column:code"`
	Name         string    `gorm:"column:name"`
	ScopeType    string    `gorm:"column:scope_type"`
	ScopeID      *int64    `gorm:"column:scope_id"`
	LevelCode    *string   `gorm:"column:level_code"`
	CustomerID   *int64    `gorm:"column:customer_id"`
	PriceMethod  string    `gorm:"column:price_method"`
	ParamValue   string    `gorm:"column:param_value"`
	RoundingRule string    `gorm:"column:rounding_rule"`
	Priority     int       `gorm:"column:priority"`
	Status       string    `gorm:"column:status"`
	CreatedAt    time.Time `gorm:"column:created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
	RequestID    *string   `gorm:"column:request_id"`
	CreatedBy    *int64    `gorm:"column:created_by"`
	UpdatedBy    *int64    `gorm:"column:updated_by"`
}

func (pricingPolicyRow) TableName() string { return "pricing_policy" }

type priceBookRow struct {
	ID            int64      `gorm:"primaryKey"`
	VersionNo     int        `gorm:"column:version_no"`
	LevelCode     string     `gorm:"column:level_code"`
	Status        string     `gorm:"column:status"`
	EffectiveTime *time.Time `gorm:"column:effective_time"`
	ValidTo       *time.Time `gorm:"column:valid_to"`
	RollbackOf    *int64     `gorm:"column:rollback_of"`
	DiffReport    []byte     `gorm:"column:diff_report"`
	CreatedBy     int64      `gorm:"column:created_by"`
	CreatedAt     time.Time  `gorm:"column:created_at"`
	UpdatedAt     time.Time  `gorm:"column:updated_at"`
	RequestID     *string    `gorm:"column:request_id"`
	UpdatedBy     *int64     `gorm:"column:updated_by"`
}

func (priceBookRow) TableName() string { return "price_book" }

type priceBookItemRow struct {
	ID              int64     `gorm:"primaryKey"`
	PriceBookID     int64     `gorm:"column:price_book_id"`
	SkuID           int64     `gorm:"column:sku_id"`
	Currency        string    `gorm:"column:currency"`
	FloorPrice      string    `gorm:"column:floor_price"`
	PolicyID        int64     `gorm:"column:policy_id"`
	BaselineVersion int       `gorm:"column:baseline_version"`
	CreatedAt       time.Time `gorm:"column:created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at"`
	RequestID       *string   `gorm:"column:request_id"`
	CreatedBy       *int64    `gorm:"column:created_by"`
	UpdatedBy       *int64    `gorm:"column:updated_by"`
}

func (priceBookItemRow) TableName() string { return "price_book_item" }

type priceBookComponentRow struct {
	ID              int64     `gorm:"primaryKey"`
	PriceBookItemID int64     `gorm:"column:price_book_item_id"`
	ComponentType   string    `gorm:"column:component_type"`
	UnitPrice       string    `gorm:"column:unit_price"`
	CreatedAt       time.Time `gorm:"column:created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at"`
	RequestID       *string   `gorm:"column:request_id"`
	CreatedBy       *int64    `gorm:"column:created_by"`
	UpdatedBy       *int64    `gorm:"column:updated_by"`
}

func (priceBookComponentRow) TableName() string { return "price_book_component" }

// ---- 转换 ----

func policyRowToDomain(r pricingPolicyRow) pricing.PricingPolicy {
	return pricing.PricingPolicy{
		ID: r.ID, Code: r.Code, Name: r.Name, ScopeType: r.ScopeType, ScopeID: r.ScopeID,
		LevelCode: r.LevelCode, PriceMethod: r.PriceMethod, ParamValue: r.ParamValue,
		Priority: r.Priority, Status: r.Status,
	}
}

// ---- 策略 CRUD ----

// ListPolicies 策略列表（按 priority 升序、id 升序）。
func (r *PricingRepo) ListPolicies(ctx context.Context, page, size int) ([]pricing.PricingPolicy, int64, error) {
	tx := r.txOf(ctx)
	var total int64
	if err := tx.Model(&pricingPolicyRow{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count policies: %w", err)
	}
	var rows []pricingPolicyRow
	if err := tx.Order("priority, id").Limit(size).Offset((page - 1) * size).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list policies: %w", err)
	}
	out := make([]pricing.PricingPolicy, 0, len(rows))
	for _, rr := range rows {
		out = append(out, policyRowToDomain(rr))
	}
	return out, total, nil
}

// CreatePolicy 创建策略。
func (r *PricingRepo) CreatePolicy(ctx context.Context, p *pricing.PricingPolicy, operatorID int64, requestID string) error {
	now := time.Now().UTC()
	row := pricingPolicyRow{
		Code: p.Code, Name: p.Name, ScopeType: p.ScopeType, ScopeID: p.ScopeID,
		LevelCode: p.LevelCode, PriceMethod: p.PriceMethod, ParamValue: p.ParamValue,
		Priority: p.Priority, Status: p.Status,
		CreatedAt: now, UpdatedAt: now,
		RequestID: strPtr(requestID), CreatedBy: int64Ptr(operatorID), UpdatedBy: int64Ptr(operatorID),
	}
	if err := r.txOf(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("insert pricing_policy: %w", err)
	}
	p.ID = row.ID
	return nil
}

// UpdatePolicy 更新策略（按 id 全量字段更新）。
func (r *PricingRepo) UpdatePolicy(ctx context.Context, p *pricing.PricingPolicy, operatorID int64, requestID string) error {
	now := time.Now().UTC()
	res := r.txOf(ctx).Model(&pricingPolicyRow{}).Where("id = ?", p.ID).Updates(map[string]any{
		"code": p.Code, "name": p.Name, "scope_type": p.ScopeType, "scope_id": p.ScopeID,
		"level_code": p.LevelCode, "price_method": p.PriceMethod, "param_value": p.ParamValue,
		"priority": p.Priority, "status": p.Status,
		"updated_at": now, "updated_by": operatorID, "request_id": requestID,
	})
	if res.Error != nil {
		return fmt.Errorf("update pricing_policy %d: %w", p.ID, res.Error)
	}
	if res.RowsAffected == 0 {
		return pricing.ErrPolicyNotFound
	}
	return nil
}

// LoadPolicyByID 按 id 读策略。
func (r *PricingRepo) LoadPolicyByID(ctx context.Context, id int64) (*pricing.PricingPolicy, error) {
	var row pricingPolicyRow
	if err := r.txOf(ctx).Where("id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, pricing.ErrPolicyNotFound
		}
		return nil, fmt.Errorf("load policy %d: %w", id, err)
	}
	p := policyRowToDomain(row)
	return &p, nil
}

// LoadPoliciesByIDs 按 id 集读策略。
func (r *PricingRepo) LoadPoliciesByIDs(ctx context.Context, ids []int64) ([]pricing.PricingPolicy, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []pricingPolicyRow
	if err := r.txOf(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load policies by ids: %w", err)
	}
	out := make([]pricing.PricingPolicy, 0, len(rows))
	for _, rr := range rows {
		out = append(out, policyRowToDomain(rr))
	}
	return out, nil
}

// ---- 生成草稿的读侧 ----

// LoadSKUContexts 加载 SKU 上下文。skuIDs 空 = 全量在架（lifecycle_status='PUBLISHED'）。
func (r *PricingRepo) LoadSKUContexts(ctx context.Context, skuIDs []int64) ([]pricing.SKUContext, error) {
	tx := r.txOf(ctx).Table("model_sku").
		Select("id, sku_code, vendor_id, family_id, native_currency").
		Where("lifecycle_status = ?", "PUBLISHED")
	if len(skuIDs) > 0 {
		tx = tx.Where("id IN ?", skuIDs)
	}
	var rows []struct {
		ID             int64  `gorm:"column:id"`
		SKUCode        string `gorm:"column:sku_code"`
		VendorID       int64  `gorm:"column:vendor_id"`
		FamilyID       int64  `gorm:"column:family_id"`
		NativeCurrency string `gorm:"column:native_currency"`
	}
	if err := tx.Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load sku contexts: %w", err)
	}
	out := make([]pricing.SKUContext, 0, len(rows))
	for _, rr := range rows {
		out = append(out, pricing.SKUContext{
			SKUID: rr.ID, SKUCode: rr.SKUCode, VendorID: rr.VendorID,
			FamilyID: rr.FamilyID, Currency: rr.NativeCurrency,
		})
	}
	return out, nil
}

// LoadCurrentUnitCosts 批量读当前成本基线的代表组件 unit_cost + baseline version + currency。
// 代表组件口径 = representCompJoin（input 优先，否则字母序第一个）——与读侧同一份 SQL 片段。
func (r *PricingRepo) LoadCurrentUnitCosts(ctx context.Context, skuIDs []int64) (map[int64]pricing.UnitCostInfo, error) {
	out := make(map[int64]pricing.UnitCostInfo, len(skuIDs))
	if len(skuIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		SkuID    int64  `gorm:"column:sku_id"`
		Version  int    `gorm:"column:version"`
		Currency string `gorm:"column:currency"`
		UnitCost string `gorm:"column:unit_cost"`
		Basis    string `gorm:"column:unit_cost_basis"`
	}
	q := `SELECT cb.sku_id, cb.version, cb.currency, rc.unit_cost, rc.component_type AS unit_cost_basis
		FROM cost_baseline cb ` + representCompJoin + `
		WHERE cb.is_current AND cb.sku_id IN ? AND rc.unit_cost IS NOT NULL`
	if err := r.txOf(ctx).Raw(q, skuIDs).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load current unit costs: %w", err)
	}
	for _, rr := range rows {
		d, err := decimal.NewFromString(rr.UnitCost)
		if err != nil {
			return nil, fmt.Errorf("unit_cost sku=%d %q 非法: %w", rr.SkuID, rr.UnitCost, err)
		}
		out[rr.SkuID] = pricing.UnitCostInfo{UnitCost: d, UnitCostBasis: rr.Basis, BaselineVersion: rr.Version, Currency: rr.Currency}
	}
	return out, nil
}

// LoadCurrentOfficialPrices 批量读当前官方价（price_version is_current）的代表组件 unit_price。
// 代表组件口径与成本侧一致（input 优先，否则字母序第一个）。OFFICIAL_ANCHOR 策略用。
func (r *PricingRepo) LoadCurrentOfficialPrices(ctx context.Context, skuIDs []int64) (map[int64]decimal.Decimal, error) {
	out := make(map[int64]decimal.Decimal, len(skuIDs))
	if len(skuIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		SkuID     int64  `gorm:"column:sku_id"`
		UnitPrice string `gorm:"column:unit_price"`
	}
	q := `SELECT pv.sku_id, pc.unit_price::text
		FROM price_version pv
		JOIN LATERAL (
			SELECT unit_price FROM price_component
			WHERE price_version_id = pv.id
			ORDER BY CASE WHEN component_type = 'input' THEN 0 ELSE 1 END, component_type
			LIMIT 1
		) pc ON true
		WHERE pv.is_current AND pv.sku_id IN ?`
	if err := r.txOf(ctx).Raw(q, skuIDs).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load current official prices: %w", err)
	}
	for _, rr := range rows {
		d, err := decimal.NewFromString(rr.UnitPrice)
		if err != nil {
			return nil, fmt.Errorf("official price sku=%d %q 非法: %w", rr.SkuID, rr.UnitPrice, err)
		}
		out[rr.SkuID] = d
	}
	return out, nil
}

// LoadOldPrices 读该 level_code 上一 EFFECTIVE 版本各 SKU 的售价（代表组件）。
// 首次生成（无 EFFECTIVE 版本）返回空 map。
func (r *PricingRepo) LoadOldPrices(ctx context.Context, levelCode string, skuIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(skuIDs))
	if len(skuIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		SkuID     int64  `gorm:"column:sku_id"`
		UnitPrice string `gorm:"column:unit_price"`
	}
	// 上一 EFFECTIVE 版本（uk_pb_effective 保证同 level 最多一个）。
	q := `SELECT pbi.sku_id, pbc.unit_price::text
		FROM price_book pb
		JOIN price_book_item pbi ON pbi.price_book_id = pb.id
		JOIN LATERAL (
			SELECT unit_price FROM price_book_component
			WHERE price_book_item_id = pbi.id
			ORDER BY CASE WHEN component_type = 'input' THEN 0 ELSE 1 END, component_type
			LIMIT 1
		) pbc ON true
		WHERE pb.level_code = ? AND pb.status = 'EFFECTIVE' AND pbi.sku_id IN ?`
	if err := r.txOf(ctx).Raw(q, levelCode, skuIDs).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load old prices: %w", err)
	}
	for _, rr := range rows {
		out[rr.SkuID] = rr.UnitPrice
	}
	return out, nil
}

// LoadMinGrossMargin 读 sys_config.min_gross_margin。
func (r *PricingRepo) LoadMinGrossMargin(ctx context.Context) (decimal.Decimal, error) {
	var v string
	if err := r.txOf(ctx).Raw(`SELECT config_value FROM sys_config WHERE config_key = 'min_gross_margin'`).
		Row().Scan(&v); err != nil {
		return decimal.Zero, fmt.Errorf("load min_gross_margin: %w", err)
	}
	d, err := decimal.NewFromString(v)
	if err != nil {
		return decimal.Zero, fmt.Errorf("min_gross_margin %q 非法: %w", v, err)
	}
	return d, nil
}

// ---- 生成草稿的写侧（单事务） ----

// SaveDraft 落草稿：price_book(DRAFT, version_no=MAX+1) + price_book_item + price_book_component + diff_report。
func (r *PricingRepo) SaveDraft(ctx context.Context, in pricing.SaveDraftInput, operatorID int64, requestID string) (int64, error) {
	now := time.Now().UTC()
	var draftID int64
	err := r.txOf(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 下一个可用版本号（uk_pb_ver UNIQUE(level_code, version_no)，草稿不用 0）。
		var maxVer *int
		if err := tx.Raw(`SELECT MAX(version_no) FROM price_book WHERE level_code = ?`, in.LevelCode).
			Row().Scan(&maxVer); err != nil {
			return fmt.Errorf("load max version_no level=%s: %w", in.LevelCode, err)
		}
		nextVer := 1
		if maxVer != nil {
			nextVer = *maxVer + 1
		}

		// 2. diff_report 序列化。
		diffJSON, err := json.Marshal(in.DiffReport)
		if err != nil {
			return fmt.Errorf("marshal diff_report: %w", err)
		}

		// 3. price_book（status='DRAFT'，created_by NOT NULL）。
		book := priceBookRow{
			VersionNo: nextVer, LevelCode: in.LevelCode, Status: pricing.BookStatusDraft,
			DiffReport: diffJSON,
			CreatedBy:  operatorID, CreatedAt: now, UpdatedAt: now,
			RequestID: strPtr(requestID), UpdatedBy: int64Ptr(operatorID),
		}
		if err := tx.Create(&book).Error; err != nil {
			return fmt.Errorf("insert price_book: %w", err)
		}
		draftID = book.ID

		// 4. price_book_item + price_book_component（逐 SKU）。
		for _, it := range in.Items {
			item := priceBookItemRow{
				PriceBookID: book.ID, SkuID: it.SKUID, Currency: it.Currency,
				FloorPrice: it.FloorPrice, PolicyID: it.PolicyID, BaselineVersion: it.BaselineVersion,
				CreatedAt: now, UpdatedAt: now,
				RequestID: strPtr(requestID), CreatedBy: int64Ptr(operatorID), UpdatedBy: int64Ptr(operatorID),
			}
			if err := tx.Create(&item).Error; err != nil {
				return fmt.Errorf("insert price_book_item sku=%d: %w", it.SKUID, err)
			}
			// 代表组件售价（component_type 取该 SKU 的代表组件，与 LoadCurrentUnitCosts 口径一致）。
			comp := priceBookComponentRow{
				PriceBookItemID: item.ID, ComponentType: it.ComponentType, UnitPrice: it.NewPrice,
				CreatedAt: now, UpdatedAt: now,
				RequestID: strPtr(requestID), CreatedBy: int64Ptr(operatorID), UpdatedBy: int64Ptr(operatorID),
			}
			if err := tx.Create(&comp).Error; err != nil {
				return fmt.Errorf("insert price_book_component sku=%d: %w", it.SKUID, err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return draftID, nil
}
