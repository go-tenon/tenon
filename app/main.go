package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/spf13/cobra"

	"github.com/ryan053857/go-kit/config"
	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/logger"
)

// Main 是产品 main 函数的全部内容：解析命令行并执行对应子命令，出错时以状态码 1 退出。
// cfg 为内嵌了 Config 的产品配置指针，调用前填好默认值：
//
//	func main() {
//		cfg := Config{Config: app.DefaultConfig()}
//		app.Main(&cfg, app.Options{
//			Name:       "myapp",
//			Version:    version,
//			Migrations: migrations.FS,
//			Setup:      func(a *app.App) error { return routes.Setup(a, &cfg) },
//		})
//	}
//
// 子命令（-c / --config 指定配置文件，默认 config.yaml）：
//
//	serve（默认）       执行迁移后启动 HTTP 服务；--migrate=false 跳过迁移
//	migrate up          执行全部未执行的迁移
//	migrate down        回滚最近一个迁移
//	migrate status      查看迁移状态
//	migrate create NAME 在迁移目录新建迁移文件（不读配置）
//	migrate publish     把 go-kit 模块的迁移复制到迁移目录（不读配置）
//	openapi             输出 OpenAPI 文档（不连接数据库）；--format json|yaml
func Main(cfg Configurer, opts Options) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := Command(cfg, opts).ExecuteContext(ctx)
	stop()
	if err != nil {
		os.Exit(1)
	}
}

// Command 返回 Main 使用的根命令。产品需要追加自己的子命令时用它代替 Main：
//
//	root := app.Command(&cfg, opts)
//	root.AddCommand(myCommand)
//	if err := root.Execute(); err != nil { os.Exit(1) }
func Command(cfg Configurer, opts Options) *cobra.Command {
	var configPath string
	// load 读取配置并初始化默认 logger，只执行一次。
	loaded := false
	load := func() error {
		if loaded {
			return nil
		}
		if err := config.Load(configPath, cfg); err != nil {
			return err
		}
		c := cfg.AppConfig()
		slog.SetDefault(logger.New(logger.Options{Level: c.Log.Level, Format: c.Log.Format}).
			With("service", opts.Name, "version", opts.Version))
		loaded = true
		return nil
	}

	var migrate bool
	serve := &cobra.Command{
		Use:   "serve",
		Short: "执行迁移并启动 HTTP 服务",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := load(); err != nil {
				return err
			}
			ctx := cmd.Context()
			slog.InfoContext(ctx, "config loaded", "config", cfg) // Secret 字段已打码
			a, err := build(ctx, cfg.AppConfig(), opts, true, migrate)
			if err != nil {
				return err
			}
			defer a.Close()
			return a.Serve(ctx)
		},
	}
	serve.Flags().BoolVar(&migrate, "migrate", true, "启动前执行迁移")

	root := &cobra.Command{
		Use:          opts.Name,
		Short:        opts.Name + " 服务",
		Version:      opts.Version,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE:         serve.RunE,
	}
	root.PersistentFlags().StringVarP(&configPath, "config", "c", "config.yaml", "配置文件路径")
	root.Flags().AddFlagSet(serve.Flags())
	root.AddCommand(serve, migrateCommand(cfg, opts, load), openapiCommand(cfg, opts, load))
	return root
}

func migrateCommand(cfg Configurer, opts Options, load func() error) *cobra.Command {
	cmd := &cobra.Command{Use: "migrate", Short: "数据库迁移"}

	// withMigrator 读取配置、连接数据库，用产品迁移创建 Provider 后调用 fn。
	withMigrator := func(cmd *cobra.Command, fn func(*migrator, context.Context) error) error {
		if opts.Migrations == nil {
			return errNoMigrations
		}
		if err := load(); err != nil {
			return err
		}
		ctx := cmd.Context()
		db, err := database.Open(ctx, cfg.AppConfig().Database)
		if err != nil {
			return err
		}
		defer db.Close()
		p, err := database.NewMigrator(db.DB, opts.Migrations)
		if err != nil {
			return err
		}
		return fn(&migrator{p: p, out: cmd.OutOrStdout()}, ctx)
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "up",
			Short: "执行全部未执行的迁移",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				if opts.Migrations == nil {
					return errNoMigrations
				}
				if err := load(); err != nil {
					return err
				}
				db, err := database.Open(cmd.Context(), cfg.AppConfig().Database)
				if err != nil {
					return err
				}
				defer db.Close()
				return database.Migrate(cmd.Context(), db.DB, opts.Migrations)
			},
		},
		&cobra.Command{
			Use:   "down",
			Short: "回滚最近一个迁移",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return withMigrator(cmd, (*migrator).down)
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "查看迁移状态",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return withMigrator(cmd, (*migrator).status)
			},
		},
		&cobra.Command{
			Use:   "create NAME",
			Short: "新建迁移文件，NAME 为 snake_case，如 create_orders",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				path, err := database.CreateMigration(opts.migrationsDir(), args[0])
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "created", path)
				return nil
			},
		},
		&cobra.Command{
			Use:   "publish",
			Short: "把 go-kit 模块尚未发布的迁移复制到迁移目录",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				paths, err := database.PublishMigrations(opts.migrationsDir(), opts.publishSources()...)
				for _, p := range paths {
					fmt.Fprintln(cmd.OutOrStdout(), "published", p)
				}
				if err == nil && len(paths) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "nothing to publish")
				}
				return err
			},
		},
	)
	return cmd
}

func openapiCommand(cfg Configurer, opts Options, load func() error) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "openapi",
		Short: "输出 OpenAPI 文档（不连接数据库）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := load(); err != nil {
				return err
			}
			a, err := build(cmd.Context(), cfg.AppConfig(), opts, false, false)
			if err != nil {
				return err
			}
			defer a.Close()

			spec := a.API().OpenAPI()
			var data []byte
			switch format {
			case "yaml":
				data, err = spec.YAML()
			case "json":
				data, err = json.MarshalIndent(spec, "", "  ")
			default:
				return fmt.Errorf("unknown format %q (json | yaml)", format)
			}
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(append(data, '\n'))
			return err
		},
	}
	cmd.Flags().StringVar(&format, "format", "yaml", "输出格式：json | yaml")
	return cmd
}

type migrator struct {
	p   *goose.Provider
	out io.Writer
}

func (m *migrator) down(ctx context.Context) error {
	r, err := m.p.Down(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(m.out, "rolled back", r.Source.Path)
	return nil
}

func (m *migrator) status(ctx context.Context) error {
	list, err := m.p.Status(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(m.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATE\tAPPLIED AT\tMIGRATION")
	for _, s := range list {
		applied := "-"
		if !s.AppliedAt.IsZero() {
			applied = s.AppliedAt.Local().Format(time.DateTime)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.State, applied, s.Source.Path)
	}
	return w.Flush()
}
