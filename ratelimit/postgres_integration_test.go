package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/ratelimit"
	"github.com/go-tenon/tenon/ratelimit/pglimit"
)

func TestPostgres(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, pglimit.Migrations())
	t.Run("limiter", func(t *testing.T) {
		testLimiter(t, func(cfg ratelimit.Config) ratelimit.Limiter { return pglimit.New(db, testName(), cfg) })
	})

	t.Run("names are isolated", func(t *testing.T) {
		ctx := context.Background()
		cfg := ratelimit.Config{Rate: 1, Interval: time.Hour, Burst: 1}
		name := testName()
		login, reset := pglimit.New(db, name, cfg), pglimit.New(db, testName(), cfg)
		if err := login.Allow(ctx, "alice"); err != nil {
			t.Fatal(err)
		}
		if err := reset.Allow(ctx, "alice"); err != nil {
			t.Fatalf("names must not share quota: %v", err)
		}
		if err := pglimit.New(db, name, cfg).Allow(ctx, "alice"); err == nil {
			t.Fatal("same name must share quota across instances")
		}
	})

	t.Run("concurrent calls are exact", func(t *testing.T) {
		l := pglimit.New(db, testName(), ratelimit.Config{Rate: 1, Interval: time.Hour, Burst: 5})
		results := make(chan error, 20)
		for range cap(results) {
			go func() { results <- l.Allow(context.Background(), "k") }()
		}
		allowed := 0
		for range cap(results) {
			if <-results == nil {
				allowed++
			}
		}
		if allowed != 5 {
			t.Fatalf("allowed=%d, want 5", allowed)
		}
	})

	t.Run("cleanup removes refilled keys", func(t *testing.T) {
		ctx := context.Background()
		db := dbtest.Open(t, pglimit.Migrations())
		refilled := pglimit.New(db, "fast", ratelimit.Config{Rate: 1, Interval: time.Millisecond, Burst: 1})
		pending := pglimit.New(db, "slow", ratelimit.Config{Rate: 1, Interval: time.Hour, Burst: 3})
		for _, key := range []string{"a", "b", "c"} {
			if err := refilled.Allow(ctx, key); err != nil {
				t.Fatal(err)
			}
		}
		if err := pending.Allow(ctx, "a"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
		if n, err := pglimit.Cleanup(db)(ctx, 2); err != nil || n != 2 {
			t.Fatalf("first cleanup = %d, %v", n, err)
		}
		if n, err := refilled.Cleanup(ctx, 10); err != nil || n != 1 {
			t.Fatalf("second cleanup = %d, %v", n, err)
		}
		var left int
		if err := db.NewRaw(`SELECT count(*) FROM rate_limits`).Scan(ctx, &left); err != nil || left != 1 {
			t.Fatalf("rows left = %d, %v", left, err)
		}
		// 删除的只是回满的 key：没有回满的配额照常生效。
		for range 2 {
			if err := pending.Allow(ctx, "a"); err != nil {
				t.Fatal(err)
			}
		}
		if err := pending.Allow(ctx, "a"); err == nil {
			t.Fatal("quota of a pending key was lost")
		}
		if _, err := refilled.Cleanup(ctx, 0); err == nil {
			t.Fatal("cleanup with limit 0 succeeded")
		}
	})
}
