package bootstrap

import (
	"github.com/spf13/cobra"

	"example.com/blog/internal/article"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/queue/queuecmd"
	"github.com/go-tenon/tenon/schedule/schedulecmd"
	"github.com/go-tenon/tenon/search/searchcmd"
)

// Commands 返回需要 App 的命令，执行时才装配 App。
func Commands() []*cobra.Command {
	return command.Bind(open, func(a *App) []*cobra.Command {
		return []*cobra.Command{
			article.Commands(a.db),
			queuecmd.New(a.db),
			schedulecmd.New(a.tasks),
			searchcmd.New(a.db, article.SearchIndex),
			seed(a.db),
		}
	})
}
