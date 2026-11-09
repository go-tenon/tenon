package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"uuid"

	"github.com/cockroachdb/errors"

	"github.com/alexedwards/scs/v2"
	"github.com/alexedwards/scs/v2/memstore"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/logger"
)

const u1 = "01900000-0000-7000-8000-000000000001"

type user struct{ id uuid.UUID }

func (u *user) UserID() uuid.UUID { return u.id }

func (u *user) String() string { return u.id.String() }

type apiClient struct{ id uuid.UUID }

func (c apiClient) UserID() uuid.UUID { return c.id }

func TestPrincipalContext(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New(logger.Options{Writer: &buf})

	ctx := WithPrincipal(context.Background(), &user{uuid.MustParse(u1)})

	u, ok := From[*user](ctx)
	if !ok || u.id.String() != u1 {
		t.Fatalf("From[*user] = %v, %v", u, ok)
	}
	// Different principal types do not collide.
	if _, ok := From[apiClient](ctx); ok {
		t.Error("From[apiClient] should not find a *user")
	}
	if _, ok := From[*user](context.Background()); ok {
		t.Error("From on empty context should be false")
	}

	log.InfoContext(ctx, "x")
	var m map[string]any
	_ = json.Unmarshal(buf.Bytes(), &m)
	if m["user_id"] != u1 {
		t.Errorf("log user_id = %v, want u-1", m["user_id"])
	}
}

func TestBearerToken(t *testing.T) {
	for in, want := range map[string]string{
		"Bearer abc":       "abc",
		"bearer abc":       "abc",
		"  BEARER   abc  ": "abc",
		"Bearer":           "",
		"Bearer   ":        "",
		"Basic abc":        "",
		"abc":              "",
		"":                 "",
	} {
		got, ok := BearerToken(in)
		if got != want || ok != (want != "") {
			t.Errorf("BearerToken(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestSafeRedirect(t *testing.T) {
	for in, want := range map[string]string{
		"/orders?page=2":      "/orders?page=2",
		"/":                   "/",
		"":                    "/home",
		"//evil.com":          "/home",
		"/\\evil.com":         "/home",
		"https://evil.com":    "/home",
		"evil.com":            "/home",
		"/\t/evil.com":        "/home",
		"javascript:alert(1)": "/home",
		"/path/with//slashes": "/path/with//slashes",
	} {
		if got := SafeRedirect(in, "/home"); got != want {
			t.Errorf("SafeRedirect(%q) = %q, want %q", in, got, want)
		}
	}
}

// sessionApp wires scs + LoadSession the way a product would.
type sessionApp struct {
	sm      *scs.SessionManager
	handler http.Handler
	loadErr error
	logs    bytes.Buffer
}

func newSessionApp(t *testing.T) *sessionApp {
	t.Helper()
	app := &sessionApp{sm: scs.New()}
	app.sm.Store = memstore.New()

	prev := slog.Default()
	slog.SetDefault(logger.New(logger.Options{Writer: &app.logs}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	load := func(ctx context.Context, id uuid.UUID) (*user, error) {
		if app.loadErr != nil {
			return nil, app.loadErr
		}
		return &user{id}, nil
	}

	r := chi.NewRouter()
	r.Use(app.sm.LoadAndSave, LoadSession(app.sm, load))
	r.Post("/login", func(w http.ResponseWriter, r *http.Request) {
		if err := Login(r.Context(), app.sm, uuid.MustParse(r.FormValue("id"))); err != nil {
			t.Error(err)
		}
	})
	r.Post("/logout", func(w http.ResponseWriter, r *http.Request) {
		if err := Logout(r.Context(), app.sm); err != nil {
			t.Error(err)
		}
	})
	r.Post("/visit", func(w http.ResponseWriter, r *http.Request) {
		app.sm.Put(r.Context(), "visited", true) // creates an anonymous session
	})
	r.Get("/me", func(w http.ResponseWriter, r *http.Request) {
		if u, ok := From[*user](r.Context()); ok {
			_, _ = io.WriteString(w, u.id.String())
		}
	})
	r.Group(func(r chi.Router) {
		r.Use(RequireLogin[*user]("/login?lang=zh"))
		r.Get("/private", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "secret") })
		r.Post("/private", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "secret") })
	})
	app.handler = r
	return app
}

func (app *sessionApp) do(method, path string, cookie *http.Cookie, hdr map[string]string) *httptest.ResponseRecorder {
	var body io.Reader
	if method == http.MethodPost && strings.HasPrefix(path, "/login?") {
		body = strings.NewReader(path[len("/login?"):])
		path = "/login"
	}
	req := httptest.NewRequest(method, path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			return c
		}
	}
	t.Fatalf("no session cookie in response: %v", rec.Header())
	return nil
}

