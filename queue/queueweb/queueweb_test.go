package queueweb_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/queue/queuetest"
	"github.com/go-tenon/tenon/queue/queueweb"
	"github.com/go-tenon/tenon/web"
	"github.com/go-tenon/tenon/web/ui"
	"github.com/go-tenon/tenon/web/webtest"
)

func insertFailed(t *testing.T, db bun.IDB, topic string, age time.Duration) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.ExecContext(context.Background(), `INSERT INTO queue_failed_jobs (id, topic, payload, metadata, attempts, error, failed_at)
		VALUES (?, ?, ?, '{"request_id":"r1"}', 3, ?, ?)`, id, topic, []byte(`{"to":"a@example.com"}`), "smtp: 550 mailbox unavailable\nstack…", time.Now().UTC().Add(-age))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

var ops = webtest.Header("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("ops:s3cret")))

func setup(t *testing.T) (*bun.DB, *webtest.Client) {
	t.Helper()
	return setupWith(t, nil)
}

// setupWith 与 setup 相同，面板用 admin 读取 JetStream。
func setupWith(t *testing.T, admin *queue.Admin) (*bun.DB, *webtest.Client) {
	t.Helper()
	db := dbtest.Open(t, queue.Migrations())
	verify := func(_ context.Context, user, password string) (bool, error) {
		return user == "ops" && password == "s3cret", nil
	}
	router := chi.NewRouter()
	router.Handle(web.AssetsPath+"*", web.Assets())
	router.Handle(ui.AssetsPath+"*", ui.Assets())
	queueweb.Register(router.With(httpx.BasicAuth("ops", verify)), "/ops/queue", db, queueweb.Options{
		Links:  map[string]string{"Grafana": "https://grafana.example.com/d/queue", "NATS": ""},
		Logger: slog.New(slog.DiscardHandler),
		Admin:  admin,
	})
	return db, webtest.New(t, router)
}

func TestOverview(t *testing.T) {
	t.Parallel()
	db, c := setup(t)
	insertFailed(t, db, "mail", time.Minute)
	insertFailed(t, db, "mail", 2*time.Minute)
	insertFailed(t, db, "report", time.Minute)

	if res := c.Get("/ops/queue/"); res.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", res.Code)
	}
	res := c.Get("/ops/queue/", ops)
	page := res.Text()
	if res.Code != http.StatusOK || !strings.Contains(page, queue.SlotName(dbName(t, db))) || !strings.Contains(page, "https://grafana.example.com/d/queue") ||
		strings.Contains(page, "NATS") || !strings.Contains(page, `href="/ops/queue/failed?topic=mail"`) || !strings.Contains(page, `hx-trigger="every 5s"`) {
		t.Fatalf("overview: %d\n%s", res.Code, page)
	}
	// 定时刷新只返回状态区域。
	res = c.Get("/ops/queue/", ops, webtest.HTMX)
	if res.Code != http.StatusOK || strings.Contains(res.Text(), "<html") || !strings.Contains(res.Text(), `id="overview"`) {
		t.Fatalf("refresh: %d\n%s", res.Code, res.Text())
	}
}

