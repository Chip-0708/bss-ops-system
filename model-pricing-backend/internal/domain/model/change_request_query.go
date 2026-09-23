// Package model 的 change_request_query.go：变更单与审批进度的只读查询类型。
//
// 联调 P1-4 / P1-5：官方价变更单与价目表发布此前只有"建单"和"审批动作"两个入口，
// 没有任何查询接口——页面刷新或换人后无法追踪审批进度（前端只能靠浏览器缓存）。
// 本文件补齐「列表 + 详情（含审批步骤链）」的查询能力，两类变更单共用。
package model

import (
	"encoding/json"
	"time"
)

// ChangeRequestItem 变更单列表行（含审批进度摘要，不含 payload 大字段）。
type ChangeRequestItem struct {
	ID         int64  `json:"id"`
	ChangeType string `json:"change_type"` // DEPRECATE / PRICE_UP / PRICE_DOWN / PRICE_BOOK_PUBLISH / PRICE_BOOK_ROLLBACK / SPECIAL_PRICE
	Status     string `json:"status"`      // PENDING / APPROVED / REJECTED
	SkuID      *int64 `json:"sku_id"`      // 价目表类变更为 null
	RiskLevel  string `json:"risk_level"`
	// 审批进度摘要：前端不必拉详情即可渲染进度条。
	StepsTotal    int       `json:"steps_total"`
	StepsApproved int       `json:"steps_approved"`
	PendingStepNo *int      `json:"pending_step_no"` // 当前待审步骤号；null = 已终结或无步骤
	PendingRole   *string   `json:"pending_role"`    // 当前待审步骤需要的角色；null = 无
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ApprovalStepView 单个审批步骤的只读视图。
type ApprovalStepView struct {
	StepNo       int        `json:"step_no"`
	RequiredRole string     `json:"required_role"`
	Decision     *string    `json:"decision"` // APPROVED / REJECTED / null（未决）
	ApproverID   *int64     `json:"approver_id"`
	Comment      *string    `json:"comment"`
	DecidedAt    *time.Time `json:"decided_at"`
}

// ChangeRequestDetail 变更单详情（含完整审批步骤链）。
type ChangeRequestDetail struct {
	ChangeRequestItem
	Payload       json.RawMessage    `json:"payload,omitempty"`
	MarginPreview json.RawMessage    `json:"margin_preview,omitempty"`
	RequestID     *string            `json:"request_id,omitempty"`
	UpdatedBy     *string            `json:"updated_by,omitempty"`
	Steps         []ApprovalStepView `json:"steps"`
}

// ChangeRequestListResult 变更单列表结果。
type ChangeRequestListResult struct {
	List  []ChangeRequestItem `json:"list"`
	Total int                 `json:"total"`
	Page  int                 `json:"page"`
	Size  int                 `json:"size"`
}

// ChangeRequestQuery 变更单列表查询条件。
type ChangeRequestQuery struct {
	ChangeType string // 空 = 全部类型
	Status     string // 空 = 全部状态
	SKUID      *int64 // 非空 = 只看该 SKU 的变更
	Page       int
	Size       int
}
