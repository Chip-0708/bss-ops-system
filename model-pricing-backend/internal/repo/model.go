// Package repo 的 model.go：模型管理 M1 的 GORM 实现。
// 写操作统一经 db.FromContext(ctx) 取事务；幂等中间件会注入同一连接。
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"model_bss/internal/domain/model"
	"model_bss/internal/infra/db"
)

// ModelRepo 是 model.Store 的 GORM 实现。
type ModelRepo struct {
	base *gorm.DB
}

// NewModelRepo 构造模型仓储。
func NewModelRepo(base *gorm.DB) *ModelRepo { return &ModelRepo{base: base} }

var _ model.Store = (*ModelRepo)(nil)

// txOf 优先使用 context 中的事务；取不到回退基础 DB。
func (r *ModelRepo) txOf(ctx context.Context) *gorm.DB {
	if tx := db.FromContext(ctx); tx != nil {
		return tx
	}
	return r.base
}

// ---- 行模型 ----

type vendorRow struct {
	ID   int64  `gorm:"primaryKey"`
	Code string `gorm:"column:code"`
	Name string `gorm:"column:name"`
}

func (vendorRow) TableName() string { return "vendor" }

type familyRow struct {
	ID       int64  `gorm:"primaryKey"`
	VendorID int64  `gorm:"column:vendor_id"`
	Name     string `gorm:"column:name"`
}

func (familyRow) TableName() string { return "model_family" }

type skuRow struct {
	ID              int64      `gorm:"primaryKey"`
	VendorID        int64      `gorm:"column:vendor_id"`
	FamilyID        int64      `gorm:"column:family_id"`
	SkuCode         string     `gorm:"column:sku_code"`
	ModelType       string     `gorm:"column:model_type"`
	NativeCurrency  string     `gorm:"column:native_currency"`
	ContextWindow   *int       `gorm:"column:context_window"`
	Capability      []byte     `gorm:"column:capability"`
	VerifyStatus    string     `gorm:"column:verify_status"`
	TierTag         *string    `gorm:"column:tier_tag"`
	Tags            []byte     `gorm:"column:tags"`
	IsSensitive     bool       `gorm:"column:is_sensitive"`
	CrossBorder     bool       `gorm:"column:cross_border"`
	LifecycleStatus string     `gorm:"column:lifecycle_status"`
	SunsetDate      *time.Time `gorm:"column:sunset_date"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
	RequestID       *string    `gorm:"column:request_id"`
	CreatedBy       *int64     `gorm:"column:created_by"`
	UpdatedBy       *int64     `gorm:"column:updated_by"`
}

// skuListRow 仅用于列表查询扫描：skuRow 是**写模型**，不能包含 JOIN 出来的别名列，
// 否则 GORM 在 INSERT/UPDATE 时会把 vendor_name / family_name 当成真实列，
// 报 column "vendor_name" of relation "model_sku" does not exist。
// skuListRow 是列表查询专用行模型：**字段全部平铺，不内嵌 skuRow**。
//
// 踩坑记录：曾写成 `skuRow` 匿名内嵌 + 别名列，结果 GORM 扫不进内嵌字段
// （skuRow 自带 TableName()，GORM 会按关联表模型处理；补 gorm:"embedded" 也无效），
// 表现为列表接口返回 {id:0, sku_code:"", lifecycle_status:""} 但 vendor_name 有值——
// 只有直接定义在本结构上的别名列映射成功。平铺是最稳的写法，与 SupplierRepo 一致。
type skuListRow struct {
	ID              int64      `gorm:"column:id"`
	VendorID        int64      `gorm:"column:vendor_id"`
	FamilyID        int64      `gorm:"column:family_id"`
	SkuCode         string     `gorm:"column:sku_code"`
	ModelType       string     `gorm:"column:model_type"`
	NativeCurrency  string     `gorm:"column:native_currency"`
	ContextWindow   *int       `gorm:"column:context_window"`
	Capability      []byte     `gorm:"column:capability"`
	VerifyStatus    string     `gorm:"column:verify_status"`
	TierTag         *string    `gorm:"column:tier_tag"`
	Tags            []byte     `gorm:"column:tags"`
	IsSensitive     bool       `gorm:"column:is_sensitive"`
	CrossBorder     bool       `gorm:"column:cross_border"`
	LifecycleStatus string     `gorm:"column:lifecycle_status"`
	SunsetDate      *time.Time `gorm:"column:sunset_date"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
	RequestID       *string    `gorm:"column:request_id"`
	CreatedBy       *int64     `gorm:"column:created_by"`
	UpdatedBy       *int64     `gorm:"column:updated_by"`
	VendorName      *string    `gorm:"column:vendor_name"`
	FamilyName      *string    `gorm:"column:family_name"`
}

