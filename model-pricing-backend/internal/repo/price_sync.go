// Package repo 的 price_sync.go：官方价采集批次 + 暂存区的 GORM 实现（阶段 7a）。
// 与所有其他仓储同一纪律：写操作统一经 txOf(ctx)（幂等中间件注入的事务优先——
// staging_price 行、审计行、幂等记录三者在同一事务里提交/回滚）。
// 读侧纪律：ListStagingPrices 固定查询数（count + paged），官方价版本由服务层
// 批量装配（LoadCurrentPriceVersions 固定 2 发），禁止逐行 N+1。
package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"model_bss/internal/domain/price"
)

// PriceSyncRepo 是 price.SyncStore 的 GORM 实现。
// 复用 SupplierRepo 的 txOf（与 CostParamRepo / CostLockRepo 同款组合）。
type PriceSyncRepo struct {
	*SupplierRepo
}

// NewPriceSyncRepo 构造采集/暂存仓储。
func NewPriceSyncRepo(base *gorm.DB) *PriceSyncRepo {
	return &PriceSyncRepo{SupplierRepo: NewSupplierRepo(base)}
}

var _ price.SyncStore = (*PriceSyncRepo)(nil)

// syncJobRow 是 sync_job 表的行模型（只用于写入与列表扫描——本表无 JOIN 别名列）。
type syncJobRow struct {
	ID         int64      `gorm:"primaryKey"`
	JobType    string     `gorm:"column:job_type"`
	Source     string     `gorm:"column:source"`
	Status     string     `gorm:"column:status"`
	StartedAt  time.Time  `gorm:"column:started_at"`
	FinishedAt *time.Time `gorm:"column:finished_at"`
	ErrorMsg   *string    `gorm:"column:error_msg"`
	ItemCount  *int       `gorm:"column:item_count"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
	UpdatedAt  time.Time  `gorm:"column:updated_at"`
	RequestID  *string    `gorm:"column:request_id"`
	CreatedBy  *int64     `gorm:"column:created_by"`
	UpdatedBy  *int64     `gorm:"column:updated_by"`
}

func (syncJobRow) TableName() string { return "sync_job" }

// stagingPriceRow 是 staging_price 表的行模型（payload 走 []byte 手工 json，
// jsonb 列不依赖 GORM 的 JSON 驱动序列化）。
type stagingPriceRow struct {
	ID          int64     `gorm:"primaryKey"`
	SyncJobID   int64     `gorm:"column:sync_job_id"`
	SKUID       *int64    `gorm:"column:sku_id"`
	RawSkuCode  *string   `gorm:"column:raw_sku_code"`
	Currency    string    `gorm:"column:currency"`
	Payload     []byte    `gorm:"column:payload"`
	Source      string    `gorm:"column:source"`
	Confidence  *string   `gorm:"column:confidence"`
	MatchStatus string    `gorm:"column:match_status"`
	Processed   bool      `gorm:"column:processed"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
	RequestID   *string   `gorm:"column:request_id"`
	CreatedBy   *int64    `gorm:"column:created_by"`
	UpdatedBy   *int64    `gorm:"column:updated_by"`
}

func (stagingPriceRow) TableName() string { return "staging_price" }

// ---- sync_job ----

// CreateSyncJob 单事务：INSERT sync_job（创建即 SUCCESS，裁决 2）+ audit_log。
// 审计红线 10：官方价采集是价格链路的第一环，录入批次必须留痕（after=批次全字段）。
func (r *PriceSyncRepo) CreateSyncJob(ctx context.Context, p price.CreateJobParams) (*price.SyncJob, error) {
	tx := r.txOf(ctx)
	finished := p.Now
	row := syncJobRow{
		JobType:    p.JobType,
		Source:     p.Source,
		Status:     price.JobStatusSuccess, // MVP 人工录入无采集过程，创建即完成
		StartedAt:  p.Now,
		FinishedAt: &finished,
		ItemCount:  intPtr(p.ItemCount),
		CreatedAt:  p.Now,
		UpdatedAt:  p.Now,
		RequestID:  strPtrIfNotEmpty(p.RequestID),
		CreatedBy:  int64Ptr(p.OperatorID),
		UpdatedBy:  int64Ptr(p.OperatorID),
	}
	if err := tx.Create(&row).Error; err != nil {
		return nil, fmt.Errorf("insert sync_job: %w", err)
	}

	after := map[string]any{
		"id": row.ID, "job_type": row.JobType, "source": row.Source,
		"status": row.Status, "item_count": p.ItemCount,
	}
	audit := auditLogRow{
		OperatorID:   p.OperatorID,
		OperatorRole: p.OperatorRole,
		Action:       "PRICE_SYNC_JOB_CREATE",
		TargetType:   "SYNC_JOB",
		TargetID:     row.ID,
		AfterValue:   mustJSON(after),
		SourceType:   "HUMAN",
		CreatedAt:    p.Now,
		UpdatedAt:    p.Now,
		RequestID:    strPtrIfNotEmpty(p.RequestID),
		CreatedBy:    int64Ptr(p.OperatorID),
		UpdatedBy:    int64Ptr(p.OperatorID),
	}
	if err := tx.Create(&audit).Error; err != nil {
		return nil, fmt.Errorf("insert audit_log PRICE_SYNC_JOB_CREATE: %w", err)
	}
	return syncJobToDTO(&row), nil
}

