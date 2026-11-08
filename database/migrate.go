package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Migrate 用 goose 执行 fsys 根目录下所有未执行的迁移（Up），每执行一个记一条日志。
//
// 执行期间持有 Postgres session 级 advisory lock，多个实例同时启动时依次执行，
// 不会重复执行同一个迁移。db 传 *bun.DB 的 DB 字段即可：
//
//	if err := database.Migrate(ctx, db.DB, migrations.FS); err != nil { ... }
//
// 回滚、查看状态、创建迁移文件等运维操作使用 goose 命令行，见包文档。
func Migrate(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	// 每秒重试一次，最多等待 5 分钟（goose 默认每 5 秒重试）。
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 300))
	if err != nil {
		return fmt.Errorf("database: migrate: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("database: migrate: %w", err)
	}

	results, err := p.Up(ctx)
	for _, r := range results {
		if r.Error == nil {
			slog.InfoContext(ctx, "migration applied", "source", r.Source.Path, "duration", r.Duration)
		}
	}
	if err != nil {
		return fmt.Errorf("database: migrate: %w", err)
	}

	version, err := p.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("database: migrate: %w", err)
	}
	slog.InfoContext(ctx, "database migrated", "version", version, "applied", len(results))
	return nil
}
