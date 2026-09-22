// Package apperr 定义应用级业务错误，实现统一的 code -> HTTP status 映射。
//
// 约定：
//   - 0 表示成功，非 0 为业务错误。
//   - HTTP status 映射遵循设计文档 §8.0 通用约定：
//     400 参数/业务校验、401 未登录、403 无权限、409 幂等冲突或状态冲突、
//     423 主体冻结、429 限流、500 系统错误。
//   - 本文件目前只包含基础通用码，业务码会在后续功能模块中继续补充。
package apperr

import (
	"errors"
	"fmt"
)

// Error 是业务层统一错误实体，通过 New 构造函数生成，避免零值误用。
type Error struct {
	Code    int    // 业务错误码，0 表示成功
	Message string // 返回给前端/调用方的可读错误信息
	Status  int    // 对应的 HTTP status code
}

// New 返回一个新的业务错误实例。
func New(code int, message string, status int) Error {
	return Error{Code: code, Message: message, Status: status}
}

// Error 实现 error 接口，返回便于日志读取的格式化字符串。
func (e Error) Error() string {
	return fmt.Sprintf("code=%d, message=%s", e.Code, e.Message)
}

// 通用错误码与 HTTP status 的映射对齐设计文档 §8.0。
var (
	// Success 表示操作成功，响应载荷中的 code 固定为 0。
	Success = New(0, "ok", 200)

	// ErrSystem 系统错误，未捕获到的内部异常兜底。
	ErrSystem = New(10000, "系统错误", 500)

	// ErrInvalidParams 参数或业务规则校验失败。
	ErrInvalidParams = New(10001, "参数校验失败", 400)

	// ErrUnauthorized 未登录或凭证失效。
	ErrUnauthorized = New(10002, "未认证", 401)

	// ErrForbidden 已认证但无权限执行当前操作。
	ErrForbidden = New(10003, "无权限", 403)

	// ErrNotFound 请求的资源不存在。
	ErrNotFound = New(10004, "记录不存在", 404)

	// ErrIdempotency 幂等键冲突或状态冲突，防止重复提交。
	ErrIdempotency = New(10005, "幂等冲突", 409)

	// ErrSubjectFrozen 主体已冻结（供应商资质冻结/客户信用冻结/账号停用），§8.0 状态码 423。
	ErrSubjectFrozen = New(10006, "主体已冻结", 423)

	// ErrTooManyRequests 触发限流（如登录失败次数耗尽），§8.0 状态码 429。
	ErrTooManyRequests = New(10007, "请求过于频繁", 429)
)

var registry = map[int]Error{
	0:     Success,
	10000: ErrSystem,
	10001: ErrInvalidParams,
	10002: ErrUnauthorized,
	10003: ErrForbidden,
	10004: ErrNotFound,
	10005: ErrIdempotency,
	10006: ErrSubjectFrozen,
	10007: ErrTooManyRequests,
}

// FromCode 根据业务码返回对应的 Error 实例；若码未注册则回退到 ErrSystem。
func FromCode(code int) Error {
	if err, ok := registry[code]; ok {
		return err
	}
	return ErrSystem
}

// Is 支持使用 errors.Is 做业务错误判定。
func (e Error) Is(target error) bool {
	if target == nil {
		return false
	}
	var t Error
	if errors.As(target, &t) {
		return e.Code == t.Code
	}
	return false
}
