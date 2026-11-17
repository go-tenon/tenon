// Package migratecmd 提供 migrate up/status/down 命令。
package migratecmd

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"text/tabwriter"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/pressly/goose/v3"
	"github.com/spf13/cobra"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/migrate"
	"github.com/go-tenon/tenon/doctor"
)

// Option 调整 New 构造的命令。
type Option func(*options)

// options 是 migrate 命令的选项。
type options struct {
	name  string                                     // 为空时是主库的 migrate 命令
	check func(cmd *cobra.Command, db *bun.DB) error // 在 up 执行迁移之前运行自检，为 nil 时不检查。
}

// Name 为另一个数据库构造迁移命令：命令名是 name，挂在主库的 migrate 命令下，例如 migrate analytics up；
// make 默认在 migrations/<name> 中新建文件。每个数据库有自己的迁移文件和迁移记录表。name 为空时 panic。
//
//	mig := migratecmd.New(migrations.FS, func(c Config) database.Config { return c.Database })
//	mig.AddCommand(migratecmd.New(analytics.FS, func(c Config) database.Config { return c.Analytics }, migratecmd.Name("analytics")))
func Name(name string) Option {
	if name == "" {
		panic("migratecmd: empty name")
	}
	return func(o *options) { o.name = name }
}

// Check 让 migrate up 在执行迁移之前运行 groups 返回的自检：有检查失败时不执行迁移，以 command.ExitUnavailable 退出，
// 错误中列出每一项问题和修改方法；提示写入日志。C 是应用的配置结构，与 New 的相同；db 是迁移使用的连接。
//
// 环境不满足要求时，迁移本身往往以难懂的方式失败，例如 wal_level 不是 logical 时队列的迁移报
// logical decoding requires "wal_level" >= "logical"。在这里检查数据库一侧的要求，迁移之前就能给出修改方法：
//
//	migratecmd.New(migrations.FS, dbConfig, migratecmd.Check(func(cfg Config, db *bun.DB) []doctor.Group {
//	    return []doctor.Group{database.Doctor(db), queue.Doctor(db, nil, cfg.Queue)}
//	}))
func Check[C any](groups func(cfg C, db *bun.DB) []doctor.Group) Option {
	return func(o *options) {
		o.check = func(cmd *cobra.Command, db *bun.DB) error {
			cfg, err := command.LoadConfig[C](cmd)
			if err != nil {
				return err
			}
			return doctor.Require(cmd.Context(), nil, groups(cfg, db)...)
		}
	}
}

// New 构造 migrate 命令及其 up、status、down、make 子命令。fsys 的根目录直接包含 SQL 迁移文件，
// dbConfig 从应用配置中取出数据库配置。数据库连不上时以 ExitUnavailable 退出；
// make 离线运行。应用连接多个数据库时，其他数据库的命令用 Name 构造。
func New[C any](fsys fs.FS, dbConfig func(cfg C) database.Config, opts ...Option) *cobra.Command {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	// withProvider 连接数据库并创建迁移器，run 结束后关闭连接。check 为真时先运行 Check 设置的自检。
	withProvider := func(check bool, run func(context.Context, io.Writer, *goose.Provider) error) func(*cobra.Command) error {
		return func(cmd *cobra.Command) error {
			cfg, err := command.LoadConfig[C](cmd)
			if err != nil {
				return err
			}
			db, err := database.Open(cmd.Context(), dbConfig(cfg))
			if err != nil {
				return err
			}
			defer db.Close()
			if check && o.check != nil {
				if err := o.check(cmd, db); err != nil {
					return err
				}
			}
			provider, err := migrate.NewProvider(db, fsys)
			if err != nil {
				return err
			}
			return run(cmd.Context(), cmd.OutOrStdout(), provider)
		}
	}
	use, short, dir := "migrate", "数据库迁移", "migrations"
	if o.name != "" {
		use, short, dir = o.name, "数据库 "+o.name+" 的迁移", "migrations/"+o.name
	}
	root := leaf(use, short, func(cmd *cobra.Command) error { return cmd.Help() })
	root.AddCommand(
		leaf("up", "执行全部未执行的迁移", withProvider(true, func(ctx context.Context, out io.Writer, p *goose.Provider) error {
			results, err := p.Up(ctx)
			printResults(out, results)
			return errors.Wrap(err, "migrate: up")
		})),
		leaf("status", "查看迁移状态", withProvider(false, func(ctx context.Context, out io.Writer, p *goose.Provider) error {
			statuses, err := p.Status(ctx)
			if err != nil {
				return errors.Wrap(err, "migrate: status")
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "VERSION\tSTATE\tAPPLIED AT\tFILE")
			for _, s := range statuses {
				applied := "-"
				if !s.AppliedAt.IsZero() {
					applied = s.AppliedAt.Local().Format(time.DateTime)
				}
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", s.Source.Version, s.State, applied, migrate.FileName(s.Source.Path))
			}
			return tw.Flush()
		})),
		leaf("down", "回滚最近一次迁移", withProvider(false, func(ctx context.Context, out io.Writer, p *goose.Provider) error {
			result, err := p.Down(ctx)
			if result != nil {
				printResults(out, []*goose.MigrationResult{result})
			}
			return errors.Wrap(err, "migrate: down")
		})),
		makeIn(dir),
	)
	return root
}

// printResults 每行输出一个已完成的迁移，例如 "up    2026_11_11_090000_create_articles_table.sql  12ms"。
func printResults(out io.Writer, results []*goose.MigrationResult) {
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, r := range results {
		if r.Error == nil {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Direction, migrate.FileName(r.Source.Path), r.Duration.Round(time.Millisecond))
		}
	}
	_ = tw.Flush() // 写的是命令的输出，与 Fprintf 一样忽略错误
}

// leaf 构造不接受位置参数的命令。
func leaf(use, short string, run func(cmd *cobra.Command) error) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  command.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return run(cmd) },
	}
}
