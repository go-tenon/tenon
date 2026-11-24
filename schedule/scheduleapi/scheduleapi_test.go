package scheduleapi_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/queue/queuetest"
	"github.com/go-tenon/tenon/schedule"
	"github.com/go-tenon/tenon/schedule/pgschedule"
	"github.com/go-tenon/tenon/schedule/scheduleapi"
)

type env struct {
	db    *bun.DB
	sched *schedule.Scheduler
	h     http.Handler
}

func setup(t *testing.T) *env {
	t.Helper()
	e := &env{db: dbtest.Open(t, queue.Migrations(), pgschedule.Migrations(), scheduleapi.Migrations())}
	e.sched = schedule.New(pgschedule.New(e.db), schedule.DefaultConfig(), nil,
		schedule.WithObserver(scheduleapi.Observer(e.db)))
	e.sched.Add("report", "@hourly", func(context.Context) error { return nil },
		schedule.Label("每日报表"), schedule.Describe("生成报表"))
	e.sched.Add("broken", "@daily", func(context.Context) error { return errors.New("boom") })
	router := chi.NewRouter()
	api := rest.New(router, rest.Options{Accounts: identity.Accounts{"user": nil}, Logger: slog.New(slog.DiscardHandler)})
	validate := func(_ context.Context, token string) (identity.Subject, error) {
		switch token {
		case "ops":
			return identity.Subject{Type: "user", ID: "ops", Permissions: []string{"jobs:manage"}}, nil
		case "viewer":
			return identity.Subject{Type: "user", ID: "viewer"}, nil
		}
		return identity.Subject{}, nil
	}
	scheduleapi.Register(api, rest.RequireToken(api, validate, "jobs:manage"), e.db,
		func() *schedule.Scheduler { return e.sched }, scheduleapi.Options{Logger: slog.New(slog.DiscardHandler)})
	e.h = router
	return e
}

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

type listBody struct {
	Items []scheduleapi.ScheduleTask `json:"items"`
}

type runsBody struct {
	Items []scheduleapi.ScheduleRun `json:"items"`
	Total int64                     `json:"total"`
}

func TestPermissions(t *testing.T) {
	t.Parallel()
	e := setup(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/schedules"},
		{http.MethodPost, "/schedules/report/run"},
		{http.MethodGet, "/schedules/runs"},
	} {
		if code, _ := e.call(t, c.method, c.path, "", nil); code != http.StatusUnauthorized {
			t.Fatalf("%s %s anonymous: %d", c.method, c.path, code)
		}
		if code, _ := e.call(t, c.method, c.path, "viewer", nil); code != http.StatusForbidden {
			t.Fatalf("%s %s viewer: %d", c.method, c.path, code)
		}
	}
}

// insertRun 写一行按表达式触发的执行记录。
func insertRun(t *testing.T, db bun.IDB, task string, tick time.Time, status scheduleapi.Status) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `INSERT INTO schedule_runs (id, created_at, updated_at, task, tick, started_at, duration_ms, status, error, manual)
		VALUES (?, ?, ?, ?, ?, ?, 12, ?, '', false)`, uuid.New(), tick, tick, task, tick, tick, status)
	if err != nil {
		t.Fatal(err)
	}
}

