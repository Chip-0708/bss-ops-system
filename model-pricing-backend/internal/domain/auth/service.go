// Package auth 的 service.go：登录、登出与权限包装配。
// 设计文档 §3.1：登录一次装配权限包并快照进 login_session.role_snapshot；
// token 用 crypto/rand 32 字节生成 hex，明文只在登录响应出现一次，库中只存 sha256。
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// FailLimiter 抽象登录失败计数（pkg/cache 实现）。
type FailLimiter interface {
	// Incr 对 key 自增并设置 TTL，返回当前累计值。
	Incr(key string, ttl time.Duration) int
	// Get 读取当前计数（用于超限预检，不自增）。
	Get(key string) int
	// Reset 清除某个 key 的计数（登录成功后调用）。
	Reset(key string)
}

// TimeFunc 可注入时钟，便于单测锁定/过期逻辑（默认 time.Now）。
type TimeFunc func() time.Time

// SessionTTLDefault 会话默认有效期（12h），供中间件滑动续期引用。
const SessionTTLDefault = 12 * time.Hour

// Service 是登录/登出的领域服务。
type Service struct {
	store Store
	limit FailLimiter
	now   TimeFunc
	cfg   Config
}

// Config 控制登录安全参数。
type Config struct {
	// MaxLoginFail 同一 login_id 连续失败多少次后锁定（默认 5）。
	MaxLoginFail int
	// LockTTL 锁定窗口（默认 15 分钟）。
	LockTTL time.Duration
	// SessionTTL 会话有效期（默认 12 小时）。
	SessionTTL time.Duration
	// MaxIPFail 同一 IP 15 分钟内失败多少次后拦截（默认 50）。
	MaxIPFail int
}

// NewService 构造登录服务。
func NewService(store Store, limit FailLimiter, cfg Config) *Service {
	if cfg.MaxLoginFail <= 0 {
		cfg.MaxLoginFail = 5
	}
	if cfg.LockTTL <= 0 {
		cfg.LockTTL = 15 * time.Minute
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 12 * time.Hour
	}
	if cfg.MaxIPFail <= 0 {
		cfg.MaxIPFail = 50
	}
	return &Service{store: store, limit: limit, now: time.Now, cfg: cfg}
}

// Login 执行账号密码登录：
//  1. IP 维度软限流 + login_id 锁定预检（超阈值 → ErrLoginLocked）
//  2. 定位账号；账号不存在**不做失败计数**（防止随机 login_id 打爆缓存）
//  3. 主体冻结/停用检查 → ErrAccountFrozen / ErrAccountInactive
//  4. bcrypt 比对失败 → 计数
//  5. 成功 → 清零计数 → 装配权限包 → 建会话 → 返回 token + 快照
func (s *Service) Login(ctx context.Context, portal, loginID, password, clientIP string) (*LoginResult, error) {
	failKey := fmt.Sprintf("login_fail:%s:%s", portal, loginID)
	ipKey := fmt.Sprintf("login_fail_ip:%s:%s", portal, clientIP)

	// 锁定预检（不自增）：同一 login_id 连续失败 >= 阈值（默认 5）即锁定，
	// 即第 5 次失败之后的下一次尝试（第 6 次）会被拦截。
	if s.limit.Get(failKey) >= s.cfg.MaxLoginFail || s.limit.Get(ipKey) >= s.cfg.MaxIPFail {
		return nil, ErrLoginLocked
	}

	acct, err := s.store.FindAccountByLogin(ctx, portal, loginID)
	switch {
	case err != nil:
		return nil, fmt.Errorf("find account: %w", err)
	case acct == nil:
		// 账号不存在：不计 login_id 维度失败（防止随机 login_id 打爆缓存），
		// 但计入 IP 维度（同一 IP 撞号行为需要被拦截）。
		if clientIP != "" {
			s.limit.Incr(ipKey, s.cfg.LockTTL)
		}
		return nil, ErrBadCredentials // 不泄露账号是否存在
	}

	// 主体状态：账号停用/资质冻结/信用冻结 → 423
	if acct.IsSubjectFrozen() {
		return nil, ErrAccountFrozen
	}
	if acct.Status != "ACTIVE" {
		return nil, ErrAccountInactive
	}

	// bcrypt 比对
	if err := bcrypt.CompareHashAndPassword([]byte(acct.PasswordHash), []byte(password)); err != nil {
		s.limit.Incr(failKey, s.cfg.LockTTL)
		if clientIP != "" {
			s.limit.Incr(ipKey, s.cfg.LockTTL)
		}
		return nil, ErrBadCredentials
	}

	// 登录成功：清失败计数
	s.limit.Reset(failKey)
	if clientIP != "" {
		s.limit.Reset(ipKey)
	}

	// 装配权限包
	snap, err := s.buildSnapshot(ctx, acct)
	if err != nil {
		return nil, err
	}

	// 生成 token：明文只在本次响应出现，库中存 sha256
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	hash := tokenHash(token)

	sess := &Session{
		AccountID:    acct.ID,
		TokenHash:    hash,
		PortalType:   portal,
		ClientType:   "WEB", // 本期仅 WEB；H5/MP/OPEN 留接入点
		OperatorType: operatorTypeOf(acct),
		OperatorID:   operatorIDOf(acct),
		RoleSnapshot: *snap,
		ExpiresAt:    s.now().UTC().Add(s.cfg.SessionTTL),
	}
	if err := s.store.CreateSession(ctx, sess); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	return &LoginResult{Token: token, Snapshot: *snap}, nil
}

