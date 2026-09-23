// Package repo 的 openapi.go：11a 开放接口仓储（GORM 实现）。
//
// 红线对齐：
//   - txOf(ctx) 优先复用幂等中间件注入的事务（db.FromContext）；
//   - 字段剔除在 SQL 层：不 SELECT cost/margin/floor_price/calc_snapshot/supplier_cost；
//   - 金额字符串：unit_cost / unit_price 全部 StringFixed(8)；
//   - 开放接口无行级过滤（成本/比价类按 §3.2 决议放开）。
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"model_bss/internal/domain/openapi"
	"model_bss/internal/infra/db"
)

// OpenApiRepo 是 openapi.Store 的 GORM 实现。
type OpenApiRepo struct {
	base *gorm.DB
}

// NewOpenApiRepo 构造。
func NewOpenApiRepo(base *gorm.DB) *OpenApiRepo {
	return &OpenApiRepo{base: base}
}

var _ openapi.Store = (*OpenApiRepo)(nil)

func (r *OpenApiRepo) txOf(ctx context.Context) *gorm.DB {
	if tx := db.FromContext(ctx); tx != nil {
		return tx
	}
	return r.base
}

// ---- token ----

// LoadClientSecret 按 client_id 从 sys_config 读 client_secret。
// 不存在 → ( "", false, nil )；config_value 为空串 → ( "", true, nil )（视为不匹配）。
func (r *OpenApiRepo) LoadClientSecret(ctx context.Context, clientID string) (string, bool, error) {
	var val string
	err := r.txOf(ctx).Table("sys_config").
		Where("config_key = ?", "open_api.client_secret").
		Select("config_value").
		Take(&val).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("load sys_config open_api.client_secret: %w", err)
	}
	// client_id 也校验：config_key=open_api.client_id 的 config_value 必须等于入参
	var idVal string
	err = r.txOf(ctx).Table("sys_config").
		Where("config_key = ?", "open_api.client_id").
		Select("config_value").
		Take(&idVal).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("load sys_config open_api.client_id: %w", err)
	}
	if idVal != clientID {
		return "", false, nil
	}
	return val, true, nil
}

// SaveToken 写一条 open_api_token。
func (r *OpenApiRepo) SaveToken(ctx context.Context, clientID, tokenHash string, expiresAt time.Time, requestID string) error {
	row := map[string]any{
		"token_hash": tokenHash,
		"client_id":  clientID,
		"expires_at": expiresAt,
		"created_at": time.Now().UTC(),
	}
	if requestID != "" {
		row["request_id"] = requestID
	}
	if err := r.txOf(ctx).Table("open_api_token").Create(&row).Error; err != nil {
		return fmt.Errorf("insert open_api_token: %w", err)
	}
	return nil
}

// LoadToken 按 token_hash 查未过期 token。不存在或已过期 → ( "", false, nil )。
func (r *OpenApiRepo) LoadToken(ctx context.Context, tokenHash string) (string, bool, error) {
	var clientID string
	err := r.txOf(ctx).Table("open_api_token").
		Where("token_hash = ? AND expires_at > now()", tokenHash).
		Select("client_id").
		Take(&clientID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("load open_api_token: %w", err)
	}
	return clientID, true, nil
}

// ---- aliases ----

// LoadCacheVersion 读 cache_version.version；key 不存在 → ( 0, false, nil )。
func (r *OpenApiRepo) LoadCacheVersion(ctx context.Context, cacheKey string) (int64, bool, error) {
	var version int64
	err := r.txOf(ctx).Table("cache_version").
		Where("cache_key = ?", cacheKey).
		Select("version").
		Take(&version).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("load cache_version %s: %w", cacheKey, err)
	}
	return version, true, nil
}

// LoadAliases 全量别名（联 model_sku 拿 sku_code，按 alias 升序）。
func (r *OpenApiRepo) LoadAliases(ctx context.Context) ([]openapi.AliasItem, error) {
	var rows []struct {
		Alias   string `gorm:"column:alias"`
		SKUID   int64  `gorm:"column:sku_id"`
		SKUCode string `gorm:"column:sku_code"`
	}
	err := r.txOf(ctx).Table("model_alias ma").
		Joins("JOIN model_sku ms ON ms.id = ma.sku_id").
		Select("ma.alias, ma.sku_id, ms.sku_code").
		Order("ma.alias").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load aliases: %w", err)
	}
	items := make([]openapi.AliasItem, 0, len(rows))
	for _, rw := range rows {
		items = append(items, openapi.AliasItem{Alias: rw.Alias, SKUID: rw.SKUID, SKUCode: rw.SKUCode})
	}
	return items, nil
}

