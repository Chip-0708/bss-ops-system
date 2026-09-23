// Package middleware 的 open_auth.go：11a 开放接口鉴权中间件。
//
// 与 AuthN 完全隔离：不查 login_session，不注入 Operator/Perms/Roles，
// 只校验 open_api_token 表（client_id + expires_at > now()）。
package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/gin-gonic/gin"

	"model_bss/pkg/apperr"
)

// OpenTokenStore 是开放接口 token 的读取接口（由 repo.OpenApiRepo 实现）。
type OpenTokenStore interface {
	LoadToken(ctx context.Context, tokenHash string) (string, bool, error)
}

// openTokenHash 计算 token 的 sha256 hex（与 domain/openapi 一致）。
func openTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// OpenAuthN 校验 Authorization: Bearer <open_api_token>：
//  1. sha256(token) 查 open_api_token；2. expires_at > now()；3. 注入 client_id。
func OpenAuthN(store OpenTokenStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c)
		if !ok {
			abortError(c, apperr.ErrUnauthorized)
			return
		}
		clientID, found, err := store.LoadToken(c.Request.Context(), openTokenHash(token))
		if err != nil || !found {
			abortError(c, apperr.ErrUnauthorized)
			return
		}
		c.Set("open.client_id", clientID)
		c.Next()
	}
}

// OpenClientID 从 gin.Context 取开放接口 client_id（调试/审计用）。
func OpenClientID(c *gin.Context) string {
	v, ok := c.Get("open.client_id")
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}
