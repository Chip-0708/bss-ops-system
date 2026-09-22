// Package repo 的 model_deprecate.go：M1 退役链路（阶段 4b-2）的 GORM 实现。
// 影响分析 → 发起退役 → 审批动作；写操作统一经 db.FromContext(ctx) 取事务。
package repo

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"model_bss/internal/domain/model"
)

// deprecationImpactRow 是 deprecation_impact 表的行模型（列名 impact_refs 规避 SQL 保留字）。
type deprecationImpactRow struct {
	ID             int64     `gorm:"primaryKey"`
	SnapshotID     string    `gorm:"column:snapshot_id"`
	SkuID          int64     `gorm:"column:sku_id"`
	ImpactRefs     []byte    `gorm:"column:impact_refs"`
	ReferenceCount int       `gorm:"column:reference_count"`
	Replacements   []byte    `gorm:"column:replacements"`
	ExpiresAt      time.Time `gorm:"column:expires_at"`
	CreatedBy      int64     `gorm:"column:created_by"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
	RequestID      *string   `gorm:"column:request_id"`
	UpdatedBy      *int64    `gorm:"column:updated_by"`
}

func (deprecationImpactRow) TableName() string { return "deprecation_impact" }

// changeRequestRow 是 change_request 表的行模型（created_by 是 varchar(24)）。
type changeRequestRow struct {
	ID         int64  `gorm:"primaryKey"`
	ChangeType string `gorm:"column:change_type"`
	SkuID      int64  `gorm:"column:sku_id"`
	RiskLevel  string `gorm:"column:risk_level"`
	Payload    []byte `gorm:"column:payload"`
	Status     string `gorm:"column:status"`
	// MarginPreview 毛利预览（jsonb，仅涨价审批用；7b 补列——M4 退役流程不用此列故原行模型未写）。
	MarginPreview []byte    `gorm:"column:margin_preview"`
	CreatedBy     string    `gorm:"column:created_by"`
	CreatedAt     time.Time `gorm:"column:created_at"`
	UpdatedAt     time.Time `gorm:"column:updated_at"`
	RequestID     *string   `gorm:"column:request_id"`
	UpdatedBy     *string   `gorm:"column:updated_by"`
}

func (changeRequestRow) TableName() string { return "change_request" }

// approvalStepRow 是 approval_step 表的行模型。
type approvalStepRow struct {
	ID           int64      `gorm:"primaryKey"`
	BizType      string     `gorm:"column:biz_type"`
	BizID        int64      `gorm:"column:biz_id"`
	StepNo       int        `gorm:"column:step_no"`
	RequiredRole string     `gorm:"column:required_role"`
	ApproverID   *int64     `gorm:"column:approver_id"`
	Decision     *string    `gorm:"column:decision"`
	Comment      *string    `gorm:"column:comment"`
	DecidedAt    *time.Time `gorm:"column:decided_at"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
	RequestID    *string    `gorm:"column:request_id"`
	CreatedBy    *int64     `gorm:"column:created_by"`
	UpdatedBy    *int64     `gorm:"column:updated_by"`
}

func (approvalStepRow) TableName() string { return "approval_step" }

