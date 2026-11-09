package notification_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/database/dbtest"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/modules/notification"
)

func newDB(t *testing.T) *bun.DB {
	t.Helper()
	cfg := database.DefaultConfig()
	cfg.DSN = dbtest.DSN(t)
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := t.TempDir()
	if _, err := database.PublishMigrations(dir, notification.Migrations); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), db.DB, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestService(t *testing.T) {
	db := newDB(t)
	s := notification.New(db)
	var pushed []string
	s.OnSend = func(_ context.Context, n *notification.Notification) { pushed = append(pushed, n.UserID.String()) }
	ctx := t.Context()
	alice, bob := uuid.New(), uuid.New()

	if err := s.Send(ctx, notification.Notification{Type: "x"}, alice); err == nil {
		t.Error("missing title accepted")
	}
	if err := s.Send(ctx, notification.Notification{Type: "welcome", Title: "欢迎"}, alice, bob); err != nil {
		t.Fatal(err)
	}
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for i := range 3 {
			err := s.SendTx(ctx, tx, notification.Notification{Type: "order.shipped", Title: "订单已发货",
				URL: "/orders/1", Data: map[string]any{"n": i}}, alice)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pushed) != 5 {
		t.Errorf("pushed = %v", pushed)
	}

	if n, _ := s.UnreadCount(ctx, alice); n != 4 {
		t.Errorf("unread = %d", n)
	}
	list, next, err := s.List(ctx, alice, notification.ListOptions{Limit: 3})
	if err != nil || len(list) != 3 || next == uuid.Nil() || list[0].Data["n"] != float64(2) {
		t.Fatalf("list = %+v %v %v", list, next, err)
	}
	rest, next2, _ := s.List(ctx, alice, notification.ListOptions{Limit: 3, Before: next})
	if len(rest) != 1 || rest[0].Type != "welcome" || next2 != uuid.Nil() {
		t.Errorf("page 2 = %+v", rest)
	}

	if err := s.MarkRead(ctx, bob, list[0].ID); !errors.Is(err, notification.ErrNotFound) {
		t.Errorf("mark other's: %v", err)
	}
	if err := s.MarkRead(ctx, alice, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRead(ctx, alice, list[0].ID); err != nil {
		t.Errorf("mark twice: %v", err)
	}
	unread, _, _ := s.List(ctx, alice, notification.ListOptions{UnreadOnly: true})
	if len(unread) != 3 {
		t.Errorf("unread list = %d", len(unread))
	}
	if err := s.MarkAllRead(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.UnreadCount(ctx, alice); n != 0 {
		t.Errorf("unread after all = %d", n)
	}
	if err := s.Delete(ctx, alice, list[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, alice, list[1].ID); !errors.Is(err, notification.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	// 只清理已读的：alice 的 3 条已读，bob 的 1 条未读保留。
	if n, err := s.Prune(ctx, -time.Hour); err != nil || n != 3 {
		t.Errorf("prune = %d, %v", n, err)
	}
	if n, _ := s.UnreadCount(ctx, bob); n != 1 {
		t.Errorf("bob unread = %d", n)
	}
}

type userKey struct{}

func TestEndpoints(t *testing.T) {
	db := newDB(t)
	s := notification.New(db)
	alice := uuid.New()
	_ = s.Send(t.Context(), notification.Notification{Type: "a", Title: "A"}, alice)
	_ = s.Send(t.Context(), notification.Notification{Type: "b", Title: "B"}, alice)
	user := func(ctx context.Context) (uuid.UUID, bool) {
		id, ok := ctx.Value(userKey{}).(uuid.UUID)
		return id, ok
	}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-User") == "alice" {
				r = r.WithContext(context.WithValue(r.Context(), userKey{}, alice))
			}
			next.ServeHTTP(w, r)
		})
	})
	api := humachi.New(r, huma.DefaultConfig("t", "1"))
	noSec := func(o *huma.Operation) { o.Security = nil }
	rest.Get(api, "/notifications", notification.ListEndpoint(s, user), noSec)
	rest.Get(api, "/notifications/unread-count", notification.UnreadCountEndpoint(s, user), noSec)
	rest.Post(api, "/notifications/{id}/read", notification.MarkReadEndpoint(s, user), noSec)
	rest.Post(api, "/notifications/read-all", notification.MarkAllReadEndpoint(s, user), noSec)
	rest.Delete(api, "/notifications/{id}", notification.DeleteEndpoint(s, user), noSec)

	do := func(method, path string, anon bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if !anon {
			req.Header.Set("X-User", "alice")
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("GET", "/notifications", true); rec.Code != 401 {
		t.Errorf("anonymous = %d", rec.Code)
	}
	rec := do("GET", "/notifications?limit=1", false)
	var page struct {
		Items []notification.DTO
		Next  string
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if rec.Code != 200 || len(page.Items) != 1 || page.Items[0].Title != "B" || page.Next == "" {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/notifications/"+page.Items[0].ID+"/read", false); rec.Code != 204 {
		t.Errorf("mark read = %d", rec.Code)
	}
	if rec := do("GET", "/notifications/unread-count", false); !strings.Contains(rec.Body.String(), `"count":1}`) {
		t.Errorf("count = %s", rec.Body)
	}
	if rec := do("POST", "/notifications/read-all", false); rec.Code != 204 {
		t.Errorf("read all = %d", rec.Code)
	}
	if rec := do("DELETE", "/notifications/"+uuid.New().String(), false); rec.Code != 404 {
		t.Errorf("delete missing = %d", rec.Code)
	}
}
