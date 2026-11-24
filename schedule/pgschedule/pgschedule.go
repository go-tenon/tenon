// Package pgschedule 是基于 PostgreSQL 的 schedule.Store 实现：每个定时任务在 schedule_tasks 表中占一行，
// 用单条原子语句推进单调触发水位并协调运行租约。
//
//	sched := schedule.New(pgschedule.New(db), cfg.Schedule)
//
// 使用前先发布 Migrations 中的迁移。
package pgschedule

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/schedule"
)

var _ schedule.Store = (*Store)(nil)

//go:embed migrations
var migrationFiles embed.FS

// Migrations 返回 schedule_tasks 表的迁移，供应用发布。
func Migrations() fs.FS {
	f, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return f
}

// Store 把每个定时任务的状态保存为 schedule_tasks 表的一行：单调递增的 last_tick 以及运行租约。
type Store struct {
	db bun.IDB
}

// New 创建 PostgreSQL 定时任务状态存储。db 必须已执行 Migrations 中的迁移，为 nil 时 panic。
func New(db bun.IDB) *Store {
	if db == nil {
		panic("pgschedule: nil database")
	}
	return &Store{db: db}
}

// Claim 实现 schedule.Store：单条原子语句推进单调水位 last_tick，并在 allowOverlap 为 false 时尝试取得运行租约。
func (s *Store) Claim(ctx context.Context, name string, tick time.Time, owner string, ttl time.Duration, allowOverlap bool) (schedule.ClaimResult, error) {
	now := time.Now().UTC()
	tick = tick.UTC()
	until := now.Add(ttl)

	if allowOverlap {
		res, err := s.db.NewRaw(`
INSERT INTO schedule_tasks AS s (name, created_at, updated_at, last_tick)
VALUES (?, ?, ?, ?)
ON CONFLICT (name) DO UPDATE SET
    updated_at = excluded.updated_at,
    last_tick  = excluded.last_tick
WHERE s.last_tick IS NULL OR s.last_tick < excluded.last_tick`,
			name, now, now, tick,
		).Exec(ctx)
		if err != nil {
			return 0, errors.Wrapf(err, "pgschedule: claim %q", name)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, errors.Wrapf(err, "pgschedule: claim %q", name)
		}
		if n == 0 {
			return schedule.AlreadyClaimed, nil
		}
		return schedule.Claimed, nil
	}

	var heldOwner string
	err := s.db.NewRaw(`
INSERT INTO schedule_tasks AS s (name, created_at, updated_at, last_tick, running_owner, running_until)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (name) DO UPDATE SET
    updated_at    = excluded.updated_at,
    last_tick     = excluded.last_tick,
    running_owner = CASE
        WHEN s.running_until IS NULL OR s.running_until <= ? THEN excluded.running_owner
        ELSE s.running_owner
    END,
    running_until = CASE
        WHEN s.running_until IS NULL OR s.running_until <= ? THEN excluded.running_until
        ELSE s.running_until
    END
WHERE s.last_tick IS NULL OR s.last_tick < excluded.last_tick
RETURNING running_owner`,
		name, now, now, tick, owner, until, now, now,
	).Scan(ctx, &heldOwner)
	if errors.Is(err, sql.ErrNoRows) {
		return schedule.AlreadyClaimed, nil
	}
	if err != nil {
		return 0, errors.Wrapf(err, "pgschedule: claim %q", name)
	}
	if heldOwner == owner {
		return schedule.Claimed, nil
	}
	return schedule.Overlap, nil
}

// AcquireRunning 实现 schedule.Store：手动触发时不推进 last_tick，仅在无未过期租约时取得运行租约。
func (s *Store) AcquireRunning(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	until := now.Add(ttl)
	res, err := s.db.NewRaw(`
INSERT INTO schedule_tasks AS s (name, created_at, updated_at, running_owner, running_until)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (name) DO UPDATE SET
    updated_at    = excluded.updated_at,
    running_owner = excluded.running_owner,
    running_until = excluded.running_until
WHERE s.running_until IS NULL OR s.running_until <= ?`,
		name, now, now, owner, until, now,
	).Exec(ctx)
	if err != nil {
		return false, errors.Wrapf(err, "pgschedule: acquire %q", name)
	}
	n, err := res.RowsAffected()
	return n == 1, errors.Wrapf(err, "pgschedule: acquire %q", name)
}

// Refresh 实现 schedule.Store：续期 owner 持有的未过期运行租约。
func (s *Store) Refresh(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	res, err := s.db.NewRaw(`
UPDATE schedule_tasks
SET updated_at = ?, running_until = ?
WHERE name = ? AND running_owner = ? AND running_until > ?`,
		now, now.Add(ttl), name, owner, now,
	).Exec(ctx)
	if err != nil {
		return false, errors.Wrapf(err, "pgschedule: refresh %q", name)
	}
	n, err := res.RowsAffected()
	return n == 1, errors.Wrapf(err, "pgschedule: refresh %q", name)
}

// Release 实现 schedule.Store：释放 owner 持有的运行租约。
func (s *Store) Release(ctx context.Context, name, owner string) error {
	now := time.Now().UTC()
	_, err := s.db.NewRaw(`
UPDATE schedule_tasks
SET updated_at = ?, running_owner = '', running_until = NULL
WHERE name = ? AND running_owner = ?`,
		now, name, owner,
	).Exec(ctx)
	return errors.Wrapf(err, "pgschedule: release %q", name)
}

// States 实现 schedule.Store：单次查询返回全部任务的触发水位和运行状态。
func (s *Store) States(ctx context.Context) (map[string]schedule.TaskState, error) {
	now := time.Now().UTC()
	var rows []struct {
		Name         string       `bun:"name"`
		LastTick     sql.NullTime `bun:"last_tick"`
		RunningUntil sql.NullTime `bun:"running_until"`
	}
	err := s.db.NewRaw(`SELECT name, last_tick, running_until FROM schedule_tasks`).Scan(ctx, &rows)
	if err != nil {
		return nil, errors.Wrap(err, "pgschedule: states")
	}
	out := make(map[string]schedule.TaskState, len(rows))
	for _, r := range rows {
		var st schedule.TaskState
		if r.LastTick.Valid {
			st.LastTick = r.LastTick.Time.UTC()
		}
		if r.RunningUntil.Valid && r.RunningUntil.Time.After(now) {
			st.Running = true
		}
		out[r.Name] = st
	}
	return out, nil
}
