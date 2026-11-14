package bootstrap

import (
	"context"

	"example.com/blog/internal/mailjob"
	"example.com/blog/internal/settings"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/queue"
)

// Worker 装配队列 worker，返回的函数转发并处理队列任务，直到 ctx 被取消。
func (a *App) Worker(*command.Daemon) (func(context.Context) error, error) {
	mailer, err := newMailer(a.cfg.Mail)
	if err != nil {
		return nil, err
	}

	w := queue.NewWorker(a.db, a.cfg.Queue, nil,
		queue.WithTelemetry(a.meters, a.tracers))
	mailjob.Send.Handle(w, mailer.Send)
	return w.Run, nil
}

// Work 只运行队列 worker，work 命令调用它。
func Work(ctx context.Context, cfg settings.Config) error {
	return run(ctx, cfg, (*App).Worker)
}
