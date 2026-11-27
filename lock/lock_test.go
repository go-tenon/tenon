package lock_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/lock/locktest"
)

func TestAcquireValidation(t *testing.T) {
	locker := lock.New(locktest.NewStore())
	ctx := context.Background()
	if _, err := locker.Acquire(ctx, "", time.Second); err == nil {
		t.Fatal("expected error for empty name")
	}
	if _, err := locker.Acquire(ctx, "a", 0); err == nil {
		t.Fatal("expected error for non-positive ttl")
	}
}

func TestWaitHonorsContext(t *testing.T) {
	locker := lock.New(locktest.NewStore())
	if _, err := locker.Acquire(context.Background(), "a", time.Minute); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := locker.Wait(ctx, "a", time.Minute, time.Minute); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}

// testStore 是所有 Store 实现共同遵守的契约，集成测试用它验证 Database。

// hookStore 包装底层 lock.Store，用于测试注入续期丢失与临时故障场景。
type hookStore struct {
	lock.Store
	onRefresh func(ctx context.Context, name, owner string, ttl time.Duration) (bool, error)
}

func (s *hookStore) Refresh(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
	if s.onRefresh != nil {
		return s.onRefresh(ctx, name, owner, ttl)
	}
	return s.Store.Refresh(ctx, name, owner, ttl)
}

func TestHoldRenewsAndReleases(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		base := locktest.NewStore()
		var refreshes atomic.Int32
		store := &hookStore{
			Store: base,
			onRefresh: func(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
				refreshes.Add(1)
				return base.Refresh(ctx, name, owner, ttl)
			},
		}
		locker := lock.New(store)
		err := locker.Hold(t.Context(), "bot:1", 3*time.Second, func(ctx context.Context) error {
			// 运行超过 3 个 TTL，期间持续自动续期且其他调用方无法抢占。
			time.Sleep(10 * time.Second)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if _, err := locker.Acquire(ctx, "bot:1", time.Second); !errors.Is(err, lock.ErrNotAcquired) {
				t.Errorf("concurrent acquire err=%v, want ErrNotAcquired", err)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("hold err=%v", err)
		}
		if refreshes.Load() < 5 {
			t.Fatalf("refreshes=%d, want >= 5", refreshes.Load())
		}
		// fn 返回后锁已主动释放，新的持有者立即可以取得。
		lk, err := locker.Acquire(t.Context(), "bot:1", time.Second)
		if err != nil {
			t.Fatalf("re-acquire after hold err=%v", err)
		}
		_ = lk.Release(t.Context())
	})
}

func TestHoldNotAcquired(t *testing.T) {
	t.Parallel()
	locker := lock.New(locktest.NewStore())
	lk, err := locker.Acquire(t.Context(), "bot:busy", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lk.Release(t.Context()) }()

	called := false
	err = locker.Hold(t.Context(), "bot:busy", time.Minute, func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, lock.ErrNotAcquired) {
		t.Fatalf("err=%v, want ErrNotAcquired", err)
	}
	if called {
		t.Fatal("fn should not be called when lock is held")
	}
}

func TestHoldCancelsOnLostLease(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		base := locktest.NewStore()
		var calls atomic.Int32
		store := &hookStore{
			Store: base,
			onRefresh: func(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
				if calls.Add(1) >= 2 {
					return false, nil // 模拟锁已被他人抢占
				}
				return base.Refresh(ctx, name, owner, ttl)
			},
		}
		locker := lock.New(store)
		var cause error
		err := locker.Hold(t.Context(), "bot:lost", 3*time.Second, func(ctx context.Context) error {
			<-ctx.Done()
			cause = context.Cause(ctx)
			return ctx.Err()
		})
		if !errors.Is(err, lock.ErrNotHeld) {
			t.Fatalf("hold err=%v, want ErrNotHeld", err)
		}
		if !errors.Is(cause, lock.ErrNotHeld) {
			t.Fatalf("cancel cause=%v, want ErrNotHeld", cause)
		}
	})
}

func TestHoldRecoversFromTransientRefreshError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		base := locktest.NewStore()
		var calls atomic.Int32
		tempErr := errors.New("temporary db error")
		store := &hookStore{
			Store: base,
			onRefresh: func(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
				if calls.Add(1) <= 2 {
					return false, tempErr
				}
				return base.Refresh(ctx, name, owner, ttl)
			},
		}
		locker := lock.New(store)
		err := locker.Hold(t.Context(), "bot:retry", 3*time.Second, func(ctx context.Context) error {
			time.Sleep(5 * time.Second)
			return ctx.Err()
		})
		if err != nil {
			t.Fatalf("hold err=%v, want nil after transient recovery", err)
		}
	})
}

func TestHoldCancelsOnPersistentRefreshError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		base := locktest.NewStore()
		dbDown := errors.New("db down")
		store := &hookStore{
			Store: base,
			onRefresh: func(context.Context, string, string, time.Duration) (bool, error) {
				return false, dbDown
			},
		}
		locker := lock.New(store)
		var cause error
		err := locker.Hold(t.Context(), "bot:down", 3*time.Second, func(ctx context.Context) error {
			<-ctx.Done()
			cause = context.Cause(ctx)
			return ctx.Err()
		})
		if !errors.Is(err, lock.ErrNotHeld) || !errors.Is(err, dbDown) {
			t.Fatalf("hold err=%v, want ErrNotHeld + dbDown", err)
		}
		if !errors.Is(cause, lock.ErrNotHeld) {
			t.Fatalf("cancel cause=%v, want ErrNotHeld", cause)
		}
	})
}

func TestHoldReleasesOnParentCancel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		locker := lock.New(locktest.NewStore())
		parent, cancel := context.WithCancel(t.Context())
		go func() {
			time.Sleep(2 * time.Second)
			cancel()
		}()
		err := locker.Hold(parent, "bot:stop", 3*time.Second, func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("hold err=%v, want context.Canceled", err)
		}
		held, err := locker.Held(t.Context(), "bot:stop")
		if err != nil || held {
			t.Fatalf("held=%v err=%v, want false", held, err)
		}
	})
}