// ---- sellable-models ----

// LoadSellableModels 上架可售 SKU（lifecycle_status IN ('PUBLISHED','PURCHASABLE')）。
func (r *OpenApiRepo) LoadSellableModels(ctx context.Context) ([]openapi.SellableModelItem, error) {
	var rows []struct {
		SKUID          int64   `gorm:"column:id"`
		SKUCode        string  `gorm:"column:sku_code"`
		ModelType      string  `gorm:"column:model_type"`
		NativeCurrency string  `gorm:"column:native_currency"`
		TierTag        *string `gorm:"column:tier_tag"`
		Tags           []byte  `gorm:"column:tags"`
	}
	err := r.txOf(ctx).Table("model_sku").
		Where("lifecycle_status IN ?", []string{"PUBLISHED", "PURCHASABLE"}).
		Select("id, sku_code, model_type, native_currency, tier_tag, tags").
		Order("id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load sellable models: %w", err)
	}
	items := make([]openapi.SellableModelItem, 0, len(rows))
	for _, rw := range rows {
		tags := []string{}
		if len(rw.Tags) > 0 {
			_ = json.Unmarshal(rw.Tags, &tags)
		}
		items = append(items, openapi.SellableModelItem{
			SKUID: rw.SKUID, SKUCode: rw.SKUCode, ModelType: rw.ModelType,
			NativeCurrency: rw.NativeCurrency, TierTag: rw.TierTag, LevelTags: tags,
		})
	}
	return items, nil
}

// ---- baseline ----

// LoadCurrentBaseline 读当前成本基线（is_current）。
// 不存在 → ( nil, false, nil )；Version 供 cost-snapshot 用。
func (r *OpenApiRepo) LoadCurrentBaseline(ctx context.Context, skuID int64) (*openapi.BaselineInfo, error) {
	return r.loadBaseline(ctx, skuID, nil)
}

// LoadBaselineAt 读 asOf 时刻生效的成本基线（valid_from <= asOf AND (valid_to IS NULL OR valid_to > asOf)）。
// 不存在 → ( nil, false, nil )。
func (r *OpenApiRepo) LoadBaselineAt(ctx context.Context, skuID int64, asOf time.Time) (*openapi.BaselineInfo, error) {
	return r.loadBaseline(ctx, skuID, &asOf)
}

func (r *OpenApiRepo) loadBaseline(ctx context.Context, skuID int64, asOf *time.Time) (*openapi.BaselineInfo, error) {
	q := r.txOf(ctx).Table("cost_baseline cb").
		Select(`cb.version, cb.currency, cb.primary_supplier_id,
			rc.component_type AS unit_cost_basis, rc.unit_cost`).
		Joins(`LEFT JOIN (
			SELECT DISTINCT ON (cost_baseline_id) cost_baseline_id, component_type, unit_cost
			FROM cost_component
			ORDER BY cost_baseline_id,
			         CASE WHEN component_type = 'input' THEN 0 ELSE 1 END,
			         component_type
		) rc ON rc.cost_baseline_id = cb.id`).
		Where("cb.sku_id = ?", skuID)
	if asOf != nil {
		q = q.Where("cb.valid_from <= ? AND (cb.valid_to IS NULL OR cb.valid_to > ?)", *asOf, *asOf).
			Order("cb.version DESC").Limit(1)
	} else {
		q = q.Where("cb.is_current")
	}
	var row struct {
		Version           int    `gorm:"column:version"`
		Currency          string `gorm:"column:currency"`
		PrimarySupplierID int64  `gorm:"column:primary_supplier_id"`
		UnitCostBasis     string `gorm:"column:unit_cost_basis"`
		UnitCost          string `gorm:"column:unit_cost"`
	}
	err := q.Scan(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load baseline sku=%d: %w", skuID, err)
	}
	if row.Version == 0 {
		return nil, nil
	}
	// 代表组件缺失 → 空串（不静默造数）
	unitCost := ""
	if row.UnitCost != "" {
		if d, perr := decimal.NewFromString(row.UnitCost); perr == nil {
			unitCost = d.StringFixed(8)
		}
	}
	return &openapi.BaselineInfo{
		Version: row.Version, Currency: row.Currency, PrimarySupplierID: row.PrimarySupplierID,
		UnitCost: unitCost, UnitCostBasis: row.UnitCostBasis,
	}, nil
}

// ---- price-book ----

