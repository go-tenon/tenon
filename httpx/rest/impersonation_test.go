package rest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/logging"
)

type impersonationOut struct {
	Body struct {
		ID, Method, Credential, Impersonator, BoundTenant, LoggedImpersonator string
		Scopes                                                                []string
	}
}

// Resolve 新建主体返回时，凭据决定的字段（主体、认证方式、凭据 ID、租户边界、限制、代为登录者）仍然取自凭据；
// 同时日志上下文自动注入 impersonator 属性。
func TestResolveKeepsCredentialFields(t *testing.T) {
	t.Parallel()
	api := rest.New(chi.NewRouter(), rest.Options{
		Tenant: func(ctx huma.Context) string { return "acme" },
		Accounts: identity.Accounts{"user": func(_ context.Context, sub identity.Subject) (identity.Subject, error) {
			return identity.Subject{
				Type: "staff", ID: "mallory", Tenant: "acme", BoundTenant: "forged",
				Permissions: []string{"*"}, Impersonator: identity.Ref{Type: "staff", ID: "forged"}, Scopes: nil,
			}, nil
		}},
		Impersonation: &identity.Impersonation{
			Actors: identity.Accounts{"staff": func(_ context.Context, sub identity.Subject) (identity.Subject, error) {
				sub.Permissions = []string{"users:impersonate"}
				return sub, nil
			}},
			Permissions: []string{"users:impersonate"},
		},
	})
	validate := func(_ context.Context, token string) (identity.Subject, error) {
		return identity.Subject{
			Type: "user", ID: "alice", Method: "bearer", Credential: "t1",
			BoundTenant: "acme", Impersonator: identity.Ref{Type: "staff", ID: "s1"}, Scopes: []string{"articles:read"},
		}, nil
	}
	op := huma.Operation{OperationID: "me", Method: http.MethodGet, Path: "/me"}
	rest.RequireToken(api, validate, "articles:read")(&op)
	rest.Register(api, op, func(ctx context.Context, _ *struct{}) (*impersonationOut, error) {
		sub, _ := identity.FromContext(ctx)
		out := &impersonationOut{}
		out.Body.ID, out.Body.Method, out.Body.Credential = sub.ID, sub.Method, sub.Credential
		out.Body.Impersonator, out.Body.BoundTenant, out.Body.Scopes = sub.Impersonator.String(), sub.BoundTenant, sub.Scopes
		for _, a := range logging.ContextAttrs(ctx) {
			if a.Key == logging.AttrImpersonator {
				out.Body.LoggedImpersonator = a.Value.String()
			}
		}
		return out, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer x")
	rr := httptest.NewRecorder()
	api.Handler().ServeHTTP(rr, req)
	want := `{"ID":"alice","Method":"bearer","Credential":"t1","Impersonator":"staff:s1","BoundTenant":"acme","LoggedImpersonator":"staff:s1","Scopes":["articles:read"]}`
	if rr.Code != http.StatusOK || rr.Body.String() != want+"\n" {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
}

// 未配置 Impersonation 的 API 默认拒绝代为登录；配置了 DirectOnly 的路由返回 403 auth.impersonation_forbidden；
// 配置了 AccountScope 的 API 或 AccountScoped 的路由放行绑定了租户的凭据并保留 BoundTenant。
func TestImpersonationPoliciesAndAccountScope(t *testing.T) {
	t.Parallel()
	staffActive := true
	staffAllowed := true
	validate := func(_ context.Context, token string) (identity.Subject, error) {
		switch token {
		case "imp-acme":
			return identity.Subject{Type: "user", ID: "alice", BoundTenant: "acme", Impersonator: identity.Ref{Type: "staff", ID: "s1"}}, nil
		default:
			return identity.Subject{Type: "user", ID: "alice"}, nil
		}
	}
	impersonation := &identity.Impersonation{
		Actors: identity.Accounts{"staff": func(_ context.Context, sub identity.Subject) (identity.Subject, error) {
			if !staffActive {
				return identity.Subject{}, identity.ErrUnauthenticated.New()
			}
			if staffAllowed {
				sub.Permissions = []string{"users:impersonate"}
			}
			return sub, nil
		}},
		Permissions: []string{"users:impersonate"},
	}

	// 未配置 Impersonation：默认拒绝代为登录（401）。
	closed := rest.New(chi.NewRouter(), rest.Options{Accounts: identity.Accounts{"user": nil}})
	op := huma.Operation{OperationID: "closed-get", Method: http.MethodGet, Path: "/closed"}
	rest.RequireToken(closed, validate)(&op)
	rest.Register(closed, op, func(context.Context, *struct{}) (*struct{}, error) { return nil, nil })
	req := httptest.NewRequest(http.MethodGet, "/closed", nil)
	req.Header.Set("Authorization", "Bearer imp-acme")
	rr := httptest.NewRecorder()
	closed.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("fail-closed expected 401, got %d %s", rr.Code, rr.Body)
	}

	// 账号自省 API：放行绑定了 acme 的代为登录凭据并保留 BoundTenant；DirectOnly 路由拒绝代登。
	accountAPI := rest.New(chi.NewRouter(), rest.Options{
		Accounts:      identity.Accounts{"user": nil},
		Impersonation: impersonation,
		AccountScope:  true,
	})
	meOp := huma.Operation{OperationID: "account-me", Method: http.MethodGet, Path: "/me"}
	rest.RequireToken(accountAPI, validate)(&meOp)
	rest.Register(accountAPI, meOp, func(ctx context.Context, _ *struct{}) (*impersonationOut, error) {
		sub, _ := identity.FromContext(ctx)
		out := &impersonationOut{}
		out.Body.ID, out.Body.BoundTenant, out.Body.Impersonator = sub.ID, sub.BoundTenant, sub.Impersonator.String()
		return out, nil
	})
	pwOp := huma.Operation{OperationID: "account-pw", Method: http.MethodPut, Path: "/me/password"}
	rest.RequireToken(accountAPI, validate)(&pwOp)
	rest.DirectOnly()(&pwOp)
	rest.Register(accountAPI, pwOp, func(context.Context, *struct{}) (*struct{}, error) { return nil, nil })

	call := func(api *rest.API, method, path, tok string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		return w
	}
	if w := call(accountAPI, http.MethodGet, "/me", "imp-acme"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"BoundTenant":"acme"`) {
		t.Fatalf("account scope me: %d %s", w.Code, w.Body)
	}
	if w := call(accountAPI, http.MethodPut, "/me/password", "imp-acme"); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"auth.impersonation_forbidden"`) {
		t.Fatalf("direct only impersonated: %d %s", w.Code, w.Body)
	}
	if w := call(accountAPI, http.MethodPut, "/me/password", "direct"); w.Code != http.StatusNoContent {
		t.Fatalf("direct only normal: %d %s", w.Code, w.Body)
	}

	// 操作者失去权限或被停用后，下一个请求立即 401。
	staffAllowed = false
	if w := call(accountAPI, http.MethodGet, "/me", "imp-acme"); w.Code != http.StatusUnauthorized {
		t.Fatalf("actor permission revoked: %d %s", w.Code, w.Body)
	}
	staffAllowed, staffActive = true, false
	if w := call(accountAPI, http.MethodGet, "/me", "imp-acme"); w.Code != http.StatusUnauthorized {
		t.Fatalf("actor disabled: %d %s", w.Code, w.Body)
	}
}
