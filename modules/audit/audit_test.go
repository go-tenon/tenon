package audit_test

import (
	"context"
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
	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/modules/audit"
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
	if _, err := database.PublishMigrations(dir, audit.Migrations); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), db.DB, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	return db
}

type userKey struct{}

func currentUser(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userKey{}).(uuid.UUID)
	return id, ok
}

func TestRecordAndList(t *testing.T) {
	db := newDB(t)
	s := audit.New(db, currentUser)
	admin, alice := uuid.New(), uuid.New()

	// 经过中间件的请求：带 IP、User-Agent、request_id 与当前用户。
	h := httpx.RequestID(audit.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), userKey{}, admin)
		if err := s.Record(ctx, audit.Entry{Action: "order.refund", ResourceType: "order", ResourceID: "o1",
			Summary: "退款", Data: map[string]any{"amount": 128}}); err != nil {
			t.Error(err)
		}
	})))
	req := httptest.NewRequest("POST", "/", nil)
	req.RemoteAddr = "10.1.2.3:4567"
	req.Header.Set("User-Agent", "test-agent")
	req.Header.Set("X-Request-ID", "req-1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	ctx := t.Context()
	s.AccountEvent(ctx, account.Event{Type: account.EventLogin, UserID: alice, SessionID: uuid.New()})
	s.AccountEvent(ctx, account.Event{Type: account.EventLoginFailed, Email: "x@example.com"})
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return s.RecordTx(ctx, tx, audit.Entry{Action: "system.cleanup"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Record(ctx, audit.Entry{}); err == nil {
		t.Error("empty action accepted")
	}

	all, next, err := s.List(ctx, audit.Filter{})
	if err != nil || len(all) != 4 || next != uuid.Nil() {
		t.Fatalf("list = %d, %v, %v", len(all), next, err)
	}
	if all[0].Action != "system.cleanup" || all[0].ActorID != uuid.Nil() {
		t.Errorf("newest = %+v", all[0])
	}
	refund := all[3]
	if refund.ActorID != admin || refund.IP != "10.1.2.3" || refund.UserAgent != "test-agent" ||
		refund.RequestID != "req-1" || refund.Data["amount"] != float64(128) {
		t.Errorf("refund = %+v", refund)
	}
	failed := all[1]
	if !failed.Failed || failed.Summary != "登录失败" || failed.Data["email"] != "x@example.com" || failed.ResourceID != "" {
		t.Errorf("login failed = %+v", failed)
	}
	if all[2].ActorID != alice || all[2].ResourceType != "session" {
		t.Errorf("login = %+v", all[2])
	}

	// 过滤与分页。
	mine, _, _ := s.List(ctx, audit.Filter{ActorID: alice})
	if len(mine) != 1 || mine[0].Action != account.EventLogin {
		t.Errorf("by actor = %+v", mine)
	}
	page1, next, _ := s.List(ctx, audit.Filter{Limit: 3})
	page2, next2, _ := s.List(ctx, audit.Filter{Limit: 3, Before: next})
	if len(page1) != 3 || len(page2) != 1 || next2 != uuid.Nil() || page2[0].ID != refund.ID {
		t.Errorf("paging = %d %d %v", len(page1), len(page2), next2)
	}
	if got, _, _ := s.List(ctx, audit.Filter{ResourceType: "order", ResourceID: "o1"}); len(got) != 1 {
		t.Errorf("by resource = %d", len(got))
	}
	if got, _, _ := s.List(ctx, audit.Filter{From: time.Now().Add(time.Hour)}); len(got) != 0 {
		t.Errorf("from future = %d", len(got))
	}

	if n, err := s.Prune(ctx, -time.Hour); err != nil || n != 4 {
		t.Errorf("prune = %d, %v", n, err)
	}
}

func TestEndpoints(t *testing.T) {
	db := newDB(t)
	s := audit.New(db, currentUser)
	alice := uuid.New()
	ctx := t.Context()
	for range 3 {
		s.Log(ctx, audit.Entry{Action: "a", ActorID: alice})
	}
	s.Log(ctx, audit.Entry{Action: "b", ActorID: uuid.New()})

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, alice)))
		})
	})
	api := humachi.New(r, huma.DefaultConfig("t", "1"))
	rest.Get(api, "/audit", audit.ListEndpoint(s), func(o *huma.Operation) { o.Security = nil })
	rest.Get(api, "/me/audit", audit.MineEndpoint(s, currentUser), func(o *huma.Operation) { o.Security = nil })

	get := func(path string) string {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s = %d %s", path, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	if body := get("/audit?action=b"); strings.Count(body, `"action":"b"`) != 1 || strings.Contains(body, `"next"`) {
		t.Errorf("list = %s", body)
	}
	body := get("/me/audit?limit=2")
	if strings.Count(body, `"action":"a"`) != 2 || !strings.Contains(body, `"next"`) {
		t.Errorf("mine = %s", body)
	}
}