// LoadEffectivePriceBook 读当前生效价目表（联 model_sku + 代表组件 unit_price）。
// 不存在 → ( nil, false, nil )。
func (r *OpenApiRepo) LoadEffectivePriceBook(ctx context.Context, levelCode string) (*openapi.PriceBookResult, error) {
	var book struct {
		ID        int64 `gorm:"column:id"`
		VersionNo int   `gorm:"column:version_no"`
	}
	err := r.txOf(ctx).Table("price_book").
		Where("level_code = ? AND status = 'EFFECTIVE'", levelCode).
		Select("id, version_no").
		Take(&book).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load price_book level=%s: %w", levelCode, err)
	}
	var rows []struct {
		SKUID     int64  `gorm:"column:sku_id"`
		SKUCode   string `gorm:"column:sku_code"`
		Currency  string `gorm:"column:currency"`
		UnitPrice string `gorm:"column:unit_price"`
	}
	err = r.txOf(ctx).Table("price_book_item pbi").
		Joins("JOIN model_sku ms ON ms.id = pbi.sku_id").
		Joins(`LEFT JOIN LATERAL (
			SELECT pbc.unit_price
			FROM price_book_component pbc
			WHERE pbc.price_book_item_id = pbi.id
			ORDER BY CASE WHEN pbc.component_type = 'input' THEN 0 ELSE 1 END, pbc.component_type
			LIMIT 1
		) comp ON true`).
		Where("pbi.price_book_id = ?", book.ID).
		Select("pbi.sku_id, ms.sku_code, pbi.currency, comp.unit_price").
		Order("pbi.sku_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load price_book items book=%d: %w", book.ID, err)
	}
	items := make([]openapi.PriceBookItem, 0, len(rows))
	currency := ""
	for _, rw := range rows {
		if currency == "" {
			currency = rw.Currency
		}
		up, perr := decimal.NewFromString(rw.UnitPrice)
		if perr != nil {
			return nil, fmt.Errorf("parse unit_price sku=%d %q: %w", rw.SKUID, rw.UnitPrice, perr)
		}
		items = append(items, openapi.PriceBookItem{
			SKUID: rw.SKUID, SKUCode: rw.SKUCode, Currency: rw.Currency, UnitPrice: up.StringFixed(8),
		})
	}
	return &openapi.PriceBookResult{
		LevelCode: levelCode, VersionNo: book.VersionNo, Currency: currency, Items: items,
	}, nil
}

// ---- events ----

