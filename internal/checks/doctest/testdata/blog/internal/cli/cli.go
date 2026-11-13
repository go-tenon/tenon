package cli

import (
	"github.com/spf13/cobra"

	"example.com/blog/internal/bootstrap"
	"example.com/blog/internal/settings"
	"example.com/blog/migrations"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/command/httpcmd"
	"github.com/go-tenon/tenon/command/migratecmd"
	"github.com/go-tenon/tenon/database"
)

// New 返回应用的根命令。
func New() *cobra.Command {
	root := command.Root("api", "Blog API")
	root.AddCommand(
		command.Run("serve", "启动 HTTP 服务", bootstrap.Serve),
		command.Run("work", "处理队列任务", bootstrap.Work),
		command.Run("schedule", "执行定时任务", bootstrap.Schedule),
		command.Run("start", "在一个进程中运行 HTTP 服务、队列和定时任务", bootstrap.Start),
		migratecmd.New(migrations.FS, dbConfig),
		command.Config[settings.Config](),
	)
	root.AddCommand(httpcmd.Commands(bootstrap.API, bootstrap.DocRoutes, bootstrap.DocRouter)...)
	root.AddCommand(bootstrap.Commands()...)
	return root
}

func dbConfig(cfg settings.Config) database.Config { return cfg.Database }
