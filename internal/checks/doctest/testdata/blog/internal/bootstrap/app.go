package bootstrap

import (
	"context"
	"log/slog"

	"example.com/blog/internal/settings"
	"github.com/go-tenon/tenon/cleanup"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/lock/redislock"
	"github.com/go-tenon/tenon/logging"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/telemetry"
	"github.com/redis/rueidis"
	"github.com/uptrace/bun"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// App 是各个运行部分共用的资源，每个进程只装配一次。
type App struct {
	cfg     settings.Config
	db      *bun.DB
	rdb     rueidis.Client
	locker  *lock.Locker
	failed  *queue.FailedJobs
	meters  metric.MeterProvider
	tracers trace.TracerProvider
}

// open 开启推送，安装全局日志器，连接数据库和 Redis，把关闭操作登记到 stack。
func open(ctx context.Context, cfg settings.Config, stack *cleanup.Stack) (*App, error) {
	tel, err := telemetry.Open(ctx, cfg.Telemetry)
	if err != nil {
		return nil, err
	}
	stack.MustAdd("telemetry", tel.Shutdown)
	slog.SetDefault(logging.New(cfg.Logs, logging.Export(tel.Logs)))

	db, err := database.Open(ctx, cfg.Database, database.WithTelemetry(tel.Meters, tel.Tracers))
	if err != nil {
		return nil, err
	}
	stack.MustAdd("database", func(context.Context) error {
		return db.Close()
	})

	opt, err := rueidis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return nil, err
	}
	rdb, err := rueidis.NewClient(opt)
	if err != nil {
		return nil, err
	}
	stack.MustAdd("redis", func(context.Context) error {
		rdb.Close()
		return nil
	})

	return &App{
		cfg:     cfg,
		db:      db,
		rdb:     rdb,
		locker:  lock.New(redislock.New(rdb)),
		failed:  queue.NewFailedJobs(db),
		meters:  tel.Meters,
		tracers: tel.Tracers,
	}, nil
}

// run 装配 App 和 parts，然后在同一个进程中运行 parts，直到收到停止信号。
func run(ctx context.Context, cfg settings.Config, parts ...func(*App, *command.Daemon) (func(context.Context) error, error)) (err error) {
	d, err := command.NewDaemon(nil)
	if err != nil {
		return err
	}
	defer d.Stop()

	var stack cleanup.Stack
	defer func() {
		err = stack.CloseWith(context.Background(), err)
	}()

	app, err := open(ctx, cfg, &stack)
	if err != nil {
		return err
	}
	runs := make([]func(context.Context) error, len(parts))
	for i, part := range parts {
		if runs[i], err = part(app, d); err != nil {
			return err
		}
	}
	return d.Run(ctx, runs...)
}

// Serve 只运行 HTTP 服务，serve 命令调用它。
func Serve(ctx context.Context, cfg settings.Config) error {
	return run(ctx, cfg, (*App).HTTP)
}

// Start 在一个进程中运行 HTTP 服务、队列 worker 和定时任务调度器，start 命令调用它。
func Start(ctx context.Context, cfg settings.Config) error {
	return run(ctx, cfg, (*App).HTTP, (*App).Worker, (*App).Scheduler)
}
