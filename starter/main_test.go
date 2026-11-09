package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ryan053857/go-kit/app"
	"github.com/ryan053857/go-kit/database/dbtest"
)

// newTestApp 用临时数据库装配完整的服务，产品的集成测试可以照此编写。
func newTestApp(t *testing.T) *app.App {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Database.DSN = dbtest.DSN(t)
	cfg.Session.SecureCookie = false
	cfg.BaseURL = "http://test"
	cfg.Account.JWTSecret = "test-secret-0123456789abcdef0123456789"

	a, err := app.New(t.Context(), &cfg.Config, options(&cfg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// browser 模拟浏览器：保存 Cookie，可选带 Bearer token。
type browser struct {
	t      *testing.T
	h      http.Handler
	cookie *http.Cookie
	bearer string
}

func (b *browser) do(method, path, body string, header ...string) *httptest.ResponseRecorder {
	b.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	switch {
	case strings.HasPrefix(body, "{"):
		req.Header.Set("Content-Type", "application/json")
	case body != "":
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	if b.cookie != nil {
		req.AddCookie(b.cookie)
	}
	if b.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+b.bearer)
	}
	rec := httptest.NewRecorder()
	b.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		b.cookie = c
	}
	return rec
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, status int, contains ...string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d\n%s", rec.Code, status, rec.Body)
	}
	for _, s := range contains {
		if !strings.Contains(rec.Body.String(), s) {
			t.Fatalf("body does not contain %q:\n%s", s, rec.Body)
		}
	}
}

func TestAPI(t *testing.T) {
	a := newTestApp(t)
	c := &browser{t: t, h: a.Router}

	expect(t, c.do("POST", "/api/v1/auth/register", `{"email":"a@example.com","password":"password1","name":"A"}`), 201)
	rec := c.do("POST", "/api/v1/auth/login", `{"email":"a@example.com","password":"password1"}`)
	expect(t, rec, 200, "access_token")
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tok)

	expect(t, c.do("GET", "/api/v1/notes", ""), 401)
	c.bearer = tok.AccessToken
	expect(t, c.do("POST", "/api/v1/notes", `{"text":"hello"}`), 201)
	expect(t, c.do("GET", "/api/v1/notes", ""), 200, `"text":"hello"`)
	expect(t, c.do("GET", "/api/v1/me", ""), 200, `"email":"a@example.com"`)
}

func TestPages(t *testing.T) {
	a := newTestApp(t)
	c := &browser{t: t, h: a.Router}

	// 未登录访问首页跳转到登录页；静态资源可以访问。
	rec := c.do("GET", "/", "")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Fatalf("GET / = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	expect(t, c.do("GET", "/login", ""), 200, "忘记密码", "/assets/css/output.css")
	expect(t, c.do("GET", "/assets/css/output.css", ""), 200)

	// 注册后自动登录，首页显示提示。
	form := url.Values{"name": {"Alice"}, "email": {"alice@example.com"}, "password": {"password1"}}.Encode()
	expect(t, c.do("POST", "/register", form), http.StatusSeeOther)
	expect(t, c.do("GET", "/", ""), 200, "我的笔记", "Alice", "注册成功")
	expect(t, c.do("POST", "/register", form), 422, "该邮箱已注册")

	// HTMX 添加笔记：返回列表项片段；内容为空时弹出提示。
	rec = c.do("POST", "/notes", "text=from+page", "HX-Request", "true")
	expect(t, rec, 200, "from page")
	rec = c.do("POST", "/notes", "text=+", "HX-Request", "true")
	expect(t, rec, 200, "笔记内容不能为空")
	if rec.Header().Get("HX-Retarget") != "body" {
		t.Errorf("empty note should toast into body: %v", rec.Header())
	}

	// 账号设置：登录设备中有当前网页会话。
	expect(t, c.do("GET", "/settings", ""), 200, "登录设备", "当前设备", "alice@example.com")
	expect(t, c.do("POST", "/settings/password", "current_password=wrong&new_password=password2"), 422, "当前密码错误")
	expect(t, c.do("POST", "/settings/password", "current_password=password1&new_password=password2"), http.StatusSeeOther)

	// 退出后需要重新登录；新密码可以登录。
	expect(t, c.do("POST", "/logout", ""), http.StatusSeeOther)
	expect(t, c.do("GET", "/", ""), http.StatusSeeOther)
	expect(t, c.do("POST", "/login", "email=alice@example.com&password=password1"), 422, "邮箱或密码错误")
	expect(t, c.do("POST", "/login", "email=alice@example.com&password=password2&next=/settings"), http.StatusSeeOther)
	expect(t, c.do("GET", "/settings", ""), 200)
}

func TestForgotPasswordPage(t *testing.T) {
	a := newTestApp(t)
	c := &browser{t: t, h: a.Router}
	expect(t, c.do("GET", "/forgot-password", ""), 200, "发送重置链接")
	// 邮箱未注册也显示同样的结果。
	expect(t, c.do("POST", "/forgot-password", "email=nobody@example.com"), 200, "请查收邮件")
	expect(t, c.do("GET", "/reset-password?token=bogus", ""), 200, "重置密码")
	expect(t, c.do("POST", "/reset-password", "token=bogus&password=password9"), 422, "链接无效或已过期")
}
