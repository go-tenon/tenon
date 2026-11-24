package scheduleapi

import (
	"context"
	"log/slog"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/schedule"
)

// Status 是一次执行的结果。
type Status string

// 一次执行的结果：成功、失败、跳过。
const (
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
	// Skipped 表示这一次没有执行，例如上一次还没结束。
	Skipped Status = "skipped"
)

// run 是一次执行的记录，即 schedule_runs 表的一行。
type run struct {
	bun.BaseModel `bun:"table:schedule_runs"`

	ID                  uuid.UUID  `bun:"id,pk"`
	database.Timestamps            // CreatedAt 是写入记录的时间，即执行结束或被跳过的时间
	Task                string     `bun:"task,notnull"`
	Tick                *time.Time `bun:"tick"` // 这次触发应当执行的时间；立即执行时为空。
	StartedAt           time.Time  `bun:"started_at,notnull"`
	DurationMS          int64      `bun:"duration_ms,notnull"`
	Status              Status     `bun:"status,notnull"`
	Error               string     `bun:"error,notnull"`
	Manual              bool       `bun:"manual,notnull"`
}

// Observer 返回调度器的 Observer：每次执行结束、被跳过时在 schedule_runs 中写一行，立即执行（RunNow）也一样。
// 由其他实例执行的触发不报告，所以每次执行只记一行。写入失败只记日志，不影响任务。
//
//	sched := schedule.New(pgschedule.New(db), cfg, logger, schedule.WithObserver(scheduleapi.Observer(db)))
func Observer(db bun.IDB) schedule.Observer {
	return func(ctx context.Context, e schedule.Event) {
		r := &run{Task: e.Task, StartedAt: e.Started, DurationMS: e.Duration.Milliseconds(), Manual: e.Manual}
		if e.Err != nil {
			r.Error = e.Err.Error()
		}
		if !e.Tick.IsZero() {
			tick := e.Tick
			r.Tick = &tick
		}
		switch e.Kind {
		case schedule.TaskSucceeded:
			r.Status = Succeeded
		case schedule.TaskFailed:
			r.Status = Failed
		case schedule.TaskSkipped:
			r.Status = Skipped
			if r.StartedAt.IsZero() {
				r.StartedAt = time.Now()
				if !e.Tick.IsZero() {
					r.StartedAt = e.Tick
				}
			}
			if r.Error == "" {
				r.Error = string(e.SkipReason)
			}
		default:
			return
		}
		r.ID = uuid.Must(uuid.NewV7())
		// 任务被取消（例如超时、停止）时 ctx 已经结束，记录照样写入。
		if _, err := db.NewInsert().Model(r).Exec(context.WithoutCancel(ctx)); err != nil {
			slog.ErrorContext(ctx, "record schedule run failed", "task", r.Task, "error", err)
		}
	}
}

// Cleanup 返回删除 keep 以前的执行记录的清理函数，交给 schedule.Cleanup 注册为定时任务：
//
//	sched.Add("schedule-runs-cleanup", "@daily", schedule.Cleanup(scheduleapi.Cleanup(db, 7*24*time.Hour), 1000))
func Cleanup(db bun.IDB, keep time.Duration) func(ctx context.Context, limit int) (int, error) {
	return func(ctx context.Context, limit int) (int, error) {
		res, err := db.NewDelete().Model((*run)(nil)).
			Where("id IN (?)", db.NewSelect().Model((*run)(nil)).Column("id").
				Where("started_at < ?", time.Now().Add(-keep)).Limit(int64(limit))).
			Exec(ctx)
		if err != nil {
			return 0, errors.Wrap(err, "cleanup schedule runs")
		}
		n, err := res.RowsAffected()
		return int(n), err
	}
}
