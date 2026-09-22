package workbench

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeAlertStore 假仓储。
type fakeAlertStore struct {
	listFunc     func(ctx context.Context, q AlertQuery) ([]AlertItem, int64, error)
	getFunc      func(ctx context.Context, id int64) (*AlertItem, error)
	handleTxFunc func(ctx context.Context, id int64, action string, note string, createTodo bool, operatorID int64, operatorRole string) (*AlertActionResult, error)
}

func (f *fakeAlertStore) ListAlerts(ctx context.Context, q AlertQuery) ([]AlertItem, int64, error) {
	return f.listFunc(ctx, q)
}

func (f *fakeAlertStore) GetAlert(ctx context.Context, id int64) (*AlertItem, error) {
	return f.getFunc(ctx, id)
}

func (f *fakeAlertStore) HandleAlertTx(ctx context.Context, id int64, action string, note string, createTodo bool, operatorID int64, operatorRole string) (*AlertActionResult, error) {
	return f.handleTxFunc(ctx, id, action, note, createTodo, operatorID, operatorRole)
}

// TestListAlerts_Pagination 分页兜底。
func TestListAlerts_Pagination(t *testing.T) {
	store := &fakeAlertStore{
		listFunc: func(ctx context.Context, q AlertQuery) ([]AlertItem, int64, error) {
			if q.Page != 1 || q.Size != 20 {
				t.Fatalf("page=%d size=%d, want 1/20", q.Page, q.Size)
			}
			return []AlertItem{}, 0, nil
		},
	}
	svc := NewAlertService(store, nil)

	// page=0 → 1, size=0 → 20
	_, err := svc.ListAlerts(context.Background(), AlertQuery{Page: 0, Size: 0})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
}

