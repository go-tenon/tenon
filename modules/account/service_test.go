package account

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/database/dbtest"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/mail/mailtest"
	"github.com/ryan053857/go-kit/messaging"
)

const testSecret = "0123456789abcdef0123456789abcdef"

// clock 是可以手动拨动的时钟。
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type env struct {
	db    *bun.DB
	svc   *Service
	clock *clock
	bus   *messaging.Bus
}

// newEnv 创建临时数据库、发布并执行本模块的迁移，返回使用可控时钟的 Service。
func newEnv(t *testing.T) *env {
	t.Helper()
	cfg := database.DefaultConfig()
	cfg.DSN = dbtest.DSN(t)
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	dir := t.TempDir()
	if _, err := database.PublishMigrations(dir, Migrations); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), db.DB, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}

	acfg := DefaultConfig()
	acfg.JWTSecret = testSecret
	svc, err := NewService(db, acfg)
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Now().Truncate(time.Microsecond)}
	svc.now = c.now

	bus, err := messaging.New(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	return &env{db: db, svc: svc, clock: c, bus: bus}
}

func (e *env) register(t *testing.T, email, pw string) *User {
	t.Helper()
	u, err := e.svc.Register(t.Context(), email, pw, "Tester")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestNewServiceRequiresSecret(t *testing.T) {
	if _, err := NewService(nil, DefaultConfig()); err == nil {
		t.Error("empty secret accepted")
	}
}

func TestRegisterAndAuthenticate(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	u := e.register(t, "  Alice@Example.com ", "password1")
	if u.Email != "alice@example.com" || u.Name != "Tester" || u.PasswordHash == "password1" {
		t.Errorf("user = %+v", u)
	}
	if _, err := e.svc.Register(ctx, "ALICE@example.com", "password1", ""); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("duplicate email: %v", err)
	}
	if _, err := e.svc.Register(ctx, "bob@example.com", "short", ""); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("weak password: %v", err)
	}

	got, err := e.svc.Authenticate(ctx, "alice@EXAMPLE.com", "password1")
	if err != nil || got.ID != u.ID || got.LastLoginAt.IsZero() {
		t.Fatalf("authenticate = %+v, %v", got, err)
	}
	if _, err := e.svc.Authenticate(ctx, "alice@example.com", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong password: %v", err)
	}
	if _, err := e.svc.Authenticate(ctx, "nobody@example.com", "password1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("unknown email: %v", err)
	}

	if err := e.svc.SetDisabled(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(ctx, "alice@example.com", "password1"); !errors.Is(err, ErrUserDisabled) {
		t.Errorf("disabled: %v", err)
	}
	if _, err := e.svc.Authenticate(ctx, "alice@example.com", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("disabled with wrong password must not reveal status: %v", err)
	}
}

func TestLoginRefreshAndVerify(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	u := e.register(t, "a@example.com", "password1")

	tok, err := e.svc.Login(ctx, "a@example.com", "password1", false, ClientInfo{IP: "1.2.3.4", UserAgent: "test"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.VerifyAccessToken(ctx, tok.AccessToken)
	if err != nil || p.ID != u.ID || p.SessionID != tok.SessionID {
		t.Fatalf("verify = %+v, %v", p, err)
	}
	if !tok.RefreshExpiresAt.Equal(e.clock.now().Add(7 * 24 * time.Hour)) {
		t.Errorf("refresh expires at %v", tok.RefreshExpiresAt)
	}

	// access token 过期。
	e.clock.add(16 * time.Minute)
	if _, err := e.svc.VerifyAccessToken(ctx, tok.AccessToken); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("expired access token: %v", err)
	}
	if _, err := e.svc.VerifyAccessToken(ctx, "garbage"); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("garbage token: %v", err)
	}

	// 刷新：轮换 refresh token，会话不变。
	tok2, err := e.svc.Refresh(ctx, tok.RefreshToken, ClientInfo{IP: "5.6.7.8"})
	if err != nil {
		t.Fatal(err)
	}
	if tok2.SessionID != tok.SessionID || tok2.RefreshToken == tok.RefreshToken {
		t.Errorf("refresh = %+v", tok2)
	}
	if _, err := e.svc.VerifyAccessToken(ctx, tok2.AccessToken); err != nil {
		t.Errorf("new access token: %v", err)
	}

	// 宽限期内重试旧 token：再次轮换。
	e.clock.add(10 * time.Second)
	tok3, err := e.svc.Refresh(ctx, tok.RefreshToken, ClientInfo{})
	if err != nil {
		t.Fatalf("retry within grace: %v", err)
	}
	// 宽限期后再用旧 token：视为泄露，整个会话注销，最新的 token 也失效。
	e.clock.add(time.Minute)
	if _, err := e.svc.Refresh(ctx, tok.RefreshToken, ClientInfo{}); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("reuse after grace: %v", err)
	}
	if _, err := e.svc.Refresh(ctx, tok3.RefreshToken, ClientInfo{}); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("session should be revoked after reuse: %v", err)
	}
	if list, _ := e.svc.Sessions(ctx, u.ID); len(list) != 0 {
		t.Errorf("sessions = %d", len(list))
	}

	// refresh token 过期。
	tok, _ = e.svc.Login(ctx, "a@example.com", "password1", false, ClientInfo{})
	e.clock.add(8 * 24 * time.Hour)
	if _, err := e.svc.Refresh(ctx, tok.RefreshToken, ClientInfo{}); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expired refresh token: %v", err)
	}

	// 记住我：有效期更长；账号停用后无法刷新。
	tok, _ = e.svc.Login(ctx, "a@example.com", "password1", true, ClientInfo{})
	e.clock.add(8 * 24 * time.Hour)
	if _, err := e.svc.Refresh(ctx, tok.RefreshToken, ClientInfo{}); err != nil {
		t.Errorf("remember me refresh: %v", err)
	}
	tok, _ = e.svc.Login(ctx, "a@example.com", "password1", false, ClientInfo{})
	_, _ = e.db.NewUpdate().Model((*User)(nil)).Set("disabled_at = now()").Where("id = ?", u.ID).Exec(ctx)
	if _, err := e.svc.Refresh(ctx, tok.RefreshToken, ClientInfo{}); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("disabled user refresh: %v", err)
	}
}

