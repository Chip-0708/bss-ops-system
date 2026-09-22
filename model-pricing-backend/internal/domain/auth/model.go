// Package auth 中的 model.go：登录 / 会话 / 权限包的领域结构定义。
package auth

import "time"

// Account 是 account 表的最小查询模型（登录校验用）。
type Account struct {
	ID           int64
	PortalType   string // INTERNAL/SUPPLIER/CUSTOMER
	OwnerType    string // STAFF/OPERATOR
	OwnerID      int64
	LoginID      string
	PasswordHash string
	Status       string // ACTIVE/SUSPENDED
	// 主体级状态（登录时一次读出，用于 423 判定）：
	// supplier: qual_status/status；customer: credit_status/status；internal: 无
	SubjectStatus string // NORMAL/WARNING/FROZEN/...
	SubjectFlag   string // QUAL / CREDIT / ACCOUNT / ""（无）
	LastLoginAt   *time.Time
}

// IsSubjectFrozen 判定主体是否冻结：账号停用、供应商资质冻结、客户信用冻结。
// 返回 true 时登录接口应返回 423 ErrSubjectFrozen。
func (a *Account) IsSubjectFrozen() bool {
	if a.Status == "SUSPENDED" {
		return true
	}
	switch a.SubjectFlag {
	case "QUAL", "CREDIT":
		return a.SubjectStatus == "FROZEN"
	}
	return false
}

// RoleRow 是 role 表的最小模型（权限包装配用）。
type RoleRow struct {
	ID           int64
	Code         string
	DataScope    DataScope
	FieldMask    []string // field_mask 的 hide 列表展开后的 jsonb
	IsFunctional bool
}

// RoleGranted 表示一个用户被授予的角色（含角色详情）。
type RoleGranted struct {
	Role RoleRow
}

// OrgNode 是 org_unit 表的最小模型（负责人路径计算用）。
type OrgNode struct {
	ID   int64
	Path string
}

// StaffRow 是 internal_staff 的最小模型。
type StaffRow struct {
	ID        int64
	OrgUnitID int64
}

// Snapshot 是 role_snapshot 权限包快照（login_session.role_snapshot jsonb）。
// JSON 字段名与设计文档 §3 权限包结构完全一致。
type Snapshot struct {
	AccountID    int64      `json:"account_id"`
	PortalType   string     `json:"portal_type"`
	OperatorType string     `json:"operator_type"` // STAFF / SUPPLIER / CUSTOMER
	OperatorID   int64      `json:"operator_id"`
	StaffID      int64      `json:"staff_id,omitempty"`  // operator_type=STAFF 时
	MyOrgID      int64      `json:"my_org_id,omitempty"` // operator_type=STAFF 时
	Roles        []RoleSnap `json:"roles"`
	Perms        []string   `json:"perms"`
	DataScope    DataScope  `json:"data_scope"`
	ScopePaths   []string   `json:"scope_paths"`
}

// RoleSnap 是快照内的单个角色信息。
type RoleSnap struct {
	Code         string    `json:"code"`
	IsFunctional bool      `json:"is_functional"`
	DataScope    DataScope `json:"data_scope"`
	FieldMask    []string  `json:"field_mask,omitempty"`
}

// Session 是 login_session 表的最小模型（写入用）。
type Session struct {
	ID           int64
	AccountID    int64
	TokenHash    string
	PortalType   string
	ClientType   string
	OperatorType string
	OperatorID   int64
	RoleSnapshot Snapshot
	ExpiresAt    time.Time
	Revoked      bool
}

// LoginRequest 是 POST /api/{portal}/auth/login 的请求体。
type LoginRequest struct {
	LoginID  string `json:"login_id"`
	Password string `json:"password"`
}

// LoginResult 是登录接口的成功载荷（token 明文只在此出现一次）。
type LoginResult struct {
	Token    string   `json:"token"`
	Snapshot Snapshot `json:"snapshot"`
}
