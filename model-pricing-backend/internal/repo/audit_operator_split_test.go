package repo

import (
	"context"
	"testing"
)

// TestAuditRecord_TwoColumns 两列填充逻辑（10c 裁决 3）。
func TestAuditRecord_TwoColumns(t *testing.T) {
	// 需要真库，跳过单测（E2E 覆盖）
	t.Skip("需要真库")
}

// TestIsInternalRole 内部角色判断（变异 #1 锚点）。
func TestIsInternalRole(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{"STAFF", true},
		{"PLATFORM_ADMIN", true},
		{"MODEL_OPS", true},
		{"PRICING_OP", true},
		{"PROCUREMENT", true},
		{"SALES", true},
		{"FINANCE", true},
		{"OPS_ADMIN", true},
		{"RETRO_OP", true},
		{"AUDIT_READONLY", true},
		{"CUSTOMER", false},
		{"SUPPLIER", false},
		{"INTERNAL", false},
		{"SYSTEM", false},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			if got := isInternalRole(tt.role); got != tt.want {
				t.Fatalf("isInternalRole(%s)=%v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

// TestResolveOperatorName_TwoColumns 两列解析（10c 裁决 4）。
func TestResolveOperatorName_TwoColumns(t *testing.T) {
	// 需要真库，跳过单测（E2E 覆盖）
	t.Skip("需要真库")
}

// TestResolveOperatorName_System SYSTEM → "系统"。
func TestResolveOperatorName_System(t *testing.T) {
	repo := &WorkbenchAuditRepo{}
	row := auditRow{OperatorID: 0, OperatorRole: "SYSTEM"}
	name := repo.resolveOperatorName(context.Background(), row)
	if name != "系统" {
		t.Fatalf("name=%s, want 系统", name)
	}
}

// TestResolveOperatorName_Unknown 查不到 → "未知(id=N, role=X)"。
func TestResolveOperatorName_Unknown(t *testing.T) {
	// 需要真库，跳过单测（E2E 覆盖）
	t.Skip("需要真库")
}