// TestListAlerts_Filter 筛选条件透传。
func TestListAlerts_Filter(t *testing.T) {
	store := &fakeAlertStore{
		listFunc: func(ctx context.Context, q AlertQuery) ([]AlertItem, int64, error) {
			if q.Severity != "CRITICAL" || q.Status != "OPEN" || q.AlertType != "QUOTE_EXPIRE" {
				t.Fatalf("filter mismatch: %+v", q)
			}
			return []AlertItem{{ID: 1, Severity: "CRITICAL"}}, 1, nil
		},
	}
	svc := NewAlertService(store, nil)

	result, err := svc.ListAlerts(context.Background(), AlertQuery{
		Severity:  "CRITICAL",
		Status:    "OPEN",
		AlertType: "QUOTE_EXPIRE",
		Page:      1,
		Size:      20,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if result.Total != 1 || len(result.List) != 1 {
		t.Fatalf("result=%+v", result)
	}
}

// TestHandleAlert_NotFound 告警不存在 → 404。
func TestHandleAlert_NotFound(t *testing.T) {
	store := &fakeAlertStore{
		getFunc: func(ctx context.Context, id int64) (*AlertItem, error) {
			return nil, nil // 不存在
		},
	}
	svc := NewAlertService(store, nil)

	_, err := svc.HandleAlert(context.Background(), AlertActionInput{
		AlertID: 999,
		Action:  "HANDLE",
	}, 1, "PLATFORM_ADMIN")
	if !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("err=%v, want ErrAlertNotFound", err)
	}
}

// TestHandleAlert_Terminal 终态 → 409（变异 #1 锚点）。
func TestHandleAlert_Terminal(t *testing.T) {
	store := &fakeAlertStore{
		getFunc: func(ctx context.Context, id int64) (*AlertItem, error) {
			return &AlertItem{ID: id, Status: "RESOLVED"}, nil
		},
	}
	svc := NewAlertService(store, nil)

	_, err := svc.HandleAlert(context.Background(), AlertActionInput{
		AlertID: 1,
		Action:  "HANDLE",
	}, 1, "PLATFORM_ADMIN")
	if !errors.Is(err, ErrAlertTerminal) {
		t.Fatalf("err=%v, want ErrAlertTerminal", err)
	}
}

// TestHandleAlert_Handle HANDLE → HANDLING。
func TestHandleAlert_Handle(t *testing.T) {
	store := &fakeAlertStore{
		getFunc: func(ctx context.Context, id int64) (*AlertItem, error) {
			return &AlertItem{ID: id, Status: "OPEN"}, nil
		},
		handleTxFunc: func(ctx context.Context, id int64, action string, note string, createTodo bool, operatorID int64, operatorRole string) (*AlertActionResult, error) {
			if action != "HANDLE" {
				t.Fatalf("action=%s, want HANDLE", action)
			}
			return &AlertActionResult{AlertID: id, Status: "HANDLING"}, nil
		},
	}
	svc := NewAlertService(store, nil)

	result, err := svc.HandleAlert(context.Background(), AlertActionInput{
		AlertID: 1,
		Action:  "HANDLE",
		Note:    "处理中",
	}, 1, "PLATFORM_ADMIN")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if result.Status != "HANDLING" {
		t.Fatalf("status=%s, want HANDLING", result.Status)
	}
}

// TestHandleAlert_Resolve RESOLVE → RESOLVED + resolved_at。
func TestHandleAlert_Resolve(t *testing.T) {
	store := &fakeAlertStore{
		getFunc: func(ctx context.Context, id int64) (*AlertItem, error) {
			return &AlertItem{ID: id, Status: "OPEN"}, nil
		},
		handleTxFunc: func(ctx context.Context, id int64, action string, note string, createTodo bool, operatorID int64, operatorRole string) (*AlertActionResult, error) {
			if action != "RESOLVE" {
				t.Fatalf("action=%s, want RESOLVE", action)
			}
			return &AlertActionResult{AlertID: id, Status: "RESOLVED"}, nil
		},
	}
	svc := NewAlertService(store, nil)

	result, err := svc.HandleAlert(context.Background(), AlertActionInput{
		AlertID: 1,
		Action:  "RESOLVE",
		Note:    "已解决",
	}, 1, "PLATFORM_ADMIN")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if result.Status != "RESOLVED" {
		t.Fatalf("status=%s, want RESOLVED", result.Status)
	}
}

// TestHandleAlert_ToTicket TO_TICKET 强制 create_todo=true。
func TestHandleAlert_ToTicket(t *testing.T) {
	store := &fakeAlertStore{
		getFunc: func(ctx context.Context, id int64) (*AlertItem, error) {
			return &AlertItem{ID: id, Status: "OPEN"}, nil
		},
		handleTxFunc: func(ctx context.Context, id int64, action string, note string, createTodo bool, operatorID int64, operatorRole string) (*AlertActionResult, error) {
			if action != "TO_TICKET" {
				t.Fatalf("action=%s, want TO_TICKET", action)
			}
			if !createTodo {
				t.Fatalf("createTodo=false, want true (TO_TICKET 强制)")
			}
			todoID := int64(123)
			return &AlertActionResult{AlertID: id, Status: "OPEN", TodoID: &todoID}, nil
		},
	}
	svc := NewAlertService(store, nil)

	result, err := svc.HandleAlert(context.Background(), AlertActionInput{
		AlertID:    1,
		Action:     "TO_TICKET",
		CreateTodo: false, // 显式 false，应被强制改 true
	}, 1, "PLATFORM_ADMIN")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if result.TodoID == nil || *result.TodoID != 123 {
		t.Fatalf("todo_id=%v, want 123", result.TodoID)
	}
}

// TestHandleAlert_InvalidAction action 非法 → 400。
func TestHandleAlert_InvalidAction(t *testing.T) {
	store := &fakeAlertStore{
		getFunc: func(ctx context.Context, id int64) (*AlertItem, error) {
			return &AlertItem{ID: id, Status: "OPEN"}, nil
		},
		handleTxFunc: func(ctx context.Context, id int64, action string, note string, createTodo bool, operatorID int64, operatorRole string) (*AlertActionResult, error) {
			t.Fatalf("不应走到 HandleAlertTx（action 校验应前置）")
			return nil, nil
		},
	}
	svc := NewAlertService(store, nil)

	_, err := svc.HandleAlert(context.Background(), AlertActionInput{
		AlertID: 1,
		Action:  "INVALID",
	}, 1, "PLATFORM_ADMIN")
	if !errors.Is(err, ErrAlertActionInvalid) {
		t.Fatalf("err=%v, want ErrAlertActionInvalid", err)
	}
}

// TestHandleAlert_WithNow 自定义 now。
func TestHandleAlert_WithNow(t *testing.T) {
	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &fakeAlertStore{
		getFunc: func(ctx context.Context, id int64) (*AlertItem, error) {
			return &AlertItem{ID: id, Status: "OPEN"}, nil
		},
		handleTxFunc: func(ctx context.Context, id int64, action string, note string, createTodo bool, operatorID int64, operatorRole string) (*AlertActionResult, error) {
			return &AlertActionResult{AlertID: id, Status: "HANDLING"}, nil
		},
	}
	svc := NewAlertService(store, func() time.Time { return fixedNow })

	_, err := svc.HandleAlert(context.Background(), AlertActionInput{
		AlertID: 1,
		Action:  "HANDLE",
	}, 1, "PLATFORM_ADMIN")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
}
