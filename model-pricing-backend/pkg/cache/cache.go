// Package cache 提供进程内缓存门面抽象。默认实现为 sync.Map（无外部依赖），
// 后续接入 Redis 时实现同一接口；设计文档 §12 要求 Redis 可选，不可用时功能不降级。
package cache

import (
	"strconv"
	"sync"
	"time"
)

// Cache 是最小化的 KV 缓存接口：登录失败计数、短期令牌桶共用。
type Cache interface {
	// Get 返回键值；键不存在或已过期返回 ok=false。
	Get(key string) (value []byte, ok bool)
	// Set 写入键值并设置 TTL；ttl<=0 视为永久（不推荐，调用方应避免）。
	Set(key string, value []byte, ttl time.Duration)
	// Delete 显式删除某个键。
	Delete(key string)
}

type inMemory struct {
	mu   sync.RWMutex
	data map[string]item
}

type item struct {
	value []byte
	exp   time.Time
}

// New 返回进程内 Cache 实现，供 AuthService 登录失败计数等高频短 TTL 场景使用。
func New() Cache {
	return &inMemory{data: make(map[string]item)}
}

func (c *inMemory) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	it, ok := c.data[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if !it.exp.IsZero() && time.Now().After(it.exp) {
		c.Delete(key)
		return nil, false
	}
	return it.value, true
}

func (c *inMemory) Set(key string, value []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	c.data[key] = item{value: append([]byte(nil), value...), exp: exp}
}

func (c *inMemory) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
}

// IncrWithTTL 自增计数器，键不存在时初始化为 1 并写入 ttl，已存在时累计。
// 返回累计后的当前值。
func IncrWithTTL(c Cache, key string, ttl time.Duration) int {
	var cnt int
	if v, ok := c.Get(key); ok {
		if n, err := strconv.Atoi(string(v)); err == nil {
			cnt = n
		}
	}
	cnt++
	c.Set(key, []byte(strconv.Itoa(cnt)), ttl)
	return cnt
}
