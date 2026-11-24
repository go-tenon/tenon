// Package scheduletest 提供内存中的 schedule.Store，供定时任务的单元测试使用。
package scheduletest

import (
	"context"
	"sync"
	"time"

	"github.com/go-tenon/tenon/schedule"
)

var _ schedule.Store = (*Store)(nil)

// entry 保存一个任务在内存中的触发水位与运行租约。
type entry struct {
	lastTick     time.Time
	runningOwner string
	runningUntil time.Time
}

// Store 是保存在进程内存中的 schedule.Store，线程安全。
type Store struct {
	mu      sync.Mutex
	entries map[string]*entry
}

// NewStore 创建空的内存状态存储。
func NewStore() *Store {
	return &Store{entries: map[string]*entry{}}
}

// Claim 实现 schedule.Store。
func (s *Store) Claim(_ context.Context, name string, tick time.Time, owner string, ttl time.Duration, allowOverlap bool) (schedule.ClaimResult, error) {
	now := time.Now().UTC()
	tick = tick.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[name]
	if e == nil {
		e = &entry{}
		s.entries[name] = e
	}
	if !e.lastTick.IsZero() && !e.lastTick.Before(tick) {
		return schedule.AlreadyClaimed, nil
	}
	e.lastTick = tick
	if allowOverlap {
		return schedule.Claimed, nil
	}
	if !e.runningUntil.IsZero() && e.runningUntil.After(now) {
		return schedule.Overlap, nil
	}
	e.runningOwner = owner
	e.runningUntil = now.Add(ttl)
	return schedule.Claimed, nil
}

// AcquireRunning 实现 schedule.Store。
func (s *Store) AcquireRunning(_ context.Context, name, owner string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[name]
	if e == nil {
		e = &entry{}
		s.entries[name] = e
	}
	if !e.runningUntil.IsZero() && e.runningUntil.After(now) {
		return false, nil
	}
	e.runningOwner = owner
	e.runningUntil = now.Add(ttl)
	return true, nil
}

// Refresh 实现 schedule.Store。
func (s *Store) Refresh(_ context.Context, name, owner string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[name]
	if e == nil || e.runningOwner != owner || !e.runningUntil.After(now) {
		return false, nil
	}
	e.runningUntil = now.Add(ttl)
	return true, nil
}

// Release 实现 schedule.Store。
func (s *Store) Release(_ context.Context, name, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[name]
	if e != nil && e.runningOwner == owner {
		e.runningOwner = ""
		e.runningUntil = time.Time{}
	}
	return nil
}

// States 实现 schedule.Store。
func (s *Store) States(_ context.Context) (map[string]schedule.TaskState, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]schedule.TaskState, len(s.entries))
	for name, e := range s.entries {
		out[name] = schedule.TaskState{
			LastTick: e.lastTick,
			Running:  !e.runningUntil.IsZero() && e.runningUntil.After(now),
		}
	}
	return out, nil
}
