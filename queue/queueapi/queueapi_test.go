package queueapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/queue/queueapi"
	"github.com/go-tenon/tenon/queue/queuetest"
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

// logBuffer 收集日志，供检查写操作的日志。
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type env struct {
	db   *bun.DB
	h    http.Handler
	logs *logBuffer
}

func setup(t *testing.T) *env {
	t.Helper()
	return setupWith(t, nil)
}

// setupWith 与 setup 相同，接口用 admin 读取 JetStream。token ops 有 queue:manage 权限，viewer 没有。
func setupWith(t *testing.T, admin *queue.Admin) *env {
	t.Helper()
	e := &env{db: dbtest.Open(t, queue.Migrations()), logs: &logBuffer{}}
	router := chi.NewRouter()
	api := rest.New(router, rest.Options{Accounts: identity.Accounts{"user": nil}, Logger: slog.New(slog.DiscardHandler)})
	validate := func(_ context.Context, token string) (identity.Subject, error) {
		switch token {
		case "ops":
			return identity.Subject{Type: "user", ID: "ops", Permissions: []string{"queue:manage"}}, nil
		case "viewer":
			return identity.Subject{Type: "user", ID: "viewer"}, nil
		}
		return identity.Subject{}, nil
	}
	queueapi.Register(api, rest.RequireToken(api, validate, "queue:manage"), e.db, queueapi.Options{
		Logger: slog.New(slog.NewJSONHandler(e.logs, nil)),
		Admin:  admin,
	})
	e.h = router
	return e
}

// call 以 token 发出请求，把响应体解码到 out（不为 nil 时），返回状态码和错误码。
func (e *env) call(t *testing.T, method, path, token string, out any) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code >= 400 {
		var p struct{ Code string }
		_ = json.Unmarshal(rr.Body.Bytes(), &p)
		return rr.Code, p.Code
	}
	if out != nil {
		if err := json.Unmarshal(rr.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: %v\n%s", method, path, err, rr.Body.String())
		}
	}
	return rr.Code, ""
}

func dbName(t *testing.T, db *bun.DB) string {
	t.Helper()
	var name string
	if err := db.NewRaw("SELECT current_database()").Scan(context.Background(), &name); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestAuthentication(t *testing.T) {
	t.Parallel()
	e := setup(t)
	id := insertFailed(t, e.db, "mail", time.Minute)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/queue/status"},
		{http.MethodGet, "/queue/failed"},
		{http.MethodGet, "/queue/failed/" + id.String()},
		{http.MethodPost, "/queue/failed/" + id.String() + "/retry"},
		{http.MethodDelete, "/queue/failed/" + id.String()},
		{http.MethodPost, "/queue/failed/retry"},
		{http.MethodDelete, "/queue/failed"},
	} {
		if code, _ := e.call(t, c.method, c.path, "", nil); code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s: %d", c.method, c.path, code)
		}
		if code, _ := e.call(t, c.method, c.path, "viewer", nil); code != http.StatusForbidden {
			t.Errorf("without permission %s %s: %d", c.method, c.path, code)
		}
	}
	if _, err := queue.NewFailedJobs(e.db).Get(context.Background(), id); err != nil {
		t.Fatalf("changed by a rejected request: %v", err)
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()
	e := setup(t)
	insertFailed(t, e.db, "mail", time.Minute)
	insertFailed(t, e.db, "mail", 2*time.Minute)
	insertFailed(t, e.db, "report", time.Minute)

	var st queueapi.QueueStatus
	if code, _ := e.call(t, http.MethodGet, "/queue/status", "ops", &st); code != http.StatusOK {
		t.Fatalf("status: %d", code)
	}
	if st.Slot.Name != queue.SlotName(dbName(t, e.db)) || !st.Slot.Exists || st.Failed != 3 || st.JetStream != nil {
		t.Fatalf("status: %+v", st)
	}
	if len(st.Topics) != 2 || st.Topics[0].Topic != "mail" || st.Topics[0].Failed != 2 || st.Topics[0].State != queueapi.StateAbsent ||
		st.Topics[1].Topic != "report" || st.Topics[1].Failed != 1 {
		t.Fatalf("topics: %+v", st.Topics)
	}
	// 没有设置 Admin 时暂停和恢复仍然注册（生成的客户端与运行时一致），调用时返回 queue.no_admin。
	for _, path := range []string{"/queue/topics/mail/pause", "/queue/topics/mail/resume"} {
		if code, body := e.call(t, http.MethodPost, path, "ops", nil); code != http.StatusServiceUnavailable || !strings.Contains(body, "queue.no_admin") {
			t.Fatalf("%s without admin: %d %s", path, code, body)
		}
	}
}

