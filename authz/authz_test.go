package authz_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
)

var catalog = authz.Catalog{
	{Name: "articles:read", Description: "查看文章"},
	{Name: "articles:write", Description: "编辑文章"},
	{Name: "comments:delete", Description: "删除评论"},
	{Name: "members:manage", Description: "管理成员"},
	{Name: "platform:tenants", Description: "管理租户"},
}

// Resolve 是一个 identity.Resolver，可以直接作为 rest.Options.Accounts 中一套账号的 Resolver。
var _ = rest.Options{Accounts: identity.Accounts{"user": (*authz.Store)(nil).Resolve}}

func newStore(t *testing.T) *authz.Store {
	t.Helper()
	return authz.NewStore(dbtest.Open(t, authz.Migrations()), catalog)
}

func mustRole(t *testing.T, s *authz.Store, r authz.Role) authz.Role {
	t.Helper()
	role, err := s.CreateRole(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return role
}

// user 返回测试中 user 账号体系的主体。
func user(id string) identity.Ref { return identity.Ref{Type: "user", ID: id} }

func mustAssign(t *testing.T, s *authz.Store, subject identity.Ref, tenant, roleID string) {
	t.Helper()
	if err := s.Assign(context.Background(), subject, tenant, roleID); err != nil {
		t.Fatal(err)
	}
}

func names(roles []authz.Role) string {
	var out []string
	for _, r := range roles {
		out = append(out, r.Tenant+"/"+r.Name)
	}
	return strings.Join(out, ",")
}

func TestNewStorePanics(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, authz.Migrations())
	for name, build := range map[string]func(){
		"nil db":            func() { authz.NewStore(nil, catalog) },
		"empty catalog":     func() { authz.NewStore(db, nil) },
		"wildcard catalog":  func() { authz.NewStore(db, authz.Catalog{{Name: "articles:*"}}) },
		"spaced permission": func() { authz.NewStore(db, authz.Catalog{{Name: "a b"}}) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: expected panic", name)
				}
			}()
			build()
		}()
	}
}