// PullEvents 增量拉取 event_outbox（id > since，ORDER BY id LIMIT 100）。
func (r *OpenApiRepo) PullEvents(ctx context.Context, sinceID int64, limit int) ([]openapi.EventItem, error) {
	var rows []struct {
		ID        int64           `gorm:"column:id"`
		EventType string          `gorm:"column:event_type"`
		Payload   json.RawMessage `gorm:"column:payload"`
		CreatedAt time.Time       `gorm:"column:created_at"`
	}
	err := r.txOf(ctx).Table("event_outbox").
		Where("id > ?", sinceID).
		Select("id, event_type, payload, created_at").
		Order("id").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("pull events since=%d: %w", sinceID, err)
	}
	items := make([]openapi.EventItem, 0, len(rows))
	for _, rw := range rows {
		payload := rw.Payload
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		items = append(items, openapi.EventItem{
			ID: rw.ID, EventType: rw.EventType, Payload: payload,
			CreatedAt: rw.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return items, nil
}

// ---- sku resolve ----

// ResolveSKUID 把 {sku} 解析为 sku_id（纯数字按 id，否则按 sku_code）。
// 不存在 → ( 0, false, nil )；sku_code 撞多行 → error。
func (r *OpenApiRepo) ResolveSKUID(ctx context.Context, sku string) (int64, bool, error) {
	tx := r.txOf(ctx)
	if id, err := strconv.ParseInt(sku, 10, 64); err == nil && id > 0 {
		var n int64
		if err := tx.Table("model_sku").Where("id = ?", id).Count(&n).Error; err != nil {
			return 0, false, fmt.Errorf("resolve sku id=%d: %w", id, err)
		}
		return id, n > 0, nil
	}
	var ids []int64
	if err := tx.Table("model_sku").Where("sku_code = ?", sku).Pluck("id", &ids).Error; err != nil {
		return 0, false, fmt.Errorf("resolve sku code=%s: %w", sku, err)
	}
	switch len(ids) {
	case 0:
		return 0, false, nil
	case 1:
		return ids[0], true, nil
	default:
		return 0, false, fmt.Errorf("sku_code %s 对应 %d 行（数据事故）", sku, len(ids))
	}
}

// ---- calc input ----

// LoadCalcInput 装配四因子评分输入（报价 + 参数 + 供应商状态）。
func (r *OpenApiRepo) LoadCalcInput(ctx context.Context, skuID int64) (*openapi.CalcInput, error) {
	tx := r.txOf(ctx)
	// 报价：quote_sheet EFFECTIVE + quote_item + quote_component + model_sku
	var rows []struct {
		SupplierID   int64     `gorm:"column:supplier_id"`
		QuoteSheetID int64     `gorm:"column:quote_sheet_id"`
		QuoteVersion int       `gorm:"column:version_no"`
		ValidFrom    time.Time `gorm:"column:valid_from"`
		Currency     string    `gorm:"column:currency"`
		Constraints  []byte    `gorm:"column:constraints_"`
		SKUCode      string    `gorm:"column:sku_code"`
		SKUCurrency  string    `gorm:"column:sku_currency"`
		CompType     string    `gorm:"column:component_type"`
		UnitPrice    string    `gorm:"column:unit_price"`
	}
	err := tx.Table("quote_sheet qs").
		Select(`qs.supplier_id, qs.id AS quote_sheet_id, qs.version_no, qs.valid_from,
			qi.currency, qi.constraints_,
			ms.sku_code, ms.native_currency AS sku_currency,
			qc.component_type, qc.unit_price`).
		Joins("JOIN quote_item qi ON qi.quote_sheet_id = qs.id AND qi.sku_id = ?", skuID).
		Joins("JOIN model_sku ms ON ms.id = qi.sku_id").
		Joins("LEFT JOIN quote_component qc ON qc.quote_item_id = qi.id").
		Where("qs.status = 'EFFECTIVE'").
		Order("qs.supplier_id, qc.component_type").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load quotes sku=%d: %w", skuID, err)
	}
	input := &openapi.CalcInput{SKUID: skuID, Quotes: []openapi.QuoteInfo{}}
	quoteIdx := make(map[int64]int)
	for i := range rows {
		rw := &rows[i]
		if input.SKUCode == "" {
			input.SKUCode = rw.SKUCode
			input.Currency = rw.SKUCurrency
		}
		idx, ok := quoteIdx[rw.QuoteSheetID]
		if !ok {
			var constraints map[string]any
			if len(rw.Constraints) > 0 {
				_ = json.Unmarshal(rw.Constraints, &constraints)
			}
			input.Quotes = append(input.Quotes, openapi.QuoteInfo{
				SupplierID: rw.SupplierID, QuoteSheetID: rw.QuoteSheetID, QuoteVersion: rw.QuoteVersion,
				ValidFrom: rw.ValidFrom, Currency: rw.Currency, Constraints: constraints,
				Components: []openapi.ComponentInfo{},
			})
			idx = len(input.Quotes) - 1
			quoteIdx[rw.QuoteSheetID] = idx
		}
		if rw.CompType != "" {
			price, perr := decimal.NewFromString(rw.UnitPrice)
			if perr != nil {
				return nil, fmt.Errorf("quote_component unit_price %q: %w", rw.UnitPrice, perr)
			}
			q := &input.Quotes[idx]
			q.Components = append(q.Components, openapi.ComponentInfo{ComponentType: rw.CompType, UnitPrice: price})
		}
	}
	// 参数：全部三级行（ResolveParams 在 domain 层解析）
	paramRepo := NewCostParamRepo(r.baseOf())
	params, err := paramRepo.ListAllParams(ctx)
	if err != nil {
		return nil, err
	}
	input.Params = make([]openapi.ParamInfo, 0, len(params))
	for _, p := range params {
		input.Params = append(input.Params, openapi.ParamInfo{
			ScopeType: p.ScopeType, ScopeID: p.ScopeID,
			LossRate: p.LossRate, ChannelRate: p.ChannelRate,
		})
	}
	// 供应商状态
	if len(input.Quotes) > 0 {
		supplierIDs := make([]int64, 0, len(input.Quotes))
		seen := make(map[int64]bool)
		for _, q := range input.Quotes {
			if !seen[q.SupplierID] {
				seen[q.SupplierID] = true
				supplierIDs = append(supplierIDs, q.SupplierID)
			}
		}
		var statuses []openapi.StatusInfo
		if err := tx.Table("supplier_profile").
			Select("id AS supplier_id, qual_status, settle_status, status").
			Where("id IN ?", supplierIDs).
			Scan(&statuses).Error; err != nil {
			return nil, fmt.Errorf("load supplier status sku=%d: %w", skuID, err)
		}
		input.Statuses = statuses
	}
	return input, nil
}

// baseOf 暴露基础 DB（CostParamRepo 构造用）。
func (r *OpenApiRepo) baseOf() *gorm.DB { return r.base }
