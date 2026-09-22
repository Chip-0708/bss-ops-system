package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSynthesize_FunctionalNonLeaderIsALL(t *testing.T) {
	// 最容易写错的用例：职能角色 + 非负责人 → 必须是 ALL，不能掉到 SELF。
	roles := []RoleDecl{{Code: "PRICING_OP", IsFunctional: true, DataScope: ScopeALL}}
	got := Synthesize(roles, nil)
	require.Equal(t, ScopeALL, got.Scope)
	require.Empty(t, got.Paths)
}

func TestSynthesize_FunctionalWithLeaderPathsStillALL(t *testing.T) {
	roles := []RoleDecl{{Code: "FINANCE", IsFunctional: true, DataScope: ScopeALL}}
	got := Synthesize(roles, []string{"/1/2/", "/1/5/"})
	require.Equal(t, ScopeALL, got.Scope)
	require.Empty(t, got.Paths, "职能角色不携带 scope_paths")
}

func TestSynthesize_NonFunctionalNonLeaderDeclaredSELF(t *testing.T) {
	roles := []RoleDecl{{Code: "SALES", IsFunctional: false, DataScope: ScopeSELF}}
	got := Synthesize(roles, nil)
	require.Equal(t, ScopeSELF, got.Scope)
}

func TestSynthesize_NonFunctionalNonLeaderDeclaredDEPT(t *testing.T) {
	// 声明域 DEPT，但非负责人 → 可得域 SELF → min = SELF
	roles := []RoleDecl{{Code: "PROCUREMENT", IsFunctional: false, DataScope: ScopeDEPT}}
	got := Synthesize(roles, nil)
	require.Equal(t, ScopeSELF, got.Scope)
}

func TestSynthesize_NonFunctionalLeaderDeclaredDEPT(t *testing.T) {
	// 声明域 DEPT，是负责人 → 可得域 DEPT_SUB → min(DEPT, DEPT_SUB) = DEPT
	roles := []RoleDecl{{Code: "PROCUREMENT", IsFunctional: false, DataScope: ScopeDEPT}}
	got := Synthesize(roles, []string{"/1/"})
	require.Equal(t, ScopeDEPT, got.Scope)
	require.Equal(t, []string{"/1/"}, got.Paths)
}

func TestSynthesize_NonFunctionalLeaderDeclaredDEPTSUB(t *testing.T) {
	// 声明域 DEPT_SUB，是负责人 → min = DEPT_SUB
	roles := []RoleDecl{{Code: "MODEL_OPS", IsFunctional: false, DataScope: ScopeDEPTSUB}}
	got := Synthesize(roles, []string{"/1/2/"})
	require.Equal(t, ScopeDEPTSUB, got.Scope)
	require.Equal(t, []string{"/1/2/"}, got.Paths)
}

func TestSynthesize_NonFunctionalLeaderDeclaredSELF(t *testing.T) {
	// 声明域 SELF，即使负责人 → min(SELF, DEPT_SUB) = SELF
	roles := []RoleDecl{{Code: "SALES", IsFunctional: false, DataScope: ScopeSELF}}
	got := Synthesize(roles, []string{"/1/2/"})
	require.Equal(t, ScopeSELF, got.Scope)
}

func TestSynthesize_MultipleLeaderPathsAllCollected(t *testing.T) {
	// 多节点负责人：scope_paths 全收 + 去重 + 排序
	roles := []RoleDecl{{Code: "OPS", IsFunctional: false, DataScope: ScopeDEPTSUB}}
	got := Synthesize(roles, []string{"/1/5/", "/1/2/", "/1/5/", "/9/"})
	require.Equal(t, ScopeDEPTSUB, got.Scope)
	require.Equal(t, []string{"/1/2/", "/1/5/", "/9/"}, got.Paths)
}

func TestSynthesize_NoRolesNonLeader(t *testing.T) {
	// 空角色集合（如 SUPPLIER 门户账号按 role_permission 为空处理）→ 非职能、无组织 → SELF
	got := Synthesize(nil, nil)
	require.Equal(t, ScopeSELF, got.Scope)
}

func TestSynthesize_NoRolesButLeaderOfNodes(t *testing.T) {
	// 无角色但却是节点负责人 → 可得域 DEPT_SUB，声明域按 SELF 起算 → SELF
	got := Synthesize(nil, []string{"/1/"})
	require.Equal(t, ScopeSELF, got.Scope)
}

func TestSynthesize_MixedRolesFunctionalWins(t *testing.T) {
	// 多个角色中有一个职能角色 → ALL（即使其他角色声明更小）
	roles := []RoleDecl{
		{Code: "SALES", IsFunctional: false, DataScope: ScopeSELF},
		{Code: "AUDIT_READONLY", IsFunctional: true, DataScope: ScopeALL},
	}
	got := Synthesize(roles, nil)
	require.Equal(t, ScopeALL, got.Scope)
}

func TestSynthesize_MixedRolesTakeMaxDeclared(t *testing.T) {
	// 多非职能角色：声明域取最大（DEPT_SUB），负责人 → DEPT_SUB
	roles := []RoleDecl{
		{Code: "A", IsFunctional: false, DataScope: ScopeSELF},
		{Code: "B", IsFunctional: false, DataScope: ScopeDEPTSUB},
	}
	got := Synthesize(roles, []string{"/3/4/"})
	require.Equal(t, ScopeDEPTSUB, got.Scope)
}
