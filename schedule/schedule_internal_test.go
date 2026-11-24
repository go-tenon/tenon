package schedule

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cockroachdb/errors"
)

// stubStore 是包内测试使用的轻量内存 Store，支持模拟租约丢失。
type stubStore struct {
	mu       sync.Mutex
	loseNext bool
	entries  map[string]*stubEntry
}

type stubEntry struct {
	lastTick     time.Time
	runningOwner string
	runningUntil time.Time
}

func newStubStore() *stubStore { return &stubStore{entries: map[string]*stubEntry{}} }

func (s *stubStore) Claim(_ context.Context, name string, tick time.Time, owner string, ttl time.Duration, allowOverlap bool) (ClaimResult, error) {
	now := time.Now().UTC()
	tick = tick.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[name]
	if e == nil {
		e = &stubEntry{}
		s.entries[name] = e
	}
	if !e.lastTick.IsZero() && !e.lastTick.Before(tick) {
		return AlreadyClaimed, nil
	}
	e.lastTick = tick
	if allowOverlap {
		return Claimed, nil
	}
	if !e.runningUntil.IsZero() && e.runningUntil.After(now) {
		return Overlap, nil
	}
	e.runningOwner = owner
	e.runningUntil = now.Add(ttl)
	return Claimed, nil
}

func (s *stubStore) AcquireRunning(_ context.Context, name, owner string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[name]
	if e == nil {
		e = &stubEntry{}
		s.entries[name] = e
	}
	if !e.runningUntil.IsZero() && e.runningUntil.After(now) {
		return false, nil
	}
	e.runningOwner = owner
	e.runningUntil = now.Add(ttl)
	return true, nil
}

func (s *stubStore) Refresh(_ context.Context, name, owner string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loseNext {
		return false, nil
	}
	e := s.entries[name]
	now := time.Now().UTC()
	if e == nil || e.runningOwner != owner || !e.runningUntil.After(now) {
		return false, nil
	}
	e.runningUntil = now.Add(ttl)
	return true, nil
}

func (s *stubStore) Release(_ context.Context, name, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[name]
	if e != nil && e.runningOwner == owner {
		e.runningOwner = ""
		e.runningUntil = time.Time{}
	}
	return nil
}

func (s *stubStore) States(_ context.Context) (map[string]TaskState, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]TaskState, len(s.entries))
	for name, e := range s.entries {
		out[name] = TaskState{LastTick: e.lastTick, Running: !e.runningUntil.IsZero() && e.runningUntil.After(now)}
	}
	return out, nil
}

func TestRunWaitsThenCancelsTasks(t *testing.T) {
	for _, tc := range []struct {
		name    string
		work    time.Duration
		timeout time.Duration
		cause   error
	}{
		{"finishes", 300 * time.Millisecond, time.Second, nil},
		{"canceled after timeout", time.Hour, 200 * time.Millisecond, context.Canceled},
		{"canceled at once", time.Hour, 0, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := New(newStubStore(), Config{ShutdownTimeout: tc.timeout}, nil)
				started := make(chan struct{}, 1)
				result := make(chan error, 1)
				s.Add("slow", "@everysecond", func(ctx context.Context) error {
					select {
					case started <- struct{}{}:
					default:
						return nil
					}
					select {
					case <-time.After(tc.work):
						result <- ctx.Err()
					case <-ctx.Done():
						result <- ctx.Err()
					}
					return nil
				})
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() { done <- s.Run(ctx) }()
				<-started
				cancel()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-result:
					if err != tc.cause {
						t.Fatalf("task ctx err = %v, want %v", err, tc.cause)
					}
				default:
					t.Fatal("Run returned before the task finished")
				}
			})
		})
	}
}

// 租约续期返回 false（租约丢失）时，立即以 ErrLockLost 取消任务 ctx 并记为失败。
func TestLeaseLossCancelsTask(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newStubStore()
		store.loseNext = true
		s := New(store, DefaultConfig(), nil)
		var cause error
		s.Add("long", "@everysecond", func(ctx context.Context) error {
			<-ctx.Done()
			cause = context.Cause(ctx)
			return ctx.Err()
		})
		err := s.RunDue(context.Background(), time.Now().Truncate(time.Second))
		if !errors.Is(err, ErrLockLost) || !errors.Is(cause, ErrLockLost) {
			t.Fatalf("err=%v cause=%v, want ErrLockLost", err, cause)
		}
	})
}