// AnalyzeDeprecation 影响分析（§7）：统计引用 + 推荐替代，写入 deprecation_impact。
func (r *ModelRepo) AnalyzeDeprecation(ctx context.Context, sku *model.SKU, operatorID int64) (*model.DeprecationImpactResult, error) {
	tx := r.txOf(ctx)

	// 引用统计：price_book（经 price_book_item）
	type pbRef struct {
		ID        int64  `gorm:"column:id"`
		LevelCode string `gorm:"column:level_code"`
	}
	var priceBooks []pbRef
	err := tx.Table("price_book_item AS pbi").
		Select("DISTINCT pb.id, pb.level_code").
		Joins("JOIN price_book pb ON pb.id = pbi.price_book_id").
		Where("pbi.sku_id = ?", sku.ID).
		Scan(&priceBooks).Error
	if err != nil {
		return nil, err
	}

	// customer_quote（FORMAL/CONTRACT 视为合同引用）
	type cqRef struct {
		ID     int64  `gorm:"column:id"`
		Status string `gorm:"column:status"`
	}
	var customerQuotes []cqRef
	err = tx.Table("customer_quote_item AS cqi").
		Select("DISTINCT cq.id, cq.status").
		Joins("JOIN customer_quote cq ON cq.id = cqi.customer_quote_id").
		Where("cqi.sku_id = ? AND cq.status IN ('FORMAL','CONTRACT')", sku.ID).
		Scan(&customerQuotes).Error
	if err != nil {
		return nil, err
	}

	// 推荐替代：同 family 且 PUBLISHED 的其他 SKU，按 id 取最近 3 个
	var repls []model.Replacement
	err = tx.Table("model_sku").
		Select("id AS sku_id, sku_code").
		Where("family_id = ? AND id != ? AND lifecycle_status = ?", sku.FamilyID, sku.ID, model.LifecyclePublished).
		Order("id DESC").
		Limit(3).
		Scan(&repls).Error
	if err != nil {
		return nil, err
	}
	for i := range repls {
		repls[i].Reason = "同系列更新版本"
	}

	refCount := len(priceBooks) + len(customerQuotes)
	refsJSON, _ := json.Marshal(map[string]any{
		"price_books":     priceBooks,
		"contracts":       []any{},
		"customer_quotes": customerQuotes,
	})
	replJSON, _ := json.Marshal(repls)

	snapshotID := newSnapshotID()
	now := time.Now()
	impact := deprecationImpactRow{
		SnapshotID:     snapshotID,
		SkuID:          sku.ID,
		ImpactRefs:     refsJSON,
		ReferenceCount: refCount,
		Replacements:   replJSON,
		ExpiresAt:      now.Add(24 * time.Hour),
		CreatedBy:      operatorID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := tx.Create(&impact).Error; err != nil {
		return nil, fmt.Errorf("insert deprecation_impact: %w", err)
	}

	return &model.DeprecationImpactResult{
		SkuID:      sku.ID,
		SnapshotID: snapshotID,
		References: map[string][]any{
			"price_books":     toAnySlice(priceBooks),
			"contracts":       {},
			"customer_quotes": toAnySlice(customerQuotes),
		},
		ReferenceCount: refCount,
		Replacements:   repls,
		GeneratedAt:    now,
	}, nil
}

// StartDeprecate 发起退役（§8）：校验 snapshot，创建 change_request + 2 步审批；SKU 状态不变。
func (r *ModelRepo) StartDeprecate(ctx context.Context, skuID int64, in model.DeprecateInput, operatorID int64, requestID string) (*model.DeprecateResult, error) {
	tx := r.txOf(ctx)

	sku, err := r.GetSKU(ctx, skuID)
	if err != nil {
		return nil, err
	}
	if sku == nil {
		return nil, model.ErrNotFound
	}
	if sku.LifecycleStatus != model.LifecyclePublished && sku.LifecycleStatus != model.LifecyclePurchasable {
		return nil, model.ErrDeprecateStateInvalid
	}

	// 校验 snapshot：存在、sku 匹配、未过期
	var impact deprecationImpactRow
	err = tx.Where("snapshot_id = ?", in.ImpactSnapshotID).Take(&impact).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, model.ErrSnapshotInvalid
	}
	if err != nil {
		return nil, err
	}
	if impact.SkuID != skuID || !impact.ExpiresAt.After(time.Now()) {
		return nil, model.ErrSnapshotInvalid
	}

	// risk 分档：0=LOW / 1~5=MID / >5=HIGH
	risk := model.RiskLow
	if impact.ReferenceCount > 5 {
		risk = model.RiskHigh
	} else if impact.ReferenceCount > 0 {
		risk = model.RiskMid
	}

	payload, _ := json.Marshal(map[string]any{
		"sunset_date":        in.SunsetDate,
		"reason":             in.Reason,
		"replacement_sku_id": in.ReplacementSkuID,
		"snapshot_id":        in.ImpactSnapshotID,
	})

	now := time.Now()
	cr := changeRequestRow{
		ChangeType: model.ChangeDeprecate,
		SkuID:      skuID,
		RiskLevel:  risk,
		Payload:    payload,
		Status:     model.CRPending,
		CreatedBy:  fmt.Sprintf("staff:%d", operatorID), // varchar(24)
		CreatedAt:  now,
		UpdatedAt:  now,
		RequestID:  strPtr(requestID),
	}
	if err := tx.Create(&cr).Error; err != nil {
		return nil, fmt.Errorf("insert change_request: %w", err)
	}

	// 2 步审批：step_no=1/2，required_role=MODEL_OPS / PRICING_OP（设计文档 §7.2 双人审批）
	for i, role := range []string{"MODEL_OPS", "PRICING_OP"} {
		step := approvalStepRow{
			BizType:      "DEPRECATE",
			BizID:        cr.ID,
			StepNo:       i + 1,
			RequiredRole: role,
			CreatedAt:    now,
			UpdatedAt:    now,
			RequestID:    strPtr(requestID),
			CreatedBy:    int64Ptr(operatorID),
		}
		if err := tx.Create(&step).Error; err != nil {
			return nil, fmt.Errorf("insert approval_step %d: %w", i+1, err)
		}
	}

	return &model.DeprecateResult{
		SkuID:           skuID,
		ApprovalID:      cr.ID,
		SunsetDate:      in.SunsetDate,
		LifecycleStatus: sku.LifecycleStatus, // 此时不变，审批通过才转 DEPRECATING
	}, nil
}

