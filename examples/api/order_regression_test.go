//go:build integration

package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	gocache_store "github.com/eko/gocache/store/go_cache/v4"
	"github.com/google/uuid"
	gocache "github.com/patrickmn/go-cache"

	"github.com/ryan053857/mygoravel/database/dbtest"
	"github.com/ryan053857/mygoravel/database/migrate"
	"github.com/ryan053857/mygoravel/examples/api/internal/order"
	"github.com/ryan053857/mygoravel/identity"
)

func TestServiceAuthorizationCacheAndCursor(t *testing.T) {
	fsys, _ := migrate.Sub(migrationsRoot, "migrations")
	db := dbtest.Open(t, fsys)
	ctx := context.Background()
	c := cache.New[string](gocache_store.NewGoCache(gocache.New(time.Minute, 0)))
	var logs bytes.Buffer
	svc := order.New(db, order.Options{Cache: c, Namespace: "testA", TTL: time.Minute, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	alice := identity.Subject{ID: "alice"}
	bob := identity.Subject{ID: "bob"}
	if _, err := svc.Create(ctx, identity.Subject{}, order.CreateParams{Title: "x"}); !errors.Is(err, order.ErrUnauthenticated) {
		t.Fatal(err)
	}
	if _, err := svc.List(ctx, identity.Subject{}, order.ListParams{}); !errors.Is(err, order.ErrUnauthenticated) {
		t.Fatal(err)
	}
	a, err := svc.Create(ctx, alice, order.CreateParams{Title: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Create(ctx, alice, order.CreateParams{Title: "b"})
	if err != nil {
		t.Fatal(err)
	}
	key := "testA:order:v1:" + a.ID.String()
	if _, err := svc.Get(ctx, alice, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, key); err != nil {
		t.Fatal("cache not populated", err)
	}
	if _, err := svc.Get(ctx, bob, a.ID); !errors.Is(err, order.ErrForbidden) {
		t.Fatal("cached ownership bypass", err)
	}
	if _, err := svc.Update(ctx, bob, a.ID, order.CreateParams{Title: "stolen"}); !errors.Is(err, order.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := svc.Update(ctx, alice, a.ID, order.CreateParams{Title: "b"}); !errors.Is(err, order.ErrConflict) {
		t.Fatal("conflict", err)
	}
	if raw, err := c.Get(ctx, key); err != nil || !strings.Contains(raw, `"Title":"a"`) {
		t.Fatal("rollback invalidated/modified cache", raw, err)
	}
	if _, err := svc.Update(ctx, alice, a.ID, order.CreateParams{Title: "updated"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, key); err == nil {
		t.Fatal("commit did not invalidate")
	}
	if v, err := svc.Get(ctx, alice, a.ID); err != nil || v.Title != "updated" {
		t.Fatal(v, err)
	}
	// Corrupt entries and backend errors have observable, distinct fallback paths.
	if err := c.Set(ctx, key, "not-json", store.WithExpiration(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if v, err := svc.Get(ctx, alice, a.ID); err != nil || v.Title != "updated" {
		t.Fatal(v, err)
	}
	if !strings.Contains(logs.String(), `"operation":"decode"`) {
		t.Fatal(logs.String())
	}
	other := order.New(db, order.Options{Cache: c, Namespace: "testB", TTL: time.Minute})
	if _, err := other.Get(ctx, alice, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, "testB:order:v1:"+a.ID.String()); err != nil {
		t.Fatal(err)
	}
	failed := order.New(db, order.Options{Cache: failingCache{c}, Namespace: "failed", TTL: time.Minute, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	if _, err := failed.Get(ctx, alice, a.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), `"operation":"get"`) || !strings.Contains(logs.String(), `"operation":"set"`) {
		t.Fatal(logs.String())
	}
	// Reverse timestamp order relative to UUID creation order.
	_, err = db.NewUpdate().Model((*order.Order)(nil)).Set("created_at = ?", time.Now().Add(time.Hour)).Where("id = ?", a.ID).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.List(ctx, alice, order.ListParams{Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != a.ID || first.Next == "" {
		t.Fatal(first, err)
	}
	second, err := svc.List(ctx, alice, order.ListParams{Limit: 1, Before: first.Next})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != b.ID {
		t.Fatal(second, err)
	}
	if _, err := svc.List(ctx, bob, order.ListParams{Before: first.Next}); !errors.Is(err, order.ErrInvalidCursor) {
		t.Fatal("cross-owner cursor", err)
	}
	if _, err := svc.List(ctx, alice, order.ListParams{Before: uuid.NewString()}); !errors.Is(err, order.ErrInvalidCursor) {
		t.Fatal(err)
	}
}

type failingCache struct{ cache.CacheInterface[string] }

func (failingCache) Get(context.Context, any) (string, error) {
	return "", errors.New("private backend failure")
}
func (failingCache) Set(context.Context, any, string, ...store.Option) error {
	return errors.New("private backend failure")
}
func TestHTTPUpdateAndConflict(t *testing.T) {
	api := setupAPI(t)
	code, body := do(t, api, "POST", "/orders", "alice", `{"title":"a"}`)
	if code != 200 {
		t.Fatal(code, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	code, body = do(t, api, "PATCH", "/orders/"+created.ID, "alice", `{"title":"updated"}`)
	if code != 200 || !strings.Contains(body, "updated") {
		t.Fatal(code, body)
	}
	code, body = do(t, api, "POST", "/orders", "alice", `{"title":"updated"}`)
	if code != 409 || !strings.Contains(body, "order.conflict") {
		t.Fatal(code, body)
	}
	code, body = do(t, api, "GET", "/orders?before=invalid", "alice", "")
	if code != 422 {
		t.Fatal(code, body)
	}
}
