package role_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/authz"
	"github.com/ryan053857/go-kit/database/dbtest"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/modules/role"
)

func TestMain(m *testing.M) {
	huma.NewErrorWithContext = rest.NewErrorWithContext
	os.Exit(m.Run())
}

type principal struct{ id uuid.UUID }

func (p principal) UserID() uuid.UUID { return p.id }

func as(ctx context.Context, id uuid.UUID) context.Context {
	return auth.WithPrincipal(ctx, principal{id})
}

type order struct{ owner uuid.UUID }

func TestRolesAndGate(t *testing.T) {
	s := role.New(dbtest.New(t, role.Migrations))
	s.Declare("audit.view", "order.refund", "order.update")
	ctx := t.Context()
	admin, support, nobody := uuid.New(), uuid.New(), uuid.New()

	if err := s.Save(ctx, &role.Role{Name: "x", Permissions: []string{"bogus"}}); !errors.Is(err, role.ErrUnknownPermission) {
		t.Errorf("unknown permission: %v", err)
	}
	super, err := s.Ensure(ctx, "超级管理员", role.All)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Ensure(ctx, "超级管理员", role.All)
	if err != nil || again.ID != super.ID {
		t.Fatalf("ensure twice = %v, %v", again, err)
	}
	sup := &role.Role{Name: "客服", Permissions: []string{"audit.view", "order.refund"}}
	if err := s.Save(ctx, sup); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, &role.Role{Name: "客服"}); !errors.Is(err, role.ErrNameTaken) {
		t.Errorf("duplicate name: %v", err)
	}
	if err := s.SetUserRoles(ctx, admin, super.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserRoles(ctx, support, sup.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserRoles(ctx, support, sup.ID, uuid.New()); !errors.Is(err, role.ErrNotFound) {
		t.Errorf("unknown role: %v", err)
	}
	perms, _ := s.UserPermissions(ctx, support)
	slices.Sort(perms)
	if !slices.Equal(perms, []string{"audit.view", "order.refund"}) {
		t.Errorf("support perms = %v", perms)
	}
	if perms, _ := s.UserPermissions(ctx, nobody); len(perms) != 0 {
		t.Errorf("nobody perms = %v", perms)
	}

	g := authz.New()
	s.Attach(g)
	authz.DefineFor(g, "order.update", func(ctx context.Context, user uuid.UUID, o *order) bool { return o.owner == user })

	if g.Allows(ctx, "audit.view") {
		t.Error("anonymous allowed")
	}
	if err := g.Authorize(ctx, "audit.view"); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("anonymous: %v", err)
	}
	if !g.Allows(as(ctx, admin), "anything.at.all") {
		t.Error("super admin denied")
	}
	if !g.Allows(as(ctx, support), "audit.view") || g.Allows(as(ctx, support), "order.update", &order{owner: admin}) {
		t.Error("support permissions wrong")
	}
	if err := g.Authorize(as(ctx, nobody), "audit.view"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("nobody: %v", err)
	}
	// 没有权限时交给规则：自己的订单可以改。
	if !g.Allows(as(ctx, nobody), "order.update", &order{owner: nobody}) || g.Allows(as(ctx, nobody), "order.update") {
		t.Error("policy rule not applied")
	}

	// 删除角色后失去权限；Middleware 内缓存。
	if err := s.Delete(ctx, sup.ID); err != nil {
		t.Fatal(err)
	}
	if g.Allows(as(ctx, support), "audit.view") {
		t.Error("deleted role still grants permission")
	}
}

func TestEndpoints(t *testing.T) {
	s := role.New(dbtest.New(t, role.Migrations))
	s.Declare("audit.view")
	ctx := t.Context()
	admin, user := uuid.New(), uuid.New()
	super, _ := s.Ensure(ctx, "超级管理员", role.All)
	_ = s.SetUserRoles(ctx, admin, super.ID)

	g := authz.New()
	s.Attach(g)
	r := chi.NewRouter()
	r.Use(s.Middleware, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id, err := uuid.Parse(r.Header.Get("X-User")); err == nil {
				r = r.WithContext(as(r.Context(), id))
			}
			next.ServeHTTP(w, r)
		})
	})
	api := humachi.New(r, huma.DefaultConfig("t", "1"))
	api.UseMiddleware(rest.Authorize[principal](api, nil, g))
	rest.Get(api, "/permissions", role.PermissionsEndpoint(s))
	rest.Get(api, "/roles", role.RolesEndpoint(s))
	rest.Post(api, "/roles", role.CreateRoleEndpoint(s))
	rest.Put(api, "/roles/{id}", role.UpdateRoleEndpoint(s))
	rest.Delete(api, "/roles/{id}", role.DeleteRoleEndpoint(s))
	rest.Get(api, "/users/{id}/roles", role.UserRolesEndpoint(s))
	rest.Put(api, "/users/{id}/roles", role.SetUserRolesEndpoint(s))

	do := func(method, path, body string, user uuid.UUID) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if user != uuid.Nil() {
			req.Header.Set("X-User", user.String())
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("GET", "/roles", "", uuid.Nil()); rec.Code != 401 {
		t.Errorf("anonymous = %d", rec.Code)
	}
	if rec := do("GET", "/roles", "", user); rec.Code != 403 || !strings.Contains(rec.Body.String(), `"code":"authz.forbidden"`) {
		t.Errorf("no permission = %d %s", rec.Code, rec.Body)
	}
	rec := do("GET", "/permissions", "", admin)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"label":"管理角色与权限"`) {
		t.Fatalf("permissions = %d %s", rec.Code, rec.Body)
	}
	rec = do("POST", "/roles", `{"name":"审计员","permissions":["audit.view"]}`, admin)
	if rec.Code != 201 {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	id := rec.Body.String()[strings.Index(rec.Body.String(), `"id":"`)+6:][:36]
	if rec := do("POST", "/roles", `{"name":"x","permissions":["nope"]}`, admin); rec.Code != 422 || !strings.Contains(rec.Body.String(), "未知的权限：nope") {
		t.Errorf("unknown permission = %d %s", rec.Code, rec.Body)
	}
	if rec := do("PUT", "/users/"+user.String()+"/roles", `{"role_ids":["`+id+`"]}`, admin); rec.Code != 200 || !strings.Contains(rec.Body.String(), "审计员") {
		t.Errorf("set user roles = %d %s", rec.Code, rec.Body)
	}
	if rec := do("PUT", "/roles/"+id, `{"name":"审计员","permissions":["audit.view","role.manage"]}`, admin); rec.Code != 200 {
		t.Errorf("update = %d %s", rec.Code, rec.Body)
	}
	if rec := do("GET", "/roles", "", user); rec.Code != 200 {
		t.Errorf("user with role.manage = %d", rec.Code)
	}
	if rec := do("DELETE", "/roles/"+id, "", admin); rec.Code != 204 {
		t.Errorf("delete = %d", rec.Code)
	}
	if rec := do("GET", "/users/"+user.String()+"/roles", "", admin); !strings.Contains(rec.Body.String(), "[]") {
		t.Errorf("roles after delete = %s", rec.Body)
	}
}