func TestRevokeAndCheckSession(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	u := e.register(t, "a@example.com", "password1")
	a, _ := e.svc.Login(ctx, "a@example.com", "password1", false, ClientInfo{})
	b, _ := e.svc.Login(ctx, "a@example.com", "password1", false, ClientInfo{})

	list, err := e.svc.Sessions(ctx, u.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("sessions = %d, %v", len(list), err)
	}
	if err := e.svc.RevokeSession(ctx, u.ID, b.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RevokeSession(ctx, u.ID, b.SessionID); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoke twice: %v", err)
	}
	if _, err := e.svc.Refresh(ctx, b.RefreshToken, ClientInfo{}); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("refresh revoked session: %v", err)
	}

	// 默认不查库：被注销会话的 access token 在过期前仍然有效。
	if _, err := e.svc.VerifyAccessToken(ctx, b.AccessToken); err != nil {
		t.Errorf("without CheckSession: %v", err)
	}
	e.svc.cfg.CheckSession = true
	if _, err := e.svc.VerifyAccessToken(ctx, b.AccessToken); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("with CheckSession: %v", err)
	}
	if _, err := e.svc.VerifyAccessToken(ctx, a.AccessToken); err != nil {
		t.Errorf("active session with CheckSession: %v", err)
	}

	// 修改密码：保留当前会话，注销其他会话。
	c, _ := e.svc.Login(ctx, "a@example.com", "password1", false, ClientInfo{})
	if err := e.svc.ChangePassword(ctx, u.ID, a.SessionID, "wrong", "password2"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong current password: %v", err)
	}
	if err := e.svc.ChangePassword(ctx, u.ID, a.SessionID, "password1", "password2"); err != nil {
		t.Fatal(err)
	}
	list, _ = e.svc.Sessions(ctx, u.ID)
	if len(list) != 1 || list[0].ID != a.SessionID {
		t.Errorf("sessions after change password = %+v", list)
	}
	if _, err := e.svc.Refresh(ctx, c.RefreshToken, ClientInfo{}); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("other session still valid: %v", err)
	}
	if _, err := e.svc.Authenticate(ctx, "a@example.com", "password2"); err != nil {
		t.Errorf("new password: %v", err)
	}
}

func TestPasswordReset(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	u := e.register(t, "a@example.com", "password1")
	sess, _ := e.svc.Login(ctx, "a@example.com", "password1", false, ClientInfo{})

	mailer := &mailtest.Recorder{}
	tokens := make(chan string, 4)
	err := messaging.Handle(e.bus, TopicPasswordReset, func(ctx context.Context, ev PasswordResetRequested) error {
		tokens <- ev.Token
		return PasswordResetMail{Mailer: mailer, URL: func(tok string) string { return "https://app.test/reset?token=" + tok }}.Handle(ctx, ev)
	})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = e.bus.Run(runCtx); close(done) }()
	t.Cleanup(func() { stop(); <-done })
	<-e.bus.Running()

	// 未注册的邮箱：静默成功，不发邮件。
	if err := e.svc.RequestPasswordReset(ctx, "nobody@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RequestPasswordReset(ctx, "A@example.com"); err != nil {
		t.Fatal(err)
	}
	// 一分钟内重复请求被忽略。
	if err := e.svc.RequestPasswordReset(ctx, "a@example.com"); err != nil {
		t.Fatal(err)
	}

	var token string
	select {
	case token = <-tokens:
	case <-time.After(10 * time.Second):
		t.Fatal("reset message not handled")
	}
	select {
	case <-tokens:
		t.Error("throttled request sent a second mail")
	case <-time.After(1500 * time.Millisecond):
	}
	sent := mailer.Sent()
	if len(sent) != 1 || sent[0].To[0] != u.Email || !strings.Contains(sent[0].Text, "https://app.test/reset?token="+token) {
		t.Fatalf("sent = %+v", sent)
	}
	if !strings.Contains(sent[0].HTML, "60 分钟") {
		t.Errorf("html = %s", sent[0].HTML)
	}

	if err := e.svc.ResetPassword(ctx, token, "short"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("weak password: %v", err)
	}
	if err := e.svc.ResetPassword(ctx, "bogus", "password2"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("bogus token: %v", err)
	}
	if err := e.svc.ResetPassword(ctx, token, "password2"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ResetPassword(ctx, token, "password3"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("token reused: %v", err)
	}
	if _, err := e.svc.Authenticate(ctx, "a@example.com", "password2"); err != nil {
		t.Errorf("new password: %v", err)
	}
	if _, err := e.svc.Refresh(ctx, sess.RefreshToken, ClientInfo{}); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("sessions should be revoked after reset: %v", err)
	}

	// 过期的令牌。
	e.clock.add(2 * time.Minute)
	if err := e.svc.RequestPasswordReset(ctx, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	select {
	case token = <-tokens:
	case <-time.After(10 * time.Second):
		t.Fatal("second reset message not handled")
	}
	e.clock.add(2 * time.Hour)
	if err := e.svc.ResetPassword(ctx, token, "password3"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expired token: %v", err)
	}
}
