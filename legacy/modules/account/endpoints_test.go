package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"uuid"

	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/i18n"
)

// registerInput 是产品定义的注册输入。
type registerInput struct {
	Body struct {
		Email    string `json:"email" format:"email"`
		Password string `json:"password"`
		Name     string `json:"name,omitempty"`
	}
}

// newServer 按包文档的推荐方式挂载全部接口与一个网页登录表单。
func newServer(t *testing.T, e *env) http.Handler {
	t.Helper()
	sm := scs.New()
	r := chi.NewRouter()
	r.Use(i18n.Middleware, sm.LoadAndSave, e.svc.LoadWebSession(sm))

	api := humachi.New(r, huma.DefaultConfig("test", "1"))
	api.UseMiddleware(rest.Authorize(api, e.svc.VerifyAccessToken, nil))
	v1 := huma.NewGroup(api, "/api/v1")
	rest.Post(v1, "/auth/register", RegisterEndpoint(e.svc, func(ctx context.Context, tx bun.Tx, in *registerInput) (uuid.UUID, error) {
		if err := e.svc.CheckPassword(in.Body.Password); err != nil {
			return uuid.Nil(), err
		}
		u := &User{}
		u.Email, u.PasswordHash = NormalizeEmail(in.Body.Email), HashPassword(in.Body.Password)
		_, err := tx.NewInsert().Model(u).Exec(ctx)
		return u.ID, err
	}))
	rest.Post(v1, "/auth/login", LoginEndpoint(e.svc))
	rest.Post(v1, "/auth/refresh", RefreshEndpoint(e.svc))
	rest.Post(v1, "/auth/logout", LogoutEndpoint(e.svc))
	rest.Post(v1, "/auth/forgot-password", ForgotPasswordEndpoint(e.svc))
	rest.Post(v1, "/auth/reset-password", ResetPasswordEndpoint(e.svc))
	rest.Put(v1, "/me/locale", SetLocaleEndpoint(e.svc))
	rest.Post(v1, "/me/password", ChangePasswordEndpoint(e.svc))
	rest.Get(v1, "/me/sessions", SessionsEndpoint(e.svc))
	rest.Delete(v1, "/me/sessions/{id}", RevokeSessionEndpoint(e.svc))

	r.Post("/login", web.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		u, err := e.svc.Authenticate(r.Context(), r.FormValue("email"), r.FormValue("password"))
		if err != nil {
			return huma.Error401Unauthorized("bad credentials")
		}
		return e.svc.WebLogin(r.Context(), sm, u.ID, r)
	}).ServeHTTP)
	r.Post("/logout", web.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		return e.svc.WebLogout(r.Context(), sm)
	}).ServeHTTP)
	r.With(auth.RequireLogin[*Principal]("/login")).Get("/private", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("private"))
	})
	return r
}

type client struct {
	t      *testing.T
	h      http.Handler
	bearer string
	cookie *http.Cookie
}

func (c *client) do(method, path, body string, header ...string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	if strings.HasPrefix(body, "{") {
		req.Header.Set("Content-Type", "application/json")
	} else if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		c.cookie = ck
	}
	return rec
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body)
	}
}

