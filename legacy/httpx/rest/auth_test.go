package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/cockroachdb/errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/logger"
)

type testUser struct {
	id     string
	scopes []string
}

// idOf 把测试中的名字转换为固定的 UUID。
func idOf(name string) uuid.UUID {
	var u uuid.UUID
	copy(u[:], name)
	return u
}

func (u *testUser) UserID() uuid.UUID      { return idOf(u.id) }
func (u *testUser) HasScope(s string) bool { return slices.Contains(u.scopes, s) }

// plainUser does not implement auth.ScopeChecker.
type plainUser struct{ id string }

func (u plainUser) UserID() uuid.UUID { return idOf(u.id) }

type whoamiOutput struct {
	Body struct {
		ID string `json:"id"`
	}
}

func verifyToken(_ context.Context, token string) (*testUser, error) {
	switch token {
	case "user":
		return &testUser{id: "bearer-user"}, nil
	case "admin":
		return &testUser{id: "bearer-admin", scopes: []string{"admin"}}, nil
	case "boom":
		return nil, errors.New("token store down")
	}
	return nil, auth.ErrUnauthenticated
}

func newAuthAPI(t *testing.T, logs *bytes.Buffer) (http.Handler, huma.API) {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(logger.New(logger.Options{Writer: logs}))
	prevNew := huma.NewErrorWithContext
	huma.NewErrorWithContext = NewErrorWithContext
	t.Cleanup(func() {
		slog.SetDefault(prev)
		huma.NewErrorWithContext = prevNew
	})

	r := chi.NewRouter()
	r.Use(httpx.RequestID, httpx.AccessLog(nil))
	// Stand-in for scs + auth.LoadSession.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id := r.Header.Get("X-Session-User"); id != "" {
				r = r.WithContext(auth.WithPrincipal(r.Context(), &testUser{id: id, scopes: []string{"admin"}}))
			}
			next.ServeHTTP(w, r)
		})
	})

	config := huma.DefaultConfig("Test", "1.0.0")
	config.Components.SecuritySchemes = SecuritySchemes("session")
	api := humachi.New(r, config)
	api.UseMiddleware(Authorize(api, verifyToken, nil))

	whoami := func(ctx context.Context, _ *struct{}) (*whoamiOutput, error) {
		out := &whoamiOutput{}
		if u, ok := auth.From[*testUser](ctx); ok {
			out.Body.ID = u.id
		}
		return out, nil
	}
	reg := func(path string, security []map[string][]string) {
		huma.Register(api, huma.Operation{
			OperationID: strings.Trim(strings.ReplaceAll(path, "/", "-"), "-"),
			Method:      http.MethodGet,
			Path:        path,
			Security:    security,
		}, whoami)
	}
	reg("/public", nil)
	reg("/private", RequireAuth())
	reg("/admin", RequireAuth("admin"))
	reg("/bearer-only", []map[string][]string{{SchemeBearer: {}}})
	reg("/optional", []map[string][]string{{SchemeBearer: {}}, {}})
	reg("/unknown-scheme", []map[string][]string{{"oauth2": {}}})
	return r, api
}

func TestAuthorize(t *testing.T) {
	var logs bytes.Buffer
	h, _ := newAuthAPI(t, &logs)

	for _, tc := range []struct {
		name, path, bearer, session string
		status                      int
		id                          string
	}{
		{"public", "/public", "", "", 200, ""},
		{"no credentials", "/private", "", "", 401, ""},
		{"session", "/private", "", "s-1", 200, "s-1"},
		{"bearer", "/private", "user", "", 200, "bearer-user"},
		{"invalid bearer", "/private", "nope", "", 401, ""},
		{"invalid bearer falls back to session", "/private", "nope", "s-1", 200, "s-1"},
		{"missing scope", "/admin", "user", "", 403, ""},
		{"scope via bearer", "/admin", "admin", "", 200, "bearer-admin"},
		{"scope via session", "/admin", "", "s-1", 200, "s-1"},
		{"bearer-only rejects session", "/bearer-only", "", "s-1", 401, ""},
		{"optional anonymous", "/optional", "", "", 200, ""},
		{"optional with bearer", "/optional", "user", "", 200, "bearer-user"},
		{"unknown scheme denies", "/unknown-scheme", "admin", "s-1", 401, ""},
		{"verifier failure", "/private", "boom", "", 500, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			if tc.session != "" {
				req.Header.Set("X-Session-User", tc.session)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
			if tc.status == 200 {
				var out struct{ ID string }
				_ = json.Unmarshal(rec.Body.Bytes(), &out)
				if out.ID != tc.id {
					t.Errorf("principal = %q, want %q", out.ID, tc.id)
				}
			}
			if tc.status == 401 && rec.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("WWW-Authenticate = %q", rec.Header().Get("WWW-Authenticate"))
			}
			if ct := rec.Header().Get("Content-Type"); tc.status >= 400 && !strings.Contains(ct, "problem+json") {
				t.Errorf("Content-Type = %q, want problem+json", ct)
			}
		})
	}

	if !strings.Contains(logs.String(), "token store down") {
		t.Errorf("verifier failure not logged: %s", logs.String())
	}
}

func TestAuthorizeAccessLogUserID(t *testing.T) {
	var logs bytes.Buffer
	h, _ := newAuthAPI(t, &logs)

	req := httptest.NewRequest("GET", "/private", nil)
	req.Header.Set("Authorization", "Bearer user")
	h.ServeHTTP(httptest.NewRecorder(), req)

	var access map[string]any
	for line := range strings.Lines(logs.String()) {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil && m["msg"] == "http request" {
			access = m
		}
	}
	if access["user_id"] != idOf("bearer-user").String() {
		t.Errorf("access log user_id = %v, want bearer-user: %s", access["user_id"], logs.String())
	}
}

func TestAuthorizeScopesWithoutChecker(t *testing.T) {
	_, api := humatestAPI(t)
	api.UseMiddleware(Authorize(api, func(context.Context, string) (plainUser, error) {
		return plainUser{"p"}, nil
	}, nil))
	huma.Register(api, huma.Operation{
		OperationID: "scoped", Method: http.MethodGet, Path: "/scoped", Security: RequireAuth("admin"),
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) { return nil, nil })

	req := httptest.NewRequest("GET", "/scoped", nil)
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	api.Adapter().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("status = %d, want 403 when principal cannot check scopes", rec.Code)
	}
}

func TestAuthorizeNilVerifier(t *testing.T) {
	_, api := humatestAPI(t)
	api.UseMiddleware(Authorize[*testUser](api, nil, nil))
	huma.Register(api, huma.Operation{
		OperationID: "p", Method: http.MethodGet, Path: "/p", Security: RequireAuth(),
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) { return nil, nil })

	req := httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	api.Adapter().ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestSecuritySchemesInOpenAPI(t *testing.T) {
	_, api := newAuthAPI(t, &bytes.Buffer{})
	spec, _ := json.Marshal(api.OpenAPI())
	for _, want := range []string{
		`"bearer":{"scheme":"bearer","type":"http"}`,
		`"session":{"in":"cookie","name":"session","type":"apiKey"}`,
		`"security":[{"bearer":[]},{"session":[]}]`,
		`"security":[{"bearer":["admin"]},{"session":["admin"]}]`,
	} {
		if !strings.Contains(string(spec), want) {
			t.Errorf("OpenAPI spec missing %s", want)
		}
	}
}

func humatestAPI(t *testing.T) (chi.Router, huma.API) {
	t.Helper()
	r := chi.NewRouter()
	return r, humachi.New(r, huma.DefaultConfig("Test", "1.0.0"))
}