func TestRoles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	editor := mustRole(t, s, authz.Role{Name: "editor", Description: "编辑", Permissions: []string{"articles:*", "articles:read", "comments:delete"}})
	if editor.ID == "" || strings.Join(editor.Permissions, ",") != "articles:*,articles:read,comments:delete" {
		t.Fatalf("editor=%+v", editor)
	}
	reviewer := mustRole(t, s, authz.Role{Tenant: "acme", Name: "reviewer", Permissions: []string{"articles:read"}})
	mustRole(t, s, authz.Role{Tenant: "globex", Name: "editor"}) // 不同租户可以同名
	mustRole(t, s, authz.Role{Name: "owner", Permissions: []string{"*"}})

	for name, r := range map[string]authz.Role{
		"unknown permission":  {Name: "x", Permissions: []string{"articles:delete"}},
		"wildcard no match":   {Name: "x", Permissions: []string{"billing:*"}},
		"all tenants role":    {Tenant: authz.AllTenants, Name: "x"},
		"empty name":          {Name: ""},
		"spaced name":         {Name: "two words"},
		"spaced permission":   {Name: "x", Permissions: []string{"articles: read"}},
		"suffix-only pattern": {Name: "x", Permissions: []string{"articles*"}},
	} {
		if _, err := s.CreateRole(ctx, r); !errors.Is(err, authz.ErrInvalid) {
			t.Errorf("%s: err=%v", name, err)
		}
	}
	if _, err := s.CreateRole(ctx, authz.Role{Name: "editor"}); !errors.Is(err, authz.ErrRoleExists) {
		t.Fatalf("duplicate: %v", err)
	}

	if roles, err := s.Roles(ctx, "acme"); err != nil || names(roles) != "/editor,/owner,acme/reviewer" {
		t.Fatalf("acme roles=%s err=%v", names(roles), err)
	}
	if roles, err := s.Roles(ctx, ""); err != nil || names(roles) != "/editor,/owner" {
		t.Fatalf("global roles=%s err=%v", names(roles), err)
	}
	if _, err := s.Role(ctx, reviewer.ID, "globex"); !errors.Is(err, authz.ErrRoleNotFound) {
		t.Fatalf("other tenant's role visible: %v", err)
	}
	if got, err := s.Role(ctx, reviewer.ID, "acme"); err != nil || got.Name != "reviewer" || got.Permissions[0] != "articles:read" {
		t.Fatalf("role=%+v err=%v", got, err)
	}
	if _, err := s.Role(ctx, "not-a-uuid", ""); !errors.Is(err, authz.ErrRoleNotFound) {
		t.Fatalf("malformed id: %v", err)
	}

	// 修改名称和权限；租户保持不变。
	updated, err := s.UpdateRole(ctx, authz.Role{ID: reviewer.ID, Tenant: "", Name: "auditor", Permissions: []string{"comments:delete"}}, "")
	if err != nil || updated.Tenant != "acme" {
		t.Fatalf("update=%+v err=%v", updated, err)
	}
	if got, _ := s.Role(ctx, reviewer.ID, ""); got.Name != "auditor" || got.Tenant != "acme" || strings.Join(got.Permissions, ",") != "comments:delete" {
		t.Fatalf("after update=%+v", got)
	}
	if _, err := s.UpdateRole(ctx, authz.Role{ID: editor.ID, Name: "owner"}, ""); !errors.Is(err, authz.ErrRoleExists) {
		t.Fatalf("rename onto existing: %v", err)
	}
	if _, err := s.UpdateRole(ctx, authz.Role{ID: editor.ID, Name: "editor", Permissions: []string{"nope"}}, ""); !errors.Is(err, authz.ErrInvalid) {
		t.Fatalf("update with unknown permission: %v", err)
	}
	if _, err := s.UpdateRole(ctx, authz.Role{ID: "00000000-0000-0000-0000-000000000000", Name: "x"}, ""); !errors.Is(err, authz.ErrRoleNotFound) {
		t.Fatalf("update missing: %v", err)
	}

	// 租户管理员只能修改、删除本租户自定义的角色：全局角色和其他租户的角色视为不存在。
	for name, err := range map[string]error{
		"update global": func() error { _, err := s.UpdateRole(ctx, authz.Role{ID: editor.ID, Name: "x"}, "acme"); return err }(),
		"update other tenant": func() error {
			_, err := s.UpdateRole(ctx, authz.Role{ID: reviewer.ID, Name: "x"}, "globex")
			return err
		}(),
		"delete global":       s.DeleteRole(ctx, editor.ID, "acme"),
		"delete other tenant": s.DeleteRole(ctx, reviewer.ID, "globex"),
	} {
		if !errors.Is(err, authz.ErrRoleNotFound) {
			t.Errorf("%s: err=%v", name, err)
		}
	}
	if got, err := s.UpdateRole(ctx, authz.Role{ID: reviewer.ID, Name: "reviewer"}, "acme"); err != nil || got.Tenant != "acme" {
		t.Fatalf("update own role=%+v err=%v", got, err)
	}
	if roles, err := s.Roles(ctx, "acme"); err != nil || names(roles) != "/editor,/owner,acme/reviewer" {
		t.Fatalf("roles after scoped changes=%s err=%v", names(roles), err)
	}
}