func TestList(t *testing.T) {
	t.Parallel()
	e := setup(t)
	var body listBody
	if code, _ := e.call(t, http.MethodGet, "/schedules", "ops", &body); code != http.StatusOK {
		t.Fatal(code)
	}
	if len(body.Items) != 2 || body.Items[0].Name != "report" || body.Items[0].Label != "每日报表" || body.Items[0].Description != "生成报表" {
		t.Fatalf("items: %+v", body.Items)
	}
	// 从来没有执行过：没有上一次的结果，也不算错过。
	if r := body.Items[0]; r.LastStatus != "" || r.LastRunAt != nil || r.Missed {
		t.Fatalf("never run: %+v", r)
	}

	// 上一次触发是两小时前：之后的整点没有执行记录，算错过。
	prev := e.sched.Tasks()[0].Prev(time.Now())
	insertRun(t, e.db, "report", prev.Add(-2*time.Hour), scheduleapi.Failed)
	if code, _ := e.call(t, http.MethodGet, "/schedules", "ops", &body); code != http.StatusOK {
		t.Fatal(code)
	}
	r := body.Items[0]
	if r.LastStatus != scheduleapi.Failed || r.LastRunAt == nil || r.LastDurationMS != 12 {
		t.Fatalf("last: %+v", r)
	}
	if time.Since(prev) > time.Minute && !r.Missed {
		t.Fatalf("missed not reported: %+v", r)
	}

	insertRun(t, e.db, "report", prev, scheduleapi.Succeeded)
	if _, _ = e.call(t, http.MethodGet, "/schedules", "ops", &body); body.Items[0].Missed {
		t.Fatalf("latest tick has a record: %+v", body.Items[0])
	}

	// 即使 schedule_runs 被清理，schedule_tasks 的持久水位仍能准确检测错过。
	if err := e.sched.RunDue(context.Background(), prev.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(context.Background(), "DELETE FROM schedule_runs"); err != nil {
		t.Fatal(err)
	}
	if _, _ = e.call(t, http.MethodGet, "/schedules", "ops", &body); time.Since(prev) > time.Minute && !body.Items[0].Missed {
		t.Fatalf("missed after runs cleanup not reported: %+v", body.Items[0])
	}
}

func TestRunNow(t *testing.T) {
	t.Parallel()
	e := setup(t)
	if code, errCode := e.call(t, http.MethodPost, "/schedules/missing/run", "ops", nil); code != http.StatusNotFound || errCode != "schedule.not_found" {
		t.Fatalf("missing: %d %s", code, errCode)
	}
	if code, _ := e.call(t, http.MethodPost, "/schedules/broken/run", "ops", nil); code != http.StatusAccepted {
		t.Fatal(code)
	}
	if jobs := queuetest.Pending(t, e.db, scheduleapi.RunNow); len(jobs) != 1 || jobs[0] != "broken" {
		t.Fatalf("jobs: %v", jobs)
	}

	// worker 执行：失败写进执行记录，不重试；找不到的任务转入失败任务。
	w := queue.NewWorker(e.db, queuetest.Conn(t), queuetest.Config(t), nil)
	scheduleapi.HandleRunNow(w, func() *schedule.Scheduler { return e.sched })
	queuetest.Run(t, w)
	var runs runsBody
	deadline := time.Now().Add(10 * time.Second)
	for len(runs.Items) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		e.call(t, http.MethodGet, "/schedules/runs", "ops", &runs)
	}
	if len(runs.Items) != 1 {
		t.Fatalf("runs: %+v", runs)
	}
	got := runs.Items[0]
	if got.Task != "broken" || got.Status != scheduleapi.Failed || !got.Manual || got.Error != "boom" {
		t.Fatalf("run: %+v", got)
	}
}

func TestRunsFilterAndCleanup(t *testing.T) {
	t.Parallel()
	e := setup(t)
	now := time.Now().Truncate(time.Second)
	insertRun(t, e.db, "report", now.Add(-10*24*time.Hour), scheduleapi.Succeeded)
	insertRun(t, e.db, "report", now.Add(-time.Hour), scheduleapi.Failed)
	insertRun(t, e.db, "broken", now, scheduleapi.Failed)

	var runs runsBody
	e.call(t, http.MethodGet, "/schedules/runs?per_page=10", "ops", &runs)
	if runs.Total != 3 || runs.Items[0].Task != "broken" {
		t.Fatalf("all: %+v", runs)
	}
	e.call(t, http.MethodGet, "/schedules/runs?task=report&status=failed", "ops", &runs)
	if runs.Total != 1 || runs.Items[0].Task != "report" || runs.Items[0].TaskLabel != "每日报表" || runs.Items[0].Status != scheduleapi.Failed {
		t.Fatalf("filtered: %+v", runs)
	}

	n, err := scheduleapi.Cleanup(e.db, 7*24*time.Hour)(context.Background(), 100)
	if err != nil || n != 1 {
		t.Fatalf("cleanup: %d %v", n, err)
	}
}

func TestObserverRecordsSkips(t *testing.T) {
	t.Parallel()
	e := setup(t)
	tick := time.Now().Truncate(time.Second)
	scheduleapi.Observer(e.db)(context.Background(), schedule.Event{
		Kind: schedule.TaskSkipped, Task: "report", Tick: tick, SkipReason: schedule.SkipOverlap,
	})
	var runs runsBody
	e.call(t, http.MethodGet, "/schedules/runs", "ops", &runs)
	if len(runs.Items) != 1 || runs.Items[0].Status != scheduleapi.Skipped || runs.Items[0].Error != "overlap" ||
		!runs.Items[0].StartedAt.Equal(tick) {
		t.Fatalf("runs: %+v", runs.Items)
	}
}