func TestFailedJobs(t *testing.T) {
	t.Parallel()
	db, c := setup(t)
	old := insertFailed(t, db, "mail", 2*time.Minute)
	insertFailed(t, db, "mail", time.Minute)
	report := insertFailed(t, db, "report", time.Minute)

	res := c.Get("/ops/queue/failed?topic=mail", ops)
	if res.Code != http.StatusOK || !strings.Contains(res.Text(), old.String()) || strings.Contains(res.Text(), report.String()) ||
		!strings.Contains(res.Text(), "smtp: 550 mailbox unavailable") || strings.Contains(res.Text(), "stack…") {
		t.Fatalf("list: %d\n%s", res.Code, res.Text())
	}

	// 详情页显示格式化的 payload、元数据和完整的错误。
	res = c.Get("/ops/queue/failed/"+old.String(), ops)
	if res.Code != http.StatusOK || !strings.Contains(res.Text(), "&#34;to&#34;: &#34;a@example.com&#34;") || !strings.Contains(res.Text(), "request_id") || !strings.Contains(res.Text(), "stack…") {
		t.Fatalf("detail: %d\n%s", res.Code, res.Text())
	}
	if res := c.Get("/ops/queue/failed/"+uuid.NewString(), ops); res.Code != http.StatusNotFound {
		t.Fatalf("missing job: %d", res.Code)
	}

	// 单条重试：重新发布，回到它所在 topic 的列表。
	res = c.Post("/ops/queue/failed/"+old.String()+"/retry", nil, ops, webtest.HTMX)
	if res.Redirect() != "/ops/queue/failed?topic=mail" {
		t.Fatalf("retry: %d %q", res.Code, res.Redirect())
	}
	if n := len(queuetest.Pending(t, db, queue.Topic[json.RawMessage]("mail"))); n != 1 {
		t.Fatalf("pending after retry: %d", n)
	}

	// 全部删除 report：返回刷新后的列表区域和条数。
	res = c.Post("/ops/queue/failed/delete?topic=report", nil, ops, webtest.HTMX)
	if res.Code != http.StatusOK || !strings.Contains(res.Text(), "Deleted 1 failed jobs") || strings.Contains(res.Text(), report.String()) {
		t.Fatalf("delete all: %d\n%s", res.Code, res.Text())
	}
	// 全部重试其余的任务。
	res = c.Post("/ops/queue/failed/retry", nil, ops, webtest.HTMX)
	if res.Code != http.StatusOK || !strings.Contains(res.Text(), "Retried 1 failed jobs") {
		t.Fatalf("retry all: %d\n%s", res.Code, res.Text())
	}
	if counts, err := queue.NewFailedJobs(db).Counts(context.Background()); err != nil || len(counts) != 0 {
		t.Fatalf("remaining %+v %v", counts, err)
	}
}

// 浏览器会给跨站请求带上 Basic 凭据，面板拒绝跨站的非安全请求。
func TestRejectsCrossSite(t *testing.T) {
	t.Parallel()
	db, c := setup(t)
	id := insertFailed(t, db, "mail", time.Minute)
	res := c.Post("/ops/queue/failed/"+id.String()+"/delete", url.Values{}, ops, webtest.Header("Sec-Fetch-Site", "cross-site"))
	if res.Code != http.StatusForbidden {
		t.Fatalf("cross-site: %d", res.Code)
	}
	if _, err := queue.NewFailedJobs(db).Get(context.Background(), id); err != nil {
		t.Fatalf("deleted by cross-site request: %v", err)
	}
}

func TestScheduled(t *testing.T) {
	t.Parallel()
	db, c := setup(t)
	if res := c.Get("/ops/queue/scheduled", ops); res.Code != http.StatusOK || !strings.Contains(res.Text(), "No scheduled jobs") {
		t.Fatalf("empty: %d\n%s", res.Code, res.Text())
	}
	at := time.Now().UTC().Add(72 * time.Hour)
	body := fmt.Sprintf(`{"id":%q,"payload":{"n":1},"available_at":%q}`, uuid.New(), at.Format(time.RFC3339Nano))
	if _, err := db.ExecContext(context.Background(), `INSERT INTO queue_scheduled (id, topic, body, available_at, created_at) VALUES (?, 'report', ?, ?, now())`,
		uuid.New(), []byte(body), at); err != nil {
		t.Fatal(err)
	}
	res := c.Get("/ops/queue/scheduled", ops)
	if res.Code != http.StatusOK || !strings.Contains(res.Text(), "report") || !strings.Contains(res.Text(), "{&#34;n&#34;:1}") {
		t.Fatalf("scheduled: %d\n%s", res.Code, res.Text())
	}
}

func dbName(t *testing.T, db *bun.DB) string {
	t.Helper()
	var name string
	if err := db.NewRaw("SELECT current_database()").Scan(context.Background(), &name); err != nil {
		t.Fatal(err)
	}
	return name
}