func TestFailedList(t *testing.T) {
	t.Parallel()
	e := setup(t)
	older := insertFailed(t, e.db, "mail", 2*time.Minute)
	newer := insertFailed(t, e.db, "mail", time.Minute)
	insertFailed(t, e.db, "report", time.Minute)

	var list queueapi.QueueFailedList
	if code, _ := e.call(t, http.MethodGet, "/queue/failed?topic=mail", "ops", &list); code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	if list.Total != 2 || len(list.Items) != 2 || list.Items[0].ID != newer || list.Items[1].ID != older ||
		list.Items[0].Error != "smtp: 550 mailbox unavailable" || list.Items[0].Attempts != 3 || list.Items[0].Topic != "mail" {
		t.Fatalf("list: %+v", list)
	}

	// 全部 topic，每页一条的第二页。
	list = queueapi.QueueFailedList{}
	if code, _ := e.call(t, http.MethodGet, "/queue/failed?page=2&per_page=1", "ops", &list); code != http.StatusOK {
		t.Fatalf("page 2: %d", code)
	}
	if list.Total != 3 || list.Page != 2 || list.PerPage != 1 || len(list.Items) != 1 {
		t.Fatalf("page 2: %+v", list)
	}
	// 没有失败任务时 items 是空数组。
	list = queueapi.QueueFailedList{}
	if code, _ := e.call(t, http.MethodGet, "/queue/failed?topic=missing", "ops", &list); code != http.StatusOK || list.Items == nil || list.Total != 0 {
		t.Fatalf("empty: %d %+v", code, list)
	}
}

func TestFailedGet(t *testing.T) {
	t.Parallel()
	e := setup(t)
	id := insertFailed(t, e.db, "mail", time.Minute)

	var job queueapi.QueueFailedJobDetail
	if code, _ := e.call(t, http.MethodGet, "/queue/failed/"+id.String(), "ops", &job); code != http.StatusOK {
		t.Fatalf("get: %d", code)
	}
	var payload map[string]string
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload["to"] != "a@example.com" {
		t.Fatalf("payload %s: %v", job.Payload, err)
	}
	if job.ID != id || job.Metadata["request_id"] != "r1" || !strings.HasSuffix(job.Error, "stack…") || job.FailedAt.IsZero() {
		t.Fatalf("job: %+v", job)
	}
	if code, errCode := e.call(t, http.MethodGet, "/queue/failed/"+uuid.NewString(), "ops", nil); code != http.StatusNotFound || errCode != "queue.failed_job_not_found" {
		t.Fatalf("missing job: %d %s", code, errCode)
	}
}

