package workbench

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeAuditStore 假仓储。
type fakeAuditStore struct {
	listFunc       func(ctx context.Context, q AuditQuery) ([]AuditItem, int64, error)
	exportListFunc func(ctx context.Context, q AuditExportQuery) ([]AuditItem, int64, error)
}

func (f *fakeAuditStore) ListAuditLogs(ctx context.Context, q AuditQuery) ([]AuditItem, int64, error) {
	return f.listFunc(ctx, q)
}

func (f *fakeAuditStore) ListAuditLogsForExport(ctx context.Context, q AuditExportQuery) ([]AuditItem, int64, error) {
	return f.exportListFunc(ctx, q)
}

// TestListAuditLogs_Pagination 分页兜底。
func TestListAuditLogs_Pagination(t *testing.T) {
	store := &fakeAuditStore{
		listFunc: func(ctx context.Context, q AuditQuery) ([]AuditItem, int64, error) {
			if q.Page != 1 || q.Size != 20 {
				t.Fatalf("page=%d size=%d, want 1/20", q.Page, q.Size)
			}
			return []AuditItem{}, 0, nil
		},
	}
	svc := NewAuditService(store)

	_, err := svc.ListAuditLogs(context.Background(), AuditQuery{Page: 0, Size: 0})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
}

// TestListAuditLogs_Filter 筛选条件透传。
func TestListAuditLogs_Filter(t *testing.T) {
	targetID := int64(123)
	operatorID := int64(456)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC)

	store := &fakeAuditStore{
		listFunc: func(ctx context.Context, q AuditQuery) ([]AuditItem, int64, error) {
			if q.Action != "CUSTOMER_QUOTE_ACCEPTED" {
				t.Fatalf("action=%s", q.Action)
			}
			if q.TargetType != "CUSTOMER_QUOTE" {
				t.Fatalf("target_type=%s", q.TargetType)
			}
			if q.TargetID == nil || *q.TargetID != 123 {
				t.Fatalf("target_id=%v", q.TargetID)
			}
			if q.OperatorID == nil || *q.OperatorID != 456 {
				t.Fatalf("operator_id=%v", q.OperatorID)
			}
			if q.OperatorRole != "CUSTOMER" {
				t.Fatalf("operator_role=%s", q.OperatorRole)
			}
			if q.SourceType != "INTERNAL" {
				t.Fatalf("source_type=%s", q.SourceType)
			}
			if q.From == nil || !q.From.Equal(from) {
				t.Fatalf("from=%v", q.From)
			}
			if q.To == nil || !q.To.Equal(to) {
				t.Fatalf("to=%v", q.To)
			}
			return []AuditItem{{ID: 1, Action: "CUSTOMER_QUOTE_ACCEPTED"}}, 1, nil
		},
	}
	svc := NewAuditService(store)

	result, err := svc.ListAuditLogs(context.Background(), AuditQuery{
		Action:       "CUSTOMER_QUOTE_ACCEPTED",
		TargetType:   "CUSTOMER_QUOTE",
		TargetID:     &targetID,
		OperatorID:   &operatorID,
		OperatorRole: "CUSTOMER",
		SourceType:   "INTERNAL",
		From:         &from,
		To:           &to,
		Page:         1,
		Size:         20,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if result.Total != 1 || len(result.Items) != 1 {
		t.Fatalf("result=%+v", result)
	}
}

// TestExportAuditLogs_CSV CSV 导出（UTF-8 BOM）。
func TestExportAuditLogs_CSV(t *testing.T) {
	store := &fakeAuditStore{
		exportListFunc: func(ctx context.Context, q AuditExportQuery) ([]AuditItem, int64, error) {
			return []AuditItem{
				{ID: 1, OperatorID: 1, OperatorName: "张三", Action: "TEST", CreatedAt: "2026-01-01T00:00:00Z"},
			}, 1, nil
		},
	}
	svc := NewAuditService(store)

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC)
	result, err := svc.ExportAuditLogs(context.Background(), AuditExportQuery{
		Format: "csv",
		From:   &from,
		To:     &to,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !strings.HasPrefix(string(result.Data), "\xEF\xBB\xBF") {
		t.Fatalf("CSV 缺 UTF-8 BOM")
	}
	if !strings.Contains(string(result.Data), "张三") {
		t.Fatalf("CSV 缺数据行")
	}
	if result.ContentType != "text/csv; charset=utf-8" {
		t.Fatalf("content_type=%s", result.ContentType)
	}
}

// TestExportAuditLogs_XLSX XLSX 导出。
func TestExportAuditLogs_XLSX(t *testing.T) {
	store := &fakeAuditStore{
		exportListFunc: func(ctx context.Context, q AuditExportQuery) ([]AuditItem, int64, error) {
			return []AuditItem{
				{ID: 1, OperatorID: 1, OperatorName: "张三", Action: "TEST", CreatedAt: "2026-01-01T00:00:00Z"},
			}, 1, nil
		},
	}
	svc := NewAuditService(store)

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC)
	result, err := svc.ExportAuditLogs(context.Background(), AuditExportQuery{
		Format: "xlsx",
		From:   &from,
		To:     &to,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(result.Data) == 0 {
		t.Fatalf("XLSX 数据为空")
	}
	if result.ContentType != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("content_type=%s", result.ContentType)
	}
}

// TestExportAuditLogs_OverLimit 超 10000 行 → 报错（变异 #2 锚点）。
func TestExportAuditLogs_OverLimit(t *testing.T) {
	store := &fakeAuditStore{
		exportListFunc: func(ctx context.Context, q AuditExportQuery) ([]AuditItem, int64, error) {
			return []AuditItem{}, 10001, nil // 超上限
		},
	}
	svc := NewAuditService(store)

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC)
	_, err := svc.ExportAuditLogs(context.Background(), AuditExportQuery{
		Format: "csv",
		From:   &from,
		To:     &to,
	})
	if err == nil || !strings.Contains(err.Error(), "超过上限 10000") {
		t.Fatalf("err=%v, want 超过上限 10000", err)
	}
}

// TestExportAuditLogs_TimeRange 时间范围透传（左闭右开）。
func TestExportAuditLogs_TimeRange(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC)

	store := &fakeAuditStore{
		exportListFunc: func(ctx context.Context, q AuditExportQuery) ([]AuditItem, int64, error) {
			if q.From == nil || !q.From.Equal(from) {
				t.Fatalf("from=%v", q.From)
			}
			if q.To == nil || !q.To.Equal(to) {
				t.Fatalf("to=%v", q.To)
			}
			return []AuditItem{}, 0, nil
		},
	}
	svc := NewAuditService(store)

	_, err := svc.ExportAuditLogs(context.Background(), AuditExportQuery{
		Format: "csv",
		From:   &from,
		To:     &to,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
}
