package rest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
)

// tokenAPI 返回一个 GET 和一个 POST 都要求 token 的 API，响应体是主体的 ID。
func tokenAPI(t *testing.T, session ...httpx.SessionCookie) *rest.API {
	t.Helper()
	opts := rest.Options{Accounts: identity.Accounts{"user": nil}}
	if len(session) > 0 {
		opts.SessionCookie = session[0]
	}
	api := rest.New(chi.NewRouter(), opts)
	validate := func(_ context.Context, token string) (identity.Subject, error) {
		if token == "bad" {
			return identity.Subject{}, nil
		}
		return identity.Subject{Type: "user", ID: token}, nil
	}
	type out struct{ Body struct{ ID string } }
	handler := func(ctx context.Context, _ *struct{}) (*out, error) {
		o := &out{}
		sub, _ := identity.FromContext(ctx)
		o.Body.ID = sub.ID
		return o, nil
	}
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		op := huma.Operation{OperationID: "me-" + map[string]string{http.MethodGet: "get", http.MethodPost: "update"}[m], Method: m, Path: "/me"}
		rest.RequireToken(api, validate)(&op)
		rest.Register(api, op, handler)
	}
	return api
}

func callMe(api *rest.API, method string, header http.Header) (int, string, http.Header) {
	req := httptest.NewRequest(method, "http://example.com/me", nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rr := httptest.NewRecorder()
	api.Handler().ServeHTTP(rr, req)
	var body struct {
		ID   string
		Code string
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Code != "" {
		return rr.Code, body.Code, rr.Header()
	}
	return rr.Code, body.ID, rr.Header()
}

// 同一个校验器接受两种携带方式；都带着时以 Authorization 为准。
func TestRequireTokenCarriers(t *testing.T) {
	t.Parallel()
	api := tokenAPI(t)
	cookie := httpx.DefaultSessionCookieName + "=alice"
	for name, c := range map[string]struct {
		header http.Header
		status int
		got    string
	}{
		"bearer":         {http.Header{"Authorization": {"Bearer bob"}}, 200, "bob"},
		"session cookie": {http.Header{"Cookie": {cookie}}, 200, "alice"},
		"bearer wins":    {http.Header{"Authorization": {"Bearer bob"}, "Cookie": {cookie}}, 200, "bob"},
		"other cookie":   {http.Header{"Cookie": {"session=alice"}}, 401, "auth.unauthenticated"},
		"invalid cookie": {http.Header{"Cookie": {httpx.DefaultSessionCookieName + "=bad"}}, 401, "auth.unauthenticated"},
		"nothing":        {nil, 401, "auth.unauthenticated"},
	} {
		status, got, h := callMe(api, http.MethodGet, c.header)
		if status != c.status || got != c.got {
			t.Errorf("%s: %d %q", name, status, got)
		}
		if status == 401 && h.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: challenge %q", name, h.Get("WWW-Authenticate"))
		}
	}
}

// 用会话 cookie 认证的写请求必须来自同源页面；读请求和 Bearer 不受影响。
func TestRequireTokenCrossSite(t *testing.T) {
	t.Parallel()
	api := tokenAPI(t)
	cookie := httpx.DefaultSessionCookieName + "=alice"
	for name, c := range map[string]struct {
		method string
		header http.Header
		status int
		got    string
	}{
		"same-origin write":     {http.MethodPost, http.Header{"Cookie": {cookie}, "Sec-Fetch-Site": {"same-origin"}}, 200, "alice"},
		"origin matches host":   {http.MethodPost, http.Header{"Cookie": {cookie}, "Origin": {"http://example.com"}}, 200, "alice"},
		"cross-site write":      {http.MethodPost, http.Header{"Cookie": {cookie}, "Sec-Fetch-Site": {"cross-site"}}, 403, "http.cross_site"},
		"same-site subdomain":   {http.MethodPost, http.Header{"Cookie": {cookie}, "Sec-Fetch-Site": {"same-site"}}, 403, "http.cross_site"},
		"foreign origin":        {http.MethodPost, http.Header{"Cookie": {cookie}, "Origin": {"https://evil.example"}}, 403, "http.cross_site"},
		"no origin information": {http.MethodPost, http.Header{"Cookie": {cookie}}, 403, "http.cross_site"},
		"cross-site read":       {http.MethodGet, http.Header{"Cookie": {cookie}, "Sec-Fetch-Site": {"cross-site"}}, 200, "alice"},
		"bearer from anywhere":  {http.MethodPost, http.Header{"Authorization": {"Bearer bob"}, "Cookie": {cookie}}, 200, "bob"},
		"no cookie, no bearer":  {http.MethodPost, http.Header{"Sec-Fetch-Site": {"cross-site"}}, 401, "auth.unauthenticated"},
	} {
		if status, got, _ := callMe(api, c.method, c.header); status != c.status || got != c.got {
			t.Errorf("%s: %d %q", name, status, got)
		}
	}
}

// 同一个源下的两套账号体系各用一个 cookie：浏览器同时带着两个，每个 API 只读自己的那一个。
func TestRequireTokenNamedSession(t *testing.T) {
	t.Parallel()
	ops := tokenAPI(t, httpx.SessionCookie{Name: "__Host-ops"})
	console := tokenAPI(t)
	both := http.Header{"Cookie": {"__Host-ops=staff; " + httpx.DefaultSessionCookieName + "=member"}}
	if _, got, _ := callMe(ops, http.MethodGet, both); got != "staff" {
		t.Fatalf("ops read %q", got)
	}
	if _, got, _ := callMe(console, http.MethodGet, both); got != "member" {
		t.Fatalf("console read %q", got)
	}
	if s := ops.OpenAPI().Components.SecuritySchemes["session"]; s.Name != "__Host-ops" {
		t.Fatalf("scheme %+v", s)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("invalid session cookie name accepted")
		}
	}()
	rest.New(chi.NewRouter(), rest.Options{SessionCookie: httpx.SessionCookie{Name: "ops"}})
}