type familyListRow struct {
	FamilyID   int64  `gorm:"column:family_id"`
	FamilyName string `gorm:"column:family_name"`
	VendorID   int64  `gorm:"column:vendor_id"`
	VendorName string `gorm:"column:vendor_name"`
	SkuCount   int    `gorm:"column:sku_count"`
}

// toSkuRow 把平铺的列表行还原成写模型结构，复用 skuToDomain 的转换逻辑。
func (r skuListRow) toSkuRow() skuRow {
	return skuRow{
		ID:              r.ID,
		VendorID:        r.VendorID,
		FamilyID:        r.FamilyID,
		SkuCode:         r.SkuCode,
		ModelType:       r.ModelType,
		NativeCurrency:  r.NativeCurrency,
		ContextWindow:   r.ContextWindow,
		Capability:      r.Capability,
		VerifyStatus:    r.VerifyStatus,
		TierTag:         r.TierTag,
		Tags:            r.Tags,
		IsSensitive:     r.IsSensitive,
		CrossBorder:     r.CrossBorder,
		LifecycleStatus: r.LifecycleStatus,
		SunsetDate:      r.SunsetDate,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
		RequestID:       r.RequestID,
		CreatedBy:       r.CreatedBy,
		UpdatedBy:       r.UpdatedBy,
	}
}

func (skuRow) TableName() string { return "model_sku" }

type aliasRow struct {
	ID     int64  `gorm:"primaryKey"`
	SkuID  int64  `gorm:"column:sku_id"`
	Alias  string `gorm:"column:alias"`
	Source string `gorm:"column:source"`
}

func (aliasRow) TableName() string { return "model_alias" }

// ---- 查询 ----

// FindVendorByID 厂商存在性。
func (r *ModelRepo) FindVendorByID(ctx context.Context, id int64) (bool, error) {
	var n int64
	err := r.txOf(ctx).Table("vendor").Where("id = ?", id).Count(&n).Error
	return n > 0, err
}

// FindFamilyByID 系列存在性 + 所属 vendor。
func (r *ModelRepo) FindFamilyByID(ctx context.Context, id int64) (int64, bool, error) {
	var f familyRow
	err := r.txOf(ctx).Where("id = ?", id).Take(&f).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return f.VendorID, true, nil
}

// ExistsSkuCode sku_code 全局唯一。
func (r *ModelRepo) ExistsSkuCode(ctx context.Context, skuCode string) (bool, error) {
	var n int64
	err := r.txOf(ctx).Table("model_sku").Where("sku_code = ?", skuCode).Count(&n).Error
	return n > 0, err
}

// ExistsAlias 别名全局唯一。
func (r *ModelRepo) ExistsAlias(ctx context.Context, alias string) (bool, error) {
	var n int64
	err := r.txOf(ctx).Table("model_alias").Where("alias = ?", alias).Count(&n).Error
	return n > 0, err
}

// CreateSKU 新建 SKU（同事务写别名）。
func (r *ModelRepo) CreateSKU(ctx context.Context, in model.CreateSKUInput, operatorID int64, requestID string) (*model.SKU, error) {
	tx := r.txOf(ctx)

	var capJSON []byte
	if in.Capability != nil {
		b, err := json.Marshal(in.Capability)
		if err != nil {
			return nil, fmt.Errorf("marshal capability: %w", err)
		}
		capJSON = b
	}

	now := time.Now()
	row := skuRow{
		VendorID:       in.VendorID,
		FamilyID:       in.FamilyID,
		SkuCode:        in.SkuCode,
		ModelType:      in.ModelType,
		NativeCurrency: in.NativeCurrency,
		ContextWindow:  in.ContextWindow,
		Capability:     capJSON,
		// tags 列是 NOT NULL：字段为 nil 时 GORM 会显式写 NULL 从而报错
		// （数据库默认值只在字段未出现在 INSERT 列时才生效），故必须初始化为空数组
		Tags:            []byte("[]"),
		VerifyStatus:    model.VerifyUnverified,
		TierTag:         in.TierTag,
		IsSensitive:     in.IsSensitive,
		CrossBorder:     in.CrossBorder,
		LifecycleStatus: model.LifecycleDraft,
		CreatedAt:       now,
		UpdatedAt:       now,
		RequestID:       strPtr(requestID),
		CreatedBy:       int64Ptr(operatorID),
	}
	if err := tx.Create(&row).Error; err != nil {
		if isUniqueViolation(err, "uk_sku_code") {
			return nil, model.ErrDuplicateSkuCode
		}
		return nil, fmt.Errorf("insert model_sku: %w", err)
	}

	for _, a := range in.Aliases {
		ar := aliasRow{SkuID: row.ID, Alias: a, Source: "MANUAL"}
		if err := tx.Create(&ar).Error; err != nil {
			if isUniqueViolation(err, "uk_alias") {
				return nil, model.ErrDuplicateAlias
			}
			return nil, fmt.Errorf("insert model_alias: %w", err)
		}
	}

	return r.GetSKU(ctx, row.ID)
}

