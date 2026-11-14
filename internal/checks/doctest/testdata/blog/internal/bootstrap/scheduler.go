package bootstrap

import (
	"context"
	"time"

	"example.com/blog/internal/article"
	"example.com/blog/internal/settings"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/schedule"
)

// tasks 创建调度器并注册全部定时任务。
func (a *App) tasks() *schedule.Scheduler {
	sched := schedule.New(a.locker, a.cfg.Schedule, nil,
		schedule.WithTelemetry(a.meters, a.tracers))
	sched.Add("articles:publish-scheduled", "* * * * *", article.NewPublishScheduled(a.db).Handle,
		schedule.Describe("发布到了预定时间的文章"))
	sched.Add("queue:prune-failed", "@daily", schedule.Cleanup(a.failed.Cleanup(30*24*time.Hour), 1000),
		schedule.Describe("删除 30 天前的失败任务"))
	return sched
}

// Scheduler 装配调度器，返回的函数按时执行定时任务，直到 ctx 被取消。
func (a *App) Scheduler(*command.Daemon) (func(context.Context) error, error) {
	return a.tasks().Run, nil
}

// Schedule 只运行调度器，schedule 命令调用它。
func Schedule(ctx context.Context, cfg settings.Config) error {
	return run(ctx, cfg, (*App).Scheduler)
}