// ListSyncJobs 分页列表（id DESC，最新批次在前）。
func (r *PriceSyncRepo) ListSyncJobs(ctx context.Context, page, size int) ([]price.SyncJob, int64, error) {
	tx := r.txOf(ctx)
	var total int64
	if err := tx.Table("sync_job").Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count sync_job: %w", err)
	}
	var rows []syncJobRow
	if err := tx.Table("sync_job").
		Order("id DESC").
		Offset((page - 1) * size).Limit(size).
		Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list sync_job: %w", err)
	}
	out := make([]price.SyncJob, 0, len(rows))
	for i := range rows {
		out = append(out, *syncJobToDTO(&rows[i]))
	}
	return out, total, nil
}

// SyncJobExists 批次存在性（404 门槛）。
func (r *PriceSyncRepo) SyncJobExists(ctx context.Context, id int64) (bool, error) {
	var n int64
	if err := r.txOf(ctx).Table("sync_job").Where("id = ?", id).Count(&n).Error; err != nil {
		return false, fmt.Errorf("check sync_job id=%d: %w", id, err)
	}
	return n > 0, nil
}

// syncJobToDTO 行模型 → 契约 DTO（时间 RFC3339 字符串）。
func syncJobToDTO(row *syncJobRow) *price.SyncJob {
	dto := &price.SyncJob{
		ID:        row.ID,
		JobType:   row.JobType,
		Source:    row.Source,
		Status:    row.Status,
		StartedAt: row.StartedAt.UTC().Format(time.RFC3339),
		ErrorMsg:  row.ErrorMsg,
		ItemCount: row.ItemCount,
	}
	if row.FinishedAt != nil {
		s := row.FinishedAt.UTC().Format(time.RFC3339)
		dto.FinishedAt = &s
	}
	return dto
}

// ---- SKU 解析 ----

// SKUExists 校验 model_sku.id 存在。
func (r *PriceSyncRepo) SKUExists(ctx context.Context, skuID int64) (bool, error) {
	var n int64
	if err := r.txOf(ctx).Table("model_sku").Where("id = ?", skuID).Count(&n).Error; err != nil {
		return false, fmt.Errorf("check model_sku id=%d: %w", skuID, err)
	}
	return n > 0, nil
}

// FindSKUIDByCode 按 sku_code 精确匹配（uk_sku_code 唯一约束保证至多一行）。
func (r *PriceSyncRepo) FindSKUIDByCode(ctx context.Context, code string) (int64, bool, error) {
	var id *int64
	err := r.txOf(ctx).Table("model_sku").
		Where("sku_code = ?", code).
		Limit(1).
		Scan(&id).Error
	if err != nil {
		return 0, false, fmt.Errorf("find model_sku code=%s: %w", code, err)
	}
	if id == nil {
		return 0, false, nil
	}
	return *id, true, nil
}

// ---- staging_price ----

// CreateStagingPrices 单事务：逐行 INSERT staging_price + audit_log（红线 10）。
// source 恒 MANUAL（MVP 人工录入；P1 自动采集写 SYNC/AGENT——设计 §11 Agent 边界）。
func (r *PriceSyncRepo) CreateStagingPrices(ctx context.Context, p price.CreateStagingParams) (*price.CreateStagingResult, error) {
	tx := r.txOf(ctx)
	res := &price.CreateStagingResult{StagingIDs: make([]int64, 0, len(p.Rows))}
	for i := range p.Rows {
		rowIn := &p.Rows[i]
		payloadJSON, err := json.Marshal(rowIn.Payload)
		if err != nil {
			return nil, fmt.Errorf("marshal staging payload row=%d: %w", i, err)
		}
		row := stagingPriceRow{
			SyncJobID:   p.SyncJobID,
			SKUID:       rowIn.SKUID,
			RawSkuCode:  rowIn.RawSkuCode,
			Currency:    rowIn.Currency,
			Payload:     payloadJSON,
			Source:      "MANUAL",
			MatchStatus: rowIn.MatchStatus,
			Processed:   false, // 裁决 5：本批恒 false，7b confirm 置 true
			CreatedAt:   p.Now,
			UpdatedAt:   p.Now,
			RequestID:   strPtrIfNotEmpty(p.RequestID),
			CreatedBy:   int64Ptr(p.OperatorID),
			UpdatedBy:   int64Ptr(p.OperatorID),
		}
		if err := tx.Create(&row).Error; err != nil {
			return nil, fmt.Errorf("insert staging_price row=%d: %w", i, err)
		}
		res.StagingIDs = append(res.StagingIDs, row.ID)
	}
	res.CreatedCount = len(res.StagingIDs)

	after := map[string]any{
		"sync_job_id": p.SyncJobID,
		"created":     res.CreatedCount,
		"staging_ids": res.StagingIDs,
	}
	audit := auditLogRow{
		OperatorID:   p.OperatorID,
		OperatorRole: p.OperatorRole,
		Action:       "STAGING_PRICE_SUBMIT",
		TargetType:   "SYNC_JOB",
		TargetID:     p.SyncJobID,
		AfterValue:   mustJSON(after),
		SourceType:   "HUMAN",
		CreatedAt:    p.Now,
		UpdatedAt:    p.Now,
		RequestID:    strPtrIfNotEmpty(p.RequestID),
		CreatedBy:    int64Ptr(p.OperatorID),
		UpdatedBy:    int64Ptr(p.OperatorID),
	}
	if err := tx.Create(&audit).Error; err != nil {
		return nil, fmt.Errorf("insert audit_log STAGING_PRICE_SUBMIT: %w", err)
	}
	res.AuditLogID = audit.ID
	return res, nil
}

