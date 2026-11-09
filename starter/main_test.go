package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ryan053857/go-kit/app"
	"github.com/ryan053857/go-kit/database/dbtest"
	"github.com/ryan053857/go-kit/starter/migrations"
)

// newTestApp 用临时数据库装配完整的服务，产品的集成测试可以照此编写。
func newTestApp(t *testing.T) *app.App {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Database.DSN = dbtest.DSN(t)
	cfg.Session.SecureCookie = false
	cfg.Demo.Name, cfg.Demo.Password, cfg.Demo.APIToken = "admin", "admin", "test-token"

	a, err := app.New(t.Context(), &cfg.Config, app.Options{
		Name:       "starter",
		Migrations: migrations.FS,
		Setup:      func(a *app.App) error { return setup(a, &cfg) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestAPI(t *testing.T) {
	a := newTestApp(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notes", strings.NewReader(`{"text":"hello"}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/notes", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	a.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"text":"hello"`) {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	a.Router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/notes", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("list without token = %d", rec.Code)
	}
}

func TestPages(t *testing.T) {
	a := newTestApp(t)

	// 未登录访问首页跳转到登录页。
	rec := httptest.NewRecorder()
	a.Router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Fatalf("GET / = %d %s", rec.Code, rec.Header().Get("Location"))
	}

	form := url.Values{"name": {"admin"}, "password": {"admin"}, "next": {"/"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	a.Router.ServeHTTP(rec, req)
	cookies := rec.Result().Cookies()
	if rec.Code != http.StatusSeeOther || len(cookies) == 0 {
		t.Fatalf("login = %d, cookies %v", rec.Code, cookies)
	}

	// 登录后用 HTMX 表单添加笔记，返回 <li> 片段。
	req = httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader("text=from+page"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	a.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "from page") {
		t.Fatalf("add note = %d %s", rec.Code, rec.Body)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	a.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "from page") {
		t.Errorf("GET / after login = %d", rec.Code)
	}
}