// UpdateSKU 维护 SKU（lifecycle_status 不可直接改）。
func (r *ModelRepo) UpdateSKU(ctx context.Context, id int64, in model.UpdateSKUInput, operatorID int64, requestID string) (*model.SKU, error) {
	tx := r.txOf(ctx)

	var existing skuRow
	err := tx.Where("id = ?", id).Take(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	updates := map[string]interface{}{
		"updated_at": time.Now(),
		"updated_by": operatorID,
	}
	if in.SkuCode != nil {
		updates["sku_code"] = *in.SkuCode
	}
	if in.ModelType != nil {
		updates["model_type"] = *in.ModelType
	}
	if in.NativeCurrency != nil {
		updates["native_currency"] = *in.NativeCurrency
	}
	if in.ContextWindow != nil {
		updates["context_window"] = *in.ContextWindow
	}
	if in.Capability != nil {
		b, err := json.Marshal(in.Capability)
		if err != nil {
			return nil, fmt.Errorf("marshal capability: %w", err)
		}
		updates["capability"] = b
	}
	if in.TierTag != nil {
		updates["tier_tag"] = in.TierTag
	}
	if in.IsSensitive != nil {
		updates["is_sensitive"] = *in.IsSensitive
	}
	if in.CrossBorder != nil {
		updates["cross_border"] = *in.CrossBorder
	}

	if err := tx.Table("model_sku").Where("id = ?", id).Updates(updates).Error; err != nil {
		if isUniqueViolation(err, "uk_sku_code") {
			return nil, model.ErrDuplicateSkuCode
		}
		return nil, fmt.Errorf("update model_sku: %w", err)
	}

	// 别名全量覆盖：删旧插新（§10.4 全量覆盖语义）
	if in.Aliases != nil {
		if err := tx.Table("model_alias").Where("sku_id = ?", id).Delete(&aliasRow{}).Error; err != nil {
			return nil, fmt.Errorf("delete old aliases: %w", err)
		}
		for _, a := range in.Aliases {
			ar := aliasRow{SkuID: id, Alias: a, Source: "MANUAL"}
			if err := tx.Create(&ar).Error; err != nil {
				if isUniqueViolation(err, "uk_alias") {
					return nil, model.ErrDuplicateAlias
				}
				return nil, fmt.Errorf("insert model_alias: %w", err)
			}
		}
	}

	return r.GetSKU(ctx, id)
}

// GetSKU 按 ID 取 SKU（含 vendor/family 名与别名）。
func (r *ModelRepo) GetSKU(ctx context.Context, id int64) (*model.SKU, error) {
	var row skuRow
	err := r.txOf(ctx).Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	sku := skuToDomain(row)

	// 冗余展示字段
	var v vendorRow
	if err := r.txOf(ctx).Where("id = ?", row.VendorID).Take(&v).Error; err == nil {
		sku.VendorName = v.Name
	}
	var f familyRow
	if err := r.txOf(ctx).Where("id = ?", row.FamilyID).Take(&f).Error; err == nil {
		sku.FamilyName = f.Name
	}

	var aliases []string
	if err := r.txOf(ctx).Table("model_alias").Where("sku_id = ?", id).Pluck("alias", &aliases).Error; err == nil {
		sku.Aliases = aliases
	}

	return sku, nil
}

// ListSKUs 列表查询。
func (r *ModelRepo) ListSKUs(ctx context.Context, q model.ListQuery) (*model.ListResult, error) {
	tx := r.txOf(ctx).Table("model_sku AS s").
		Select("s.*, v.name AS vendor_name, f.name AS family_name").
		Joins("LEFT JOIN vendor v ON v.id = s.vendor_id").
		Joins("LEFT JOIN model_family f ON f.id = s.family_id")

	if q.Keyword != "" {
		tx = tx.Where("s.sku_code ILIKE ?", "%"+q.Keyword+"%")
	}
	if q.VendorID != nil {
		tx = tx.Where("s.vendor_id = ?", *q.VendorID)
	}
	if q.FamilyID != nil {
		tx = tx.Where("s.family_id = ?", *q.FamilyID)
	}
	if q.ModelType != "" {
		tx = tx.Where("s.model_type = ?", q.ModelType)
	}
	if q.LifecycleStatus != "" {
		tx = tx.Where("s.lifecycle_status = ?", q.LifecycleStatus)
	}
	if q.TierTag != "" {
		tx = tx.Where("s.tier_tag = ?", q.TierTag)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, err
	}

	offset := (q.Page - 1) * q.Size
	var rows []skuListRow
	if err := tx.Offset(offset).Limit(q.Size).Order("s.id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	list := make([]model.SKU, 0, len(rows))
	for i := range rows {
		row := rows[i]
		sku := skuToDomain(row.toSkuRow())
		if row.VendorName != nil {
			sku.VendorName = *row.VendorName
		}
		if row.FamilyName != nil {
			sku.FamilyName = *row.FamilyName
		}
		list = append(list, *sku)
	}

	return &model.ListResult{List: list, Total: total, Page: q.Page, Size: q.Size}, nil
}

// ListFamilies 按系列计数和分页。筛选条件只决定系列是否命中，children 不做裁剪。
func (r *ModelRepo) ListFamilies(ctx context.Context, q model.ListQuery) (*model.FamilyListResult, error) {
	database := r.txOf(ctx)

	countQuery := database.Table("model_family AS f").
		Joins("JOIN model_sku AS matched ON matched.family_id = f.id")
	countQuery = applyFamilyFilters(countQuery, q, "f", "matched")

	var total int64
	if err := countQuery.Distinct("f.id").Count(&total).Error; err != nil {
		return nil, err
	}

	matchedSKU := database.Table("model_sku AS matched").
		Select("1").
		Where("matched.family_id = f.id")
	matchedSKU = applyFamilySKUFilters(matchedSKU, q, "matched")

	query := database.Table("model_family AS f").
		Select(`f.id AS family_id, f.name AS family_name, f.vendor_id,
			v.name AS vendor_name,
			(SELECT COUNT(*) FROM model_sku AS child_count WHERE child_count.family_id = f.id) AS sku_count`).
		Joins("LEFT JOIN vendor AS v ON v.id = f.vendor_id").
		Where("EXISTS (?)", matchedSKU)
	query = applyFamilyIdentityFilters(query, q, "f")

	offset := (q.Page - 1) * q.Size
	var rows []familyListRow
	if err := query.Order("f.id DESC").Offset(offset).Limit(q.Size).Find(&rows).Error; err != nil {
		return nil, err
	}

	result := &model.FamilyListResult{
		List:  make([]model.FamilyView, 0, len(rows)),
		Total: total,
		Page:  q.Page,
		Size:  q.Size,
	}
	if len(rows) == 0 {
		return result, nil
	}

	familyIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		familyIDs = append(familyIDs, row.FamilyID)
		result.List = append(result.List, model.FamilyView{
			FamilyID:   row.FamilyID,
			FamilyName: row.FamilyName,
			VendorID:   row.VendorID,
			VendorName: row.VendorName,
			SkuCount:   row.SkuCount,
			Children:   []model.SKU{},
		})
	}

	var childRows []skuListRow
	if err := database.Table("model_sku AS s").
		Select("s.*, v.name AS vendor_name, f.name AS family_name").
		Joins("LEFT JOIN vendor AS v ON v.id = s.vendor_id").
		Joins("LEFT JOIN model_family AS f ON f.id = s.family_id").
		Where("s.family_id IN ?", familyIDs).
		Order("s.family_id ASC, s.id ASC").
		Find(&childRows).Error; err != nil {
		return nil, err
	}

	skuIDs := make([]int64, 0, len(childRows))
	childrenByFamily := make(map[int64][]model.SKU, len(rows))
	childrenByID := make(map[int64]*model.SKU, len(childRows))
	for i := range childRows {
		row := childRows[i]
		sku := skuToDomain(row.toSkuRow())
		if row.VendorName != nil {
			sku.VendorName = *row.VendorName
		}
		if row.FamilyName != nil {
			sku.FamilyName = *row.FamilyName
		}
		sku.Aliases = []string{}
		childrenByFamily[sku.FamilyID] = append(childrenByFamily[sku.FamilyID], *sku)
		skuIDs = append(skuIDs, sku.ID)
	}

	for familyID, children := range childrenByFamily {
		for i := range children {
			childrenByID[children[i].ID] = &children[i]
		}
		childrenByFamily[familyID] = children
	}
	if len(skuIDs) > 0 {
		var aliases []aliasRow
		if err := database.Where("sku_id IN ?", skuIDs).Order("id ASC").Find(&aliases).Error; err != nil {
			return nil, err
		}
		for _, alias := range aliases {
			if child := childrenByID[alias.SkuID]; child != nil {
				child.Aliases = append(child.Aliases, alias.Alias)
			}
		}
	}

	for i := range result.List {
		result.List[i].Children = childrenByFamily[result.List[i].FamilyID]
	}
	return result, nil
}

// ListOptions 返回模型筛选和新建表单所需的全部厂商、系列。
func (r *ModelRepo) ListOptions(ctx context.Context) (*model.OptionsResult, error) {
	database := r.txOf(ctx)

	var vendorRows []vendorRow
	if err := database.Order("name ASC, id ASC").Find(&vendorRows).Error; err != nil {
		return nil, err
	}
	var familyRows []familyRow
	if err := database.Order("vendor_id ASC, name ASC, id ASC").Find(&familyRows).Error; err != nil {
		return nil, err
	}

	result := &model.OptionsResult{
		Vendors:  make([]model.VendorOption, 0, len(vendorRows)),
		Families: make([]model.FamilyOption, 0, len(familyRows)),
	}
	for _, row := range vendorRows {
		result.Vendors = append(result.Vendors, model.VendorOption{ID: row.ID, Name: row.Name})
	}
	for _, row := range familyRows {
		result.Families = append(result.Families, model.FamilyOption{
			ID: row.ID, VendorID: row.VendorID, Name: row.Name,
		})
	}
	return result, nil
}

func applyFamilyFilters(query *gorm.DB, q model.ListQuery, familyAlias, skuAlias string) *gorm.DB {
	query = applyFamilyIdentityFilters(query, q, familyAlias)
	return applyFamilySKUFilters(query, q, skuAlias)
}

func applyFamilyIdentityFilters(query *gorm.DB, q model.ListQuery, alias string) *gorm.DB {
	if q.VendorID != nil {
		query = query.Where(alias+".vendor_id = ?", *q.VendorID)
	}
	if q.FamilyID != nil {
		query = query.Where(alias+".id = ?", *q.FamilyID)
	}
	return query
}

func applyFamilySKUFilters(query *gorm.DB, q model.ListQuery, alias string) *gorm.DB {
	if q.Keyword != "" {
		query = query.Where(alias+".sku_code ILIKE ?", "%"+q.Keyword+"%")
	}
	if q.ModelType != "" {
		query = query.Where(alias+".model_type = ?", q.ModelType)
	}
	if q.LifecycleStatus != "" {
		query = query.Where(alias+".lifecycle_status = ?", q.LifecycleStatus)
	}
	if q.TierTag != "" {
		query = query.Where(alias+".tier_tag = ?", q.TierTag)
	}
	return query
}

// skuToDomain 行模型转领域模型（值传递，便于列表行直接构造后传入）。
func skuToDomain(row skuRow) *model.SKU {
	sku := &model.SKU{
		ID:              row.ID,
		VendorID:        row.VendorID,
		FamilyID:        row.FamilyID,
		SkuCode:         row.SkuCode,
		ModelType:       row.ModelType,
		NativeCurrency:  row.NativeCurrency,
		ContextWindow:   row.ContextWindow,
		VerifyStatus:    row.VerifyStatus,
		TierTag:         row.TierTag,
		IsSensitive:     row.IsSensitive,
		CrossBorder:     row.CrossBorder,
		LifecycleStatus: row.LifecycleStatus,
		SunsetDate:      row.SunsetDate,
	}
	if len(row.Capability) > 0 {
		var cap model.Capability
		if err := json.Unmarshal(row.Capability, &cap); err == nil {
			sku.Capability = &cap
		}
	}
	if len(row.Tags) > 0 {
		tags := make([]string, 0, 8)
		if err := json.Unmarshal(row.Tags, &tags); err == nil {
			sku.Tags = tags
		}
	}
	return sku
}

// isUniqueViolation 判断是否为指定约束的唯一键冲突。
func isUniqueViolation(err error, constraint string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, constraint) || strings.Contains(msg, "duplicate key")
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func int64Ptr(v int64) *int64 { return &v }

// ---- 阶段 4b-1：别名 / 合并 / 批量 / 上架 ----

// ReplaceAliases 全量覆盖该 SKU 的别名（差集增删）。
func (r *ModelRepo) ReplaceAliases(ctx context.Context, skuID int64, aliases []string, source string, operatorID int64) error {
	tx := r.txOf(ctx)

	// 冲突预检：别名指向其他 SKU → 400
	for _, a := range aliases {
		var ar aliasRow
		err := tx.Where("alias = ?", a).Take(&ar).Error
		if err == nil && ar.SkuID != skuID {
			return model.ErrDuplicateAlias
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}

	// 差集增删：先查现有，再删多余的、插缺失的
	var existing []aliasRow
	if err := tx.Where("sku_id = ?", skuID).Find(&existing).Error; err != nil {
		return err
	}
	existingSet := map[string]bool{}
	for _, e := range existing {
		existingSet[e.Alias] = true
	}
	wantSet := map[string]bool{}
	for _, a := range aliases {
		wantSet[a] = true
	}
	for _, e := range existing {
		if !wantSet[e.Alias] {
			if err := tx.Where("id = ?", e.ID).Delete(&aliasRow{}).Error; err != nil {
				return err
			}
		}
	}
	for _, a := range aliases {
		if !existingSet[a] {
			ar := aliasRow{SkuID: skuID, Alias: a, Source: source}
			if err := tx.Create(&ar).Error; err != nil {
				if isUniqueViolation(err, "uk_alias") {
					return model.ErrDuplicateAlias
				}
				return err
			}
		}
	}
	return nil
}

// SuggestAliases 按 pg_trgm 相似度返回 Top N 查重建议。
func (r *ModelRepo) SuggestAliases(ctx context.Context, keyword string, limit int) ([]model.Suggestion, error) {
	type hit struct {
		SkuID   int64   `gorm:"column:sku_id"`
		SkuCode string  `gorm:"column:sku_code"`
		Score   float64 `gorm:"column:score"`
	}
	var hits []hit
	err := r.txOf(ctx).
		Table("model_sku").
		Select("id AS sku_id, sku_code, similarity(sku_code, ?) AS score", keyword).
		Where("sku_code % ?", keyword).
		Order("score DESC").
		Limit(limit).
		Scan(&hits).Error
	if err != nil {
		return nil, err
	}
	out := make([]model.Suggestion, 0, len(hits))
	for _, h := range hits {
		out = append(out, model.Suggestion{SkuID: h.SkuID, SkuCode: h.SkuCode, Score: h.Score})
	}
	return out, nil
}

// IsSkuReferenced 校验 SKU 是否被价目表/报价引用（合并前置）。
func (r *ModelRepo) IsSkuReferenced(ctx context.Context, skuID int64) (bool, error) {
	tx := r.txOf(ctx)
	var n int64
	if err := tx.Table("price_book_item").Where("sku_id = ?", skuID).Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	if err := tx.Table("quote_item").Where("sku_id = ?", skuID).Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	if err := tx.Table("customer_quote_item").Where("sku_id = ?", skuID).Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// MergeAlias 把 source_sku 的名称作为别名挂到 target_sku（source='MERGE'）。
func (r *ModelRepo) MergeAlias(ctx context.Context, targetSkuID, sourceSkuID int64, alias string, operatorID int64) (int64, error) {
	tx := r.txOf(ctx)
	ar := aliasRow{SkuID: targetSkuID, Alias: alias, Source: "MERGE"}
	if err := tx.Create(&ar).Error; err != nil {
		if isUniqueViolation(err, "uk_alias") {
			return 0, model.ErrDuplicateAlias
		}
		return 0, err
	}
	return ar.ID, nil
}

// BatchAction 批量动作（部分成功语义）。
func (r *ModelRepo) BatchAction(ctx context.Context, in model.BatchInput, operatorID int64) (*model.BatchResult, error) {
	tx := r.txOf(ctx)
	res := &model.BatchResult{Total: len(in.SkuIDs), Failed: []model.BatchFailed{}}

	for _, skuID := range in.SkuIDs {
		if err := r.applyBatchOne(tx, skuID, in, operatorID); err != nil {
			res.Failed = append(res.Failed, model.BatchFailed{
				SkuID:   skuID,
				Code:    10001,
				Message: err.Error(),
			})
			continue
		}
		res.Succeeded++
	}
	return res, nil
}

// applyBatchOne 应用单个批量动作；失败返回 error（部分成功由上层聚合）。
func (r *ModelRepo) applyBatchOne(tx *gorm.DB, skuID int64, in model.BatchInput, operatorID int64) error {
	var row skuRow
	if err := tx.Where("id = ?", skuID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("sku %d not found", skuID)
		}
		return err
	}

	switch in.Action {
	case model.BatchSubmitVerify:
		if row.LifecycleStatus != model.LifecycleDraft {
			return fmt.Errorf("状态不允许该操作：当前 %s，仅 DRAFT 可提交验证", row.LifecycleStatus)
		}
		return tx.Table("model_sku").Where("id = ? AND lifecycle_status = ?", skuID, model.LifecycleDraft).
			Updates(map[string]interface{}{"lifecycle_status": model.LifecyclePendingVerify, "updated_at": time.Now(), "updated_by": operatorID}).Error

	case model.BatchSetTier:
		tier, _ := in.Payload["tier_tag"].(string)
		return tx.Table("model_sku").Where("id = ?", skuID).
			Updates(map[string]interface{}{"tier_tag": tier, "updated_at": time.Now(), "updated_by": operatorID}).Error

	case model.BatchAddTag:
		tag, _ := in.Payload["tag"].(string)
		tags := make([]string, 0, 8)
		_ = json.Unmarshal(row.Tags, &tags)
		for _, t := range tags {
			if t == tag {
				return nil // 幂等：已存在
			}
		}
		tags = append(tags, tag)
		b, _ := json.Marshal(tags)
		return tx.Table("model_sku").Where("id = ?", skuID).
			Updates(map[string]interface{}{"tags": b, "updated_at": time.Now(), "updated_by": operatorID}).Error

	case model.BatchRemoveTag:
		tag, _ := in.Payload["tag"].(string)
		tags := make([]string, 0, 8)
		_ = json.Unmarshal(row.Tags, &tags)
		out := make([]string, 0, len(tags))
		for _, t := range tags {
			if t != tag {
				out = append(out, t)
			}
		}
		b, _ := json.Marshal(out)
		return tx.Table("model_sku").Where("id = ?", skuID).
			Updates(map[string]interface{}{"tags": b, "updated_at": time.Now(), "updated_by": operatorID}).Error

	default:
		return model.ErrBatchActionInvalid
	}
}

// Publish 上架：条件更新 WHERE lifecycle_status='PURCHASABLE' → PUBLISHED。
// 更新行数为 0 时返回 ErrPublishConflict（并发占位失败或前置状态非法）。
func (r *ModelRepo) Publish(ctx context.Context, skuID int64, operatorID int64) (*model.PublishResult, error) {
	tx := r.txOf(ctx)

	// 前置状态读取（用于区分 400 vs 409）
	var row skuRow
	if err := tx.Where("id = ?", skuID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, model.ErrNotFound
		}
		return nil, err
	}
	if row.LifecycleStatus != model.LifecyclePurchasable {
		return nil, model.ErrPublishStateInvalid
	}

	now := time.Now()
	res := tx.Table("model_sku").
		Where("id = ? AND lifecycle_status = ?", skuID, model.LifecyclePurchasable).
		Updates(map[string]interface{}{
			"lifecycle_status": model.LifecyclePublished,
			"updated_at":       now,
			"updated_by":       operatorID,
		})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, model.ErrPublishConflict
	}
	return &model.PublishResult{SkuID: skuID, LifecycleStatus: model.LifecyclePublished, PublishedAt: now}, nil
}
