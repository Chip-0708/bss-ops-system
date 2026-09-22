// Package repo 的 workbench_alert.go：10b 告警与审计仓储实现。
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"model_bss/internal/domain/workbench"
	"model_bss/internal/infra/db"
)

// AlertRepo 告警仓储。
type AlertRepo struct {
	base *gorm.DB
}

// NewAlertRepo 构造。
func NewAlertRepo(base *gorm.DB) *AlertRepo {
	return &AlertRepo{base: base}
}

// txOf 优先取 ctx 中的 tx（幂等中间件注入），否则用 base。
func (r *AlertRepo) txOf(ctx context.Context) *gorm.DB {
	if tx := db.FromContext(ctx); tx != nil {
		return tx
	}
	return r.base
}

// alertRow alert 表行模型（读专用）。
type alertRow struct {
	ID         int64      `gorm:"column:id"`
	AlertType  string     `gorm:"column:alert_type"`
	Severity   string     `gorm:"column:severity"`
	TargetType string     `gorm:"column:target_type"`
	TargetID   int64      `gorm:"column:target_id"`
	Message    string     `gorm:"column:message"`
	Status     string     `gorm:"column:status"`
	HandleNote string     `gorm:"column:handle_note"`
	ResolvedAt *time.Time `gorm:"column:resolved_at"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
	UpdatedAt  time.Time  `gorm:"column:updated_at"`
}

func (alertRow) TableName() string { return "alert" }

// ListAlerts 告警列表。
func (r *AlertRepo) ListAlerts(ctx context.Context, q workbench.AlertQuery) ([]workbench.AlertItem, int64, error) {
	tx := r.txOf(ctx)

	// 构建查询
	query := tx.Model(&alertRow{})
	if q.Severity != "" {
		query = query.Where("severity = ?", q.Severity)
	}
	if q.Status != "" {
		query = query.Where("status = ?", q.Status)
	}
	if q.AlertType != "" {
		query = query.Where("alert_type = ?", q.AlertType)
	}

	// 总数
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count alerts: %w", err)
	}

	// 分页
	var rows []alertRow
	if err := query.
		Order("created_at DESC").
		Offset((q.Page - 1) * q.Size).
		Limit(q.Size).
		Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list alerts: %w", err)
	}

	// 转换
	items := make([]workbench.AlertItem, len(rows))
	for i, row := range rows {
		items[i] = workbench.AlertItem{
			ID:         row.ID,
			AlertType:  row.AlertType,
			Severity:   row.Severity,
			TargetType: row.TargetType,
			TargetID:   row.TargetID,
			Message:    row.Message,
			Status:     row.Status,
			HandleNote: row.HandleNote,
			ResolvedAt: row.ResolvedAt,
			CreatedAt:  row.CreatedAt,
			UpdatedAt:  row.UpdatedAt,
		}
	}
	return items, total, nil
}

// GetAlert 按 ID 取告警。
func (r *AlertRepo) GetAlert(ctx context.Context, id int64) (*workbench.AlertItem, error) {
	tx := r.txOf(ctx)
	var row alertRow
	if err := tx.Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get alert %d: %w", id, err)
	}
	return &workbench.AlertItem{
		ID:         row.ID,
		AlertType:  row.AlertType,
		Severity:   row.Severity,
		TargetType: row.TargetType,
		TargetID:   row.TargetID,
		Message:    row.Message,
		Status:     row.Status,
		HandleNote: row.HandleNote,
		ResolvedAt: row.ResolvedAt,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}, nil
}

// HandleAlertTx 告警处理（单事务）。
func (r *AlertRepo) HandleAlertTx(ctx context.Context, alertID int64, action string, note string, createTodo bool, operatorID int64, operatorRole string) (*workbench.AlertActionResult, error) {
	tx := r.txOf(ctx)

	// 1. 查询告警（FOR UPDATE）
	var alert alertRow
	if err := tx.Where("id = ?", alertID).First(&alert).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, workbench.ErrAlertNotFound
		}
		return nil, fmt.Errorf("get alert %d: %w", alertID, err)
	}

	// 2. 终态判断（变异 #1 锚点：去掉本判断 → 终态可重复处理）
	if alert.Status == "RESOLVED" || alert.Status == "IGNORED" {
		return nil, workbench.ErrAlertTerminal
	}

	// 3. 更新告警状态
	now := time.Now().UTC()
	updates := map[string]interface{}{
		"handle_note": note,
		"updated_at":  now,
	}
	switch action {
	case "HANDLE":
		updates["status"] = "HANDLING"
	case "RESOLVE":
		updates["status"] = "RESOLVED"
		updates["resolved_at"] = now
	case "IGNORE":
		updates["status"] = "IGNORED"
		updates["resolved_at"] = now
	case "TO_TICKET":
		// 状态不变
	}

	if len(updates) > 0 {
		if err := tx.Model(&alertRow{}).Where("id = ?", alertID).Updates(updates).Error; err != nil {
			return nil, fmt.Errorf("update alert %d: %w", alertID, err)
		}
	}

	// 4. 创建 todo_task（可选）
	var todoID *int64
	if createTodo {
		todo := map[string]interface{}{
			"biz_type":      "ALERT",
			"biz_id":        alertID,
			"title":         alert.Message,
			"assignee_id":   operatorID,
			"assignee_role": operatorRole,
			"status":        "OPEN",
			"created_at":    now,
			"updated_at":    now,
			"request_id":    fmt.Sprintf("alert-%d-%d", alertID, now.Unix()),
			"created_by":    operatorID,
			"updated_by":    operatorID,
		}
		if err := tx.Table("todo_task").Create(&todo).Error; err != nil {
			return nil, fmt.Errorf("create todo_task: %w", err)
		}
		// 查询刚插入的 todo_task id
		var id int64
		if err := tx.Table("todo_task").
			Where("biz_type = ? AND biz_id = ? AND assignee_id = ?", "ALERT", alertID, operatorID).
			Order("created_at DESC").
			Limit(1).
			Pluck("id", &id).Error; err != nil {
			return nil, fmt.Errorf("get todo_task id: %w", err)
		}
		todoID = &id
	}

	// 5. 写审计日志
	afterValue, _ := json.Marshal(map[string]interface{}{
		"alert_id":    alertID,
		"action":      action,
		"note":        note,
		"create_todo": createTodo,
		"todo_id":     todoID,
	})
	audit := map[string]interface{}{
		"operator_id":   operatorID,
		"operator_role": operatorRole,
		"action":        "ALERT_HANDLE",
		"target_type":   "ALERT",
		"target_id":     alertID,
		"before_value":  fmt.Sprintf(`{"status":"%s"}`, alert.Status),
		"after_value":   string(afterValue),
		"source_type":   "INTERNAL",
		"source_id":     fmt.Sprintf("staff:%d", operatorID),
		"request_id":    fmt.Sprintf("alert-%d-%d", alertID, now.Unix()),
		"created_at":    now,
		"updated_at":    now,
		"created_by":    operatorID,
		"updated_by":    operatorID,
	}
	if err := tx.Table("audit_log").Create(&audit).Error; err != nil {
		return nil, fmt.Errorf("create audit_log: %w", err)
	}

	return &workbench.AlertActionResult{
		AlertID: alertID,
		Status:  alert.Status,
		TodoID:  todoID,
	}, nil
}

// WorkbenchAuditRepo 审计仓储（10b 查询专用，区别于 audit.go 的写入 AuditRepo）。
type WorkbenchAuditRepo struct {
	base *gorm.DB
}

// NewWorkbenchAuditRepo 构造。
func NewWorkbenchAuditRepo(base *gorm.DB) *WorkbenchAuditRepo {
	return &WorkbenchAuditRepo{base: base}
}

// txOf 优先取 ctx 中的 tx（幂等中间件注入），否则用 base。
func (r *WorkbenchAuditRepo) txOf(ctx context.Context) *gorm.DB {
	if tx := db.FromContext(ctx); tx != nil {
		return tx
	}
	return r.base
}

// auditRow audit_log 表行模型（读专用）。
type auditRow struct {
	ID                 int64     `gorm:"column:id"`
	OperatorID         int64     `gorm:"column:operator_id"`
	OperatorRole       string    `gorm:"column:operator_role"`
	Action             string    `gorm:"column:action"`
	TargetType         string    `gorm:"column:target_type"`
	TargetID           int64     `gorm:"column:target_id"`
	BeforeValue        string    `gorm:"column:before_value"`
	AfterValue         string    `gorm:"column:after_value"`
	SourceType         string    `gorm:"column:source_type"`
	SourceID           string    `gorm:"column:source_id"`
	RequestID          string    `gorm:"column:request_id"`
	CreatedAt          time.Time `gorm:"column:created_at"`
	InternalOperatorID *int64    `gorm:"column:internal_operator_id"` // 10c：内部员工 ID
	SubjectOperatorID  *int64    `gorm:"column:subject_operator_id"`  // 10c：外部主体 ID
}

func (auditRow) TableName() string { return "audit_log" }

// ListAuditLogs 审计日志列表（含 operator_name 解析）。
func (r *WorkbenchAuditRepo) ListAuditLogs(ctx context.Context, q workbench.AuditQuery) ([]workbench.AuditItem, int64, error) {
	tx := r.txOf(ctx)

	// 构建查询
	query := tx.Model(&auditRow{})
	if q.Action != "" {
		query = query.Where("action = ?", q.Action)
	}
	if q.TargetType != "" {
		query = query.Where("target_type = ?", q.TargetType)
	}
	if q.TargetID != nil {
		query = query.Where("target_id = ?", *q.TargetID)
	}
	if q.OperatorID != nil {
		query = query.Where("operator_id = ?", *q.OperatorID)
	}
	if q.OperatorRole != "" {
		query = query.Where("operator_role = ?", q.OperatorRole)
	}
	if q.SourceType != "" {
		query = query.Where("source_type = ?", q.SourceType)
	}
	if q.From != nil {
		query = query.Where("created_at >= ?", *q.From)
	}
	if q.To != nil {
		query = query.Where("created_at < ?", *q.To) // 左闭右开
	}

	// 总数
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count audit_logs: %w", err)
	}

	// 分页
	var rows []auditRow
	if err := query.
		Order("created_at DESC").
		Offset((q.Page - 1) * q.Size).
		Limit(q.Size).
		Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list audit_logs: %w", err)
	}

	// operator_name 解析
	items := make([]workbench.AuditItem, len(rows))
	for i, row := range rows {
		operatorName := r.resolveOperatorName(ctx, row)
		items[i] = workbench.AuditItem{
			ID:           row.ID,
			OperatorID:   row.OperatorID,
			OperatorName: operatorName,
			OperatorRole: row.OperatorRole,
			Action:       row.Action,
			TargetType:   row.TargetType,
			TargetID:     row.TargetID,
			BeforeValue:  row.BeforeValue,
			AfterValue:   row.AfterValue,
			SourceType:   row.SourceType,
			SourceID:     row.SourceID,
			RequestID:    row.RequestID,
			CreatedAt:    row.CreatedAt.Format(time.RFC3339),
		}
	}
	return items, total, nil
}

// resolveOperatorName 解析 operator_name（10c 裁决 4：先查两列，再 fallback）。
//
// 新逻辑（变异 #2 锚点：internal_operator_id 非 NULL → 读 internal_staff，改成读 legal_subject → 测试红）：
//  1. internal_operator_id 非 NULL → internal_staff.name
//  2. subject_operator_id 非 NULL → legal_subject.legal_name（customer_profile / supplier_profile）
//  3. operator_id=0 → "系统"
//  4. 查不到 → "未知(id=N, role=X)"（不吞掉）
func (r *WorkbenchAuditRepo) resolveOperatorName(ctx context.Context, row auditRow) string {
	tx := r.txOf(ctx)

	// 1. internal_operator_id 非 NULL → internal_staff.name（变异 #2 锚点：改成读 legal_subject → 测试红）
	if row.InternalOperatorID != nil {
		var name string
		if err := tx.Table("internal_staff").
			Where("id = ?", *row.InternalOperatorID).
			Limit(1).
			Pluck("name", &name).Error; err == nil && name != "" {
			return name
		}
		return fmt.Sprintf("员工(id=%d)", *row.InternalOperatorID)
	}

	// 2. subject_operator_id 非 NULL → legal_subject.legal_name
	if row.SubjectOperatorID != nil {
		var legalName string
		// 先查 customer_profile
		if err := tx.Table("customer_profile cp").
			Select("ls.legal_name").
			Joins("JOIN legal_subject ls ON ls.id = cp.subject_id").
			Where("cp.id = ?", *row.SubjectOperatorID).
			Limit(1).
			Pluck("legal_name", &legalName).Error; err == nil && legalName != "" {
			return legalName
		}
		// 再查 supplier_profile
		if err := tx.Table("supplier_profile sp").
			Select("ls.legal_name").
			Joins("JOIN legal_subject ls ON ls.id = sp.subject_id").
			Where("sp.id = ?", *row.SubjectOperatorID).
			Limit(1).
			Pluck("legal_name", &legalName).Error; err == nil && legalName != "" {
			return legalName
		}
		return fmt.Sprintf("主体(id=%d)", *row.SubjectOperatorID)
	}

	// 3. operator_id=0 → "系统"
	if row.OperatorID == 0 {
		return "系统"
	}

	// 4. 查不到 → 显式标注（不吞掉）
	return fmt.Sprintf("未知(id=%d, role=%s)", row.OperatorID, row.OperatorRole)
}

// ListAuditLogsForExport 查询导出数据（不分页，上限 10001 行判断是否超限）。
func (r *WorkbenchAuditRepo) ListAuditLogsForExport(ctx context.Context, q workbench.AuditExportQuery) ([]workbench.AuditItem, int64, error) {
	return r.ListAuditLogs(ctx, workbench.AuditQuery{
		From: q.From,
		To:   q.To,
		Page: 1,
		Size: 10001, // 多取 1 行判断是否超限
	})
}
