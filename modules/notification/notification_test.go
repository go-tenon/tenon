package notification_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database/dbtest"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/i18n"
	"github.com/ryan053857/go-kit/mail"
	"github.com/ryan053857/go-kit/mail/mailtest"
	"github.com/ryan053857/go-kit/messaging"
	"github.com/ryan053857/go-kit/modules/notification"
)

func TestMain(m *testing.M) {
	huma.NewErrorWithContext = rest.NewErrorWithContext
	i18n.MustLoad(fstest.MapFS{
		"zh-CN.yaml": {Data: []byte("test.shipped: \"订单 {{.No}} 已发货\"\n")},
		"en.yaml":    {Data: []byte("test.shipped: \"Order {{.No}} shipped\"\n")},
	})
	os.Exit(m.Run())
}

// shipped 是产品定义的通知：站内信 + 邮件。
type shipped struct {
	No string `json:"no"`
}

func (shipped) Type() string { return "test.shipped" }

func (n shipped) ToDatabase(ctx context.Context, r notification.Recipient) (notification.Content, error) {
	return notification.Content{Title: i18n.T(ctx, "test.shipped", map[string]any{"No": n.No}), URL: "/orders/" + n.No}, nil
}

func (n shipped) ToMail(ctx context.Context, r notification.Recipient) (mail.Message, error) {
	return mail.Message{Subject: i18n.T(ctx, "test.shipped", map[string]any{"No": n.No}), Text: "hi " + r.Name}, nil
}

// inAppOnly 只发站内信（Via 筛选）。
type inAppOnly struct{ shipped }

func (inAppOnly) Type() string { return "test.in_app" }

func (inAppOnly) Via(notification.Recipient) []string { return []string{"database"} }

type unregistered struct{}

func (unregistered) Type() string { return "test.unregistered" }

type env struct {
	db     *bun.DB
	s      *notification.Service
	mailer *mailtest.Recorder
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := dbtest.New(t, notification.Migrations)
	bus, err := messaging.New(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	s, err := notification.New(db, bus)
	if err != nil {
		t.Fatal(err)
	}
	mailer := &mailtest.Recorder{}
	s.AddChannel(s.DatabaseChannel())
	s.AddChannel(notification.MailChannel(mailer))
	notification.Register[shipped](s)
	notification.Register[inAppOnly](s)

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = bus.Run(ctx); close(done) }()
	t.Cleanup(func() { stop(); <-done })
	<-bus.Running()
	return &env{db: db, s: s, mailer: mailer}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition not met")
}