// LoadChangeType 读 change_request.change_type（7b：通用审批入口按类型分发 onApproved）。
func (r *ModelRepo) LoadChangeType(ctx context.Context, changeRequestID int64) (string, error) {
	var cr changeRequestRow
	if err := r.txOf(ctx).Select("change_type").Where("id = ?", changeRequestID).Take(&cr).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", model.ErrNotFound
		}
		return "", err
	}
	return cr.ChangeType, nil
}

// LoadChangePayload 读 change_request.payload（7b：生效连锁回调需要 prices/sku_ids/effective_time）。
func (r *ModelRepo) LoadChangePayload(ctx context.Context, changeRequestID int64) ([]byte, error) {
	var cr changeRequestRow
	if err := r.txOf(ctx).Select("payload").Where("id = ?", changeRequestID).Take(&cr).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, model.ErrNotFound
		}
		return nil, err
	}
	return cr.Payload, nil
}

// DecideApproval 审批动作（§3.3）：角色匹配 + 禁止自审 + 状态机推进。
// onApproved 全步 APPROVED 时回调（7b：PRICE_UP/PRICE_DOWN 注入生效连锁；退役传 nil）。
// biz_type 从 change_request.change_type 读（不再硬编码 "DEPRECATE"）——approval_step.biz_type
// 与 change_request.change_type 同值（DDL 注释虽写 "DEPRECATE"，实际 seed/代码均按 change_type 写入）。
func (r *ModelRepo) DecideApproval(ctx context.Context, changeRequestID int64, in model.DecisionInput, operatorID int64, roles []string, requestID string, onApproved func(context.Context, int64, int64, string) error) (*model.DecisionResult, error) {
	tx := r.txOf(ctx)

	// 先读 change_request 拿 change_type（审批步查找、全步通过后的分支都依赖它）。
	var cr changeRequestRow
	if err := tx.Where("id = ?", changeRequestID).Take(&cr).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, model.ErrNotFound
		}
		return nil, err
	}
	bizType := cr.ChangeType

	// 找当前步骤
	var step approvalStepRow
	err := tx.Where("biz_type = ? AND biz_id = ? AND step_no = ?", bizType, changeRequestID, in.StepNo).Take(&step).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if step.Decision != nil {
		// 已决策的步骤不允许重复审批
		return nil, model.ErrDeprecateConflict
	}

	// 校验 a：当前操作员角色必须匹配 required_role
	matched := false
	for _, role := range roles {
		if role == step.RequiredRole {
			matched = true
			break
		}
	}
	if !matched {
		return nil, model.ErrApproverRoleMismatch
	}

	// 校验 b：同一 change_request 的两个步骤不得由同一操作员完成
	var other approvalStepRow
	err = tx.Where("biz_type = ? AND biz_id = ? AND step_no != ?", bizType, changeRequestID, in.StepNo).Take(&other).Error
	if err == nil && other.ApproverID != nil && *other.ApproverID == operatorID {
		return nil, model.ErrSelfApproval
	}

	// 写审批结果
	now := time.Now()
	decision := in.Decision
	comment := in.Comment
	if err := tx.Model(&approvalStepRow{}).Where("id = ?", step.ID).Updates(map[string]any{
		"approver_id": operatorID,
		"decision":    decision,
		"comment":     comment,
		"decided_at":  now,
		"updated_at":  now,
		"request_id":  strPtr(requestID),
	}).Error; err != nil {
		return nil, err
	}

	finalStatus := model.CRPending
	if decision == model.DecisionRejected {
		// 任一 REJECTED → 流程终止，SKU 状态不变
		finalStatus = model.CRRejected
		if err := tx.Model(&changeRequestRow{}).Where("id = ?", changeRequestID).Updates(map[string]any{
			"status":     finalStatus,
			"updated_at": now,
			"updated_by": fmt.Sprintf("staff:%d", operatorID),
		}).Error; err != nil {
			return nil, err
		}
	} else {
		// 全部步骤 APPROVED → change_request APPROVED + 业务生效（步数动态：降价 1 步、涨价/退役 2 步）
		var steps []approvalStepRow
		if err := tx.Where("biz_type = ? AND biz_id = ?", bizType, changeRequestID).Find(&steps).Error; err != nil {
			return nil, err
		}
		allApproved := len(steps) >= 1
		for _, st := range steps {
			if st.Decision == nil || *st.Decision != model.DecisionApproved {
				allApproved = false
				break
			}
		}
		if allApproved {
			finalStatus = model.CRApproved
			if err := tx.Model(&changeRequestRow{}).Where("id = ?", changeRequestID).Updates(map[string]any{
				"status":     finalStatus,
				"updated_at": now,
				"updated_by": fmt.Sprintf("staff:%d", operatorID),
			}).Error; err != nil {
				return nil, err
			}

			switch bizType {
			case model.ChangeDeprecate:
				// 退役：SKU 条件更新 DEPRECATING。
				var payload struct {
					SunsetDate string `json:"sunset_date"`
				}
				_ = json.Unmarshal(cr.Payload, &payload)
				sunset, _ := time.Parse("2006-01-02", payload.SunsetDate)

				res := tx.Table("model_sku").
					Where("id = ? AND lifecycle_status IN ?", cr.SkuID, []string{model.LifecyclePublished, model.LifecyclePurchasable}).
					Updates(map[string]any{
						"lifecycle_status": model.LifecycleDeprecating,
						"sunset_date":      sunset,
						"updated_at":       now,
						"updated_by":       operatorID,
					})
				if res.Error != nil {
					return nil, res.Error
				}
				if res.RowsAffected == 0 {
					return nil, model.ErrDeprecateConflict
				}
			default:
				// PRICE_UP/PRICE_DOWN 等：回调生效连锁（7b 由 price.ApplyOfficialPriceChange 注入）。
				if onApproved != nil {
					if err := onApproved(ctx, changeRequestID, operatorID, requestID); err != nil {
						return nil, err
					}
				}
			}
		}
	}

	return &model.DecisionResult{
		ChangeRequestID: changeRequestID,
		StepNo:          in.StepNo,
		Decision:        decision,
		FinalStatus:     finalStatus,
	}, nil
}

// toAnySlice 把 []T 转为 []any（JSON 序列化用）。
func toAnySlice[T any](in []T) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// newSnapshotID 生成 snapshot_id（UUID v4 形态）。
func newSnapshotID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
