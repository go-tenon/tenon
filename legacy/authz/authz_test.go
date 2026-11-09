package authz_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"uuid"

	"github.com/ryan053857/go-kit/authz"
	"github.com/ryan053857/go-kit/httpx/auth"
)

type user struct{ id uuid.UUID }

func (u user) UserID() uuid.UUID { return u.id }

// apiKey 模拟限定了 scope 的凭证。
type apiKey struct {
	user
	scopes []string
}

func (k apiKey) HasScope(s string) bool { return slices.Contains(k.scopes, s) }

type post struct{ author uuid.UUID }

func TestGate(t *testing.T) {
	alice, bob, admin := uuid.New(), uuid.New(), uuid.New()
	g := authz.New()
	g.Define("post.create", func(context.Context, uuid.UUID) bool { return true })
	authz.DefineFor(g, "post.update", func(_ context.Context, u uuid.UUID, p *post) bool { return p.author == u })
	g.Before(func(_ context.Context, u uuid.UUID, _ string) (bool, bool) { return u == admin, u == admin })

	as := func(p auth.Principal) context.Context { return auth.WithPrincipal(context.Background(), p) }
	mine := &post{author: alice}

	if err := g.Authorize(context.Background(), "post.create"); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("anonymous: %v", err)
	}
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		ability string
		obj     []any
		want    bool
	}{
		{"defined", as(user{alice}), "post.create", nil, true},
		{"undefined ability", as(user{alice}), "post.delete", nil, false},
		{"own post", as(user{alice}), "post.update", []any{mine}, true},
		{"other's post", as(user{bob}), "post.update", []any{mine}, false},
		{"policy without object", as(user{alice}), "post.update", nil, false},
		{"wrong object type", as(user{alice}), "post.update", []any{"x"}, false},
		{"before", as(user{admin}), "post.delete", nil, true},
		{"api key with scope", as(apiKey{user{alice}, []string{"post.create"}}), "post.create", nil, true},
		{"api key without scope", as(apiKey{user{alice}, []string{"post.create"}}), "post.update", []any{mine}, false},
	} {
		if got := g.Allows(tc.ctx, tc.ability, tc.obj...); got != tc.want {
			t.Errorf("%s: Allows = %v, want %v", tc.name, got, tc.want)
		}
	}
	if err := g.Authorize(as(user{bob}), "post.update", mine); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("forbidden: %v", err)
	}

	defer func() {
		if recover() == nil {
			t.Error("duplicate Define did not panic")
		}
	}()
	g.Define("post.create", func(context.Context, uuid.UUID) bool { return false })
}

func TestRequire(t *testing.T) {
	alice := uuid.New()
	g := authz.New()
	g.Define("admin.access", func(_ context.Context, u uuid.UUID) bool { return false })
	h := authz.Require(g, "admin.access")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Errorf("anonymous = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req.WithContext(auth.WithPrincipal(req.Context(), user{alice})))
	if rec.Code != 403 || rec.Body.String() != "没有权限执行此操作\n" {
		t.Errorf("forbidden = %d %q", rec.Code, rec.Body)
	}
}