func TestSend(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	alice := notification.Recipient{UserID: uuid.New(), Email: "alice@example.com", Name: "Alice", Locale: "en"}
	bob := notification.Recipient{UserID: uuid.New(), Locale: "zh-CN"} // 没有邮箱：跳过邮件

	if err := e.s.Send(ctx, alice, unregistered{}); err == nil {
		t.Error("unregistered type accepted")
	}
	if err := e.s.Send(ctx, alice, shipped{No: "A1"}); err != nil {
		t.Fatal(err)
	}
	err := e.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return e.s.SendTx(ctx, tx, bob, shipped{No: "B1"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.Send(ctx, alice, inAppOnly{shipped{No: "A2"}}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool { n, _ := e.s.UnreadCount(ctx, alice.UserID); return n == 2 })
	waitFor(t, func() bool { n, _ := e.s.UnreadCount(ctx, bob.UserID); return n == 1 })
	waitFor(t, func() bool { return len(e.mailer.Sent()) == 1 })

	// 按收件人的语言渲染。
	list, _, _ := e.s.List(ctx, alice.UserID, notification.ListOptions{})
	titles := []string{list[0].Title, list[1].Title}
	if !strings.Contains(strings.Join(titles, "|"), "Order A1 shipped") || list[0].Data["no"] == nil {
		t.Errorf("alice = %+v", list)
	}
	bobs, _, _ := e.s.List(ctx, bob.UserID, notification.ListOptions{})
	if bobs[0].Title != "订单 B1 已发货" || bobs[0].URL != "/orders/B1" || bobs[0].Type != "test.shipped" {
		t.Errorf("bob = %+v", bobs[0])
	}
	sent := e.mailer.Sent()
	if sent[0].To[0] != "alice@example.com" || sent[0].Subject != "Order A1 shipped" {
		t.Errorf("mail = %+v", sent[0])
	}

	// 已读、删除、清理。
	if err := e.s.MarkRead(ctx, bob.UserID, list[0].ID); err == nil {
		t.Error("marked another user's message")
	}
	if err := e.s.MarkRead(ctx, alice.UserID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if unread, _, _ := e.s.List(ctx, alice.UserID, notification.ListOptions{UnreadOnly: true}); len(unread) != 1 {
		t.Errorf("unread = %d", len(unread))
	}
	if err := e.s.MarkAllRead(ctx, alice.UserID); err != nil {
		t.Fatal(err)
	}
	if n, err := e.s.Prune(ctx, -time.Hour); err != nil || n != 2 {
		t.Errorf("prune = %d, %v", n, err)
	}
	if err := e.s.Delete(ctx, bob.UserID, bobs[0].ID); err != nil {
		t.Fatal(err)
	}
}

type principal struct{ id uuid.UUID }

func (p principal) UserID() uuid.UUID { return p.id }

func TestEndpoints(t *testing.T) {
	e := newEnv(t)
	alice := notification.Recipient{UserID: uuid.New(), Locale: "zh-CN"}
	for _, no := range []string{"1", "2", "3"} {
		if err := e.s.Send(t.Context(), alice, inAppOnly{shipped{No: no}}); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { n, _ := e.s.UnreadCount(t.Context(), alice.UserID); return n == 3 })

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-User") == "alice" {
				r = r.WithContext(auth.WithPrincipal(r.Context(), principal{alice.UserID}))
			}
			next.ServeHTTP(w, r)
		})
	})
	api := humachi.New(r, huma.DefaultConfig("t", "1"))
	api.UseMiddleware(rest.Authorize[principal](api, nil, nil))
	rest.Get(api, "/notifications", notification.ListEndpoint(e.s))
	rest.Get(api, "/notifications/unread-count", notification.UnreadCountEndpoint(e.s))
	rest.Post(api, "/notifications/{id}/read", notification.MarkReadEndpoint(e.s))
	rest.Post(api, "/notifications/read-all", notification.MarkAllReadEndpoint(e.s))
	rest.Delete(api, "/notifications/{id}", notification.DeleteEndpoint(e.s))

	do := func(method, path string, user bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if user {
			req.Header.Set("X-User", "alice")
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("GET", "/notifications", false); rec.Code != 401 {
		t.Errorf("anonymous = %d", rec.Code)
	}
	rec := do("GET", "/notifications?limit=2", true)
	var page rest.Page[notification.MessageDTO]
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if rec.Code != 200 || len(page.Items) != 2 || page.Items[0].Title != "订单 3 已发货" || page.Next == "" {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	rec = do("GET", "/notifications?limit=2&before="+page.Next, true)
	page = rest.Page[notification.MessageDTO]{}
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Next != "" {
		t.Errorf("page 2 = %s", rec.Body)
	}
	if rec := do("POST", "/notifications/"+page.Items[0].ID+"/read", true); rec.Code != 204 {
		t.Errorf("mark read = %d", rec.Code)
	}
	if rec := do("GET", "/notifications/unread-count", true); !strings.Contains(rec.Body.String(), `"count":2`) {
		t.Errorf("count = %s", rec.Body)
	}
	if rec := do("POST", "/notifications/read-all", true); rec.Code != 204 {
		t.Errorf("read all = %d", rec.Code)
	}
	rec = do("DELETE", "/notifications/"+uuid.New().String(), true)
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"code":"notification.not_found"`) {
		t.Errorf("delete missing = %d %s", rec.Code, rec.Body)
	}
}
