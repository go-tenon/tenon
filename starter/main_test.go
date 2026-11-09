package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
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
	cfg.Storage.Local.Dir = t.TempDir()

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

// multipartBody 返回只包含一个文件字段的 multipart 请求体与 Content-Type。
func multipartBody(t *testing.T, field, filename string, data []byte) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile(field, filename)
	_, _ = fw.Write(data)
	_ = mw.Close()
	return buf.String(), mw.FormDataContentType()
}

func pngBytes() []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 64, 64)))
	return buf.Bytes()
}

func TestAvatarExportAndFiles(t *testing.T) {
	a := newTestApp(t)
	c := &browser{t: t, h: a.Router}
	form := url.Values{"name": {"Alice"}, "email": {"alice@example.com"}, "password": {"password1"}}.Encode()
	expect(t, c.do("POST", "/register", form), http.StatusSeeOther)

	// 账号设置页上传头像，页头显示头像。
	body, ct := multipartBody(t, "avatar", "me.png", pngBytes())
	expect(t, c.do("POST", "/settings/avatar", body, "Content-Type", ct), http.StatusSeeOther)
	rec := c.do("GET", "/settings", "")
	expect(t, rec, 200, "头像已更新", `src="/avatars/`)
	src := rec.Body.String()[strings.Index(rec.Body.String(), `src="/avatars/`)+5:]
	src = src[:strings.Index(src, `"`)]
	rec = c.do("GET", src, "")
	expect(t, rec, 200)
	if rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("avatar content type = %s", rec.Header().Get("Content-Type"))
	}
	body, ct = multipartBody(t, "avatar", "x.txt", []byte("not an image"))
	expect(t, c.do("POST", "/settings/avatar", body, "Content-Type", ct), http.StatusSeeOther)
	expect(t, c.do("GET", "/settings", ""), 200, "请上传 JPEG")

	// 导出笔记。
	expect(t, c.do("POST", "/notes", "text=%3D1%2B1", "HX-Request", "true"), 200)
	rec = c.do("GET", "/notes/export?format=csv", "")
	expect(t, rec, 200, "内容,创建时间", "'=1+1")
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	rec = c.do("GET", "/notes/export?format=xlsx", "")
	expect(t, rec, 200)
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("PK")) {
		t.Error("xlsx is not a zip file")
	}
	expect(t, c.do("GET", "/notes/export?format=pdf", ""), 400)

	// App：用 access token 上传文件，再用同一个 token 下载；其他用户下载不到。
	app := &browser{t: t, h: a.Router}
	rec = app.do("POST", "/api/v1/auth/login", `{"email":"alice@example.com","password":"password1"}`)
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tok)
	app.bearer = tok.AccessToken
	body, ct = multipartBody(t, "file", "notes.txt", []byte("hello file"))
	rec = app.do("POST", "/api/v1/files", body, "Content-Type", ct)
	expect(t, rec, 201, `"filename":"notes.txt"`)
	var f struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &f)
	expect(t, app.do("GET", f.URL, ""), 200, "hello file")

	other := &browser{t: t, h: a.Router}
	expect(t, other.do("POST", "/api/v1/auth/register", `{"email":"bob@example.com","password":"password1"}`), 201)
	rec = other.do("POST", "/api/v1/auth/login", `{"email":"bob@example.com","password":"password1"}`)
	_ = json.Unmarshal(rec.Body.Bytes(), &tok)
	other.bearer = tok.AccessToken
	expect(t, other.do("GET", f.URL, ""), 404)
}
