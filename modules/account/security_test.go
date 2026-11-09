package account

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/pquerna/otp/totp"
	"golang.org/x/oauth2"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/mail/mailtest"
	"github.com/ryan053857/go-kit/messaging"
)

// events 记录 Service 发出的事件类型。
type events struct {
	mu   sync.Mutex
	list []Event
}

func (r *events) record(_ context.Context, e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, e)
}

func (r *events) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.list {
		out = append(out, e.Type)
	}
	return out
}

func (e *env) runBus(t *testing.T) {
	t.Helper()
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = e.bus.Run(runCtx); close(done) }()
	t.Cleanup(func() { stop(); <-done })
	<-e.bus.Running()
}

func TestEmailVerification(t *testing.T) {
	e := newEnv(t)
	e.svc.cfg.VerifyEmail = true
	ctx := t.Context()
	ev := &events{}
	e.svc.OnEvent(ev.record)

	mailer := &mailtest.Recorder{}
	tokens := make(chan string, 4)
	err := messaging.Handle(e.bus, TopicEmailVerification, func(ctx context.Context, m EmailVerificationRequested) error {
		err := EmailVerificationMail{Mailer: mailer, URL: func(tok string) string { return "https://app.test/verify?token=" + tok }}.Handle(ctx, m)
		tokens <- m.Token
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	e.runBus(t)

	u := e.register(t, "a@example.com", "password1")
	if u.EmailVerified() {
		t.Fatal("verified right after register")
	}
	var token string
	select {
	case token = <-tokens:
	case <-time.After(10 * time.Second):
		t.Fatal("verification message not handled")
	}
	if sent := mailer.Sent(); len(sent) != 1 || !strings.Contains(sent[0].Text, token) || !strings.Contains(sent[0].HTML, "24 小时") {
		t.Fatalf("sent = %+v", sent)
	}

	if _, err := e.svc.VerifyEmail(ctx, token[:len(token)-2]+"AA"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("tampered token: %v", err)
	}
	if _, err := e.svc.VerifyEmail(ctx, "garbage"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("garbage token: %v", err)
	}
	got, err := e.svc.VerifyEmail(ctx, token)
	if err != nil || !got.EmailVerified() {
		t.Fatalf("verify = %+v, %v", got, err)
	}
	if _, err := e.svc.VerifyEmail(ctx, token); err != nil {
		t.Errorf("verify twice: %v", err)
	}
	// 已验证：重新发送什么也不做。
	if err := e.svc.RequestEmailVerification(ctx, u.ID); err != nil {
		t.Fatal(err)
	}

	// 过期与修改邮箱后失效。
	b := e.register(t, "b@example.com", "password1")
	tokB := e.svc.emailToken(b.ID, b.Email, e.clock.now().Add(time.Hour).Truncate(time.Second))
	if _, err := e.svc.db.NewUpdate().Model(b).Set("email = ?", "c@example.com").WherePK().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.VerifyEmail(ctx, tokB); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("email changed: %v", err)
	}
	tokB = e.svc.emailToken(b.ID, "c@example.com", e.clock.now().Add(time.Hour).Truncate(time.Second))
	e.clock.add(2 * time.Hour)
	if _, err := e.svc.VerifyEmail(ctx, tokB); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expired: %v", err)
	}

	if got := ev.types(); !slices.Equal(got, []string{EventRegistered, EventEmailVerified, EventRegistered}) {
		t.Errorf("events = %v", got)
	}
}

func totpCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestMFA(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	ev := &events{}
	e.svc.OnEvent(ev.record)
	u := e.register(t, "a@example.com", "password1")

	if st, _ := e.svc.MFAStatus(ctx, u.ID); st.Enabled {
		t.Fatal("enabled before setup")
	}
	if _, err := e.svc.EnableMFA(ctx, u.ID, "123456"); !errors.Is(err, ErrMFANotEnabled) {
		t.Errorf("enable without setup: %v", err)
	}
	setup, err := e.svc.SetupMFA(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(setup.URL, "otpauth://totp/go-kit:a@example.com?") {
		t.Errorf("url = %s", setup.URL)
	}
	if png, err := MFAQRCode(setup.URL, 200); err != nil || len(png) < 100 {
		t.Errorf("qr code: %d bytes, %v", len(png), err)
	}
	// 重新生成密钥：旧密钥作废。
	setup2, err := e.svc.SetupMFA(ctx, u.ID)
	if err != nil || setup2.Secret == setup.Secret {
		t.Fatalf("setup again = %v, %v", setup2, err)
	}
	if _, err := e.svc.EnableMFA(ctx, u.ID, totpCode(t, setup.Secret, e.clock.now())); !errors.Is(err, ErrInvalidMFACode) {
		t.Errorf("old secret: %v", err)
	}
	codes, err := e.svc.EnableMFA(ctx, u.ID, totpCode(t, setup2.Secret, e.clock.now()))
	if err != nil || len(codes) != 10 {
		t.Fatalf("enable = %v, %v", codes, err)
	}
	if _, err := e.svc.SetupMFA(ctx, u.ID); !errors.Is(err, ErrMFAEnabled) {
		t.Errorf("setup when enabled: %v", err)
	}

	// 登录需要两步验证。
	_, err = e.svc.Login(ctx, "a@example.com", "password1", true, ClientInfo{})
	mfaErr, ok := errors.AsType[*MFARequiredError](err)
	if !ok {
		t.Fatalf("login = %v", err)
	}
	// 同一个验证码（同一时间步）只能用一次：启用时已经用过。
	if _, err := e.svc.LoginMFA(ctx, mfaErr.Challenge, totpCode(t, setup2.Secret, e.clock.now()), ClientInfo{}); !errors.Is(err, ErrInvalidMFACode) {
		t.Errorf("replayed code: %v", err)
	}
	e.clock.add(30 * time.Second)
	tok, err := e.svc.LoginMFA(ctx, mfaErr.Challenge, totpCode(t, setup2.Secret, e.clock.now()), ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if !tok.RefreshExpiresAt.Equal(e.clock.now().Add(30 * 24 * time.Hour)) {
		t.Errorf("remember flag lost: %v", tok.RefreshExpiresAt)
	}
	if _, err := e.svc.LoginMFA(ctx, "bogus", "000000", ClientInfo{}); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("bogus challenge: %v", err)
	}
	// challenge 不能当作 access token。
	if _, err := e.svc.VerifyAccessToken(ctx, mfaErr.Challenge); err == nil {
		t.Error("challenge accepted as access token")
	}

	// 恢复码：可用一次，大小写与连字符不敏感。
	_, err = e.svc.Login(ctx, "a@example.com", "password1", false, ClientInfo{})
	mfaErr, _ = errors.AsType[*MFARequiredError](err)
	if _, err := e.svc.LoginMFA(ctx, mfaErr.Challenge, strings.ToUpper(strings.ReplaceAll(codes[0], "-", "")), ClientInfo{}); err != nil {
		t.Fatalf("recovery code: %v", err)
	}
	if err := e.svc.VerifyMFA(ctx, u.ID, codes[0]); !errors.Is(err, ErrInvalidMFACode) {
		t.Errorf("recovery code reused: %v", err)
	}
	if st, _ := e.svc.MFAStatus(ctx, u.ID); !st.Enabled || st.RecoveryCodes != 9 {
		t.Errorf("status = %+v", st)
	}
	newCodes, err := e.svc.RegenerateRecoveryCodes(ctx, u.ID, codes[1])
	if err != nil || len(newCodes) != 10 {
		t.Fatalf("regenerate = %v, %v", newCodes, err)
	}
	if err := e.svc.VerifyMFA(ctx, u.ID, codes[2]); !errors.Is(err, ErrInvalidMFACode) {
		t.Errorf("old recovery code after regenerate: %v", err)
	}

	e.clock.add(30 * time.Second)
	if err := e.svc.DisableMFA(ctx, u.ID, "000000"); !errors.Is(err, ErrInvalidMFACode) {
		t.Errorf("disable with wrong code: %v", err)
	}
	if err := e.svc.DisableMFA(ctx, u.ID, totpCode(t, setup2.Secret, e.clock.now())); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Login(ctx, "a@example.com", "password1", false, ClientInfo{}); err != nil {
		t.Errorf("login after disable: %v", err)
	}

	want := []string{EventRegistered, EventMFAEnabled, EventLoginFailed, EventLogin, EventLogin, EventMFADisabled, EventLogin}
	if got := ev.types(); !slices.Equal(got, want) {
		t.Errorf("events = %v\nwant %v", got, want)
	}
}

func TestAPIKeys(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	u := e.register(t, "a@example.com", "password1")

	k, token, err := e.svc.CreateAPIKey(ctx, u.ID, " CI ", []string{"notes:read"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "sk_") || k.Name != "CI" || k.Hint != token[len(token)-4:] {
		t.Errorf("key = %+v, token %q", k, token)
	}
	p, err := e.svc.VerifyAccessToken(ctx, token)
	if err != nil || p.ID != u.ID || !p.IsAPIKey() || !p.HasScope("notes:read") || p.HasScope("notes:write") {
		t.Fatalf("verify = %+v, %v", p, err)
	}
	if list, _ := e.svc.APIKeys(ctx, u.ID); len(list) != 1 || list[0].LastUsedAt.IsZero() {
		t.Errorf("list = %+v", list)
	}
	if _, err := e.svc.VerifyAccessToken(ctx, "sk_bogus"); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("bogus key: %v", err)
	}

	// 过期。
	_, expiring, _ := e.svc.CreateAPIKey(ctx, u.ID, "tmp", nil, e.clock.now().Add(time.Hour))
	e.clock.add(2 * time.Hour)
	if _, err := e.svc.VerifyAccessToken(ctx, expiring); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("expired key: %v", err)
	}
	// 账号停用后失效；删除后失效。
	_ = e.svc.SetDisabled(ctx, u.ID, true)
	if _, err := e.svc.VerifyAccessToken(ctx, token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("disabled user: %v", err)
	}
	_ = e.svc.SetDisabled(ctx, u.ID, false)
	if err := e.svc.RevokeAPIKey(ctx, u.ID, k.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RevokeAPIKey(ctx, u.ID, k.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoke twice: %v", err)
	}
	if _, err := e.svc.VerifyAccessToken(ctx, token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("revoked key: %v", err)
	}
}

// fakeProvider 是一个最小的 OAuth 2.0 授权服务器。
func fakeProvider(t *testing.T, user *OAuthUser) (OAuthProvider, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	var verifier string
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "pkce required", 400)
			return
		}
		u, _ := url.Parse(q.Get("redirect_uri"))
		u.RawQuery = url.Values{"code": {"the-code"}, "state": {q.Get("state")}}.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		verifier = r.PostForm.Get("code_verifier")
		if r.PostForm.Get("code") != "the-code" || verifier == "" {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer"}`))
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at" {
			http.Error(w, "unauthorized", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(user)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return OAuthProvider{
		Name: "fake", Label: "Fake",
		Config: oauth2.Config{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://app.test/auth/fake/callback",
			Endpoint: oauth2.Endpoint{AuthURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token"}},
		User: func(ctx context.Context, c *http.Client) (*OAuthUser, error) {
			ou := new(OAuthUser)
			return ou, getJSON(ctx, c, srv.URL+"/user", ou)
		},
	}, srv
}

func TestOAuthWeb(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	ou := &OAuthUser{Subject: "42", Email: "Gh@Example.com", EmailVerified: true, Name: "GH User"}
	provider, _ := fakeProvider(t, ou)

	sm := scs.New()
	o := &OAuthWeb{Users: e.svc, Sessions: sm, Providers: []OAuthProvider{provider}}
	r := chi.NewRouter()
	r.Use(sm.LoadAndSave, e.svc.LoadWebSession(sm))
	r.Get("/auth/{provider}", o.Start)
	r.Get("/auth/{provider}/callback", o.Callback)
	r.Get("/me", func(w http.ResponseWriter, r *http.Request) {
		if p, ok := auth.From[*Principal](r.Context()); ok {
			_, _ = w.Write([]byte(p.ID.String()))
		}
	})
	r.Post("/login", func(w http.ResponseWriter, r *http.Request) {
		u, err := e.svc.Authenticate(r.Context(), r.FormValue("email"), r.FormValue("password"))
		if err == nil {
			err = e.svc.WebLogin(r.Context(), sm, u, r)
		}
		if err != nil {
			http.Error(w, err.Error(), 401)
		}
	})
	r.Post("/mfa", func(w http.ResponseWriter, r *http.Request) {
		if err := e.svc.WebCompleteMFA(r.Context(), sm, r.FormValue("code"), r); err != nil {
			http.Error(w, err.Error(), 422)
		}
	})

	// login 走一遍完整的跳转，返回最终跳转地址与浏览器的 cookie。
	login := func(c *client, next string) string {
		t.Helper()
		rec := c.do("GET", "/auth/fake"+next, "")
		expect(t, rec, http.StatusFound)
		resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).
			Get(rec.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		cb, _ := url.Parse(resp.Header.Get("Location"))
		rec = c.do("GET", cb.RequestURI(), "")
		expect(t, rec, http.StatusFound)
		return rec.Header().Get("Location")
	}

	// 1. 新用户：自动注册，邮箱已验证，没有密码。
	c := &client{t: t, h: r}
	if loc := login(c, "?next=/notes"); loc != "/notes" {
		t.Fatalf("redirect = %s", loc)
	}
	uid := c.do("GET", "/me", "").Body.String()
	u, err := e.svc.Authenticate(ctx, "gh@example.com", "")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("oauth user should not log in with empty password: %v %v", u, err)
	}
	ids, _ := e.svc.Identities(ctx, mustParse(t, uid))
	if len(ids) != 1 || ids[0].Provider != "fake" || ids[0].Subject != "42" {
		t.Fatalf("identities = %+v", ids)
	}
	got, _ := e.svc.User(ctx, mustParse(t, uid))
	if !got.EmailVerified() || got.HasPassword() || got.Name != "GH User" {
		t.Errorf("user = %+v", got)
	}
	// 唯一的登录方式不能解绑；设置密码后可以。
	if err := e.svc.UnlinkIdentity(ctx, got.ID, ids[0].ID); !errors.Is(err, ErrLastLoginMethod) {
		t.Errorf("unlink last: %v", err)
	}
	if err := e.svc.ChangePassword(ctx, got.ID, mustParse(t, uid), "", "password1"); err != nil {
		t.Fatalf("set first password: %v", err)
	}

	// 2. 再次登录：同一个用户；开放重定向被忽略。
	c2 := &client{t: t, h: r}
	if loc := login(c2, "?next=//evil.com"); loc != "/" {
		t.Errorf("open redirect: %s", loc)
	}
	if c2.do("GET", "/me", "").Body.String() != uid {
		t.Error("second login is a different user")
	}

	// 3. 邮箱被未验证的账号占用：拒绝自动绑定。
	_ = e.register(t, "squat@example.com", "password1")
	ou.Subject, ou.Email = "43", "squat@example.com"
	if loc := login(&client{t: t, h: r}, ""); loc != "/login?error=email_taken" {
		t.Errorf("squatted email: %s", loc)
	}

	// 4. 已登录时绑定到当前账号；第三方账号已属于别人时拒绝。
	other := e.register(t, "other@example.com", "password1")
	c3 := &client{t: t, h: r}
	expect(t, c3.do("POST", "/login", "email=other@example.com&password=password1"), 200)
	ou.Subject, ou.Email = "44", "x@example.com"
	if loc := login(c3, "?next=/settings"); loc != "/settings" {
		t.Errorf("link redirect: %s", loc)
	}
	if ids, _ := e.svc.Identities(ctx, other.ID); len(ids) != 1 || ids[0].Subject != "44" {
		t.Errorf("linked = %+v", ids)
	}
	ou.Subject = "42"
	if loc := login(c3, ""); loc != "/login?error=identity_taken" {
		t.Errorf("identity taken: %s", loc)
	}

	// 5. 启用了两步验证：跳转到验证码页面，完成后才登录。
	setup, _ := e.svc.SetupMFA(ctx, other.ID)
	if _, err := e.svc.EnableMFA(ctx, other.ID, totpCode(t, setup.Secret, e.clock.now())); err != nil {
		t.Fatal(err)
	}
	ou.Subject = "44"
	c4 := &client{t: t, h: r}
	if loc := login(c4, ""); loc != "/login/mfa" {
		t.Fatalf("mfa redirect: %s", loc)
	}
	if c4.do("GET", "/me", "").Body.String() != "" {
		t.Error("logged in before mfa")
	}
	expect(t, c4.do("POST", "/mfa", "code=000000"), 422)
	e.clock.add(30 * time.Second)
	expect(t, c4.do("POST", "/mfa", "code="+totpCode(t, setup.Secret, e.clock.now())), 200)
	if c4.do("GET", "/me", "").Body.String() != other.ID.String() {
		t.Error("not logged in after mfa")
	}

	// 6. state 不匹配。
	if loc := c4.do("GET", "/auth/fake/callback?code=x&state=forged", "").Header().Get("Location"); loc != "/login?error=failed" {
		t.Errorf("forged state: %s", loc)
	}
}

func TestSecurityEndpoints(t *testing.T) {
	e := newEnv(t)
	sm := scs.New()
	r := chi.NewRouter()
	r.Use(sm.LoadAndSave, e.svc.LoadWebSession(sm))
	api := humachi.New(r, huma.DefaultConfig("test", "1"))
	api.UseMiddleware(rest.Authorize(api, e.svc.VerifyAccessToken))
	rest.Post(api, "/auth/login", LoginEndpoint(e.svc))
	rest.Post(api, "/auth/login/mfa", LoginMFAEndpoint(e.svc))
	rest.Get(api, "/me", MeEndpoint(e.svc))
	rest.Post(api, "/me/password", ChangePasswordEndpoint(e.svc))
	rest.Post(api, "/me/mfa/setup", SetupMFAEndpoint(e.svc))
	rest.Post(api, "/me/mfa/enable", EnableMFAEndpoint(e.svc))
	rest.Get(api, "/me/mfa", MFAStatusEndpoint(e.svc))
	rest.Get(api, "/me/api-keys", APIKeysEndpoint(e.svc))
	rest.Post(api, "/me/api-keys", CreateAPIKeyEndpoint(e.svc))
	rest.Delete(api, "/me/api-keys/{id}", RevokeAPIKeyEndpoint(e.svc))
	rest.Get(api, "/notes", rest.Endpoint[struct{}, struct{}]{
		Operation: huma.Operation{Security: rest.RequireAuth("notes:read")},
		Handler:   func(context.Context, *struct{}) (*struct{}, error) { return nil, nil },
	})
	rest.Post(api, "/notes", rest.Endpoint[struct{}, struct{}]{
		Operation: huma.Operation{Security: rest.RequireAuth("notes:write")},
		Handler:   func(context.Context, *struct{}) (*struct{}, error) { return nil, nil },
	})

	e.register(t, "a@example.com", "password1")
	c := &client{t: t, h: r}
	rec := c.do("POST", "/auth/login", `{"email":"a@example.com","password":"password1"}`)
	expect(t, rec, 200)
	var login LoginDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &login)
	c.bearer = login.AccessToken

	// API Key：scope 限制，不能访问敏感接口。
	rec = c.do("POST", "/me/api-keys", `{"name":"CI","scopes":["notes:read"]}`)
	expect(t, rec, 201)
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	keyClient := &client{t: t, h: r, bearer: created.Token}
	expect(t, keyClient.do("GET", "/notes", ""), 204)
	expect(t, keyClient.do("POST", "/notes", ""), 403)
	expect(t, keyClient.do("GET", "/me", ""), 200)
	expect(t, keyClient.do("POST", "/me/api-keys", `{"name":"escalate"}`), 403)
	expect(t, keyClient.do("POST", "/me/password", `{"current_password":"password1","new_password":"password2"}`), 403)
	expect(t, c.do("GET", "/notes", ""), 204) // access token 拥有全部 scope
	if body := c.do("GET", "/me/api-keys", "").Body.String(); !strings.Contains(body, `"name":"CI"`) || strings.Contains(body, created.Token) {
		t.Errorf("list = %s", body)
	}
	expect(t, c.do("DELETE", "/me/api-keys/"+created.ID, ""), 204)
	expect(t, keyClient.do("GET", "/notes", ""), 401)

	// 两步验证。
	rec = c.do("POST", "/me/mfa/setup", "")
	expect(t, rec, 200)
	var setup struct{ Secret string }
	_ = json.Unmarshal(rec.Body.Bytes(), &setup)
	expect(t, c.do("POST", "/me/mfa/enable", `{"code":"000000"}`), 422)
	rec = c.do("POST", "/me/mfa/enable", `{"code":"`+totpCode(t, setup.Secret, e.clock.now())+`"}`)
	expect(t, rec, 200)
	if !strings.Contains(c.do("GET", "/me/mfa", "").Body.String(), `"recovery_codes":10`) {
		t.Error("mfa status")
	}

	anon := &client{t: t, h: r}
	rec = anon.do("POST", "/auth/login", `{"email":"a@example.com","password":"password1"}`)
	expect(t, rec, 200)
	login = LoginDTO{}
	_ = json.Unmarshal(rec.Body.Bytes(), &login)
	if !login.MFARequired || login.MFAToken == "" || login.AccessToken != "" {
		t.Fatalf("login = %s", rec.Body)
	}
	e.clock.add(30 * time.Second)
	expect(t, anon.do("POST", "/auth/login/mfa", `{"mfa_token":"`+login.MFAToken+`","code":"000000"}`), 422)
	rec = anon.do("POST", "/auth/login/mfa", `{"mfa_token":"`+login.MFAToken+`","code":"`+totpCode(t, setup.Secret, e.clock.now())+`"}`)
	expect(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"access_token"`) {
		t.Errorf("mfa login = %s", rec.Body)
	}
}

func mustParse(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return id
}
