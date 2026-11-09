//go:build integration

package cacheaside_test

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	gocache_store "github.com/eko/gocache/store/go_cache/v4"
	redis_store "github.com/eko/gocache/store/redis/v4"
	gocache "github.com/patrickmn/go-cache"
	"github.com/redis/go-redis/v9"
	"github.com/uptrace/bun"

	"github.com/ryan053857/mygoravel/cleanup"
	"github.com/ryan053857/mygoravel/database/dbtest"
	"github.com/ryan053857/mygoravel/database/migrate"
)

//go:embed migrations
var migrationsRoot embed.FS

type item struct {
	ID   string `bun:"id,pk"`
	Name string `bun:"name"`
}

func openDB(t *testing.T) *bun.DB {
	t.Helper()
	fsys, err := migrate.Sub(migrationsRoot, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	return dbtest.Open(t, fsys)
}

func key(ns, id string) string { return ns + ":item:" + id }

func isNotFound(err error) bool {
	return errors.As(err, new(store.NotFound)) || errors.Is(err, store.NotFound{})
}

func loadItem(ctx context.Context, db bun.IDB, c cache.CacheInterface[string], ns, id string) (string, error) {
	if v, err := c.Get(ctx, key(ns, id)); err == nil {
		return v, nil
	} else if !isNotFound(err) {
		return "", fmt.Errorf("cache get: %w", err)
	}
	var it item
	if err := db.NewSelect().Model(&it).Where("id = ?", id).Scan(ctx); err != nil {
		return "", err
	}
	_ = c.Set(ctx, key(ns, id), it.Name, store.WithExpiration(time.Minute))
	return it.Name, nil
}

func TestMemoryCacheAsideCommitInvalidate(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	client := gocache.New(5*time.Minute, 10*time.Minute)
	c := cache.New[string](gocache_store.NewGoCache(client))
	ns := "appA"

	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewInsert().Model(&item{ID: "1", Name: "alpha"}).Exec(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	name, err := loadItem(ctx, db, c, ns, "1")
	if err != nil || name != "alpha" {
		t.Fatalf("got %q err=%v", name, err)
	}
	if v, err := c.Get(ctx, key(ns, "1")); err != nil || v != "alpha" {
		t.Fatalf("cache %q err=%v", v, err)
	}

	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewUpdate().Model(&item{ID: "1", Name: "beta"}).Column("name").WherePK().Exec(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Delete(ctx, key(ns, "1"))
	name, err = loadItem(ctx, db, c, ns, "1")
	if err != nil || name != "beta" {
		t.Fatalf("after invalidate got %q err=%v", name, err)
	}
}

func TestKeyNamespaceIsolation(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	client := gocache.New(5*time.Minute, 10*time.Minute)
	c := cache.New[string](gocache_store.NewGoCache(client))
	_, _ = db.NewInsert().Model(&item{ID: "1", Name: "x"}).Exec(ctx)
	if _, err := loadItem(ctx, db, c, "appA", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, key("appB", "1")); !isNotFound(err) {
		t.Fatalf("appB should miss, err=%v", err)
	}
}

func TestRedisStoreAndCleanup(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Fatal("REDIS_TEST_ADDR is required for this test")
	}
	db := openDB(t)
	ctx := context.Background()

	rdb := redis.NewClient(&redis.Options{Addr: addr})
	var stack cleanup.Stack
	_ = stack.Add("redis", func(context.Context) error { return rdb.Close() })
	t.Cleanup(func() { _ = stack.Close(context.Background()) })

	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	c := cache.New[string](redis_store.NewRedis(rdb, store.WithExpiration(time.Minute)))
	_, _ = db.NewInsert().Model(&item{ID: "r1", Name: "redis-item"}).Exec(ctx)
	name, err := loadItem(ctx, db, c, "appR", "r1")
	if err != nil || name != "redis-item" {
		t.Fatalf("got %q err=%v", name, err)
	}
	_ = c.Delete(ctx, key("appR", "r1"))
	_, err = c.Get(ctx, key("appR", "r1"))
	if !isNotFound(err) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestWorksWithoutCache(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	if _, err := db.NewInsert().Model(&item{ID: "n1", Name: "plain"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var it item
	if err := db.NewSelect().Model(&it).Where("id = ?", "n1").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if it.Name != "plain" {
		t.Fatalf("got %#v", it)
	}
}
