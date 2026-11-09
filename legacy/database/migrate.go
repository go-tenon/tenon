package database

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// NewMigrator 返回执行 fsys 根目录下 goose 迁移的 Provider，用于 Up、Down、Status 等操作。
//
// 执行期间持有 Postgres session 级 advisory lock，多个实例同时启动时依次执行，
// 不会重复执行同一个迁移。db 传 *bun.DB 的 DB 字段即可。
func NewMigrator(db *sql.DB, fsys fs.FS) (*goose.Provider, error) {
	// 每秒重试一次，最多等待 5 分钟（goose 默认每 5 秒重试）。
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 300))
	if err != nil {
		return nil, errors.Wrap(err, "database: migrate")
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, errors.Wrap(err, "database: migrate")
	}
	return p, nil
}

// Migrate 执行 fsys 根目录下所有未执行的迁移（Up），每执行一个记一条日志：
//
//	if err := database.Migrate(ctx, db.DB, migrations.FS); err != nil { ... }
func Migrate(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	p, err := NewMigrator(db, fsys)
	if err != nil {
		return err
	}

	results, err := p.Up(ctx)
	for _, r := range results {
		if r.Error == nil {
			slog.InfoContext(ctx, "migration applied", "source", r.Source.Path, "duration", r.Duration)
		}
	}
	if err != nil {
		return errors.Wrap(err, "database: migrate")
	}

	version, err := p.GetDBVersion(ctx)
	if err != nil {
		return errors.Wrap(err, "database: migrate")
	}
	slog.InfoContext(ctx, "database migrated", "version", version, "applied", len(results))
	return nil
}

// MigrationSource 是随 go-kit 模块发布的一组迁移。
//
// 模块的迁移不直接执行，而是由 PublishMigrations 复制到产品的迁移目录，
// 与产品自己的迁移一起提交、一起执行。
type MigrationSource struct {
	// Name 为模块名，作为发布后文件名的一部分，只能包含小写字母、数字与下划线。
	Name string
	// FS 根目录下的 *.sql 为迁移文件，按文件名排序，如 0001_create_users.sql。
	// 已发布的文件名不能再修改，否则会被当作新迁移再次发布。
	FS fs.FS
}

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_]*$`)

// PublishMigrations 把 sources 中尚未发布的迁移复制到 dir，返回新写入的文件路径。
//
// 发布后的文件名为 <版本号>_<模块名>_<原文件名>，例如
// 20261109120000_account_0001_create_users.sql。版本号取当前时间（YYYYMMDDHHMMSS），
// 且保证大于 dir 中已有的最大版本号，因此 go-kit 升级后新增的迁移总排在产品已有迁移之后。
//
// dir 中已存在以 _<模块名>_<原文件名> 结尾的文件即视为已发布，可以重复执行。
// 复制过来的文件归产品所有，可以审阅修改；内容变化不影响是否已发布的判断。
func PublishMigrations(dir string, sources ...MigrationSource) ([]string, error) {
	existing, version, err := scanMigrations(dir)
	if err != nil {
		return nil, err
	}

	var written []string
	for _, src := range sources {
		if !namePattern.MatchString(src.Name) {
			return written, errors.Newf("database: invalid migration source name %q", src.Name)
		}
		files, err := fs.Glob(src.FS, "*.sql")
		if err != nil {
			return written, errors.Wrapf(err, "database: %s migrations", src.Name)
		}
		slices.Sort(files)
		for _, file := range files {
			suffix := "_" + src.Name + "_" + file
			if slices.ContainsFunc(existing, func(name string) bool { return strings.HasSuffix(name, suffix) }) {
				continue
			}
			data, err := fs.ReadFile(src.FS, file)
			if err != nil {
				return written, errors.Wrapf(err, "database: %s migrations", src.Name)
			}
			version++
			name := strconv.FormatInt(version, 10) + suffix
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return written, errors.Wrap(err, "database: publish migration")
			}
			existing = append(existing, name)
			written = append(written, path)
		}
	}
	return written, nil
}

const migrationTemplate = `-- +goose Up


-- +goose Down

`

// CreateMigration 在 dir 中创建一个空的 SQL 迁移 <版本号>_<name>.sql 并返回其路径，
// 版本号规则与 PublishMigrations 相同。name 只能包含小写字母、数字与下划线，如 create_orders。
func CreateMigration(dir, name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", errors.Newf("database: invalid migration name %q (use snake_case)", name)
	}
	_, version, err := scanMigrations(dir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, strconv.FormatInt(version+1, 10)+"_"+name+".sql")
	if err := os.WriteFile(path, []byte(migrationTemplate), 0o644); err != nil {
		return "", errors.Wrap(err, "database: create migration")
	}
	return path, nil
}

// scanMigrations 返回 dir 中的 .sql 文件名，以及下一个版本号的前一个值：
// max(dir 中最大版本号, 当前时间 - 1 秒)，调用方加一后使用。dir 不存在时创建。
func scanMigrations(dir string) (names []string, version int64, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, 0, errors.Wrap(err, "database")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, errors.Wrap(err, "database")
	}
	now, _ := strconv.ParseInt(time.Now().UTC().Format("20060102150405"), 10, 64)
	version = now - 1
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		names = append(names, name)
		prefix, _, _ := strings.Cut(name, "_")
		if v, err := strconv.ParseInt(prefix, 10, 64); err == nil && v > version {
			version = v
		}
	}
	return names, version, nil
}
