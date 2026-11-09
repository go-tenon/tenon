package audit_test

import (
	"context"
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

	"github.com/ryan053857/go-kit/authz"
	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/database/dbtest"
	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/i18n"
	"github.com/ryan053857/go-kit/modules/audit"
)

func TestMain(m *testing.M) {
	huma.NewErrorWithContext = rest.NewErrorWithContext
	i18n.MustLoad(fstest.MapFS{
		"zh-CN.yaml": {Data: []byte("audit.order.refund: \"退款 ¥{{.amount}}\"\n")},
		"en.yaml":    {Data: []byte("audit.order.refund: \"Refunded ¥{{.amount}}\"\n")},
	})
	os.Exit(m.Run())
}

type principal struct{ id uuid.UUID }

func (p principal) UserID() uuid.UUID { return p.id }

func TestRecordAndList(t *testing.T) {
	db := dbtest.New(t, audit.Migrations)
	s := audit.New(db)
	admin := uuid.New()

	// 经过中间件的请求：带 IP、User-Agent、request_id 与当前用户。
	h := httpx.RequestID(audit.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithPrincipal(r.Context(), principal{admin})
		if err := s.Record(ctx, audit.Entry{Action: "order.refund", ResourceType: "order", ResourceID: "o1",
			Data: map[string]any{"amount": "128.00"}}); err != nil {
			t.Error(err)
		}
	})))
	req := httptest.NewRequest("POST", "/", nil)
	req.RemoteAddr = "10.1.2.3:4567"
	req.Header.Set("User-Agent", "test-agent")
	req.Header.Set("X-Request-ID", "req-1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	ctx := t.Context()
	s.Log(ctx, audit.Entry{Action: "account.login_failed", Data: map[string]any{"email": "x@example.com"}, Failed: true})
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
	if err != nil || len(all) != 3 || next != uuid.Nil() {
		t.Fatalf("list = %d, %v, %v", len(all), next, err)
	}
	if all[0].Action != "system.cleanup" || all[0].ActorID != uuid.Nil() {
		t.Errorf("newest = %+v", all[0])
	}
	refund := all[2]
	if refund.ActorID != admin || refund.IP != "10.1.2.3" || refund.UserAgent != "test-agent" || refund.RequestID != "req-1" {
		t.Errorf("refund = %+v", refund)
	}

	// 摘要按语言翻译；没有消息时为动作本身。
	if got := audit.Summary(i18n.WithLocale(ctx, "en"), &refund); got != "Refunded ¥128.00" {
		t.Errorf("summary en = %q", got)
	}
	if got := audit.Summary(ctx, &refund); got != "退款 ¥128.00" {
		t.Errorf("summary zh = %q", got)
	}
	if got := audit.Summary(ctx, &all[0]); got != "system.cleanup" {
		t.Errorf("summary fallback = %q", got)
	}

	// 过滤与分页。
	if got, _, _ := s.List(ctx, audit.Filter{ActorID: admin}); len(got) != 1 {
		t.Errorf("by actor = %d", len(got))
	}
	page1, next, _ := s.List(ctx, audit.Filter{Cursor: database.Cursor{Limit: 2}})
	page2, next2, _ := s.List(ctx, audit.Filter{Cursor: database.Cursor{Limit: 2, Before: next}})
	if len(page1) != 2 || len(page2) != 1 || next2 != uuid.Nil() || page2[0].ID != refund.ID {
		t.Errorf("paging = %d %d %v", len(page1), len(page2), next2)
	}
	if got, _, _ := s.List(ctx, audit.Filter{From: time.Now().Add(time.Hour)}); len(got) != 0 {
		t.Errorf("from future = %d", len(got))
	}
	if n, err := s.Prune(ctx, -time.Hour); err != nil || n != 3 {
		t.Errorf("prune = %d, %v", n, err)
	}
}

func TestEndpoints(t *testing.T) {
	db := dbtest.New(t, audit.Migrations)
	s := audit.New(db)
	alice, admin := uuid.New(), uuid.New()
	for range 3 {
		s.Log(t.Context(), audit.Entry{Action: "a", ActorID: alice})
	}
	s.Log(t.Context(), audit.Entry{Action: "order.refund", ActorID: admin, Data: map[string]any{"amount": "1"}})

	gate := authz.New()
	gate.Define(audit.View, func(_ context.Context, user uuid.UUID) bool { return user == admin })
	r := chi.NewRouter()
	r.Use(i18n.Middleware, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id, err := uuid.Parse(r.Header.Get("X-User")); err == nil {
				r = r.WithContext(auth.WithPrincipal(r.Context(), principal{id}))
			}
			next.ServeHTTP(w, r)
		})
	})
	api := humachi.New(r, huma.DefaultConfig("t", "1"))
	api.UseMiddleware(rest.Authorize[principal](api, nil, gate))
	rest.Get(api, "/audit", audit.ListEndpoint(s))
	rest.Get(api, "/me/audit", audit.MineEndpoint(s))

	get := func(path string, user uuid.UUID, status int) string {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("X-User", user.String())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s = %d %s", path, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	get("/audit", alice, 403)
	if body := get("/audit?action=order.refund", admin, 200); !strings.Contains(body, `"summary":"退款 ¥1"`) || strings.Contains(body, `"next"`) {
		t.Errorf("list = %s", body)
	}
	if body := get("/me/audit?limit=2", alice, 200); strings.Count(body, `"action":"a"`) != 2 || !strings.Contains(body, `"next"`) {
		t.Errorf("mine = %s", body)
	}
}
