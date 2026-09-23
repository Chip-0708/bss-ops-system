// Package repo 的 change_request_query.go：变更单与审批进度的只读查询（联调 P1-4 / P1-5）。
//
// 官方价变更单（PRICE_UP / PRICE_DOWN）与价目表发布（PRICE_BOOK_PUBLISH / ROLLBACK）
// 此前只有"建单"与"审批动作"，没有查询入口——页面刷新或换人后无法追踪进度。
// 本文件补齐列表（带进度摘要）与详情（带完整步骤链），两类变更单共用同一套查询。
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"model_bss/internal/domain/model"
)

// ListChangeRequests 变更单列表（含审批进度摘要）。
//
// 进度摘要：先查本页 change_request，再一次性按 biz_id 批量取 approval_step
// 并在内存聚合——避免每条一次子查询（N+1）。
func (r *ModelRepo) ListChangeRequests(ctx context.Context, q model.ChangeRequestQuery) (*model.ChangeRequestListResult, error) {
	tx := r.txOf(ctx)

	filter := func(db *gorm.DB) *gorm.DB {
		if s := strings.TrimSpace(q.ChangeType); s != "" {
			db = db.Where("change_type = ?", strings.ToUpper(s))
		}
		if s := strings.TrimSpace(q.Status); s != "" {
			db = db.Where("status = ?", strings.ToUpper(s))
		}
		if q.SKUID != nil {
			db = db.Where("sku_id = ?", *q.SKUID)
		}
		return db
	}

	var total int64
	if err := filter(tx.Table("change_request")).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count change_requests: %w", err)
	}

	var rows []changeRequestRow
	if err := filter(tx.Table("change_request")).
		Select("id, change_type, sku_id, risk_level, status, created_by, created_at, updated_at").
		Order("id DESC").
		Offset((q.Page - 1) * q.Size).Limit(q.Size).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list change_requests: %w", err)
	}

	ids := make([]int64, 0, len(rows))
	for _, rw := range rows {
		ids = append(ids, rw.ID)
	}
	stepsByID := map[int64][]approvalStepRow{}
	if len(ids) > 0 {
		var steps []approvalStepRow
		if err := tx.Table("approval_step").
			Where("biz_id IN ?", ids).
			Order("biz_id ASC, step_no ASC").
			Scan(&steps).Error; err != nil {
			return nil, fmt.Errorf("list approval steps: %w", err)
		}
		for _, st := range steps {
			stepsByID[st.BizID] = append(stepsByID[st.BizID], st)
		}
	}

	list := make([]model.ChangeRequestItem, 0, len(rows))
	for _, rw := range rows {
		item := model.ChangeRequestItem{
			ID:         rw.ID,
			ChangeType: rw.ChangeType,
			Status:     rw.Status,
			RiskLevel:  rw.RiskLevel,
			CreatedBy:  rw.CreatedBy,
			CreatedAt:  rw.CreatedAt,
			UpdatedAt:  rw.UpdatedAt,
		}
		if rw.SkuID != 0 {
			sku := rw.SkuID
			item.SkuID = &sku
		}
		item.StepsTotal = len(stepsByID[rw.ID])
		summarizeSteps(&item, stepsByID[rw.ID])
		list = append(list, item)
	}

	return &model.ChangeRequestListResult{
		List: list, Total: int(total), Page: q.Page, Size: q.Size,
	}, nil
}

// GetChangeRequest 变更单详情（含完整审批步骤链与建单 payload）。
func (r *ModelRepo) GetChangeRequest(ctx context.Context, id int64) (*model.ChangeRequestDetail, error) {
	tx := r.txOf(ctx)

	var cr changeRequestRow
	if err := tx.Table("change_request").Where("id = ?", id).Take(&cr).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, model.ErrNotFound
		}
		return nil, fmt.Errorf("load change_request %d: %w", id, err)
	}

	var steps []approvalStepRow
	if err := tx.Table("approval_step").
		Where("biz_id = ?", id).
		Order("step_no ASC").
		Scan(&steps).Error; err != nil {
		return nil, fmt.Errorf("load approval steps: %w", err)
	}

	detail := &model.ChangeRequestDetail{
		ChangeRequestItem: model.ChangeRequestItem{
			ID:         cr.ID,
			ChangeType: cr.ChangeType,
			Status:     cr.Status,
			RiskLevel:  cr.RiskLevel,
			CreatedBy:  cr.CreatedBy,
			CreatedAt:  cr.CreatedAt,
			UpdatedAt:  cr.UpdatedAt,
		},
		RequestID: cr.RequestID,
		UpdatedBy: cr.UpdatedBy,
		Steps:     make([]model.ApprovalStepView, 0, len(steps)),
	}
	if cr.SkuID != 0 {
		sku := cr.SkuID
		detail.SkuID = &sku
	}
	if len(cr.Payload) > 0 {
		detail.Payload = json.RawMessage(cr.Payload)
	}
	if len(cr.MarginPreview) > 0 {
		detail.MarginPreview = json.RawMessage(cr.MarginPreview)
	}

	detail.StepsTotal = len(steps)
	summarizeSteps(&detail.ChangeRequestItem, steps)
	for _, st := range steps {
		detail.Steps = append(detail.Steps, model.ApprovalStepView{
			StepNo:       st.StepNo,
			RequiredRole: st.RequiredRole,
			Decision:     st.Decision,
			ApproverID:   st.ApproverID,
			Comment:      st.Comment,
			DecidedAt:    st.DecidedAt,
		})
	}
	return detail, nil
}

// summarizeSteps 由步骤链填充进度摘要（已批数 + 首个未决步骤 = 当前待审）。
// 列表与详情共用同一口径，避免两处逻辑漂移。
func summarizeSteps(item *model.ChangeRequestItem, steps []approvalStepRow) {
	for _, st := range steps {
		if st.Decision != nil && *st.Decision == model.DecisionApproved {
			item.StepsApproved++
		}
		if item.PendingStepNo == nil && st.Decision == nil {
			no := st.StepNo
			role := st.RequiredRole
			item.PendingStepNo = &no
			item.PendingRole = &role
		}
	}
}
