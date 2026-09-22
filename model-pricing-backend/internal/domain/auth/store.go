// Package auth 的 store.go：登录/权限装配所需的数据访问抽象。
// 单测一律注入 stub，不连真实数据库。
package auth

import (
	"context"
	"time"
)

// Store 聚合登录与权限包装配所需的全部查询。
type Store interface {
	// FindAccountByLogin 按 portal + login_id 唯一定位账号（含主体冻结状态）。
	FindAccountByLogin(ctx context.Context, portal, loginID string) (*Account, error)

	// ListRolesByOwner 返回账号所属主体下授予的全部角色（按 role_grant 关联 role）。
	// owner: portal_type=INTERNAL → STAFF:staff_id；SUPPLIER/CUSTOMER → 由实现方按 operator 归属组装。
	ListRolesByOperator(ctx context.Context, operatorType string, operatorID int64) ([]RoleGranted, error)

	// ListPermPointsByRole 返回单个角色的全部权限点字符串。
	ListPermPointsByRole(ctx context.Context, roleID int64) ([]string, error)

	// ListOrgNodesByLeader 返回 leader_staff_id = staffID 的全部组织节点。
	ListOrgNodesByLeader(ctx context.Context, staffID int64) ([]OrgNode, error)

	// FindStaff 返回员工的组织归属。
	FindStaff(ctx context.Context, staffID int64) (*StaffRow, error)

	// CreateSession 落库新会话并返回会话 ID。
	CreateSession(ctx context.Context, s *Session) error

	// RevokeSession 按 token_hash 吊销会话（logout）。
	RevokeSession(ctx context.Context, tokenHash string) error
}

// SessionReader 是鉴权中间件（AuthN）依赖的会话读取接口。
// 与 Store 分离，便于中间件只依赖读取面，也便于单测注入桩实现。
type SessionReader interface {
	// FindSessionByTokenHash 按 token_hash 返回会话及其权限包快照；不存在返回 (nil, nil)。
	FindSessionByTokenHash(ctx context.Context, tokenHash string) (*SessionView, error)
	// RefreshSessionExpiry 把会话过期时间续到新值（滑动续期，仅剩余 < 2h 时调用）。
	RefreshSessionExpiry(ctx context.Context, sessionID int64, newExpiresAt time.Time) error
}

// SessionView 是会话读取结果。
type SessionView struct {
	ID        int64
	Revoked   bool
	ExpiresAt time.Time
	Snapshot  *Snapshot
}
