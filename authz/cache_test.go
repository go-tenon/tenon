package authz_test

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/identity"
)

// openRedis 连接 REDIS_TEST_URL 指向的 Redis。
func openRedis(t *testing.T) *redis.Client {
	t.Helper()
	raw := os.Getenv("REDIS_TEST_URL")
	if raw == "" {
		t.Fatal("REDIS_TEST_URL is required for this test")
	}
	opts, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatalf("REDIS_TEST_URL: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

// cached 返回使用 rdb 和随机命名空间缓存的选项。
func cached(t *testing.T, rdb *redis.Client) authz.Option {
	return authz.Cache(rdb, "test:"+rand.Text()+":", time.Minute)
}

func newCachedStore(t *testing.T) *authz.Store {
	t.Helper()
	return authz.NewStore(dbtest.Open(t, authz.Migrations()), catalog, cached(t, openRedis(t)))
}

func perms(t *testing.T, s *authz.Store, ref identity.Ref, tenant string) string {
	t.Helper()
	p, member, err := s.Permissions(context.Background(), ref, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if !member {
		return "-"
	}
	return strings.Join(p, ",")
}

// 权限读取一次后来自缓存；同一个命名空间的另一个 Store（另一个实例）修改之后，缓存失效。
func TestCacheSharedAcrossInstances(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, rdb := dbtest.Open(t, authz.Migrations()), openRedis(t)
	opt := cached(t, rdb)
	a, b := authz.NewStore(db, catalog, opt), authz.NewStore(db, catalog, opt)
	reader := mustRole(t, a, authz.Role{Name: "reader", Permissions: []string{"articles:read"}})
	bob := user("bob")
	mustAssign(t, a, bob, "acme", reader.ID)
	if got := perms(t, a, bob, "acme"); got != "articles:read" {
		t.Fatalf("perms=%s", got)
	}

	// 绕过 Store 修改表：缓存仍然返回旧的权限，说明读取没有访问数据库。
	if _, err := db.NewDelete().TableExpr("authz_assignments").Where("subject_id = ?", "bob").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got := perms(t, a, bob, "acme"); got != "articles:read" {
		t.Fatalf("not cached: perms=%s", got)
	}

	// 另一个 Store 修改分配，a 读到新的权限。
	mustAssign(t, b, bob, "acme", reader.ID)
	if _, err := b.UpdateRole(ctx, authz.Role{ID: reader.ID, Name: "reader", Permissions: []string{"articles:read", "articles:write"}}, ""); err != nil {
		t.Fatal(err)
	}
	if got := perms(t, a, bob, "acme"); got != "articles:read,articles:write" {
		t.Fatalf("after update role: perms=%s", got)
	}

	// 不同命名空间的缓存相互独立。
	other := authz.NewStore(db, catalog, cached(t, rdb))
	if got := perms(t, other, bob, "acme"); got != "articles:read,articles:write" {
		t.Fatalf("other namespace: perms=%s", got)
	}
}

// Store 建在 database.Tx 上时，缓存在事务提交之后失效；回滚时不变。
func TestCacheInTransaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := dbtest.Open(t, authz.Migrations())
	opt := cached(t, openRedis(t))
	s := authz.NewStore(db, catalog, opt)
	reader := mustRole(t, s, authz.Role{Name: "reader", Permissions: []string{"articles:read"}})
	bob := user("bob")
	if got := perms(t, s, bob, "acme"); got != "-" {
		t.Fatalf("perms=%s", got)
	}

	rollback := errors.New("rollback")
	err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := authz.NewStore(tx, catalog, opt).Assign(ctx, bob, "acme", reader.ID); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if got := perms(t, s, bob, "acme"); got != "-" {
		t.Fatalf("after rollback: perms=%s", got)
	}

	err = database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := authz.NewStore(tx, catalog, opt).Assign(ctx, bob, "acme", reader.ID); err != nil {
			return err
		}
		// 提交之前读取，缓存的是提交之前的权限。
		if got := perms(t, s, bob, "acme"); got != "-" {
			t.Errorf("before commit: perms=%s", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := perms(t, s, bob, "acme"); got != "articles:read" {
		t.Fatalf("after commit: perms=%s", got)
	}
}

// 与缓存一起使用的事务必须能确定何时提交。
func TestCacheRejectsPlainTx(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, authz.Migrations())
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	opt := cached(t, openRedis(t))
	for name, conn := range map[string]bun.IDB{"tx": tx, "*tx": &tx} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: expected panic", name)
				}
			}()
			authz.NewStore(conn, catalog, opt)
		}()
	}
	authz.NewStore(tx, catalog) // 不缓存时可以
}

// Redis 不可用时直接查询数据库，修改照常成功。
func TestCacheDown(t *testing.T) {
	t.Parallel()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	s := authz.NewStore(dbtest.Open(t, authz.Migrations()), catalog, cached(t, rdb))
	reader := mustRole(t, s, authz.Role{Name: "reader", Permissions: []string{"articles:read"}})
	mustAssign(t, s, user("bob"), "acme", reader.ID)
	if got := perms(t, s, user("bob"), "acme"); got != "articles:read" {
		t.Fatalf("perms=%s", got)
	}
	if err := s.Unassign(context.Background(), user("bob"), "acme", reader.ID); err != nil {
		t.Fatal(err)
	}
	if got := perms(t, s, user("bob"), "acme"); got != "-" {
		t.Fatalf("after unassign: perms=%s", got)
	}
}

// Erase 撤销主体在所有域中的角色；在 database.Tx 中时，缓存在提交之后失效。
func TestEraseInvalidatesAfterCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := dbtest.Open(t, authz.Migrations())
	s := authz.NewStore(db, catalog, cached(t, openRedis(t)))
	reader := mustRole(t, s, authz.Role{Name: "reader", Permissions: []string{"articles:read"}})
	bob, alice := user("bob"), user("alice")
	mustAssign(t, s, bob, "acme", reader.ID)
	mustAssign(t, s, bob, "globex", reader.ID)
	mustAssign(t, s, alice, "acme", reader.ID)
	for _, tenant := range []string{"acme", "globex"} {
		if got := perms(t, s, bob, tenant); got != "articles:read" {
			t.Fatalf("%s: perms=%s", tenant, got)
		}
	}

	err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := s.Erase(ctx, tx, bob); err != nil {
			return err
		}
		// 提交之前读取，缓存的仍然是提交之前的权限。
		if got := perms(t, s, bob, "acme"); got != "articles:read" {
			t.Errorf("before commit: perms=%s", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []string{"acme", "globex"} {
		if got := perms(t, s, bob, tenant); got != "-" {
			t.Fatalf("after commit %s: perms=%s", tenant, got)
		}
	}
	if got := perms(t, s, alice, "acme"); got != "articles:read" {
		t.Fatalf("alice: perms=%s", got)
	}

	// 不在事务中时立即失效。
	mustAssign(t, s, alice, "globex", reader.ID)
	if got := perms(t, s, alice, "globex"); got != "articles:read" {
		t.Fatalf("alice globex: perms=%s", got)
	}
	if err := s.Erase(ctx, db, alice); err != nil {
		t.Fatal(err)
	}
	if got := perms(t, s, alice, "acme"); got != "-" {
		t.Fatalf("alice after erase: perms=%s", got)
	}

	// 有缓存时，不能确定何时提交的事务被拒绝。
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.Erase(ctx, tx, bob); err == nil {
		t.Fatal("expected error for plain bun.Tx")
	}
	if err := s.Erase(ctx, db, identity.Ref{}); err == nil {
		t.Fatal("expected error for empty subject")
	}
}
