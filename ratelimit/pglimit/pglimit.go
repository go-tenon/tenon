// Package pglimit 是基于 PostgreSQL 的限流器，实现 ratelimit.Limiter，多个实例共享配额。
//
//	logins := pglimit.New(db, "login", ratelimit.Config{Rate: 5, Interval: 15 * time.Minute, Burst: 5})
//
// 计数保存在 rate_limits 表中，使用前先发布 Migrations 中的迁移。每次 Allow 都写一次数据库，
// 适合登录、验证码、发邮件这类低频操作；每个请求都要经过的全局限流用 ratelimit.NewMemory。
package pglimit

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/ratelimit"
)

var _ ratelimit.Limiter = (*Limiter)(nil)

//go:embed migrations
var migrationFiles embed.FS

// Migrations 返回 rate_limits 表的迁移，供应用发布。
func Migrations() fs.FS {
	f, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return f
}

// Limiter 把每个 key 的理论到达时间（tat）保存为 rate_limits 表的一行，主键是 (name, key)。
// 算法与 ratelimit.Memory 相同（GCRA）；一次 Allow 是一条语句，同一个 key 的并发调用由行锁排队，
// 放行的次数准确。时间统一使用 PostgreSQL 的权威时钟 clock_timestamp()，避免多实例时钟漂移导致配额失真。
//
// tat 早于当前时间的行与不存在的行等价，由 Cleanup 删除。
type Limiter struct {
	db    bun.IDB
	name  string
	step  time.Duration // 补充一个配额的间隔
	ahead time.Duration // tat 最多领先当前时间的量
}

// New 创建 PostgreSQL 限流器。name 区分不同的限流器，同名的限流器共享配额，因此 name 应与 cfg 一一对应。
// db 通常是应用的连接池，必须已执行 Migrations 中的迁移。db 为 nil、name 为空或 cfg 不合法时 panic。
func New(db bun.IDB, name string, cfg ratelimit.Config) *Limiter {
	switch {
	case db == nil:
		panic("pglimit: nil database")
	case name == "":
		panic("pglimit: empty name")
	}
	if err := cfg.Validate(); err != nil {
		panic("pglimit: " + err.Error())
	}
	step := cfg.Interval / time.Duration(cfg.Rate)
	return &Limiter{db: db, name: name, step: step, ahead: step * time.Duration(cfg.Burst-1)}
}

// Allow 实现 ratelimit.Limiter。
//
// 通过 PostgreSQL 18 的 RETURNING old/new 单条原子语句完成判定：新的 key 插入 now + step；
// 已有的 key 在 tat 不晚于 now + ahead 时把 tat 推后 step，否则保持原值并直接用 old.tat 与 now 计算等待时间。
func (l *Limiter) Allow(ctx context.Context, key string) error {
	stepUS := l.step.Microseconds()
	aheadUS := l.ahead.Microseconds()
	var oldTat sql.NullTime
	var newTat, now time.Time
	err := l.db.NewRaw(`
WITH now_ts(t) AS MATERIALIZED (SELECT clock_timestamp())
INSERT INTO rate_limits AS r (name, key, tat)
SELECT ?, ?, t + ? * interval '1 microsecond' FROM now_ts
ON CONFLICT (name, key) DO UPDATE SET
    tat = CASE
        WHEN r.tat <= (SELECT t FROM now_ts) + ? * interval '1 microsecond'
        THEN greatest(r.tat, (SELECT t FROM now_ts)) + ? * interval '1 microsecond'
        ELSE r.tat
    END
RETURNING old.tat, new.tat, (SELECT t FROM now_ts) AS now`,
		l.name, key, stepUS, aheadUS, stepUS).Scan(ctx, &oldTat, &newTat, &now)
	if err != nil {
		return errors.Wrap(err, "ratelimit: allow")
	}
	if !oldTat.Valid || !newTat.Equal(oldTat.Time) {
		return nil
	}
	wait := oldTat.Time.Sub(now) - time.Duration(aheadUS)*time.Microsecond
	return ratelimit.ErrExceeded.New(errorx.RetryAfter(wait))
}

// Reset 实现 ratelimit.Limiter：删除 key 的行，与从未出现过的 key 等价。
func (l *Limiter) Reset(ctx context.Context, key string) error {
	_, err := l.db.NewRaw(`DELETE FROM rate_limits WHERE name = ? AND key = ?`, l.name, key).Exec(ctx)
	return errors.Wrap(err, "ratelimit: reset")
}

// Cleanup 返回删除 db 中配额已经回满的 key（涵盖所有限流器）的清理函数，每次最多删除 limit 个，返回删除的个数，
// 可以直接交给 schedule.Cleanup 定时执行。db 为 nil 时 panic。
func Cleanup(db bun.IDB) func(ctx context.Context, limit int) (int, error) {
	if db == nil {
		panic("pglimit: nil database")
	}
	return func(ctx context.Context, limit int) (int, error) {
		if limit <= 0 {
			return 0, errors.New("pglimit: cleanup limit must be positive")
		}
		res, err := db.NewRaw(`
DELETE FROM rate_limits WHERE (name, key) IN (
    SELECT name, key FROM rate_limits WHERE tat <= clock_timestamp() ORDER BY tat LIMIT ? FOR UPDATE SKIP LOCKED
)`, limit).Exec(ctx)
		if err != nil {
			return 0, errors.Wrap(err, "pglimit: cleanup")
		}
		n, err := res.RowsAffected()
		return int(n), errors.Wrap(err, "pglimit: cleanup")
	}
}

// Cleanup 删除最多 limit 个配额已经回满的 key（所有限流器的），返回删除的个数，可以直接交给 schedule.Cleanup 定时执行。
func (l *Limiter) Cleanup(ctx context.Context, limit int) (int, error) {
	return Cleanup(l.db)(ctx, limit)
}
