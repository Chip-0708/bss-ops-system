// Package repo 的 audit.go：审计日志的最小写入 helper。
// 红线 10：价格相关操作 100% 写 audit_log（前后值 JSON）。
// F 阶段会在此扩展查询侧；本轮只有写入。
package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"model_bss/internal/infra/db"
)

// AuditEntry 是一条审计日志的写入参数。
type AuditEntry struct {
	// OperatorID 操作员 ID。
	// ⚠️ 已知歧义（F 阶段统一治理）：operator_id 在 internal_staff 与 subject_operator
	// 两个命名空间会撞号（两边都有 id=1），当前靠 OperatorRole 区分命名空间。
	OperatorID   int64
	OperatorRole string // PROCUREMENT / PRICING_OP / SUPPLIER / CUSTOMER / SYSTEM ...
	Action       string // QUOTE_SUBMIT / QUOTE_APPROVE / ...
	TargetType   string // QUOTE_SHEET / PRICE_BOOK / ...
	TargetID     int64
	BeforeValue  any    // 可 JSON 序列化；无前置值传 nil
	AfterValue   any    // 可 JSON 序列化；无后置值传 nil
	Reason       string // 可空
	SourceType   string // HUMAN / CRON / WORKER / AGENT
	SourceID     *string
	RequestID    string
}

// AuditRepo 是 audit_log 的写入仓储。
type AuditRepo struct {
	base *gorm.DB
}

// NewAuditRepo 构造审计仓储。
func NewAuditRepo(base *gorm.DB) *AuditRepo { return &AuditRepo{base: base} }

type auditLogRow struct {
	ID                 int64     `gorm:"primaryKey"`
	OperatorID         int64     `gorm:"column:operator_id"`
	OperatorRole       string    `gorm:"column:operator_role"`
	Action             string    `gorm:"column:action"`
	TargetType         string    `gorm:"column:target_type"`
	TargetID           int64     `gorm:"column:target_id"`
	BeforeValue        []byte    `gorm:"column:before_value"`
	AfterValue         []byte    `gorm:"column:after_value"`
	Reason             *string   `gorm:"column:reason"`
	SourceType         string    `gorm:"column:source_type"`
	SourceID           *string   `gorm:"column:source_id"`
	CreatedAt          time.Time `gorm:"column:created_at"`
	UpdatedAt          time.Time `gorm:"column:updated_at"`
	RequestID          *string   `gorm:"column:request_id"`
	CreatedBy          *int64    `gorm:"column:created_by"`
	UpdatedBy          *int64    `gorm:"column:updated_by"`
	InternalOperatorID *int64    `gorm:"column:internal_operator_id"` // 10c：内部员工 ID
	SubjectOperatorID  *int64    `gorm:"column:subject_operator_id"`  // 10c：外部主体 ID
}

func (auditLogRow) TableName() string { return "audit_log" }

// Record 写入一条审计日志。优先使用 ctx 中的事务（与业务变更同生共死）。
func (r *AuditRepo) Record(ctx context.Context, e AuditEntry) error {
	tx := db.FromContext(ctx)
	if tx == nil {
		tx = r.base
	}

	var beforeJSON, afterJSON []byte
	if e.BeforeValue != nil {
		b, err := json.Marshal(e.BeforeValue)
		if err != nil {
			return fmt.Errorf("marshal audit before_value: %w", err)
		}
		beforeJSON = b
	}
	if e.AfterValue != nil {
		b, err := json.Marshal(e.AfterValue)
		if err != nil {
			return fmt.Errorf("marshal audit after_value: %w", err)
		}
		afterJSON = b
	}

	now := time.Now().UTC()
	row := auditLogRow{
		OperatorID:   e.OperatorID,
		OperatorRole: e.OperatorRole,
		Action:       e.Action,
		TargetType:   e.TargetType,
		TargetID:     e.TargetID,
		BeforeValue:  beforeJSON,
		AfterValue:   afterJSON,
		SourceType:   e.SourceType,
		SourceID:     e.SourceID,
		CreatedAt:    now,
		UpdatedAt:    now,
		RequestID:    strPtr(e.RequestID),
		CreatedBy:    int64Ptr(e.OperatorID),
		UpdatedBy:    int64Ptr(e.OperatorID),
	}
	if e.Reason != "" {
		row.Reason = &e.Reason
	}
	if row.SourceType == "" {
		row.SourceType = "HUMAN"
	}

	// 10c 裁决 3：两列填充（按 operator_role 分派）
	// 内部角色 → internal_operator_id；外部角色 → subject_operator_id；SYSTEM/operator_id=0 → 两列 NULL
	if e.OperatorID != 0 && e.OperatorRole != "SYSTEM" {
		if isInternalRole(e.OperatorRole) {
			row.InternalOperatorID = int64Ptr(e.OperatorID)
		} else {
			row.SubjectOperatorID = int64Ptr(e.OperatorID)
		}
	}

	if err := tx.Create(&row).Error; err != nil {
		return fmt.Errorf("insert audit_log: %w", err)
	}
	return nil
}

// isInternalRole 判断是否内部角色（变异 #1 锚点：改成外部角色 → 测试红）。
func isInternalRole(role string) bool {
	switch role {
	case "STAFF", "PLATFORM_ADMIN", "MODEL_OPS", "PRICING_OP", "PROCUREMENT", "SALES", "FINANCE", "OPS_ADMIN", "RETRO_OP", "AUDIT_READONLY":
		return true
	default:
		return false
	}
}
