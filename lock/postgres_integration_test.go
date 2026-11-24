package lock_test

import (
	"context"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/lock/locktest"
	"github.com/go-tenon/tenon/lock/pglock"
)

func TestPostgresStore(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, pglock.Migrations())
	// 每个子测试用自己的锁名前缀，共用一个数据库。
	n := 0
	locktest.TestStore(t, func() lock.Store {
		n++
		return prefixed{pglock.New(db), string(rune('a'+n)) + ":"}
	})
}

// 并发取得同一把锁时只有一方成功。
func TestPostgresStoreConcurrentAcquire(t *testing.T) {
	t.Parallel()
	store := pglock.New(dbtest.Open(t, pglock.Migrations()))
	results := make(chan bool, 20)
	for i := range cap(results) {
		go func() {
			ok, err := store.Acquire(context.Background(), "job", string(rune('a'+i)), time.Minute)
			if err != nil {
				t.Error(err)
			}
			results <- ok
		}()
	}
	won := 0
	for range cap(results) {
		if <-results {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d owners acquired the lock", won)
	}
}

// Refresh 把过期时间改为 ttl 之后；Cleanup 只删除过期的行。
func TestPostgresStoreRefreshAndCleanup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := dbtest.Open(t, pglock.Migrations())
	store := pglock.New(db)
	for _, name := range []string{"stale-1", "stale-2", "stale-3"} {
		if ok, err := store.Acquire(ctx, name, "o", time.Millisecond); !ok || err != nil {
			t.Fatalf("acquire %s: ok=%v err=%v", name, ok, err)
		}
	}
	if ok, err := store.Acquire(ctx, "live", "o", time.Minute); !ok || err != nil {
		t.Fatalf("acquire live: ok=%v err=%v", ok, err)
	}
	if ok, err := store.Refresh(ctx, "live", "o", 30*time.Second); !ok || err != nil {
		t.Fatalf("refresh: ok=%v err=%v", ok, err)
	}
	var left time.Duration
	if err := db.NewRaw(`SELECT (extract(epoch FROM expires_at - now()) * 1e9)::bigint FROM locks WHERE name = 'live'`).Scan(ctx, &left); err != nil {
		t.Fatal(err)
	}
	if left <= 0 || left > 30*time.Second {
		t.Fatalf("expires in %v after refresh", left)
	}
	time.Sleep(10 * time.Millisecond)

	if n, err := pglock.Cleanup(db)(ctx, 2); err != nil || n != 2 {
		t.Fatalf("first cleanup = %d, %v", n, err)
	}
	if n, err := store.Cleanup(ctx, 10); err != nil || n != 1 {
		t.Fatalf("second cleanup = %d, %v", n, err)
	}
	if names := lockNames(t, db); len(names) != 1 || names[0] != "live" {
		t.Fatalf("rows left = %v", names)
	}
	if _, err := store.Cleanup(ctx, 0); err == nil {
		t.Fatal("cleanup with limit 0 succeeded")
	}
}

func lockNames(t *testing.T, db *bun.DB) []string {
	t.Helper()
	var names []string
	if err := db.NewRaw(`SELECT name FROM locks ORDER BY name`).Scan(context.Background(), &names); err != nil {
		t.Fatal(err)
	}
	return names
}
