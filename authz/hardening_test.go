package authz_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/identity"
)

// fakeRechecker 记录 authz 自动发出的凭据复查事件。
type fakeRechecker struct {
	mu          sync.Mutex
	credentials []string
	subjects    []identity.Ref
	tenants     []string
}

// RecheckCredential 记录凭据级复查。
func (f *fakeRechecker) RecheckCredential(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.credentials = append(f.credentials, id)
	return nil
}

// RecheckSubject 记录主体级复查。
func (f *fakeRechecker) RecheckSubject(_ context.Context, ref identity.Ref) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subjects = append(f.subjects, ref)
	return nil
}

// RecheckTenant 记录租户级复查。
func (f *fakeRechecker) RecheckTenant(_ context.Context, tenant string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tenants = append(f.tenants, tenant)
	return nil
}

func (f *fakeRechecker) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.credentials = nil
	f.subjects = nil
	f.tenants = nil
}

func TestWithDBAndCredentials(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, authz.Migrations())
	rec := &fakeRechecker{}
	catalog := authz.Catalog{
		{Name: "articles:read"},
		{Name: "articles:write"},
		{Name: "members:manage"},
	}
	store := authz.NewStore(db, catalog, authz.Credentials(rec))
	ctx := t.Context()

	r1, err := store.CreateRole(ctx, authz.Role{Tenant: "t1", Name: "editor", Permissions: []string{"articles:read"}})
	if err != nil {
		t.Fatal(err)
	}
	ref := identity.Ref{Type: "member", ID: "m1"}

	// 回滚的事务不触发复查。
	_ = database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := store.WithDB(tx).Assign(ctx, ref, "t1", r1.ID); err != nil {
			t.Fatal(err)
		}
		return context.Canceled
	})
	if len(rec.subjects) != 0 {
		t.Fatalf("expected no recheck on rollback, got %+v", rec.subjects)
	}

	// 提交的事务自动触发主体复查。
	if err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
		return store.WithDB(tx).Assign(ctx, ref, "t1", r1.ID)
	}); err != nil {
		t.Fatal(err)
	}
	if len(rec.subjects) != 1 || rec.subjects[0] != ref {
		t.Fatalf("subjects = %+v, want [%v]", rec.subjects, ref)
	}

	// 更新角色触发租户复查。
	rec.reset()
	if _, err := store.UpdateRole(ctx, authz.Role{ID: r1.ID, Name: "editor", Permissions: []string{"articles:read", "articles:write"}}, "t1"); err != nil {
		t.Fatal(err)
	}
	if len(rec.tenants) != 1 || rec.tenants[0] != "t1" {
		t.Fatalf("tenants = %+v, want [t1]", rec.tenants)
	}

	// 传入裸 bun.Tx 时 WithDB 必须 panic。
	bunTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bunTx.Rollback() }()
	var panicked bool
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		_ = store.WithDB(bunTx)
	}()
	if !panicked {
		t.Fatal("expected panic when passing raw bun.Tx to WithDB with Credentials")
	}
}

func TestSetRolesConcurrentSingleRole(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, authz.Migrations())
	catalog := authz.Catalog{{Name: "a:read"}, {Name: "b:read"}, {Name: "c:read"}}
	store := authz.NewStore(db, catalog)
	ctx := t.Context()

	r1, err := store.CreateRole(ctx, authz.Role{Tenant: "t1", Name: "r1", Permissions: []string{"a:read"}})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := store.CreateRole(ctx, authz.Role{Tenant: "t1", Name: "r2", Permissions: []string{"b:read"}})
	if err != nil {
		t.Fatal(err)
	}
	r3, err := store.CreateRole(ctx, authz.Role{Tenant: "t1", Name: "r3", Permissions: []string{"c:read"}})
	if err != nil {
		t.Fatal(err)
	}

	ref := identity.Ref{Type: "member", ID: "m1"}
	if err := store.Assign(ctx, ref, "t1", r1.ID); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		target := r2.ID
		if i%2 == 1 {
			target = r3.ID
		}
		wg.Go(func() {
			if err := store.SetRoles(ctx, ref, "t1", target); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent SetRoles error: %v", err)
	}

	got, err := store.RolesOf(ctx, ref, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected strictly 1 role after concurrent SetRoles, got %d: %+v", len(got), got)
	}
}

func TestCheckGrant(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, authz.Migrations())
	catalog := authz.Catalog{
		{Name: "articles:read"},
		{Name: "articles:write"},
		{Name: "members:manage"},
	}
	store := authz.NewStore(db, catalog)

	limited := identity.Subject{Type: "member", ID: "m1", Permissions: []string{"members:manage", "articles:read"}}
	if err := store.CheckGrant(limited, []string{"articles:read"}); err != nil {
		t.Fatalf("subset grant rejected: %v", err)
	}
	if err := store.CheckGrant(limited, []string{"articles:write"}); !errors.Is(err, authz.ErrExceedsPermissions) {
		t.Fatalf("expected ErrExceedsPermissions, got %v", err)
	}
	if err := store.CheckGrant(limited, []string{"articles:*"}); !errors.Is(err, authz.ErrExceedsPermissions) {
		t.Fatalf("expected ErrExceedsPermissions for wildcard, got %v", err)
	}
	if err := store.CheckGrant(limited, []string{"unknown:perm"}); !errors.Is(err, authz.ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}

	sys := identity.Subject{Type: identity.System, ID: "system"}
	if err := store.CheckGrant(sys, []string{"*"}); err != nil {
		t.Fatalf("system actor should bypass CheckGrant, got %v", err)
	}
}

func TestTenantRoleCannotGrantPlatformPermissions(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, authz.Migrations())
	catalog := authz.Catalog{
		{Name: "articles:read", Domain: identity.DomainTenant},
		{Name: "platform:billing", Domain: identity.DomainPlatform},
	}
	store := authz.NewStore(db, catalog)
	ctx := t.Context()

	// 全局角色可以授予平台域权限或 *。
	if _, err := store.CreateRole(ctx, authz.Role{Name: "superadmin", Permissions: []string{"*"}}); err != nil {
		t.Fatalf("global role rejected: %v", err)
	}
	// 租户自定义角色不能授予平台域权限或覆盖平台域的通配符。
	for _, perms := range [][]string{{"platform:billing"}, {"platform:*"}, {"*"}} {
		if _, err := store.CreateRole(ctx, authz.Role{Tenant: "acme", Name: "bad", Permissions: perms}); !errors.Is(err, authz.ErrInvalid) {
			t.Fatalf("tenant role with %v: got %v, want ErrInvalid", perms, err)
		}
	}
	if _, err := store.CreateRole(ctx, authz.Role{Tenant: "acme", Name: "reader", Permissions: []string{"articles:read"}}); err != nil {
		t.Fatalf("valid tenant role rejected: %v", err)
	}
}