// 设置了 Admin 时，概览显示 JetStream 中的 stream 和各 topic，可以暂停和恢复 topic。
func TestOverviewJetStream(t *testing.T) {
	t.Parallel()
	cfg := queuetest.Config(t)
	admin := queue.NewAdmin(queuetest.Conn(t), cfg)
	db, c := setupWith(t, admin)
	if page := c.Get("/ops/queue/", ops).Text(); !strings.Contains(page, "Not created yet") {
		t.Fatalf("before the worker ran:\n%s", page)
	}

	release := make(chan struct{})
	w := queue.NewWorker(db, queuetest.Conn(t), cfg, slog.New(slog.DiscardHandler))
	queue.Topic[int]("mail").Handle(w, func(context.Context, int) error {
		<-release
		return nil
	})
	queuetest.Run(t, w)
	t.Cleanup(func() { close(release) })
	ctx := context.Background()
	for _, topic := range []string{"mail", "orphan"} {
		if err := queue.Topic[int](topic).Publish(ctx, db, 1); err != nil {
			t.Fatal(err)
		}
	}
	insertFailed(t, db, "mail", time.Minute)

	var page string
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(page, "No handler registered") || !strings.Contains(page, ">OK<") {
		if time.Now().After(deadline) {
			t.Fatalf("overview:\n%s", page)
		}
		time.Sleep(50 * time.Millisecond)
		page = c.Get("/ops/queue/", ops, webtest.HTMX).Text()
	}
	if !strings.Contains(page, cfg.Stream) || !strings.Contains(page, `href="/ops/queue/failed?topic=mail"`) ||
		!strings.Contains(page, `hx-post="/ops/queue/topics/mail/pause"`) || strings.Contains(page, "topics/orphan/pause") {
		t.Fatalf("overview:\n%s", page)
	}

	res := c.Post("/ops/queue/topics/mail/pause", nil, ops, webtest.HTMX)
	if res.Code != http.StatusOK || !strings.Contains(res.Text(), "Paused until") || !strings.Contains(res.Text(), "topics/mail/resume") {
		t.Fatalf("pause: %d\n%s", res.Code, res.Text())
	}
	res = c.Post("/ops/queue/topics/mail/resume", nil, ops, webtest.HTMX)
	if res.Code != http.StatusOK || strings.Contains(res.Text(), "Paused until") {
		t.Fatalf("resume: %d\n%s", res.Code, res.Text())
	}
	if res := c.Post("/ops/queue/topics/missing/pause", nil, ops, webtest.HTMX); res.Code != http.StatusNotFound {
		t.Fatalf("pause a missing topic: %d", res.Code)
	}
	if res := c.Post("/ops/queue/topics/mail/pause", url.Values{}, ops, webtest.Header("Sec-Fetch-Site", "cross-site")); res.Code != http.StatusForbidden {
		t.Fatalf("cross-site pause: %d", res.Code)
	}
}

// NATS 不可用时概览照常显示数据库一侧的状态；没有设置 Admin 时没有暂停的地址。
func TestOverviewWithoutJetStream(t *testing.T) {
	t.Parallel()
	nc := queuetest.Conn(t)
	nc.Close()
	admin := queue.NewAdmin(nc, queue.DefaultConfig())
	db, c := setupWith(t, admin)
	res := c.Get("/ops/queue/", ops)
	if res.Code != http.StatusOK || !strings.Contains(res.Text(), "Cannot read JetStream") || !strings.Contains(res.Text(), queue.SlotName(dbName(t, db))) {
		t.Fatalf("overview: %d\n%s", res.Code, res.Text())
	}
	_, c = setup(t)
	if res := c.Post("/ops/queue/topics/mail/pause", nil, ops, webtest.HTMX); res.Code != http.StatusNotFound {
		t.Fatalf("pause without admin: %d", res.Code)
	}
}
