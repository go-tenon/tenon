package article

import (
	"github.com/spf13/cobra"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/command"
)

// Commands 声明 article 模块的命令，bootstrap.Commands 调用它。
func Commands(db *bun.DB) *cobra.Command {
	group := &cobra.Command{Use: "article", Short: "管理文章"}
	group.AddCommand(&cobra.Command{
		Use:   "publish <id>",
		Short: "立即发布一篇文章",
		Args:  command.ExactArgs(1),
		RunE:  NewPublish(db).Command,
	})
	return group
}
