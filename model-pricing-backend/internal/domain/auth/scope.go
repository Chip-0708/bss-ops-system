// Package auth 实现登录、会话、权限包装配与数据域合成。
// 数据域合成规则严格按设计文档 §3.2：
//  1. 持有任一 is_functional=true 的角色 → 数据域 = ALL，不受组织位置限制；
//  2. 否则：可得域 =（是该用户负责的节点 ? DEPT_SUB : SELF），
//     实际域 = min(角色声明域, 可得域)，序 SELF < DEPT < DEPT_SUB < ALL；
//  3. scope_paths = 该用户作为负责人的所有 org_unit.path（可能多个）。
package auth

import (
	"sort"
)

// DataScope 表示数据权域，序：SELF < DEPT < DEPT_SUB < ALL。
type DataScope string

// 数据域枚举值，严格对应设计文档 §3.2。
const (
	ScopeSELF    DataScope = "SELF"
	ScopeDEPT    DataScope = "DEPT"
	ScopeDEPTSUB DataScope = "DEPT_SUB"
	ScopeALL     DataScope = "ALL"
)

// scopeRank 返回数据域的强度排序，用于计算 min(声明域, 可得域)。
func scopeRank(s DataScope) int {
	switch s {
	case ScopeSELF:
		return 1
	case ScopeDEPT:
		return 2
	case ScopeDEPTSUB:
		return 3
	case ScopeALL:
		return 4
	default:
		return 0 // 未知域视为最低，后续 min 会取到更严格的另一个
	}
}

// RoleDecl 是角色声明的简化形态（登录装配时从 role 表读出）。
type RoleDecl struct {
	Code         string
	IsFunctional bool
	DataScope    DataScope
}

// SynthesizeResult 是数据域合成结果。
type SynthesizeResult struct {
	Scope DataScope
	Paths []string // scope_paths：用户作为负责人的全部 org_unit.path
}

// Synthesize 是数据域合成的纯函数，不依赖任何 DB。
//
// 参数：
//   - roles：用户持有的全部角色声明（可能为空——空角色集合按非职能处理）。
//   - leaderPaths：该用户作为负责人（org_unit.leader_staff_id = me）的全部节点 path，可能为空。
//
// 返回：合成后的实际数据域与 scope_paths。
func Synthesize(roles []RoleDecl, leaderPaths []string) SynthesizeResult {
	// 规则 1：任一职能角色 → ALL
	for _, r := range roles {
		if r.IsFunctional {
			return SynthesizeResult{Scope: ScopeALL, Paths: nil}
		}
	}

	// 可得域 = 是负责人 ? DEPT_SUB : SELF
	attainable := ScopeSELF
	if len(leaderPaths) > 0 {
		attainable = ScopeDEPTSUB
	}

	// 实际域 = min(所有角色声明域, 可得域)；无角色时按 SELF 起算
	declared := ScopeSELF
	for _, r := range roles {
		if scopeRank(r.DataScope) > scopeRank(declared) {
			declared = r.DataScope
		}
	}

	actual := declared
	if scopeRank(attainable) < scopeRank(actual) {
		actual = attainable
	}

	// scope_paths：仅当实际域需要展开到组织树（DEPT_SUB）时才有意义。
	// 为与登录装配时的快照语义一致，这里始终返回去重排序后的负责人路径，
	// 由调用方决定是否保留（合成后非 DEPT_SUB/ALL 时调用方置空亦可）。
	paths := dedupePaths(leaderPaths)

	return SynthesizeResult{Scope: actual, Paths: paths}
}

// dedupePaths 去重并排序 path，保证结果稳定（用于快照比较与单测）。
func dedupePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