// ListStagingPrices 分页列表（id ASC = 录入序，E2E 断言按录入顺序核 diff）。
func (r *PriceSyncRepo) ListStagingPrices(ctx context.Context, q price.StagingQuery) ([]price.StagingRow, int64, error) {
	tx := r.txOf(ctx)
	countTx := tx.Table("staging_price")
	listTx := tx.Table("staging_price")
	if q.SyncJobID != nil {
		countTx = countTx.Where("sync_job_id = ?", *q.SyncJobID)
		listTx = listTx.Where("sync_job_id = ?", *q.SyncJobID)
	}
	var total int64
	if err := countTx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count staging_price: %w", err)
	}
	var rows []stagingPriceRow
	if err := listTx.
		Order("id ASC").
		Offset((q.Page - 1) * q.Size).Limit(q.Size).
		Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list staging_price: %w", err)
	}
	out := make([]price.StagingRow, 0, len(rows))
	for i := range rows {
		rw := &rows[i]
		payload := map[string]string{}
		if len(rw.Payload) > 0 {
			if err := json.Unmarshal(rw.Payload, &payload); err != nil {
				return nil, 0, fmt.Errorf("unmarshal staging_price id=%d payload: %w", rw.ID, err)
			}
		}
		out = append(out, price.StagingRow{
			ID:          rw.ID,
			SyncJobID:   rw.SyncJobID,
			SKUID:       rw.SKUID,
			RawSkuCode:  rw.RawSkuCode,
			Currency:    rw.Currency,
			Payload:     payload,
			MatchStatus: rw.MatchStatus,
			Processed:   rw.Processed,
			CreatedAt:   rw.CreatedAt,
		})
	}
	return out, total, nil
}

// LoadCurrentPriceVersions 批量取当前官方价版本 + 组件（固定 2 发查询：
// 版本行 + 组件行，unit_price 走 ::text 避免 numeric → float 丢精度——
// 与 supplier.go:129 / supplier_quote.go FindOfficialComponents 同款纪律）。
func (r *PriceSyncRepo) LoadCurrentPriceVersions(ctx context.Context, skuIDs []int64) ([]price.CurrentPriceVersion, error) {
	if len(skuIDs) == 0 {
		return []price.CurrentPriceVersion{}, nil
	}
	tx := r.txOf(ctx)
	var pvRows []priceVersionRow
	if err := tx.Table("price_version").
		Select("id, sku_id, version_no, currency, tax_basis").
		Where("is_current = true AND sku_id IN ?", skuIDs).
		Scan(&pvRows).Error; err != nil {
		return nil, fmt.Errorf("list current price_version: %w", err)
	}
	if len(pvRows) == 0 {
		return []price.CurrentPriceVersion{}, nil
	}
	versionIDs := make([]int64, 0, len(pvRows))
	for i := range pvRows {
		versionIDs = append(versionIDs, pvRows[i].ID)
	}
	var comps []priceComponentRow
	if err := tx.Table("price_component").
		Select("price_version_id, component_type, unit_price::text AS unit_price").
		Where("price_version_id IN ?", versionIDs).
		Order("price_version_id, component_type").
		Scan(&comps).Error; err != nil {
		return nil, fmt.Errorf("list price_component: %w", err)
	}
	out := make([]price.CurrentPriceVersion, 0, len(pvRows))
	for i := range pvRows {
		pv := &pvRows[i]
		v := price.CurrentPriceVersion{
			SKUID:      pv.SkuID,
			Currency:   pv.Currency,
			VersionNo:  pv.VersionNo,
			Components: map[string]string{},
		}
		for j := range comps {
			if comps[j].PriceVersionID == pv.ID {
				v.Components[comps[j].ComponentType] = comps[j].UnitPrice
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// mustJSON 序列化审计载荷（map[string]any 必然成功——失败即 panic，
// 与「审计写不进去宁可整体回滚」同一纪律，绝不静默丢审计）。
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("audit payload marshal: %v", err))
	}
	return b
}

// intPtr 返回 int 指针（sync_job.item_count 可空列）。
func intPtr(v int) *int { return &v }