// Logout 按 token 明文吊销会话。
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.RevokeSession(ctx, tokenHash(token))
}

// buildSnapshot 装配 role_snapshot：
// perms = 所有角色 role_permission 并集；field_mask = 所有角色 field_mask 并集；
// data_scope/scope_paths 由 Synthesize 纯函数合成。
func (s *Service) buildSnapshot(ctx context.Context, acct *Account) (*Snapshot, error) {
	operatorType := operatorTypeOf(acct)
	operatorID := operatorIDOf(acct)

	grants, err := s.store.ListRolesByOperator(ctx, operatorType, operatorID)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}

	snap := &Snapshot{
		AccountID:    acct.ID,
		PortalType:   acct.PortalType,
		OperatorType: operatorType,
		OperatorID:   operatorID,
		Roles:        []RoleSnap{},
		Perms:        []string{},
		DataScope:    ScopeSELF,
		ScopePaths:   nil,
	}

	roleDecls := make([]RoleDecl, 0, len(grants))
	fieldSet := map[string]struct{}{}
	for _, g := range grants {
		role := g.Role
		roleDecls = append(roleDecls, RoleDecl{
			Code:         role.Code,
			IsFunctional: role.IsFunctional,
			DataScope:    role.DataScope,
		})
		snap.Roles = append(snap.Roles, RoleSnap{
			Code:         role.Code,
			IsFunctional: role.IsFunctional,
			DataScope:    role.DataScope,
			FieldMask:    role.FieldMask,
		})
		for _, f := range role.FieldMask {
			fieldSet[f] = struct{}{}
		}

		perms, err := s.store.ListPermPointsByRole(ctx, role.ID)
		if err != nil {
			return nil, fmt.Errorf("list perms of role %s: %w", role.Code, err)
		}
		snap.Perms = append(snap.Perms, perms...)
	}

	// staff 组织信息（operator_type=STAFF 时）
	if operatorType == "STAFF" {
		staff, err := s.store.FindStaff(ctx, operatorID)
		if err != nil {
			return nil, fmt.Errorf("find staff: %w", err)
		}
		if staff != nil {
			snap.StaffID = staff.ID
			snap.MyOrgID = staff.OrgUnitID
		}
	}

	// 数据域合成
	leaderPaths := []string{}
	if operatorType == "STAFF" {
		nodes, err := s.store.ListOrgNodesByLeader(ctx, operatorID)
		if err != nil {
			return nil, fmt.Errorf("list leader nodes: %w", err)
		}
		for _, n := range nodes {
			leaderPaths = append(leaderPaths, n.Path)
		}
	}

	res := Synthesize(roleDecls, leaderPaths)
	snap.DataScope = res.Scope
	snap.ScopePaths = res.Paths

	// perms 去重（并集语义）
	snap.Perms = dedupe(snap.Perms)
	return snap, nil
}

// FieldMask 提取快照中所有角色 field_mask 的并集（不含快照 JSON 结构字段）。
func (s *Snapshot) FieldMask() []string {
	set := map[string]struct{}{}
	for _, r := range s.Roles {
		for _, f := range r.FieldMask {
			set[f] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	return out
}

func operatorTypeOf(a *Account) string {
	switch a.PortalType {
	case "INTERNAL":
		return "STAFF"
	default:
		return a.PortalType // SUPPLIER / CUSTOMER
	}
}

func operatorIDOf(a *Account) int64 {
	// INTERNAL: owner_id = internal_staff.id；门户：owner_id = subject_operator.id
	// 但 account.owner_type 已表达归属类型，owner_id 直接作为 operator_id。
	return a.OwnerID
}

// randomToken 生成 32 字节随机 hex（64 字符）。
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// tokenHash 计算 token 的 sha256 hex。
func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// dedupe 稳定去重字符串切片。
func dedupe(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