func TestEndpointsAPI(t *testing.T) {
	e := newEnv(t)
	c := &client{t: t, h: newServer(t, e)}

	expect(t, c.do("POST", "/api/v1/auth/register", `{"email":"a@example.com","password":"password1","name":"A"}`), 201)
	expect(t, c.do("POST", "/api/v1/auth/register", `{"email":"a@example.com","password":"password1"}`), 409)
	rec := c.do("POST", "/api/v1/auth/register", `{"email":"b@example.com","password":"short"}`)
	expect(t, rec, 422)
	if !strings.Contains(rec.Body.String(), `"code":"account.weak_password"`) || !strings.Contains(rec.Body.String(), "密码至少 8 位") {
		t.Errorf("weak password = %s", rec.Body)
	}
	rec = c.do("POST", "/api/v1/auth/login", `{"email":"a@example.com","password":"nope"}`, "Accept-Language", "en")
	expect(t, rec, 401)
	if !strings.Contains(rec.Body.String(), "Incorrect email or password.") {
		t.Errorf("localized error = %s", rec.Body)
	}

	rec = c.do("POST", "/api/v1/auth/login", `{"email":"a@example.com","password":"password1"}`, "Accept-Language", "en")
	expect(t, rec, 200)
	var tok TokensDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &tok)
	if tok.TokenType != "Bearer" || tok.AccessToken == "" || tok.RefreshToken == "" || tok.ExpiresIn < 890 {
		t.Fatalf("tokens = %+v", tok)
	}

	expect(t, c.do("GET", "/api/v1/me/sessions", ""), 401)
	c.bearer = tok.AccessToken
	expect(t, c.do("PUT", "/api/v1/me/locale", `{"locale":"en-US"}`), 204)
	if u, _ := e.svc.Authenticate(t.Context(), "a@example.com", "password1"); u.Locale != "en" {
		t.Errorf("locale = %q", u.Locale)
	}

	rec = c.do("POST", "/api/v1/auth/refresh", `{"refresh_token":"`+tok.RefreshToken+`"}`)
	expect(t, rec, 200)
	expect(t, c.do("POST", "/api/v1/auth/refresh", `{"refresh_token":"bogus"}`), 401)

	rec = c.do("GET", "/api/v1/me/sessions", "")
	expect(t, rec, 200)
	var sessions []SessionDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &sessions)
	if len(sessions) != 1 || !sessions[0].Current || sessions[0].Kind != KindAPI {
		t.Errorf("sessions = %+v", sessions)
	}

	expect(t, c.do("POST", "/api/v1/me/password", `{"current_password":"nope","new_password":"password2"}`), 422)
	expect(t, c.do("POST", "/api/v1/me/password", `{"current_password":"password1","new_password":"password2"}`), 204)
	expect(t, c.do("DELETE", "/api/v1/me/sessions/"+sessions[0].ID, ""), 204)
	expect(t, c.do("DELETE", "/api/v1/me/sessions/"+sessions[0].ID, ""), 404)
	expect(t, c.do("POST", "/api/v1/auth/logout", ""), 204)

	// 未注册的邮箱同样返回 202。
	c.bearer = ""
	expect(t, c.do("POST", "/api/v1/auth/forgot-password", `{"email":"nobody@example.com"}`), 202)
	expect(t, c.do("POST", "/api/v1/auth/reset-password", `{"token":"bogus","new_password":"password3"}`), 400)
}

func TestEndpointsWebSession(t *testing.T) {
	e := newEnv(t)
	e.register(t, "a@example.com", "password1")
	c := &client{t: t, h: newServer(t, e)}

	expect(t, c.do("GET", "/private", ""), http.StatusSeeOther)
	form := url.Values{"email": {"a@example.com"}, "password": {"password1"}}.Encode()
	expect(t, c.do("POST", "/login", form), 200)
	expect(t, c.do("GET", "/private", ""), 200)

	// 网页会话同样可以访问需要登录的 API，并出现在登录设备中。
	rec := c.do("GET", "/api/v1/me/sessions", "")
	expect(t, rec, 200)
	var sessions []SessionDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &sessions)
	if len(sessions) != 1 || sessions[0].Kind != KindWeb || !sessions[0].Current {
		t.Fatalf("sessions = %+v", sessions)
	}

	// 从另一台设备（App）踢掉网页会话后，网页立即变为未登录。
	app := &client{t: t, h: c.h}
	rec = app.do("POST", "/api/v1/auth/login", `{"email":"a@example.com","password":"password1"}`)
	var tok TokensDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &tok)
	app.bearer = tok.AccessToken
	expect(t, app.do("DELETE", "/api/v1/me/sessions/"+sessions[0].ID, ""), 204)
	expect(t, c.do("GET", "/private", ""), http.StatusSeeOther)

	// 重新登录后退出。
	expect(t, c.do("POST", "/login", form), 200)
	expect(t, c.do("GET", "/private", ""), 200)
	expect(t, c.do("POST", "/logout", ""), 200)
	expect(t, c.do("GET", "/private", ""), http.StatusSeeOther)
}