func TestRetryAndDelete(t *testing.T) {
	t.Parallel()
	e := setup(t)
	retried := insertFailed(t, e.db, "mail", 2*time.Minute)
	deleted := insertFailed(t, e.db, "mail", time.Minute)

	// 单条重试：重新发布，失败记录删除。
	if code, _ := e.call(t, http.MethodPost, "/queue/failed/"+retried.String()+"/retry", "ops", nil); code != http.StatusNoContent {
		t.Fatalf("retry: %d", code)
	}
	if n := len(queuetest.Pending(t, e.db, queue.Topic[json.RawMessage]("mail"))); n != 1 {
		t.Fatalf("pending after retry: %d", n)
	}
	if code, _ := e.call(t, http.MethodPost, "/queue/failed/"+retried.String()+"/retry", "ops", nil); code != http.StatusNotFound {
		t.Fatalf("retry again: %d", code)
	}

	// 单条删除。
	if code, _ := e.call(t, http.MethodDelete, "/queue/failed/"+deleted.String(), "ops", nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if code, errCode := e.call(t, http.MethodDelete, "/queue/failed/"+deleted.String(), "ops", nil); code != http.StatusNotFound || errCode != "queue.failed_job_not_found" {
		t.Fatalf("delete again: %d %s", code, errCode)
	}
	if counts, err := queue.NewFailedJobs(e.db).Counts(context.Background()); err != nil || len(counts) != 0 {
		t.Fatalf("remaining %+v %v", counts, err)
	}
	logs := e.logs.String()
	if !strings.Contains(logs, `"msg":"failed job retried","component":"queueapi","subject":"user:ops","topic":"mail"`) ||
		!strings.Contains(logs, `"msg":"failed job deleted"`) {
		t.Fatalf("logs:\n%s", logs)
	}
}

func TestBulk(t *testing.T) {
	t.Parallel()
	e := setup(t)
	insertFailed(t, e.db, "mail", 2*time.Minute)
	insertFailed(t, e.db, "mail", time.Minute)
	insertFailed(t, e.db, "report", time.Minute)
	// 失败时间在请求之后的任务不处理。
	later := insertFailed(t, e.db, "report", -time.Hour)

	var res queueapi.QueueBulkResult
	if code, _ := e.call(t, http.MethodPost, "/queue/failed/retry?topic=mail", "ops", &res); code != http.StatusOK || res.Count != 2 {
		t.Fatalf("retry mail: %d %+v", code, res)
	}
	if n := len(queuetest.Pending(t, e.db, queue.Topic[json.RawMessage]("mail"))); n != 2 {
		t.Fatalf("pending after retry: %d", n)
	}
	res = queueapi.QueueBulkResult{}
	if code, _ := e.call(t, http.MethodDelete, "/queue/failed", "ops", &res); code != http.StatusOK || res.Count != 1 {
		t.Fatalf("delete all: %d %+v", code, res)
	}
	jobs, err := queue.NewFailedJobs(e.db).List(context.Background(), queue.FailedQuery{})
	if err != nil || len(jobs) != 1 || jobs[0].ID != later {
		t.Fatalf("remaining %+v %v", jobs, err)
	}
	res = queueapi.QueueBulkResult{}
	if code, _ := e.call(t, http.MethodPost, "/queue/failed/retry", "ops", &res); code != http.StatusOK || res.Count != 0 {
		t.Fatalf("retry all: %d %+v", code, res)
	}
	logs := e.logs.String()
	if !strings.Contains(logs, `"msg":"failed jobs retried","component":"queueapi","subject":"user:ops","topic":"mail","count":2`) ||
		!strings.Contains(logs, `"msg":"failed jobs deleted","component":"queueapi","subject":"user:ops","topic":"","count":1`) {
		t.Fatalf("logs:\n%s", logs)
	}
}

// 设置了 Admin 时，概览带上 JetStream 中的 stream 和各 topic，可以暂停和恢复 topic。
func TestStatusJetStream(t *testing.T) {
	t.Parallel()
	cfg := queuetest.Config(t)
	admin := queue.NewAdmin(queuetest.Conn(t), cfg)
	e := setupWith(t, admin)
	var st queueapi.QueueStatus
	if e.call(t, http.MethodGet, "/queue/status", "ops", &st); st.JetStream == nil || st.JetStream.Exists || st.JetStream.Error != "" {
		t.Fatalf("before the worker ran: %+v", st.JetStream)
	}

	release := make(chan struct{})
	w := queue.NewWorker(e.db, queuetest.Conn(t), cfg, slog.New(slog.DiscardHandler))
	queue.Topic[int]("mail").Handle(w, func(context.Context, int) error {
		<-release
		return nil
	})
	queuetest.Run(t, w)
	t.Cleanup(func() { close(release) })
	ctx := context.Background()
	for _, topic := range []string{"mail", "orphan"} {
		if err := queue.Topic[int](topic).Publish(ctx, e.db, 1); err != nil {
			t.Fatal(err)
		}
	}
	insertFailed(t, e.db, "mail", time.Minute)

	states := func() map[string]queueapi.QueueTopic {
		st = queueapi.QueueStatus{}
		if code, _ := e.call(t, http.MethodGet, "/queue/status", "ops", &st); code != http.StatusOK {
			t.Fatalf("status: %d", code)
		}
		m := map[string]queueapi.QueueTopic{}
		for _, row := range st.Topics {
			m[row.Topic] = row
		}
		return m
	}
	deadline := time.Now().Add(5 * time.Second)
	topics := states()
	for topics["mail"].State != queueapi.StateOK || topics["mail"].InFlight != 1 || topics["orphan"].State != queueapi.StateNoHandler {
		if time.Now().After(deadline) {
			t.Fatalf("status: %+v %+v", st.JetStream, st.Topics)
		}
		time.Sleep(50 * time.Millisecond)
		topics = states()
	}
	if js := st.JetStream; js.Stream != cfg.Stream || !js.Exists || js.Messages == 0 || js.Replicas != 1 ||
		topics["mail"].Failed != 1 || topics["mail"].LastDelivered == nil || topics["orphan"].Pending != 1 {
		t.Fatalf("status: %+v %+v", st.JetStream, st.Topics)
	}

	var paused queueapi.QueuePaused
	if code, _ := e.call(t, http.MethodPost, "/queue/topics/mail/pause", "ops", &paused); code != http.StatusOK ||
		paused.Topic != "mail" || time.Until(paused.PausedUntil) < 59*time.Minute {
		t.Fatalf("pause: %d %+v", code, paused)
	}
	if mail := states()["mail"]; mail.State != queueapi.StatePaused || mail.PausedUntil == nil {
		t.Fatalf("after pause: %+v", mail)
	}
	if code, _ := e.call(t, http.MethodPost, "/queue/topics/mail/resume", "ops", nil); code != http.StatusNoContent {
		t.Fatalf("resume: %d", code)
	}
	if mail := states()["mail"]; mail.State == queueapi.StatePaused || mail.PausedUntil != nil {
		t.Fatalf("after resume: %+v", mail)
	}
	for _, path := range []string{"/queue/topics/missing/pause", "/queue/topics/orphan/pause", "/queue/topics/missing/resume"} {
		if code, errCode := e.call(t, http.MethodPost, path, "ops", nil); code != http.StatusNotFound || errCode != "queue.topic_not_found" {
			t.Fatalf("%s: %d %s", path, code, errCode)
		}
	}
	if code, _ := e.call(t, http.MethodPost, "/queue/topics/mail/pause", "viewer", nil); code != http.StatusForbidden {
		t.Fatalf("pause without permission: %d", code)
	}
	logs := e.logs.String()
	if !strings.Contains(logs, `"msg":"topic paused","component":"queueapi","subject":"user:ops","topic":"mail"`) ||
		!strings.Contains(logs, `"msg":"topic resumed"`) {
		t.Fatalf("logs:\n%s", logs)
	}
}

// NATS 不可用时概览照常返回数据库一侧的状态，JetStream 中带着错误。
func TestStatusWithoutJetStream(t *testing.T) {
	t.Parallel()
	nc := queuetest.Conn(t)
	nc.Close()
	e := setupWith(t, queue.NewAdmin(nc, queue.DefaultConfig()))
	insertFailed(t, e.db, "mail", time.Minute)
	var st queueapi.QueueStatus
	if code, _ := e.call(t, http.MethodGet, "/queue/status", "ops", &st); code != http.StatusOK {
		t.Fatalf("status: %d", code)
	}
	if st.JetStream == nil || st.JetStream.Error == "" || st.JetStream.Exists || st.Slot.Name != queue.SlotName(dbName(t, e.db)) ||
		len(st.Topics) != 1 || st.Topics[0].State != queueapi.StateAbsent || st.Topics[0].Failed != 1 {
		t.Fatalf("status: %+v", st)
	}
}
