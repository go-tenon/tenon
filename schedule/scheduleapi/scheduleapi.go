// Package scheduleapi 是定时任务的执行记录和运维接口：调度器的 Observer 把每次执行写进 schedule_runs 表，
// 接口列出每个任务的说明、表达式、下一次执行、上一次的结果，标出错过的触发，可以立即执行（经由队列交给 worker），
// 并按任务查看执行记录。@go-tenon/ui 的 SchedulePanel 是现成的页面。
//
// 装配（表由 Migrations 发布）：
//
//	sched := schedule.New(locker, cfg.Schedule, logger, schedule.WithObserver(scheduleapi.Observer(db)))
//	sched.Add("schedule-runs-cleanup", "@daily", schedule.Cleanup(scheduleapi.Cleanup(db, 7*24*time.Hour), 1000))
//	tasks := func() *schedule.Scheduler { return sched }
//	scheduleapi.Register(api, rest.RequireToken(api, tokens.Validate, "jobs:manage"), db, tasks) // HTTP 进程
//	scheduleapi.HandleRunNow(worker, tasks)                                                     // worker 进程
//
// 立即执行各记一条 Info 日志，带上操作者（identity.Subject 的 Ref）和任务名。
package scheduleapi

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/logging"
	"github.com/go-tenon/tenon/page"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/schedule"
)

// ErrNotFound 表示没有这个定时任务。
var ErrNotFound = errorx.Define(errorx.NotFound, "schedule.not_found")

// RunNow 是“立即执行”的队列任务，payload 是任务名：交给 worker 执行，不占用 HTTP 请求。
var RunNow = queue.Topic[string]("schedules.run")

