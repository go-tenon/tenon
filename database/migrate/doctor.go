package migrate

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/doctor"
)

// Doctor 返回检查迁移状态的一组自检，组名为 migrations：up_to_date 确认 fsys 中的迁移都已执行。
// 有没执行的迁移时失败；数据库的版本比 fsys 中最新的迁移还新时提示，例如回滚了程序、或者滚动升级中旧版本还在运行。
//
// 检查不持有迁移锁，迁移正在执行时也不会等待。数据库中还没有迁移记录表时会创建它，与第一次 migrate up 相同。
// db 和 fsys 在检查时才使用。
func Doctor(db *bun.DB, fsys fs.FS) doctor.Group {
	return doctor.Group{Name: "migrations", Checks: []doctor.Check{
		{Name: "up_to_date", Run: func(ctx context.Context) error {
			p, err := NewProvider(db, fsys)
			if err != nil {
				return err
			}
			pending, err := p.HasPending(ctx)
			if err != nil {
				return doctor.Fail(err.Error(), "compare the files with `migrate status` and apply the missing ones")
			}
			current, target, err := p.GetVersions(ctx)
			if err != nil {
				return err
			}
			return checkVersions(pending, current, target)
		}},
	}}
}

// checkVersions 根据是否有没执行的迁移、数据库的版本 current 和程序中最新的版本 target 给出结论。
func checkVersions(pending bool, current, target int64) error {
	switch {
	case pending:
		return doctor.Fail(
			fmt.Sprintf("migrations have not been applied: the database is at version %d, this program expects %d", current, target),
			"run `migrate up` before starting this release",
		).See("docs/database/migrations.md")
	case current > target:
		return doctor.Warn(
			fmt.Sprintf("the database is at version %d, newer than this program's latest migration %d", current, target),
			"this program is older than the schema: finish rolling out the newer release, or deploy it again",
		)
	}
	return nil
}
