// Package middleware（internal/api/middleware）提供鉴权三件套：AuthN / AuthZ / DataScope。
//
// 注册顺序（gin r.Use 顺序即执行顺序）必须为：
//
//	Recovery → RequestID → AccessLog → AuthN → AuthZ → DataScope → handler
//
// AuthZ 依赖 AuthN 注入的会话快照，顺序颠倒会拿到 nil。
//
// 包注释命名说明：internal/infra/middleware 下是通用中间件（request_id、访问日志），
// 本包专注登录态 / 功能权限 / 数据域注入，消费 domain/auth 的数据结构。
package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"model_bss/internal/domain/auth"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// ctxKeySnapshot 等注入 gin.Context 的键名。
const (
	ctxKeySnapshot = "auth.snapshot"
	ctxKeyPerms    = "auth.perms"
	ctxKeyOperator = "auth.operator" // *Operator 指针
)

// SessionStore 会话读取接口，直接复用 domain/auth.SessionReader。
type SessionStore = auth.SessionReader

// SessionWithSnapshot 会话 + 权限包快照的读取结果，直接复用 domain/auth.SessionView。
type SessionWithSnapshot = auth.SessionView

// Operator 是经过鉴权后注入请求上下文的操作员信息（DataScope 中间件与 repo 层 GORM Scope 使用）。
type Operator struct {
	PortalType   string
	OperatorType string // STAFF / SUPPLIER / CUSTOMER
	OperatorID   int64
	AccountID    int64
	StaffID      int64
	MyOrgID      int64
	DataScope    auth.DataScope
	ScopePaths   []string
	FieldMask    []string // 响应剔除并集（DTO 序列前 fieldmask.Apply 用）
	Perms        []string
	Roles        []string // 角色 code 列表（审批 required_role 匹配用）
}

// AuthN 校验 Authorization: Bearer <token>：
//  1. sha256(token) 查会话；2. revoked=false 且未过期；3. 快照解码后注入 gin.Context；
//  4. 剩余有效期 < 2h 时才滑动续期（不是每请求都写库）。
func AuthN(reader auth.SessionReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c)
		if !ok {
			abortError(c, apperr.ErrUnauthorized)
			return
		}

		sess, err := reader.FindSessionByTokenHash(c.Request.Context(), tokenHash(token))
		if err != nil {
			abortError(c, apperr.ErrUnauthorized)
			return
		}
		if sess == nil || sess.Revoked || time.Now().After(sess.ExpiresAt) {
			abortError(c, apperr.ErrUnauthorized)
			return
		}

		// 滑动续期：仅当剩余 < 2h
		remaining := time.Until(sess.ExpiresAt)
		if remaining < 2*time.Hour {
			newExp := time.Now().Add(auth.SessionTTLDefault) // 重置为完整 12h 会话窗口
			_ = reader.RefreshSessionExpiry(c.Request.Context(), sess.ID, newExp)
		}

		snap := sess.Snapshot
		if snap == nil {
			abortError(c, apperr.ErrUnauthorized)
			return
		}

		op := &Operator{
			PortalType:   snap.PortalType,
			OperatorType: snap.OperatorType,
			OperatorID:   snap.OperatorID,
			AccountID:    snap.AccountID,
			StaffID:      snap.StaffID,
			MyOrgID:      snap.MyOrgID,
			DataScope:    snap.DataScope,
			ScopePaths:   snap.ScopePaths,
			FieldMask:    snap.FieldMask(),
			Perms:        snap.Perms,
			Roles:        roleCodesOf(snap),
		}
		c.Set(ctxKeySnapshot, snap)
		c.Set(ctxKeyPerms, snap.Perms)
		c.Set(ctxKeyOperator, op)
		c.Next()
	}
}

// RequirePerm 返回 AuthZ 中间件：会话快照 perms 缺失指定权限点 → 403。
// 使用方法：rg.Use(middleware.RequirePerm(perm.M4Approve))
func RequirePerm(required string) gin.HandlerFunc {
	return func(c *gin.Context) {
		perms, ok := c.Get(ctxKeyPerms)
		if !ok {
			abortError(c, apperr.ErrUnauthorized)
			return
		}
		list, ok := perms.([]string)
		if !ok {
			abortError(c, apperr.ErrForbidden)
			return
		}
		if !contains(list, required) {
			abortError(c, apperr.ErrForbidden)
			return
		}
		c.Next()
	}
}

func contains(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

// DataScope 把操作员上下文（data_scope / scope_paths / operator_type / operator_id）
// 放进 gin.Context，供 repo 层 GORM Scope 统一取用。
// 本阶段不实现具体业务表过滤；取值函数 OperatorFrom 是本阶段预留的统一出口。
func DataScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 公开路由（login 等）不经过 AuthN；DataScope 仅对已鉴权请求有意义，
		// 未鉴权时不做拒绝，保持中间件可复用在公开组之后。
		c.Next()
	}
}

// OperatorFrom 从 gin.Context 取操作员信息；未鉴权时返回 nil。
// repo 层 GORM Scope 的统一取值入口：op := middleware.OperatorFrom(c)
func OperatorFrom(c *gin.Context) *Operator {
	v, ok := c.Get(ctxKeyOperator)
	if !ok {
		return nil
	}
	op, ok := v.(*Operator)
	if !ok {
		return nil
	}
	return op
}

// SnapshotFrom 返回会话快照（调试/审计用）。
func SnapshotFrom(c *gin.Context) *auth.Snapshot {
	v, ok := c.Get(ctxKeySnapshot)
	if !ok {
		return nil
	}
	snap, ok := v.(*auth.Snapshot)
	if !ok {
		return nil
	}
	return snap
}

// abortError 用统一响应包中断请求（不直接写 status，交由 response.Error 处理）。
func abortError(c *gin.Context, e apperr.Error) {
	response.Error(c, e)
	c.Abort()
}

// bearerToken 从 Authorization 头提取 Bearer token。
func bearerToken(c *gin.Context) (string, bool) {
	h := c.GetHeader("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	if tok == "" {
		return "", false
	}
	return tok, true
}

// tokenHash 计算 token 的 sha256 hex（与 domain/auth 中登录写入侧一致）。
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// roleCodesOf 从会话快照提取角色 code 列表（审批 required_role 匹配用）。
func roleCodesOf(snap *auth.Snapshot) []string {
	codes := make([]string, 0, len(snap.Roles))
	for _, r := range snap.Roles {
		codes = append(codes, r.Code)
	}
	return codes
}
