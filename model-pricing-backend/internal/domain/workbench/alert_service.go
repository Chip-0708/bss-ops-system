// Package workbench 的 alert_service.go：10b §4 告警处理（list + handle）。
//
// 权限（契约 F:V/F:E → M12:V/M12:E，stage10b 裁决 1）：
//   - GET /alerts → M12:V
//   - POST /alerts → M12:E + 幂等
//
// 状态机（§4.2）：
//   - HANDLE    → HANDLING, handle_note
//   - RESOLVE   → RESOLVED, handle_note, resolved_at=now
//   - IGNORE    → IGNORED,  handle_note, resolved_at=now
//   - TO_TICKET → 状态不变，创建 todo_task
//
// 终态（RESOLVED/IGNORED）→ 409（重复处理）。
// TO_TICKET 强制 create_todo=true（否则动作无意义）。
package workbench

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// 领域错误（sentinel error，api 层映射到 apperr）。
var (
	// ErrAlertNotFound 告警不存在 → 404。
	ErrAlertNotFound = errors.New("告警不存在")
	// ErrAlertTerminal 告警已终态（RESOLVED/IGNORED）→ 409（变异 #1 锚点：去掉终态判断 → 重复处理返回 200）。
	ErrAlertTerminal = errors.New("告警已处理，不可重复操作")
	// ErrAlertActionInvalid action 不在枚举内 → 400。
	ErrAlertActionInvalid = errors.New("action 必须是 HANDLE/RESOLVE/IGNORE/TO_TICKET")
)

// AlertService 告警服务。
type AlertService struct {
	store AlertStore
	now   func() time.Time
}

// NewAlertService 构造；now=nil 用 time.Now。
func NewAlertService(store AlertStore, now func() time.Time) *AlertService {
	if now == nil {
		now = time.Now
	}
	return &AlertService{store: store, now: now}
}

// AlertStore 告警仓储抽象。
type AlertStore interface {
	// ListAlerts 告警列表（多维筛选 + 分页）。
	ListAlerts(ctx context.Context, q AlertQuery) ([]AlertItem, int64, error)
	// GetAlert 按 ID 取告警（含 status 判定终态）。
	GetAlert(ctx context.Context, id int64) (*AlertItem, error)
	// HandleAlertTx 单事务：UPDATE alert + 可选 INSERT todo_task + audit。
	HandleAlertTx(ctx context.Context, id int64, action string, note string, createTodo bool, operatorID int64, operatorRole string) (*AlertActionResult, error)
}

// AlertQuery 告警查询条件。
type AlertQuery struct {
	Severity  string
	Status    string
	AlertType string
	Page      int
	Size      int
}

// AlertItem 告警行。
type AlertItem struct {
	ID           int64      `json:"id"`
	AlertType    string     `json:"alert_type"`
	Severity     string     `json:"severity"`
	TargetType   string     `json:"target_type"`
	TargetID     int64      `json:"target_id"`
	Message      string     `json:"message"`
	Status       string     `json:"status"`
	AssignedRole string     `json:"assigned_role"`
	HandleNote   string     `json:"handle_note"`
	ResolvedAt   *time.Time `json:"resolved_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// AlertListResult 告警列表结果。
type AlertListResult struct {
	List  []AlertItem `json:"list"`
	Total int         `json:"total"`
	Page  int         `json:"page"`
	Size  int         `json:"size"`
}

// AlertActionInput 告警处理入参。
type AlertActionInput struct {
	AlertID    int64  `json:"alert_id" binding:"required"`
	Action     string `json:"action" binding:"required,oneof=HANDLE RESOLVE IGNORE TO_TICKET"`
	Note       string `json:"note"`
	CreateTodo bool   `json:"create_todo"`
}

// AlertActionResult 告警处理结果。
type AlertActionResult struct {
	AlertID int64  `json:"alert_id"`
	Status  string `json:"status"`
	TodoID  *int64 `json:"todo_id,omitempty"`
}

// 终态集合（不可再处理）。
var alertTerminalStatus = map[string]bool{
	"RESOLVED": true,
	"IGNORED":  true,
}

// ListAlerts 告警列表。
func (s *AlertService) ListAlerts(ctx context.Context, q AlertQuery) (*AlertListResult, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size <= 0 {
		q.Size = 20
	}
	if q.Size > 200 {
		q.Size = 200
	}
	list, total, err := s.store.ListAlerts(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	return &AlertListResult{List: list, Total: int(total), Page: q.Page, Size: q.Size}, nil
}

// HandleAlert 告警处理（裁决 5：状态机 + 终态 409 + TO_TICKET 建工单）。
//
// 状态机（§4.2）：
//   - HANDLE    → HANDLING, handle_note
//   - RESOLVE   → RESOLVED, handle_note, resolved_at=now
//   - IGNORE    → IGNORED,  handle_note, resolved_at=now
//   - TO_TICKET → 状态不变，创建 todo_task
//
// 终态 → 409（变异 #1 锚点：去掉终态判断 → 重复处理返回 200）。
// TO_TICKET 强制 create_todo=true（否则动作无意义）。
func (s *AlertService) HandleAlert(ctx context.Context, in AlertActionInput, operatorID int64, operatorRole string) (*AlertActionResult, error) {
	// action 校验（service 层兜底，binding oneof 之外）
	if !isValidAlertAction(in.Action) {
		return nil, ErrAlertActionInvalid
	}

	// TO_TICKET 强制 create_todo=true
	if in.Action == "TO_TICKET" {
		in.CreateTodo = true
	}

	alert, err := s.store.GetAlert(ctx, in.AlertID)
	if err != nil {
		return nil, fmt.Errorf("get alert %d: %w", in.AlertID, err)
	}
	if alert == nil {
		return nil, ErrAlertNotFound
	}
	// 终态守卫（变异 #1 锚点：去掉本判断 → 终态可重复处理）
	if alertTerminalStatus[alert.Status] {
		return nil, ErrAlertTerminal
	}

	res, err := s.store.HandleAlertTx(ctx, in.AlertID, in.Action, in.Note, in.CreateTodo, operatorID, operatorRole)
	if err != nil {
		return nil, fmt.Errorf("handle alert tx: %w", err)
	}
	return res, nil
}

// isValidAlertAction 校验 action 是否在枚举内。
func isValidAlertAction(action string) bool {
	switch action {
	case "HANDLE", "RESOLVE", "IGNORE", "TO_TICKET":
		return true
	default:
		return false
	}
}
