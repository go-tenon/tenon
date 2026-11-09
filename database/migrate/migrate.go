package migrate

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"

	"github.com/cockroachdb/errors"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// NewProvider 使用 goose.Provider 与 Postgres session locker 装配迁移执行器。
// fsys 根目录为应用线性 SQL 迁移；禁用全局 goose 注册机制。
func NewProvider(db *sql.DB, fsys fs.FS) (*goose.Provider, error) {
	if db == nil {
		return nil, errors.New("migrate: nil db")
	}
	if fsys == nil {
		return nil, errors.New("migrate: nil fs")
	}
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 300))
	if err != nil {
		return nil, errors.Wrap(err, "migrate")
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, errors.Wrap(err, "migrate")
	}
	return p, nil
}

// Up 执行全部未应用迁移并记录日志。
func Up(ctx context.Context, p *goose.Provider) error {
	results, err := p.Up(ctx)
	for _, r := range results {
		if r.Error == nil {
			slog.InfoContext(ctx, "migration applied", "source", r.Source.Path, "duration", r.Duration)
		}
	}
	if err != nil {
		return errors.Wrap(err, "migrate: up")
	}
	return nil
}
