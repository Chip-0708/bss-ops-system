package repo

import (
	"testing"

	"model_bss/internal/domain/workbench"
)

// TestResolveOperatorName_Customer CUSTOMER → legal_subject.legal_name。
func TestResolveOperatorName_Customer(t *testing.T) {
	// 需要真库，跳过单测（E2E 覆盖）
	t.Skip("需要真库")
}

// TestResolveOperatorName_Staff 其他 → internal_staff.name。
func TestResolveOperatorName_Staff(t *testing.T) {
	// 需要真库，跳过单测（E2E 覆盖）
	t.Skip("需要真库")
}

// TestListAuditLogs_TimeRange 时间范围左闭右开。
func TestListAuditLogs_TimeRange(t *testing.T) {
	// 需要真库，跳过单测（E2E 覆盖）
	t.Skip("需要真库")
}

// TestListAuditLogsForExport_OverLimit 超 10000 行 → 报错。
func TestListAuditLogsForExport_OverLimit(t *testing.T) {
	// 需要真库，跳过单测（E2E 覆盖）
	t.Skip("需要真库")
}

// 确保实现接口。
var _ workbench.AuditStore = (*WorkbenchAuditRepo)(nil)