// 并发创建同名角色时只有一个成功，其余返回 ErrRoleExists；并发分配同一个角色都成功。
func TestConcurrentWrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	errs := make([]error, 4)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { _, errs[i] = s.CreateRole(ctx, authz.Role{Name: "editor"}) })
	}
	wg.Wait()
	created := 0
	for _, err := range errs {
		switch {
		case err == nil:
			created++
		case !errors.Is(err, authz.ErrRoleExists):
			t.Fatalf("create: %v", err)
		}
	}
	if created != 1 {
		t.Fatalf("created %d roles", created)
	}

	role, err := s.CreateRole(ctx, authz.Role{Name: "reader", Permissions: []string{"articles:read"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := range errs {
		wg.Go(func() { errs[i] = s.Assign(ctx, user("alice"), "acme", role.ID) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	if roles, err := s.RolesOf(ctx, user("alice"), "acme"); err != nil || len(roles) != 1 {
		t.Fatalf("roles=%v err=%v", roles, err)
	}
}

// 不缓存和缓存的 Store 结果相同：每次修改之后的读取都得到新的权限。
func TestAssignAndResolve(t *testing.T) {
	t.Parallel()
	for name, open := range map[string]func(t *testing.T) *authz.Store{"database": newStore, "cached": newCachedStore} {
		t.Run(name, func(t *testing.T) { testAssignAndResolve(t, open(t)) })
	}
}

func testAssignAndResolve(t *testing.T, s *authz.Store) {
	ctx := context.Background()
	owner := mustRole(t, s, authz.Role{Name: "owner", Permissions: []string{"*"}})
	member := mustRole(t, s, authz.Role{Name: "member", Permissions: []string{"articles:read"}})
	moderator := mustRole(t, s, authz.Role{Tenant: "acme", Name: "moderator", Permissions: []string{"articles:read", "comments:delete"}})
	empty := mustRole(t, s, authz.Role{Tenant: "acme", Name: "guest"})
	support := mustRole(t, s, authz.Role{Name: "support", Permissions: []string{"articles:read"}})
	platform := mustRole(t, s, authz.Role{Name: "platform-admin", Permissions: []string{"platform:tenants"}})

	// staff 与 alice 的 ID 相同，属于另一套账号体系，角色互不相通。
	alice, bob, guest, staff := user("alice"), user("bob"), user("guest"), identity.Ref{Type: "staff", ID: "alice"}
	mustAssign(t, s, alice, "acme", owner.ID)
	mustAssign(t, s, bob, "acme", member.ID)
	mustAssign(t, s, bob, "acme", moderator.ID)
	mustAssign(t, s, bob, "acme", moderator.ID) // 重复分配保持原样
	mustAssign(t, s, bob, "globex", member.ID)
	mustAssign(t, s, guest, "acme", empty.ID)
	mustAssign(t, s, staff, authz.AllTenants, support.ID)
	mustAssign(t, s, staff, "", platform.ID)

	for name, call := range map[string]func() error{
		"custom role in other tenant": func() error { return s.Assign(ctx, bob, "globex", moderator.ID) },
		"custom role in all tenants":  func() error { return s.Assign(ctx, bob, authz.AllTenants, moderator.ID) },
		"custom role on platform":     func() error { return s.Assign(ctx, bob, "", moderator.ID) },
		"empty subject":               func() error { return s.Assign(ctx, identity.Ref{}, "acme", member.ID) },
		"subject without type":        func() error { return s.Assign(ctx, identity.Ref{ID: "bob"}, "acme", member.ID) },
	} {
		if err := call(); !errors.Is(err, authz.ErrInvalid) {
			t.Errorf("%s: err=%v", name, err)
		}
	}
	if err := s.Assign(ctx, bob, "acme", "00000000-0000-0000-0000-000000000000"); !errors.Is(err, authz.ErrRoleNotFound) {
		t.Fatalf("missing role: %v", err)
	}

	resolve := func(ref identity.Ref, tenant string) (identity.Subject, error) {
		return s.Resolve(identity.WithTenant(ctx, tenant), identity.Subject{Type: ref.Type, ID: ref.ID, Method: "bearer"})
	}
	for _, tc := range []struct {
		subject       identity.Ref
		tenant, perms string
	}{
		{alice, "acme", "*"},
		{bob, "acme", "articles:read,comments:delete"}, // 两个角色合并去重
		{bob, "globex", "articles:read"},
		{guest, "acme", ""},              // 有角色但没有权限，仍然是成员
		{staff, "acme", "articles:read"}, // 来自所有租户的分配，没有同 ID 的 alice 的 owner
		{staff, "", "platform:tenants"},  // 平台权限只来自平台的分配
		{bob, "", ""},                    // 平台中没有角色也可以访问，权限为空
		{alice, "", ""},                  // 没有同 ID 的 staff 的平台权限
	} {
		sub, err := resolve(tc.subject, tc.tenant)
		if err != nil || sub.Tenant != tc.tenant || strings.Join(sub.Permissions, ",") != tc.perms || sub.Method != "bearer" {
			t.Errorf("%v@%q: sub=%+v err=%v", tc.subject, tc.tenant, sub, err)
		}
	}
	if sub, _ := resolve(alice, "acme"); !sub.Can("members:manage") {
		t.Error("owner cannot manage members")
	}
	for _, tc := range []struct {
		subject identity.Ref
		tenant  string
	}{{user("carol"), "acme"}, {alice, "globex"}} {
		if _, err := resolve(tc.subject, tc.tenant); !errors.Is(err, authz.ErrNotMember) {
			t.Errorf("%v@%s: err=%v", tc.subject, tc.tenant, err)
		}
	}

	if tenants, err := s.Tenants(ctx, bob); err != nil || strings.Join(tenants, ",") != "acme,globex" {
		t.Fatalf("tenants=%v err=%v", tenants, err)
	}
	if tenants, _ := s.Tenants(ctx, staff); len(tenants) != 0 {
		t.Fatalf("staff tenants=%v", tenants)
	}
	members, err := s.Members(ctx, "acme")
	if err != nil || len(members) != 3 || members[0].Subject != alice || members[1].Subject != bob ||
		names(members[1].Roles) != "/member,acme/moderator" {
		t.Fatalf("members=%+v err=%v", members, err)
	}
	if roles, err := s.RolesOf(ctx, bob, "acme"); err != nil || names(roles) != "/member,acme/moderator" {
		t.Fatalf("roles of bob=%s err=%v", names(roles), err)
	}
	if roles, err := s.RolesOf(ctx, staff, ""); err != nil || names(roles) != "/platform-admin" {
		t.Fatalf("roles of staff=%s err=%v", names(roles), err)
	}

	if err := s.Unassign(ctx, bob, "acme", moderator.ID); err != nil {
		t.Fatal(err)
	}
	if sub, _ := resolve(bob, "acme"); slices.Contains(sub.Permissions, "comments:delete") {
		t.Fatalf("unassigned role still grants: %v", sub.Permissions)
	}
	if n, err := s.RemoveMember(ctx, bob, "acme"); n != 1 || err != nil {
		t.Fatalf("remove member n=%d err=%v", n, err)
	}
	if _, err := resolve(bob, "acme"); !errors.Is(err, authz.ErrNotMember) {
		t.Fatalf("removed member: %v", err)
	}

	// 删除角色同时删除它的分配。
	if err := s.DeleteRole(ctx, owner.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(alice, "acme"); !errors.Is(err, authz.ErrNotMember) {
		t.Fatalf("deleted role still assigned: %v", err)
	}
	if err := s.DeleteRole(ctx, owner.ID, ""); !errors.Is(err, authz.ErrRoleNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestCatalog(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	got := s.Catalog()
	got[0].Name = "changed"
	if s.Catalog()[0].Name != "articles:read" {
		t.Fatal("Catalog returned shared slice")
	}
}

// exported 把 Export 的结果编码为 JSON。
func exported(t *testing.T) func(v any, err error) string {
	return func(v any, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
}

func contains(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s does not contain %s", got, w)
		}
	}
}

func excludes(t *testing.T, got string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(got, w) {
			t.Errorf("%s contains %s", got, w)
		}
	}
}

func TestExport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := dbtest.Open(t, authz.Migrations())
	s := authz.NewStore(db, catalog)
	reader := mustRole(t, s, authz.Role{Name: "reader", Permissions: []string{"articles:read"}})
	mustAssign(t, s, user("alice"), "acme", reader.ID)
	mustAssign(t, s, user("alice"), authz.AllTenants, reader.ID)
	mustAssign(t, s, user("bob"), "globex", reader.ID)
	got := exported(t)(s.Export(ctx, db, user("alice")))
	contains(t, got, `{"tenant":"*","role":"reader","assigned_at":`, `{"tenant":"acme","role":"reader","assigned_at":`)
	excludes(t, got, "globex")
	if got := exported(t)(s.Export(ctx, db, user("nobody"))); got != "[]" {
		t.Fatalf("no roles: %s", got)
	}
	if _, err := s.Export(ctx, db, identity.Ref{}); err == nil {
		t.Fatal("expected error for empty subject")
	}
}

// 不缓存和缓存的 Store 结果相同：EnsureRole 改了权限之后，读取立即得到新的权限。
func TestEnsureRoleAndRoleByName(t *testing.T) {
	t.Parallel()
	for name, open := range map[string]func(t *testing.T) *authz.Store{"database": newStore, "cached": newCachedStore} {
		t.Run(name, func(t *testing.T) { testEnsureRole(t, open(t)) })
	}
}

func testEnsureRole(t *testing.T, s *authz.Store) {
	ctx := context.Background()

	// 不存在时创建；同名的租户角色和全局角色互不影响。
	owner, err := s.EnsureRole(ctx, authz.Role{Name: "owner", Description: "所有者", Permissions: []string{"members:manage", "articles:*"}})
	if err != nil || owner.ID == "" || strings.Join(owner.Permissions, ",") != "articles:*,members:manage" {
		t.Fatalf("create: %+v, %v", owner, err)
	}
	acmeOwner, err := s.EnsureRole(ctx, authz.Role{Tenant: "acme", Name: "owner", Permissions: []string{"articles:read"}})
	if err != nil || acmeOwner.ID == owner.ID {
		t.Fatalf("tenant role: %+v, %v", acmeOwner, err)
	}
	mustAssign(t, s, user("alice"), "acme", owner.ID)
	if perms, _, err := s.Permissions(ctx, user("alice"), "acme"); err != nil || strings.Join(perms, ",") != "articles:*,members:manage" {
		t.Fatalf("alice permissions %v, %v", perms, err)
	}

	// 没有变化时保持原样，ID 不变。
	again, err := s.EnsureRole(ctx, authz.Role{Name: "owner", Description: "所有者", Permissions: []string{"articles:*", "members:manage", "articles:*"}})
	if err != nil || again.ID != owner.ID {
		t.Fatalf("again: %+v, %v", again, err)
	}

	// 代码中改了说明和权限：同一个角色被改成新的样子，拥有它的主体立即得到新的权限。
	changed, err := s.EnsureRole(ctx, authz.Role{Name: "owner", Description: "工作区的所有者", Permissions: []string{"articles:read"}})
	if err != nil || changed.ID != owner.ID {
		t.Fatalf("changed: %+v, %v", changed, err)
	}
	got, err := s.RoleByName(ctx, "owner", "")
	if err != nil || got.ID != owner.ID || got.Description != "工作区的所有者" || strings.Join(got.Permissions, ",") != "articles:read" {
		t.Fatalf("RoleByName: %+v, %v", got, err)
	}
	if perms, _, err := s.Permissions(ctx, user("alice"), "acme"); err != nil || strings.Join(perms, ",") != "articles:read" {
		t.Fatalf("alice permissions %v, %v", perms, err)
	}
	if got, err := s.RoleByName(ctx, "owner", "acme"); err != nil || got.ID != acmeOwner.ID {
		t.Fatalf("tenant RoleByName: %+v, %v", got, err)
	}
	if _, err := s.RoleByName(ctx, "missing", ""); !errors.Is(err, authz.ErrRoleNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := s.EnsureRole(ctx, authz.Role{Name: "bad", Permissions: []string{"unknown:perm"}}); !errors.Is(err, authz.ErrInvalid) {
		t.Fatalf("unknown permission: %v", err)
	}
}

// 多个实例同时启动、同时调用 EnsureRole：都成功，得到同一个角色。
func TestEnsureRoleConcurrently(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	roles := make([]authz.Role, 8)
	errs := make([]error, len(roles))
	var wg sync.WaitGroup
	for i := range roles {
		wg.Go(func() {
			roles[i], errs[i] = s.EnsureRole(ctx, authz.Role{Name: "platform-admin", Permissions: []string{"platform:tenants"}})
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil || roles[i].ID != roles[0].ID {
			t.Fatalf("call %d: %+v, %v", i, roles[i], err)
		}
	}
	if all, err := s.Roles(ctx, ""); err != nil || len(all) != 1 {
		t.Fatalf("roles %v, %v", all, err)
	}
}

// DeleteRole 传入 RestrictInUse 时，角色还有分配就拒绝删除并返回 ErrRoleInUse（带公开参数 count）；
// 撤销全部分配后可以删除。不传 RestrictInUse 时照常级联删除。
func TestDeleteRoleRestrictInUse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	editor := mustRole(t, s, authz.Role{Tenant: "acme", Name: "editor", Permissions: []string{"articles:read"}})
	mustAssign(t, s, user("alice"), "acme", editor.ID)
	mustAssign(t, s, user("bob"), "acme", editor.ID)

	err := s.DeleteRole(ctx, editor.ID, "acme", authz.RestrictInUse())
	if !errors.Is(err, authz.ErrRoleInUse) {
		t.Fatalf("delete in-use role: err=%v, want ErrRoleInUse", err)
	}
	var ex *errorx.Error
	if !errors.As(err, &ex) || ex.Public()["count"] != 2 {
		t.Fatalf("err public=%v, want count=2", ex.Public())
	}
	// 角色和分配保持原样。
	if got, err := s.Role(ctx, editor.ID, "acme"); err != nil || got.ID != editor.ID {
		t.Fatalf("role after rejected delete: %+v, %v", got, err)
	}
	if members, err := s.Members(ctx, "acme"); err != nil || len(members) != 2 {
		t.Fatalf("members after rejected delete: %+v, %v", members, err)
	}

	// 撤销全部分配后可以删除。
	if err := s.Unassign(ctx, user("alice"), "acme", editor.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Unassign(ctx, user("bob"), "acme", editor.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRole(ctx, editor.ID, "acme", authz.RestrictInUse()); err != nil {
		t.Fatalf("delete unused role: %v", err)
	}
	if _, err := s.Role(ctx, editor.ID, "acme"); !errors.Is(err, authz.ErrRoleNotFound) {
		t.Fatalf("role after delete: %v", err)
	}
}

// 事务内 Assign 持锁未提交时，并发的 DeleteRole(RestrictInUse) 会等待该事务提交后再统计分配数并返回 ErrRoleInUse；
// 不会在 Assign 提交前误删角色。
func TestAssignAndDeleteRoleConcurrentSafety(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := dbtest.Open(t, authz.Migrations())
	s := authz.NewStore(db, catalog)

	role := mustRole(t, s, authz.Role{Tenant: "acme", Name: "agent", Permissions: []string{"articles:read"}})
	assigned := make(chan struct{})
	release := make(chan struct{})
	assignDone := make(chan error, 1)
	go func() {
		assignDone <- database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
			if err := authz.NewStore(tx, catalog).Assign(ctx, user("alice"), "acme", role.ID); err != nil {
				return err
			}
			close(assigned)
			<-release
			return nil
		})
	}()

	<-assigned
	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- s.DeleteRole(ctx, role.ID, "acme", authz.RestrictInUse())
	}()

	// DeleteRole 必须被 Assign 的行锁阻塞，在 Assign 提交前不能提前返回。
	select {
	case err := <-deleteDone:
		t.Fatalf("DeleteRole returned before Assign tx committed: err=%v", err)
	case <-time.After(80 * time.Millisecond):
	}

	close(release)
	if err := <-assignDone; err != nil {
		t.Fatalf("assign tx: %v", err)
	}
	if err := <-deleteDone; !errors.Is(err, authz.ErrRoleInUse) {
		t.Fatalf("delete after concurrent assign: err=%v, want ErrRoleInUse", err)
	}
	if roles, err := s.RolesOf(ctx, user("alice"), "acme"); err != nil || len(roles) != 1 || roles[0].ID != role.ID {
		t.Fatalf("alice roles=%+v err=%v", roles, err)
	}

	// 反向竞态：多个 goroutine 同时删除空角色与分配该角色，每轮要么删除成功且分配返回 ErrRoleNotFound，
	// 要么分配成功且删除返回 ErrRoleInUse，不会留下孤儿分配。
	for i := range 10 {
		r := mustRole(t, s, authz.Role{Tenant: "acme", Name: "race-" + string(rune('a'+i)), Permissions: []string{"articles:read"}})
		start := make(chan struct{})
		var assignErr, delErr error
		var wg sync.WaitGroup
		wg.Go(func() {
			<-start
			assignErr = s.Assign(ctx, user("bob"), "acme", r.ID)
		})
		wg.Go(func() {
			<-start
			delErr = s.DeleteRole(ctx, r.ID, "acme", authz.RestrictInUse())
		})
		close(start)
		wg.Wait()

		switch {
		case delErr == nil && errors.Is(assignErr, authz.ErrRoleNotFound):
			// 删除先提交，分配看到角色已不存在。
		case assignErr == nil && errors.Is(delErr, authz.ErrRoleInUse):
			// 分配先提交，删除看到使用中并拒绝。
		default:
			t.Fatalf("round %d: assignErr=%v delErr=%v", i, assignErr, delErr)
		}
	}
}

func TestActionWildcardRoles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cat := authz.Catalog{
		{Name: "articles:read", Domain: identity.DomainTenant, Description: "查看文章"},
		{Name: "articles:write", Domain: identity.DomainTenant, Description: "编辑文章"},
		{Name: "billing:read", Domain: identity.DomainTenant, Description: "查看账单"},
		{Name: "platform:read", Domain: identity.DomainPlatform, Description: "查看平台概览"},
	}
	s := authz.NewStore(dbtest.Open(t, authz.Migrations()), cat)

	// 全局角色允许使用 *:read 覆盖全部 :read 权限。
	auditor := mustRole(t, s, authz.Role{Name: "auditor", Permissions: []string{"*:read"}})
	mustAssign(t, s, user("alice"), "", auditor.ID)
	sub, err := s.Resolve(ctx, identity.Subject{Type: "user", ID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if !sub.Can("articles:read") || !sub.Can("billing:read") || !sub.Can("platform:read") || sub.Can("articles:write") {
		t.Fatalf("unexpected permissions for *:read: %+v", sub.Permissions)
	}
	// 租户自定义角色使用 *:read 会命中平台域的 platform:read，必须被拒绝。
	if _, err := s.CreateRole(ctx, authz.Role{Tenant: "acme", Name: "tenant-auditor", Permissions: []string{"*:read"}}); !errors.Is(err, authz.ErrInvalid) {
		t.Fatalf("tenant role with *:read hitting platform domain = %v, want ErrInvalid", err)
	}
	// 无匹配权限的动作通配符被拒绝。
	if _, err := s.CreateRole(ctx, authz.Role{Name: "none", Permissions: []string{"*:delete"}}); !errors.Is(err, authz.ErrInvalid) {
		t.Fatalf("unmatched *:delete = %v, want ErrInvalid", err)
	}
}
