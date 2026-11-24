package ratelimit

import (
	"context"
	"sync"
	"time"

	"github.com/go-tenon/tenon/errorx"
)

// defaultMaxMemoryKeys 是 Memory 默认保留的最大 key 数，防止随机 key 泛洪耗尽内存。
const defaultMaxMemoryKeys = 65536

// Memory 在进程内存中计数，多实例部署时每个实例分别计数。
//
// 已回满的 key 与从未出现过的 key 等价，Memory 在调用 Allow 时顺带删除它们，
// 并在活跃 key 数达到上限时主动淘汰已回满或最早恢复的 key，确保内存有界。
type Memory struct {
	gcra

	mu      sync.Mutex
	tats    map[string]time.Time // 每个 key 的理论到达时间
	swept   time.Time
	maxKeys int
}

// NewMemory 创建进程内限流器，cfg 不合法时 panic。
func NewMemory(cfg Config) *Memory {
	return &Memory{
		gcra:    newGCRA("NewMemory", cfg),
		tats:    map[string]time.Time{},
		swept:   time.Now(),
		maxKeys: defaultMaxMemoryKeys,
	}
}

// Allow 实现 Limiter，在内存中完成，总是成功。
func (m *Memory) Allow(_ context.Context, key string) error {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweep(now)
	tat, exists := m.tats[key]
	next, wait := m.take(tat, now)
	if wait > 0 {
		return ErrExceeded.New(errorx.RetryAfter(wait))
	}
	if !exists && m.maxKeys > 0 && len(m.tats) >= m.maxKeys {
		m.evict(now)
	}
	m.tats[key] = next
	return nil
}

// Reset 实现 Limiter，总是成功。
func (m *Memory) Reset(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tats, key)
	return nil
}

// sweep 每隔一个 Burst 周期删除已回满的 key，摊销后每次调用为 O(1)。
func (m *Memory) sweep(now time.Time) {
	if now.Sub(m.swept) < m.ahead+m.step {
		return
	}
	for key, tat := range m.tats {
		if !tat.After(now) {
			delete(m.tats, key)
		}
	}
	m.swept = now
}

// evict 在活跃 key 达到上限时先清理已回满的 key，仍满时淘汰一批最早恢复的 key。
func (m *Memory) evict(now time.Time) {
	for key, tat := range m.tats {
		if !tat.After(now) {
			delete(m.tats, key)
		}
	}
	m.swept = now
	if len(m.tats) < m.maxKeys {
		return
	}
	drop := max(m.maxKeys/10, 1)
	for key := range m.tats {
		delete(m.tats, key)
		drop--
		if drop == 0 {
			break
		}
	}
}
