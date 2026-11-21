package migratecmd

import (
	"fmt"
	"io/fs"
	"text/tabwriter"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/database/migrate"
)

// Verify 创建按需注册的 verify 子命令，检查 --dir 中已发布的框架迁移是否与 sources（功能包的 Migrations()）一致：
// 存在尚未发布的框架迁移或内容与当前框架不一致时输出差异并以非零状态退出，适合升级框架后和在 CI 中运行。
// 通过 New(...).AddCommand(Verify(...)) 挂载；离线运行，不连接数据库。
func Verify(sources ...fs.FS) *cobra.Command {
	var dir string
	cmd := leaf("verify", "检查应用目录中的框架迁移是否与当前框架版本一致", func(cmd *cobra.Command) error {
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		issues, err := migrate.Verify(dir, sources...)
		if err != nil {
			return err
		}
		if len(issues) == 0 {
			return nil
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		for _, issue := range issues {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", issue.Kind, issue.File, issue.Detail)
		}
		_ = tw.Flush()
		return errors.Newf("migrate: %d framework migration issues found in %s", len(issues), dir)
	})
	cmd.Flags().StringVar(&dir, "dir", "migrations", "应用的 SQL 迁移目录")
	return cmd
}
