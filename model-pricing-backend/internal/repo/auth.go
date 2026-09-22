// Package repo 提供数据访问实现。本文件实现鉴权相关的仓储：
// auth.Store（登录/权限包装配）与 auth.SessionReader（中间件校验会话）。
//
// 仅覆盖鉴权所需的最小列集；业务表的完整模型由各功能模块在后续阶段补充。
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"model_bss/internal/domain/auth"
)

// ---------------- GORM 行模型（最小列集） ----------------

type accountRow struct {
	ID           int64 `gorm:"primaryKey"`
	PortalType   string
	OwnerType    string
	OwnerID      int64
	LoginID      string
	PasswordHash string
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (accountRow) TableName() string { return "account" }

type staffRow struct {
	ID        int64 `gorm:"primaryKey"`
	OrgUnitID int64
	Name      string
}

func (staffRow) TableName() string { return "internal_staff" }

type orgUnitRow struct {
	ID            int64 `gorm:"primaryKey"`
	ParentID      *int64
	Path          string
	LeaderStaffID *int64
}

func (orgUnitRow) TableName() string { return "org_unit" }

type roleRow struct {
	ID           int64 `gorm:"primaryKey"`
	Code         string
	Name         string
	DataScope    string
	FieldMask    []byte
	IsFunctional bool
}

func (roleRow) TableName() string { return "role" }

type loginSessionRow struct {
	ID           int64 `gorm:"primaryKey"`
	AccountID    int64
	TokenHash    string
	PortalType   string
	ClientType   string
	OperatorType string
	OperatorID   int64
	RoleSnapshot []byte
	ExpiresAt    time.Time
	Revoked      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (loginSessionRow) TableName() string { return "login_session" }

// fieldMaskDoc 对应 role.field_mask 的 jsonb 结构：{"hide":["cost","margin"]}。
type fieldMaskDoc struct {
	Hide []string `json:"hide"`
}

// ---------------- AuthRepo ----------------

// AuthRepo 是鉴权相关查询的 GORM 实现。
type AuthRepo struct {
	db *gorm.DB
}

// NewAuthRepo 构造鉴权仓储。
func NewAuthRepo(db *gorm.DB) *AuthRepo { return &AuthRepo{db: db} }

// 编译期确认接口实现。
var (
	_ auth.Store         = (*AuthRepo)(nil)
	_ auth.SessionReader = (*AuthRepo)(nil)
)

// FindAccountByLogin 按 portal_type + login_id 定位账号，并附带主体冻结状态。
func (r *AuthRepo) FindAccountByLogin(ctx context.Context, portal, loginID string) (*auth.Account, error) {
	var row accountRow
	err := r.db.WithContext(ctx).
		Where("portal_type = ? AND login_id = ?", portal, loginID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("take account: %w", err)
	}

	acct := &auth.Account{
		ID:           row.ID,
		PortalType:   row.PortalType,
		OwnerType:    row.OwnerType,
		OwnerID:      row.OwnerID,
		LoginID:      row.LoginID,
		PasswordHash: row.PasswordHash,
		Status:       row.Status,
	}

	// 主体级状态：供应商看 qual_status，客户看 credit_status（§3.1 登录校验）
	switch portal {
	case "SUPPLIER":
		var p struct {
			QualStatus string
			Status     string
		}
		err := r.db.WithContext(ctx).
			Table("subject_operator AS o").
			Select("p.qual_status AS qual_status, p.status AS status").
			Joins("JOIN supplier_profile p ON p.subject_id = o.subject_id").
			Where("o.id = ?", row.OwnerID).
			Take(&p).Error
		if err == nil {
			acct.SubjectFlag = "QUAL"
			acct.SubjectStatus = p.QualStatus
			if p.Status != "" && p.Status != "ACTIVE" {
				acct.Status = p.Status // 档案停用等同账号停用
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("load supplier profile: %w", err)
		}
	case "CUSTOMER":
		var p struct {
			CreditStatus string
			Status       string
		}
		err := r.db.WithContext(ctx).
			Table("subject_operator AS o").
			Select("p.credit_status AS credit_status, p.status AS status").
			Joins("JOIN customer_profile p ON p.subject_id = o.subject_id").
			Where("o.id = ?", row.OwnerID).
			Take(&p).Error
		if err == nil {
			acct.SubjectFlag = "CREDIT"
			acct.SubjectStatus = p.CreditStatus
			if p.Status != "" && p.Status != "ACTIVE" {
				acct.Status = p.Status
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("load customer profile: %w", err)
		}
	}

	return acct, nil
}

// ListRolesByOperator 返回操作员持有的角色。
// 外部门户（SUPPLIER/CUSTOMER）不参与 M1–M12 权限点矩阵，返回空集合是预期行为。
func (r *AuthRepo) ListRolesByOperator(ctx context.Context, operatorType string, operatorID int64) ([]auth.RoleGranted, error) {
	if operatorType != "STAFF" {
		return nil, nil
	}

	var rows []roleRow
	err := r.db.WithContext(ctx).
		Table("role_grant AS g").
		Select("r.id, r.code, r.name, r.data_scope, r.field_mask, r.is_functional").
		Joins("JOIN role r ON r.id = g.role_id").
		Where("g.staff_id = ? AND (g.expires_at IS NULL OR g.expires_at > now())", operatorID).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}

	out := make([]auth.RoleGranted, 0, len(rows))
	for _, row := range rows {
		out = append(out, auth.RoleGranted{Role: auth.RoleRow{
			ID:           row.ID,
			Code:         row.Code,
			DataScope:    auth.DataScope(row.DataScope),
			FieldMask:    parseFieldMask(row.FieldMask),
			IsFunctional: row.IsFunctional,
		}})
	}
	return out, nil
}

// parseFieldMask 解析 role.field_mask 的 jsonb：{"hide":[...]}；非法/空返回 nil。
func parseFieldMask(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var doc fieldMaskDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	return doc.Hide
}

// ListPermPointsByRole 返回角色的全部权限点（形如 "M4:A"）。
func (r *AuthRepo) ListPermPointsByRole(ctx context.Context, roleID int64) ([]string, error) {
	var points []string
	err := r.db.WithContext(ctx).
		Table("role_permission AS rp").
		Select("p.module_code || ':' || p.action_code").
		Joins("JOIN permission_point p ON p.id = rp.permission_point_id").
		Where("rp.role_id = ?", roleID).
		Scan(&points).Error
	if err != nil {
		return nil, fmt.Errorf("list role perms: %w", err)
	}
	return points, nil
}

// ListOrgNodesByLeader 返回该员工作为负责人的全部组织节点。
func (r *AuthRepo) ListOrgNodesByLeader(ctx context.Context, staffID int64) ([]auth.OrgNode, error) {
	var rows []orgUnitRow
	err := r.db.WithContext(ctx).
		Where("leader_staff_id = ?", staffID).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list leader nodes: %w", err)
	}
	out := make([]auth.OrgNode, 0, len(rows))
	for _, row := range rows {
		out = append(out, auth.OrgNode{ID: row.ID, Path: row.Path})
	}
	return out, nil
}

// FindStaff 返回员工的组织归属。
func (r *AuthRepo) FindStaff(ctx context.Context, staffID int64) (*auth.StaffRow, error) {
	var row staffRow
	err := r.db.WithContext(ctx).Take(&row, staffID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("take staff: %w", err)
	}
	return &auth.StaffRow{ID: row.ID, OrgUnitID: row.OrgUnitID}, nil
}

// CreateSession 落库会话（role_snapshot 以 jsonb 存入）。
func (r *AuthRepo) CreateSession(ctx context.Context, s *auth.Session) error {
	raw, err := json.Marshal(s.RoleSnapshot)
	if err != nil {
		return fmt.Errorf("marshal role snapshot: %w", err)
	}
	row := loginSessionRow{
		AccountID:    s.AccountID,
		TokenHash:    s.TokenHash,
		PortalType:   s.PortalType,
		ClientType:   s.ClientType,
		OperatorType: s.OperatorType,
		OperatorID:   s.OperatorID,
		RoleSnapshot: raw,
		ExpiresAt:    s.ExpiresAt,
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("insert login_session: %w", err)
	}
	s.ID = row.ID
	return nil
}

// RevokeSession 按 token_hash 吊销会话。
func (r *AuthRepo) RevokeSession(ctx context.Context, tokenHash string) error {
	err := r.db.WithContext(ctx).
		Model(&loginSessionRow{}).
		Where("token_hash = ?", tokenHash).
		Update("revoked", true).Error
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// FindSessionByTokenHash 按 token_hash 取会话（鉴权中间件使用）。
func (r *AuthRepo) FindSessionByTokenHash(ctx context.Context, tokenHash string) (*auth.SessionView, error) {
	var row loginSessionRow
	err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("take session: %w", err)
	}

	var snap auth.Snapshot
	if len(row.RoleSnapshot) > 0 {
		if err := json.Unmarshal(row.RoleSnapshot, &snap); err != nil {
			return nil, fmt.Errorf("unmarshal role snapshot: %w", err)
		}
	}
	return &auth.SessionView{
		ID:        row.ID,
		Revoked:   row.Revoked,
		ExpiresAt: row.ExpiresAt,
		Snapshot:  &snap,
	}, nil
}

// RefreshSessionExpiry 滑动续期：把 expires_at 更新为新值。
func (r *AuthRepo) RefreshSessionExpiry(ctx context.Context, sessionID int64, newExpiresAt time.Time) error {
	err := r.db.WithContext(ctx).
		Model(&loginSessionRow{}).
		Where("id = ?", sessionID).
		Update("expires_at", newExpiresAt).Error
	if err != nil {
		return fmt.Errorf("refresh session expiry: %w", err)
	}
	return nil
}

// AllPoints 返回 permission_point 表中的全部权限点，供 perm.ValidateInDB 启动校验。
func (r *AuthRepo) AllPoints() ([]string, error) {
	var points []string
	err := r.db.WithContext(context.Background()).
		Table("permission_point").
		Select("module_code || ':' || action_code").
		Scan(&points).Error
	if err != nil {
		return nil, fmt.Errorf("list permission points: %w", err)
	}
	return points, nil
}
