package apperr

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestErrorMapping 验证基础错误码与 HTTP status 的映射关系，对齐设计文档 §8.0.
func TestErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        Error
		wantCode   int
		wantStatus int
	}{
		{"success", Success, 0, http.StatusOK},
		{"system", ErrSystem, 10000, http.StatusInternalServerError},
		{"invalid params", ErrInvalidParams, 10001, http.StatusBadRequest},
		{"unauthorized", ErrUnauthorized, 10002, http.StatusUnauthorized},
		{"forbidden", ErrForbidden, 10003, http.StatusForbidden},
		{"not found", ErrNotFound, 10004, http.StatusNotFound},
		{"idempotency", ErrIdempotency, 10005, http.StatusConflict},
		{"subject frozen", ErrSubjectFrozen, 10006, 423},
		{"too many requests", ErrTooManyRequests, 10007, http.StatusTooManyRequests},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantCode, tt.err.Code)
			require.Equal(t, tt.wantStatus, tt.err.Status)
			require.False(t, tt.err.Message == "", "message should not be empty")
		})
	}
}

// TestFromCode 验证 FromCode 的注册表查找与未注册码的兜底行为。
func TestFromCode(t *testing.T) {
	require.Equal(t, ErrSystem, FromCode(9999), "unknown code should fall back to system error")
	require.Equal(t, ErrInvalidParams, FromCode(10001))
}

// TestIs 验证 errors.Is 可用于判定同一种业务错误。
func TestIs(t *testing.T) {
	target := New(10001, "参数校验失败", http.StatusBadRequest)
	other := New(10002, "未认证", http.StatusUnauthorized)

	require.True(t, errors.Is(ErrInvalidParams, target))
	require.False(t, errors.Is(ErrInvalidParams, other))
}
