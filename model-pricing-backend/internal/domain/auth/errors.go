// Package auth 的 errors.go：内部错误语义与 apperr 码的映射说明。
// 登录链路中 423 / 429 由 apperr.ErrSubjectFrozen / ErrTooManyRequests 表达；
// 账号不存在或密码错误统一按 ErrUnauthorized 返回（不泄露账号是否存在）。
package auth

import (
	"errors"

	"model_bss/pkg/apperr"
)

// 登录链路可判定的内部错误。
var (
	// ErrBadCredentials 账号不存在或密码不匹配。
	ErrBadCredentials = errors.New("bad credentials")
	// ErrLoginLocked 登录失败次数超限，账号暂时锁定。
	ErrLoginLocked = errors.New("login locked")
	// ErrAccountFrozen 主体冻结（资质/信用/停用）。
	ErrAccountFrozen = errors.New("account frozen")
	// ErrAccountInactive 账号非 ACTIVE 状态（非冻结的其他禁用态）。
	ErrAccountInactive = errors.New("account inactive")
)

// ToAppErr 把登录链路的内部错误映射为 HTTP 响应错误码。
func ToAppErr(err error) apperr.Error {
	switch {
	case errors.Is(err, ErrAccountFrozen):
		return apperr.ErrSubjectFrozen
	case errors.Is(err, ErrLoginLocked):
		return apperr.ErrTooManyRequests
	case errors.Is(err, ErrBadCredentials), errors.Is(err, ErrAccountInactive):
		return apperr.ErrUnauthorized
	default:
		return apperr.ErrSystem
	}
}
