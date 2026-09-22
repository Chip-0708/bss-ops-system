// Package api 中本文件实现认证接口：登录 / 登出（三门户共用，portal 由路由前缀决定）。
package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"model_bss/internal/domain/auth"
	"model_bss/pkg/apperr"
	"model_bss/pkg/response"
)

// LoginHandler 返回 POST /api/{portal}/auth/login 的处理函数。
// portal 来自路由前缀（internal/supplier/customer），不从请求体读取。
//
// TODO(后续阶段)：手机号 OTP 与微信授权登录（H5/小程序）未实现。
// 接入点：
//   - OTP：新增 POST /api/{portal}/auth/otp/send 与 /auth/otp/login，
//     校验走 pkg/cache（验证码 TTL 5min），复用本文件的 role_snapshot 装配与建会话逻辑；
//   - 微信：POST /api/{portal}/auth/wx/login 收 code，换 openid 后按 account.wx_openid 定位账号。
//
// @Summary 登录（三门户共用，portal 由路由前缀决定）
// @Description 账号密码登录。成功返回 token（明文仅在响应中出现一次，服务端只存 sha256）与权限包快照 role_snapshot。连续失败 5 次锁定 15 分钟（429）；主体冻结返回 423。
// @Tags 认证
// @Accept json
// @Produce json
// @Param body body auth.LoginRequest true "登录参数"
// @Success 200 {object} api.APIResponse "code=0；data={token, snapshot}"
// @Failure 400 {object} api.APIResponse "code=10001 参数校验失败"
// @Failure 401 {object} api.APIResponse "code=10002 账号或密码错误"
// @Failure 423 {object} api.APIResponse "code=10006 主体已冻结"
// @Failure 429 {object} api.APIResponse "code=10007 失败次数超限，锁定 15 分钟"
// @Router /api/internal/auth/login [post]
// @Router /api/supplier/auth/login [post]
// @Router /api/customer/auth/login [post]
func LoginHandler(svc *auth.Service, portal string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req auth.LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, apperr.ErrInvalidParams)
			return
		}
		if req.LoginID == "" || req.Password == "" {
			response.Error(c, apperr.ErrInvalidParams)
			return
		}

		res, err := svc.Login(c.Request.Context(), strings.ToUpper(portal), req.LoginID, req.Password, c.ClientIP())
		if err != nil {
			response.Error(c, auth.ToAppErr(err))
			return
		}
		response.Success(c, res)
	}
}

// LogoutHandler 返回 POST /api/{portal}/auth/logout 的处理函数。
//
// @Summary 登出（吊销当前会话）
// @Description 按 Authorization 头中的 Bearer token 吊销会话（revoked=true）。成功返回 204 无响应体。
// @Tags 认证
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 204 "会话已吊销"
// @Failure 401 {object} api.APIResponse "code=10002 未携带或非法 token"
// @Router /api/internal/auth/logout [post]
// @Router /api/supplier/auth/logout [post]
// @Router /api/customer/auth/logout [post]
func LogoutHandler(svc *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerFromHeader(c)
		if token == "" {
			response.Error(c, apperr.ErrUnauthorized)
			return
		}
		if err := svc.Logout(c.Request.Context(), token); err != nil {
			response.Error(c, auth.ToAppErr(err))
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// bearerFromHeader 提取 Authorization: Bearer <token>。
// TODO：与 internal/api/middleware 的解析逻辑重复，待中间件包稳定后合并为一处。
func bearerFromHeader(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || h[:len(prefix)] != prefix {
		return ""
	}
	return h[len(prefix):]
}