func TestSessionLoginFlow(t *testing.T) {
	app := newSessionApp(t)

	// Anonymous session first, to check that Login renews the token.
	anon := sessionCookie(t, app.do("POST", "/visit", nil, nil))
	if body := app.do("GET", "/me", anon, nil).Body.String(); body != "" {
		t.Fatalf("anonymous /me = %q, want empty", body)
	}

	logged := sessionCookie(t, app.do("POST", "/login?id="+u1, anon, nil))
	if logged.Value == anon.Value {
		t.Error("Login must renew the session token")
	}
	if body := app.do("GET", "/me", logged, nil).Body.String(); body != u1 {
		t.Errorf("/me = %q, want u-1", body)
	}
	if rec := app.do("GET", "/private", logged, nil); rec.Code != 200 || rec.Body.String() != "secret" {
		t.Errorf("/private = %d %q", rec.Code, rec.Body)
	}

	app.do("POST", "/logout", logged, nil)
	if body := app.do("GET", "/me", logged, nil).Body.String(); body != "" {
		t.Errorf("/me after logout = %q, want empty", body)
	}
}

func TestLoadSessionErrors(t *testing.T) {
	t.Run("unauthenticated user is logged out", func(t *testing.T) {
		app := newSessionApp(t)
		cookie := sessionCookie(t, app.do("POST", "/login?id="+u1, nil, nil))

		app.loadErr = errors.Join(errors.New("user disabled"), ErrUnauthenticated)
		if rec := app.do("GET", "/me", cookie, nil); rec.Code != 200 || rec.Body.String() != "" {
			t.Errorf("/me = %d %q, want anonymous", rec.Code, rec.Body)
		}
		// The user ID was removed from the session: loading is no longer attempted.
		app.loadErr = errors.New("must not be called")
		if rec := app.do("GET", "/me", cookie, nil); rec.Code != 200 {
			t.Errorf("/me after removal = %d, want 200", rec.Code)
		}
	})

	t.Run("other errors respond 500 and log", func(t *testing.T) {
		app := newSessionApp(t)
		cookie := sessionCookie(t, app.do("POST", "/login?id="+u1, nil, nil))

		app.loadErr = errors.New("db down")
		if rec := app.do("GET", "/me", cookie, nil); rec.Code != 500 {
			t.Errorf("/me = %d, want 500", rec.Code)
		}
		if !strings.Contains(app.logs.String(), "db down") {
			t.Errorf("error not logged: %s", app.logs.String())
		}
	})
}

func TestRequireLogin(t *testing.T) {
	app := newSessionApp(t)

	t.Run("GET redirects with next", func(t *testing.T) {
		rec := app.do("GET", "/private?tab=a", nil, nil)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/login?lang=zh&next=%2Fprivate%3Ftab%3Da" {
			t.Errorf("Location = %q", loc)
		}
	})

	t.Run("POST redirects without next", func(t *testing.T) {
		rec := app.do("POST", "/private", nil, nil)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?lang=zh" {
			t.Errorf("got %d %q", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("HTMX gets HX-Redirect to the current page", func(t *testing.T) {
		rec := app.do("POST", "/private", nil, map[string]string{
			"HX-Request":     "true",
			"HX-Current-URL": "https://example.com/orders?page=2",
		})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
		if got := rec.Header().Get("HX-Redirect"); got != "/login?lang=zh&next=%2Forders%3Fpage%3D2" {
			t.Errorf("HX-Redirect = %q", got)
		}
		if rec.Header().Get("Location") != "" {
			t.Error("HTMX response must not use Location")
		}
	})
}
