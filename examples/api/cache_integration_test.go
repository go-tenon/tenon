//go:build integration

package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ryan053857/mygoravel/cleanup"
	"github.com/ryan053857/mygoravel/database/dbtest"
	"github.com/ryan053857/mygoravel/examples/api/internal/order"
	"github.com/ryan053857/mygoravel/identity"
)

func TestNativeCacheAssemblyAndOwnership(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Fatal("REDIS_TEST_ADDR is required")
	}
	for _, driver := range []string{"none", "memory", "redis"} {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			var stack cleanup.Stack
			t.Cleanup(func() {
				if err := stack.Close(ctx); err != nil {
					t.Error(err)
				}
			})
			cfg := defaultConfig().Cache
			cfg.Driver = driver
			cfg.Addr = addr
			cfg.Namespace = "test-" + uuid.NewString()
			c, err := openCache(ctx, cfg, &stack)
			if err != nil {
				t.Fatal(err)
			}
			fsys, err := migrateFS()
			if err != nil {
				t.Fatal(err)
			}
			db := dbtest.Open(t, fsys)
			svc := order.New(db, order.Options{Cache: c, Namespace: cfg.Namespace, TTL: cfg.TTL})
			sub := identity.Subject{ID: "alice"}
			item, err := svc.Create(ctx, sub, order.CreateParams{Title: "cache test"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Get(ctx, sub, item.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Update(ctx, sub, item.ID, order.CreateParams{Title: "updated"}); err != nil {
				t.Fatal(err)
			}
			if o, err := svc.Get(ctx, sub, item.ID); err != nil || o.Title != "updated" {
				t.Fatal(o, err)
			}
			if c != nil {
				if err := c.Delete(ctx, cfg.Namespace+":order:v1:"+item.ID.String()); err != nil {
					t.Fatal(err)
				}
			}
			if err := stack.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if driver == "redis" {
				if _, err := c.Get(ctx, "closed"); err == nil {
					t.Fatal("client remained open")
				}
			}
		})
	}
}
func TestFailedCacheOpenCanBeCleaned(t *testing.T) {
	var stack cleanup.Stack
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := openCache(ctx, cacheConfig{Driver: "redis", Addr: "127.0.0.1:0"}, &stack)
	if err == nil {
		t.Fatal("unreachable cache accepted")
	}
	if err := stack.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