// OpenAPI 中两种携带方式任选其一，会话 cookie 在前；路由表的访问策略是 token。
func TestRequireTokenOpenAPI(t *testing.T) {
	t.Parallel()
	api := tokenAPI(t)
	schemes := api.OpenAPI().Components.SecuritySchemes
	if s := schemes["bearer"]; s == nil || s.Type != "http" || s.Scheme != "bearer" {
		t.Fatalf("bearer scheme %+v", s)
	}
	if s := schemes["session"]; s == nil || s.Type != "apiKey" || s.In != "cookie" || s.Name != httpx.DefaultSessionCookieName {
		t.Fatalf("session scheme %+v", s)
	}
	op := api.OpenAPI().Paths["/me"].Get
	if len(op.Security) != 2 || op.Security[0]["session"] == nil || op.Security[1]["bearer"] == nil {
		t.Fatalf("security %v", op.Security)
	}
	for _, r := range httpx.Routes(api.Router()) {
		if r.Path == "/me" && r.Access != rest.AccessToken {
			t.Fatalf("route %+v", r)
		}
	}
}

// SameOrigin 用于设置会话 cookie 的公开接口，例如浏览器的登录。
func TestSameOrigin(t *testing.T) {
	t.Parallel()
	api := rest.New(chi.NewRouter(), rest.Options{})
	op := huma.Operation{OperationID: "session-create", Method: http.MethodPost, Path: "/me"}
	rest.Public()(&op)
	rest.SameOrigin(api)(&op)
	rest.Register(api, op, func(context.Context, *struct{}) (*struct{}, error) { return nil, nil })
	if status, _, _ := callMe(api, http.MethodPost, http.Header{"Sec-Fetch-Site": {"same-origin"}}); status != http.StatusNoContent {
		t.Fatalf("same-origin login %d", status)
	}
	for _, h := range []http.Header{{"Sec-Fetch-Site": {"cross-site"}}, {"Origin": {"https://evil.example"}}, nil} {
		if status, code, _ := callMe(api, http.MethodPost, h); status != http.StatusForbidden || code != "http.cross_site" {
			t.Fatalf("%v: %d %q", h, status, code)
		}
	}
}
