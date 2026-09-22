package cost

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBackoff_Series(t *testing.T) {
	// 6b-3 退避常量：1min / 5min（1min × 5^(n-1)），第 3 次起由 MarkFailed 置 DEAD
	require.Equal(t, time.Minute, Backoff(1))
	require.Equal(t, 5*time.Minute, Backoff(2))
	require.Equal(t, 25*time.Minute, Backoff(3))
}

func TestBackoff_Guard(t *testing.T) {
	// 防御：0/负数按首次失败 1min 计
	require.Equal(t, time.Minute, Backoff(0))
	require.Equal(t, time.Minute, Backoff(-3))
}

func TestMaxRetry_LockedAt3(t *testing.T) {
	// 锁死 MaxRetry=3（retry_count>=3 置 DEAD）——改动必须同时改 6b-3 E2E 断言
	require.Equal(t, 3, MaxRetry)
}
