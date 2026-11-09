package account

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/httpx/web"
)

// newServer 按包文档的推荐方式挂载全部接口与一个网页登录表单。
func newServer(t *testing.T, e *env) http.Handler {
	t.Helper()
	sm := scs.New()
	r := chi.NewRouter()
	r.Use(sm.LoadAndSave, e.svc.LoadWebSession(sm))

	api := humachi.New(r, huma.DefaultConfig("test", "1"))
	api.UseMiddleware(rest.Authorize(api, e.svc.VerifyAccessToken))
	v1 := huma.NewGroup(api, "/api/v1")
	rest.Post(v1, "/auth/register", RegisterEndpoint(e.svc))
	rest.Post(v1, "/auth/login", LoginEndpoint(e.svc))
	rest.Post(v1, "/auth/refresh", RefreshEndpoint(e.svc))
	rest.Post(v1, "/auth/logout", LogoutEndpoint(e.svc))
	rest.Post(v1, "/auth/forgot-password", ForgotPasswordEndpoint(e.svc))
	rest.Post(v1, "/auth/reset-password", ResetPasswordEndpoint(e.svc))
	rest.Get(v1, "/me", MeEndpoint(e.svc))
	rest.Patch(v1, "/me", UpdateMeEndpoint(e.svc))
	rest.Post(v1, "/me/password", ChangePasswordEndpoint(e.svc))
	rest.Get(v1, "/me/sessions", SessionsEndpoint(e.svc))
	rest.Delete(v1, "/me/sessions/{id}", RevokeSessionEndpoint(e.svc))

	r.Post("/login", web.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		u, err := e.svc.Authenticate(r.Context(), r.FormValue("email"), r.FormValue("password"))
		if err != nil {
			return huma.Error401Unauthorized("bad credentials")
		}
		return e.svc.WebLogin(r.Context(), sm, u, r)
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

func (c *client) do(method, path, body string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
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
	expect(t, c.do("POST", "/api/v1/auth/register", `{"email":"b@example.com","password":"short"}`), 422)
	expect(t, c.do("POST", "/api/v1/auth/login", `{"email":"a@example.com","password":"nope"}`), 401)

	rec := c.do("POST", "/api/v1/auth/login", `{"email":"a@example.com","password":"password1"}`)
	expect(t, rec, 200)
	var tok TokensDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &tok)
	if tok.TokenType != "Bearer" || tok.AccessToken == "" || tok.RefreshToken == "" || tok.ExpiresIn < 890 {
		t.Fatalf("tokens = %+v", tok)
	}

	expect(t, c.do("GET", "/api/v1/me", ""), 401)
	c.bearer = tok.AccessToken
	rec = c.do("GET", "/api/v1/me", "")
	expect(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"email":"a@example.com"`) {
		t.Errorf("me = %s", rec.Body)
	}
	rec = c.do("PATCH", "/api/v1/me", `{"name":"Alice"}`)
	expect(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"name":"Alice"`) {
		t.Errorf("update me = %s", rec.Body)
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
