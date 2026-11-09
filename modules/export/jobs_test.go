package export_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/database/dbtest"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/messaging"
	"github.com/ryan053857/go-kit/modules/export"
	"github.com/ryan053857/go-kit/storage"
)

type scoreRow struct {
	Name  string
	Score int
}

var rowsTable = export.Table[scoreRow]{Name: "成绩", Columns: []export.Column[scoreRow]{
	{Header: "姓名", Value: func(r scoreRow) any { return r.Name }},
	{Header: "分数", Value: func(r scoreRow) any { return r.Score }},
}}

type userKey struct{}

func currentUser(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userKey{}).(uuid.UUID)
	return id, ok
}

func TestJobs(t *testing.T) {
	cfg := database.DefaultConfig()
	cfg.DSN = dbtest.DSN(t)
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := t.TempDir()
	if _, err := database.PublishMigrations(dir, export.Migrations); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), db.DB, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	store := storage.NewLocal(t.TempDir())
	bus, err := messaging.New(db.DB)
	if err != nil {
		t.Fatal(err)
	}

	jobs := export.NewJobs(db, store)
	jobs.Register("scores", "成绩", func(ctx context.Context, job *export.Job, w io.Writer) error {
		var p struct{ Min int }
		if err := job.Decode(&p); err != nil {
			return err
		}
		if p.Min < 0 {
			return errors.New("boom")
		}
		var rows []scoreRow
		for i := range 5 {
			if i >= p.Min {
				rows = append(rows, scoreRow{Name: "学生" + job.UserID.String()[:4], Score: i})
			}
		}
		return export.Write(w, job.Format, rowsTable, export.SliceRows(rows))
	})
	finished := make(chan *export.Job, 4)
	jobs.OnFinish = func(_ context.Context, job *export.Job) { finished <- job }
	if err := messaging.Handle(bus, export.TopicExport, jobs.Handle); err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = bus.Run(runCtx); close(done) }()
	t.Cleanup(func() { stop(); <-done })
	<-bus.Running()

	alice := uuid.New()
	ctx := t.Context()
	if _, err := jobs.Start(ctx, alice, "nope", export.CSV, nil); !errors.Is(err, export.ErrUnknownKind) {
		t.Errorf("unknown kind: %v", err)
	}

	// HTTP 接口：创建、轮询、下载。
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
	rest.Post(api, "/api/exports", export.StartEndpoint(jobs, currentUser), noSec)
	rest.Get(api, "/api/exports", export.JobsEndpoint(jobs, currentUser), noSec)
	rest.Get(api, "/api/exports/{id}", export.JobEndpoint(jobs, currentUser), noSec)
	r.Get("/exports/{id}/download", jobs.DownloadHandler(currentUser).ServeHTTP)

	do := func(method, path, body, user string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User", user)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	rec := do("POST", "/api/exports", `{"kind":"scores","format":"csv","params":{"Min":2}}`, "alice")
	if rec.Code != 202 {
		t.Fatalf("start = %d %s", rec.Code, rec.Body)
	}
	var started export.JobDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	if started.Status != export.StatusPending || !strings.HasPrefix(started.Filename, "成绩-") || started.DownloadURL != "" {
		t.Errorf("started = %+v", started)
	}

	var job *export.Job
	select {
	case job = <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("job not finished")
	}
	if job.Status != export.StatusDone || job.Size == 0 {
		t.Fatalf("job = %+v", job)
	}
	var got export.JobDTO
	_ = json.Unmarshal(do("GET", "/api/exports/"+started.ID, "", "alice").Body.Bytes(), &got)
	if got.Status != export.StatusDone || got.DownloadURL != "/exports/"+started.ID+"/download" {
		t.Fatalf("status = %+v", got)
	}
	if rec := do("GET", "/api/exports/"+started.ID, "", "bob"); rec.Code != 401 {
		t.Errorf("anonymous = %d", rec.Code)
	}

	rec = do("GET", got.DownloadURL, "", "alice")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") ||
		!strings.Contains(body, "姓名,分数") || strings.Count(body, "\n") != 4 {
		t.Fatalf("download = %d %q", rec.Code, body)
	}
	if rec := do("GET", got.DownloadURL, "", "bob"); rec.Code != 404 {
		t.Errorf("other user download = %d", rec.Code)
	}

	// 导出函数出错：任务失败，不重试。
	failed, err := jobs.Start(ctx, alice, "scores", export.XLSX, map[string]int{"Min": -1})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case job = <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("failing job not finished")
	}
	if job.ID != failed.ID || job.Status != export.StatusFailed || job.Error == "" {
		t.Errorf("failed job = %+v", job)
	}
	if list := do("GET", "/api/exports", "", "alice").Body.String(); strings.Count(list, `"id"`) != 2 {
		t.Errorf("list = %s", list)
	}

	// 过期清理：删除任务与文件。
	if _, err := db.NewUpdate().Model((*export.Job)(nil)).Set("expires_at = ?", time.Now().Add(-time.Minute)).
		Where("TRUE").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := jobs.Cleanup(ctx); err != nil || n != 2 {
		t.Errorf("cleanup = %d, %v", n, err)
	}
	if _, err := store.Get(ctx, job.Key); job.Key != "" && !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("file not deleted: %v", err)
	}
}