// ScheduleTask 是任务列表（schedule-list）中的一个任务。类型名带前缀，避免与应用的类型在 OpenAPI 中重名。
type ScheduleTask struct {
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Expr           string     `json:"expr" doc:"表达式，例如 @daily、0 3 * * *"`
	NextRun        time.Time  `json:"next_run" doc:"下一次执行的时间"`
	Running        bool       `json:"running" doc:"此刻是否在任何一个实例上执行"`
	LastStatus     Status     `json:"last_status,omitempty" enum:"succeeded,failed,skipped" doc:"最近一次执行的结果"` // 最近一次执行的结果，没有执行过时没有这个字段
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`                                                 // 最近一次执行的时间，没有执行过时没有这个字段
	LastDurationMS int64      `json:"last_duration_ms,omitempty"`                                            // 最近一次执行的耗时（毫秒），没有执行过时没有这个字段
	LastError      string     `json:"last_error,omitempty"`                                                  // 最近一次执行的错误，没有执行过时没有这个字段
	LastManual     bool       `json:"last_manual,omitempty" doc:"最近一次是立即执行的"`
	Missed         bool       `json:"missed" doc:"上一次应当执行的触发没有执行记录，例如调度器停止或部署期间"`
}

// ScheduleRun 是执行记录（schedule-runs）中的一次执行。
type ScheduleRun struct {
	ID         string    `json:"id"`
	Task       string    `json:"task"`
	Status     Status    `json:"status" enum:"succeeded,failed,skipped"`
	Manual     bool      `json:"manual" doc:"立即执行的，不是按表达式触发"`
	StartedAt  time.Time `json:"started_at"`
	DurationMS int64     `json:"duration_ms"`
	Error      string    `json:"error" doc:"失败的原因；跳过时是跳过的原因，例如 overlap"`
}

// ScheduleTaskList 是任务列表的响应体。
type ScheduleTaskList struct {
	Items []ScheduleTask `json:"items" nullable:"false"`
}

type listOutput struct{ Body ScheduleTaskList }

type nameInput struct {
	Name string `path:"name"`
}

type runsInput struct {
	page.OffsetParams
	Task   string `query:"task" doc:"任务名，为空时全部任务"`
	Status Status `query:"status" enum:"succeeded,failed,skipped" doc:"结果，为空时全部"`
}

type runsOutput struct{ Body page.Offset[ScheduleRun] }

type handlers struct {
	db     *bun.DB
	tasks  func() *schedule.Scheduler
	logger *slog.Logger
}

// Options 是 Register 的选项，都可以为零值。
type Options struct {
	Logger *slog.Logger // 记录立即执行；nil 表示 slog.Default()。
}

// Register 在 api 上注册定时任务的接口，protect 保护全部接口，通常是 rest.RequireToken(api, validate, 权限)。
//
//	GET  /schedules              任务列表（schedule-list）
//	POST /schedules/{name}/run   立即执行，经由队列交给 worker（schedule-run）
//	GET  /schedules/runs         执行记录，按任务、结果筛选，分页（schedule-runs）
//
// tasks 返回注册了全部任务的调度器，只用来读取定义和执行状态，不会运行它。db、tasks 只在处理请求时使用，
// openapi、client 命令离线构造路由时可以为 nil。api 或 protect 为 nil 时 panic。
func Register(api *rest.API, protect func(*huma.Operation), db *bun.DB, tasks func() *schedule.Scheduler, opts ...Options) {
	if api == nil || protect == nil {
		panic("scheduleapi: nil API or protect")
	}
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	h := &handlers{db: db, tasks: tasks, logger: logging.Or(o.Logger).With(logging.AttrComponent, "scheduleapi")}
	op := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"schedule"}}
		protect(&op)
		return op
	}
	rest.Register(api, op("schedule-list", http.MethodGet, "/schedules", "定时任务"), h.list)
	run := op("schedule-run", http.MethodPost, "/schedules/{name}/run", "立即执行")
	run.DefaultStatus = http.StatusAccepted
	rest.Register(api, run, h.run)
	rest.Register(api, op("schedule-runs", http.MethodGet, "/schedules/runs", "执行记录"), h.runs)
}

// last 读出每个任务最近的一次执行。
func (h *handlers) last(ctx context.Context) (map[string]run, error) {
	var rows []run
	err := h.db.NewSelect().Model(&rows).DistinctOn("task").OrderExpr("task, started_at DESC").Scan(ctx)
	out := make(map[string]run, len(rows))
	for _, r := range rows {
		out[r.Task] = r
	}
	return out, err
}

// lastTicks 读出每个任务最近一次按表达式触发的时刻（不含立即执行）。
func (h *handlers) lastTicks(ctx context.Context) (map[string]time.Time, error) {
	var rows []struct {
		Task string    `bun:"task"`
		Tick time.Time `bun:"tick"`
	}
	err := h.db.NewSelect().Model((*run)(nil)).ColumnExpr("task, max(tick) AS tick").
		Where("tick IS NOT NULL").Group("task").Scan(ctx, &rows)
	out := make(map[string]time.Time, len(rows))
	for _, r := range rows {
		out[r.Task] = r.Tick
	}
	return out, err
}

func (h *handlers) list(ctx context.Context, _ *struct{}) (*listOutput, error) {
	sched := h.tasks()
	running, err := sched.Running(ctx)
	if err != nil {
		return nil, err
	}
	last, err := h.last(ctx)
	if err != nil {
		return nil, err
	}
	lastTick, err := h.lastTicks(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := &listOutput{}
	out.Body.Items = []ScheduleTask{}
	for _, t := range sched.Tasks() {
		it := ScheduleTask{Name: t.Name, Description: t.Description, Expr: t.Expr, NextRun: t.Next(now), Running: running[t.Name]}
		if r, ok := last[t.Name]; ok {
			at := r.StartedAt
			it.LastStatus, it.LastRunAt, it.LastDurationMS, it.LastError, it.LastManual = r.Status, &at, r.DurationMS, r.Error, r.Manual
		}
		// 应当执行的时间过去了一分钟，这次触发却没有执行记录，也没有正在执行：这次触发丢了，例如调度器停止或部署期间。
		// 从来没有按表达式执行过的任务不算（刚部署时每小时、每天的任务都还没到第一次），立即执行的记录也不算。
		tick := lastTick[t.Name]
		prev := t.Prev(now)
		it.Missed = !tick.IsZero() && prev.After(tick) && now.Sub(prev) > time.Minute && !it.Running &&
			!t.EveryInstance && !t.AllowOverlap
		out.Body.Items = append(out.Body.Items, it)
	}
	slices.SortFunc(out.Body.Items, func(a, b ScheduleTask) int { return a.NextRun.Compare(b.NextRun) })
	return out, nil
}

func (h *handlers) run(ctx context.Context, in *nameInput) (*struct{}, error) {
	if !slices.ContainsFunc(h.tasks().Tasks(), func(t schedule.Task) bool { return t.Name == in.Name }) {
		return nil, ErrNotFound.New()
	}
	if err := RunNow.Publish(ctx, h.db, in.Name); err != nil {
		return nil, err
	}
	h.logger.InfoContext(ctx, "scheduled task run requested", "subject", operator(ctx), "task", in.Name)
	return nil, nil
}

func (h *handlers) runs(ctx context.Context, in *runsInput) (*runsOutput, error) {
	var rows []run
	q := h.db.NewSelect().Model(&rows).OrderExpr("started_at DESC").Limit(in.Limit()).Offset(in.Offset())
	if in.Task != "" {
		q = q.Where("task = ?", in.Task)
	}
	if in.Status != "" {
		q = q.Where("status = ?", in.Status)
	}
	total, err := q.ScanAndCount(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]ScheduleRun, len(rows))
	for i, r := range rows {
		items[i] = ScheduleRun{
			ID: r.ID.String(), Task: r.Task, Status: r.Status, Manual: r.Manual,
			StartedAt: r.StartedAt.UTC(), DurationMS: r.DurationMS, Error: r.Error,
		}
	}
	return &runsOutput{Body: page.NewOffset(items, in.OffsetParams, int64(total))}, nil
}

// HandleRunNow 在 worker 上注册“立即执行”：调用 tasks().RunNow，执行记录由 Observer 写入。
// 任务失败、正在执行（没有 AllowOverlap）时都不重试：结果已经在执行记录中，和按表达式触发一样等下一次；
// 任务不存在（例如部署了删掉它的版本）时转入失败任务。
func HandleRunNow(w *queue.Worker, tasks func() *schedule.Scheduler) {
	RunNow.Handle(w, func(ctx context.Context, name string) error {
		err := tasks().RunNow(ctx, name)
		if errors.Is(err, schedule.ErrUnknownTask) {
			return ErrNotFound.Wrap(err) // 业务错误：不重试，直接转入失败任务表
		}
		return nil
	})
}

// operator 返回操作者的引用，写入日志。
func operator(ctx context.Context) string {
	sub, _ := identity.FromContext(ctx)
	return sub.Ref().String()
}
