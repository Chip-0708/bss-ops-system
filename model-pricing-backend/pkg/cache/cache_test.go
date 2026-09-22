package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoginFailureCounter_LockAfter5(t *testing.T) {
	c := New()
	key := "login_fail:internal:ops1"
	// 前 5 次失败计数递增
	for i := 1; i <= 5; i++ {
		got := IncrWithTTL(c, key, 15*time.Minute)
		require.Equal(t, i, got)
	}
	// 第 6 次被锁定
	got := IncrWithTTL(c, key, 15*time.Minute)
	require.Equal(t, 6, got, "6th attempt should exceed threshold")
	require.True(t, got > 5, "caller checks count > 5 to deny")
}

func TestLoginFailureCounter_IsolatedPerLoginID(t *testing.T) {
	c := New()
	IncrWithTTL(c, "login_fail:internal:alice", 15*time.Minute)
	IncrWithTTL(c, "login_fail:internal:alice", 15*time.Minute)
	IncrWithTTL(c, "login_fail:internal:bob", 15*time.Minute)

	alice, _ := c.Get("login_fail:internal:alice")
	require.Equal(t, "2", string(alice))
	bob, _ := c.Get("login_fail:internal:bob")
	require.Equal(t, "1", string(bob))
}

func TestLoginFailureCounter_TTLExpiryResets(t *testing.T) {
	c := New()
	key := "login_fail:internal:carol"
	for i := 1; i <= 5; i++ {
		IncrWithTTL(c, key, 100*time.Millisecond)
	}
	got, ok := c.Get(key)
	require.True(t, ok, "counter present before expiry")
	require.Equal(t, "5", string(got))

	// 等 TTL 过去后计数自动清空
	time.Sleep(300 * time.Millisecond)
	_, ok = c.Get(key)
	require.False(t, ok, "counter should expire after TTL")
	require.Equal(t, 1, IncrWithTTL(c, key, 15*time.Minute), "counter resets to 1 after expiry")
}

func TestCache_SetGetDelete(t *testing.T) {
	c := New()
	c.Set("k", []byte("v"), time.Minute)
	got, ok := c.Get("k")
	require.True(t, ok)
	require.Equal(t, "v", string(got))

	c.Delete("k")
	_, ok = c.Get("k")
	require.False(t, ok)
}
