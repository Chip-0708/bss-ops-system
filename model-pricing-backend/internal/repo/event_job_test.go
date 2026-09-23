// Package repo 的 event_job_test.go：11b event_outbox 推送的单测。
package repo

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEventBackoff_Series(t *testing.T) {
	require.Equal(t, time.Minute, EventBackoff(1))
	require.Equal(t, 5*time.Minute, EventBackoff(2))
	require.Equal(t, 25*time.Minute, EventBackoff(3))
	require.Equal(t, 125*time.Minute, EventBackoff(4))
}

func TestEventBackoff_Guard(t *testing.T) {
	require.Equal(t, time.Minute, EventBackoff(0))
	require.Equal(t, time.Minute, EventBackoff(-3))
}

func TestEventMaxRetry_LockedAt5(t *testing.T) {
	// 锁死 EventMaxRetry=5（retry_count>=5 置 DEAD）——改动必须同时改 11b E2E 断言
	require.Equal(t, 5, EventMaxRetry)
}
