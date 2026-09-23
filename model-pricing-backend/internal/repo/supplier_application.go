// Package repo 的 supplier_application.go：新模型申请 GORM 实现（供应商侧提交/查询 + 内部审核）。
//
// 红线：
//   - 行级过滤：供应商侧查询强制 WHERE supplier_id = ?（Service 层已强制，这里再兜一层）。
//   - 终态守卫：审核走**条件更新**（WHERE status='SUBMITTED'），
//     并发双审 / 对已终结单再审时 RowsAffected=0，不会覆盖既有结论。
//   - 查重用 pg_trgm 的 similarity()（迁移 000003 已建扩展）；只提示不拦截。
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"model_bss/internal/domain/supplier"
)

// modelApplicationRow 是 model_application 表的行模型。
type modelApplicationRow struct {
	ID           int64     `gorm:"primaryKey;column:id"`
	SupplierID   int64     `gorm:"column:supplier_id"`
	ModelName    string    `gorm:"column:model_name"`
	VendorID     *int64    `gorm:"column:vendor_id"`
	Payload      []byte    `gorm:"column:payload"`
	DupTop3      []byte    `gorm:"column:dup_top3"`
	Status       string    `gorm:"column:status"`
	MergedSKUID  *int64    `gorm:"column:merged_sku_id"`
	RejectReason *string   `gorm:"column:reject_reason"`
	CreatedAt    time.Time `gorm:"column:created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
	RequestID    *string   `gorm:"column:request_id"`
	CreatedBy    *int64    `gorm:"column:created_by"`
	UpdatedBy    *int64    `gorm:"column:updated_by"`
}

func (modelApplicationRow) TableName() string { return "model_application" }

// toModelApplication 行模型 → 域模型。
func toModelApplication(r *modelApplicationRow) *supplier.ModelApplication {
	out := &supplier.ModelApplication{
		ID:           r.ID,
		SupplierID:   r.SupplierID,
		ModelName:    r.ModelName,
		VendorID:     r.VendorID,
		Status:       r.Status,
		MergedSKUID:  r.MergedSKUID,
		RejectReason: r.RejectReason,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
	if len(r.Payload) > 0 {
		out.Payload = json.RawMessage(r.Payload)
	}
	if len(r.DupTop3) > 0 {
		out.DupTop3 = json.RawMessage(r.DupTop3)
	}
	return out
}

// SubmitModelApplication 落库一条 SUBMITTED 申请，并附带查重结果（dup_top3）。
func (r *SupplierRepo) SubmitModelApplication(ctx context.Context, supplierID int64, in supplier.SubmitApplicationInput, operatorID int64, requestID string) (*supplier.ModelApplication, error) {
	tx := r.txOf(ctx)
	now := time.Now()

	// vendor_id 若传了必须存在（外键列，提前校验以给出明确错误而非 23503）。
	if in.VendorID != nil {
		var cnt int64
		if err := tx.Table("vendor").Where("id = ?", *in.VendorID).Count(&cnt).Error; err != nil {
			return nil, fmt.Errorf("check vendor %d: %w", *in.VendorID, err)
		}
		if cnt == 0 {
			return nil, supplier.ErrApplicationInvalid
		}
	}

	// 查重：与已有 SKU 的 model_name 相似度 top3。失败不阻断提交（仅提示性信息）。
	var dupJSON []byte
	if dups, derr := r.findDupCandidates(tx, in.ModelName, 3); derr == nil && len(dups) > 0 {
		if b, merr := json.Marshal(dups); merr == nil {
			dupJSON = b
		}
	}

	row := modelApplicationRow{
		SupplierID: supplierID,
		ModelName:  in.ModelName,
		VendorID:   in.VendorID,
		Payload:    in.Payload,
		DupTop3:    dupJSON,
		Status:     supplier.AppStatusSubmitted,
		CreatedAt:  now,
		UpdatedAt:  now,
		RequestID:  strPtr(requestID),
		CreatedBy:  int64Ptr(operatorID),
		UpdatedBy:  int64Ptr(operatorID),
	}
	if err := tx.Create(&row).Error; err != nil {
		return nil, fmt.Errorf("insert model_application: %w", err)
	}
	return toModelApplication(&row), nil
}

// findDupCandidates 用 pg_trgm 相似度找"同名/近似名"的已有 SKU（top N）。
//
// "模型名"在本系统分散在三处，故分别比对（UNION）后统一排序，并标明命中来源：
//   - model_alias.alias（归一化别名，如 "gpt-5"）——最常见命中
//   - model_sku.sku_code（含版本编码，如 "gpt-5-2026-04-11"）
//   - model_family.name（系列名）
//
// 阈值 0.3 为经验值：过低会把无关模型带进来，过高漏掉改名场景。
func (r *SupplierRepo) findDupCandidates(tx *gorm.DB, name string, limit int) ([]supplier.DupCandidate, error) {
	const dupSQL = `
SELECT sku_id, sku_code, matched_on, matched_val, sim FROM (
  SELECT DISTINCT ON (sku_id) sku_id, sku_code, matched_on, matched_val, sim
  FROM (
    SELECT ma.sku_id AS sku_id, ms.sku_code AS sku_code, 'ALIAS' AS matched_on,
           ma.alias AS matched_val, similarity(ma.alias, ?) AS sim
    FROM model_alias ma JOIN model_sku ms ON ms.id = ma.sku_id
    WHERE similarity(ma.alias, ?) > ?
    UNION ALL
    SELECT ms.id, ms.sku_code, 'SKU_CODE', ms.sku_code, similarity(ms.sku_code, ?)
    FROM model_sku ms
    WHERE similarity(ms.sku_code, ?) > ?
    UNION ALL
    SELECT ms.id, ms.sku_code, 'FAMILY', mf.name, similarity(mf.name, ?)
    FROM model_family mf JOIN model_sku ms ON ms.family_id = mf.id
    WHERE similarity(mf.name, ?) > ?
  ) src
  -- 同一 SKU 可能同时命中别名/编码/系列（如 family="GPT-5" 且 sku_code 含 "gpt-5"），
  -- 按 SKU 去重并保留相似度最高的那条命中来源。
  ORDER BY sku_id, sim DESC
) t
ORDER BY sim DESC, sku_id ASC
LIMIT ?`

	const threshold = 0.3
	var rows []struct {
		SKUID      int64   `gorm:"column:sku_id"`
		SkuCode    string  `gorm:"column:sku_code"`
		MatchedOn  string  `gorm:"column:matched_on"`
		MatchedVal string  `gorm:"column:matched_val"`
		Similarity float64 `gorm:"column:sim"`
	}
	err := tx.Raw(dupSQL,
		name, name, threshold,
		name, name, threshold,
		name, name, threshold,
		limit,
	).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("find dup candidates: %w", err)
	}
	out := make([]supplier.DupCandidate, 0, len(rows))
	for _, rw := range rows {
		out = append(out, supplier.DupCandidate{
			SKUID:      rw.SKUID,
			SkuCode:    rw.SkuCode,
			MatchedOn:  rw.MatchedOn,
			MatchedVal: rw.MatchedVal,
			Similarity: rw.Similarity,
		})
	}
	return out, nil
}

// ListModelApplications 申请列表。q.SupplierID 非空时强制行级过滤（供应商侧）。
func (r *SupplierRepo) ListModelApplications(ctx context.Context, q supplier.ApplicationQuery) (*supplier.ApplicationListResult, error) {
	tx := r.txOf(ctx)

	filter := func(db *gorm.DB) *gorm.DB {
		if q.SupplierID != nil {
			db = db.Where("supplier_id = ?", *q.SupplierID)
		}
		if q.Status != "" {
			db = db.Where("status = ?", q.Status)
		}
		return db
	}

	var total int64
	if err := filter(tx.Table("model_application")).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count model_applications: %w", err)
	}

	var rows []modelApplicationRow
	if err := filter(tx.Table("model_application")).
		Order("id DESC").
		Offset((q.Page - 1) * q.Size).Limit(q.Size).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list model_applications: %w", err)
	}

	list := make([]supplier.ModelApplication, 0, len(rows))
	for i := range rows {
		list = append(list, *toModelApplication(&rows[i]))
	}
	return &supplier.ApplicationListResult{
		List: list, Total: int(total), Page: q.Page, Size: q.Size,
	}, nil
}

// DecideModelApplication 审核：条件更新保证「只有 SUBMITTED 可推进」。
func (r *SupplierRepo) DecideModelApplication(ctx context.Context, id int64, in supplier.ApplicationDecisionInput, operatorID int64, requestID string) (*supplier.ModelApplication, error) {
	tx := r.txOf(ctx)
	now := time.Now()
	targetStatus := supplier.ApplicationStatusOf(in.Action)

	// 目标 SKU 必须存在（APPROVE / MERGE 已由 domain 校验必填）。
	if in.TargetSKUID != nil {
		var cnt int64
		if err := tx.Table("model_sku").Where("id = ?", *in.TargetSKUID).Count(&cnt).Error; err != nil {
			return nil, fmt.Errorf("check target sku %d: %w", *in.TargetSKUID, err)
		}
		if cnt == 0 {
			return nil, supplier.ErrTargetSKUNotFound
		}
	}

	upd := tx.Table("model_application").
		Where("id = ? AND status = ?", id, supplier.AppStatusSubmitted).
		Updates(map[string]any{
			"status":        targetStatus,
			"merged_sku_id": in.TargetSKUID,
			"reject_reason": in.Reason,
			"updated_at":    now,
			"updated_by":    operatorID,
			"request_id":    requestID,
		})
	if upd.Error != nil {
		return nil, fmt.Errorf("update model_application %d: %w", id, upd.Error)
	}
	if upd.RowsAffected == 0 {
		// 0 行有两种语义：记录不存在 / 已是终态。区分后给出准确错误码。
		var probe modelApplicationRow
		if err := tx.Table("model_application").Where("id = ?", id).Take(&probe).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, supplier.ErrApplicationNotFound
			}
			return nil, fmt.Errorf("load model_application %d: %w", id, err)
		}
		return nil, supplier.ErrApplicationConflict
	}

	var row modelApplicationRow
	if err := tx.Table("model_application").Where("id = ?", id).Take(&row).Error; err != nil {
		return nil, fmt.Errorf("reload model_application %d: %w", id, err)
	}
	return toModelApplication(&row), nil
}
