// Package pglock 是基于 PostgreSQL 的锁存储，实现 lock.Store。
//
//	locker := lock.New(pglock.New(db))
//
// 锁保存在 locks 表中，使用前先发布 Migrations 中的迁移。
package pglock

import (
	"context"
	"embed"
	"io/fs"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/lock"
)

var _ lock.Store = (*Store)(nil)

//go:embed migrations
var migrationFiles embed.FS

// Migrations 返回 locks 表的迁移，供应用发布。
func Migrations() fs.FS {
	f, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return f
}

// Store 把每个锁保存为 locks 表的一行：锁名、持有者和过期时间。每个操作是一条语句，原子地完成。
// 时间按应用服务器的时钟计算（与框架的其他 SQL 一样，测试中的假时钟对它生效），各实例的时钟需要同步，
// 偏差要远小于锁的有效期。
//
// 过期的行等同于不存在：下一次 Acquire 直接接管它。剩下的过期行由 Cleanup 删除，
// 例如定时任务的锁名带有触发时刻，每次触发留下一行，需要定时清理。
type Store struct {
	db bun.IDB
}

// New 创建 PostgreSQL 锁存储。db 是连接池或事务都可以，通常是应用的连接池；db 必须已执行 Migrations 中的迁移。
// db 为 nil 时 panic。
func New(db bun.IDB) *Store {
	if db == nil {
		panic("pglock: nil database")
	}
	return &Store{db: db}
}

// Acquire 实现 lock.Store：插入锁；已有同名的锁时，只接管已经过期的。
func (s *Store) Acquire(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
	now := time.Now()
	return s.exec(ctx, "acquire", `
INSERT INTO locks AS l (name, owner, expires_at) VALUES (?, ?, ?)
ON CONFLICT (name) DO UPDATE SET owner = excluded.owner, expires_at = excluded.expires_at
WHERE l.expires_at <= ?`, name, owner, now.Add(ttl), now)
}

// Refresh 实现 lock.Store：持有者仍持有、锁没有过期时，把过期时间改为 ttl 之后。
func (s *Store) Refresh(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
	now := time.Now()
	return s.exec(ctx, "refresh", `
UPDATE locks SET expires_at = ? WHERE name = ? AND owner = ? AND expires_at > ?`, now.Add(ttl), name, owner, now)
}

// Release 实现 lock.Store：删除持有者仍持有、没有过期的锁。已经过期的行留给 Cleanup。
func (s *Store) Release(ctx context.Context, name, owner string) (bool, error) {
	return s.exec(ctx, "release", `
DELETE FROM locks WHERE name = ? AND owner = ? AND expires_at > ?`, name, owner, time.Now())
}

// Held 实现 lock.Store。
func (s *Store) Held(ctx context.Context, name string) (bool, error) {
	var held bool
	err := s.db.NewRaw(`SELECT EXISTS (SELECT 1 FROM locks WHERE name = ? AND expires_at > ?)`, name, time.Now()).Scan(ctx, &held)
	return held, errors.Wrap(err, "pglock: held")
}

// Cleanup 删除最多 limit 个已经过期的锁，返回删除的个数，可以直接交给 schedule.Cleanup 定时执行。
func (s *Store) Cleanup(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, errors.New("pglock: cleanup limit must be positive")
	}
	res, err := s.db.NewRaw(`
DELETE FROM locks WHERE name IN (
    SELECT name FROM locks WHERE expires_at <= ? ORDER BY expires_at LIMIT ? FOR UPDATE SKIP LOCKED
)`, time.Now(), limit).Exec(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "pglock: cleanup")
	}
	n, err := res.RowsAffected()
	return int(n), errors.Wrap(err, "pglock: cleanup")
}

// exec 执行 query，返回是否有行受影响。
func (s *Store) exec(ctx context.Context, op, query string, args ...any) (bool, error) {
	res, err := s.db.NewRaw(query, args...).Exec(ctx)
	if err != nil {
		return false, errors.Wrap(err, "pglock: "+op)
	}
	n, err := res.RowsAffected()
	return n == 1, errors.Wrap(err, "pglock: "+op)
}
