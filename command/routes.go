package command

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"

	"github.com/ryan053857/mygoravel/httpx/rest"
)

// NewRoutes 构造路由总览命令，支持 --method 和 --path 过滤。
// buildAPI 应在 ModeOpenAPI 下调用应用的统一路由注册入口，不打开运行时资源。
// 命令遍历该 API 的实际路由；ACCESS 显示声明的策略，无法识别时为 unknown。
// SUMMARY 显示路由摘要，没有摘要时为 -。
func NewRoutes(buildAPI func(context.Context) (*rest.API, error)) *cobra.Command {
	var method, path string
	cmd := &cobra.Command{Use: "routes", Short: "查看应用的全部路由"}
	cmd.Flags().StringVar(&method, "method", "", "按 HTTP 方法过滤（不区分大小写）")
	cmd.Flags().StringVar(&path, "path", "", "按路径包含的文本过滤")
	cmd.RunE = wrap(func(ctx context.Context, cmd *cobra.Command, _ []string) error {
		if buildAPI == nil {
			return errors.New("routes: nil API factory")
		}
		api, err := buildAPI(ctx)
		if err != nil {
			return err
		}
		if api == nil {
			return errors.New("routes: API factory returned nil")
		}
		out := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(out, "METHOD\tPATH\tOPERATION ID\tACCESS\tSUMMARY"); err != nil {
			return err
		}
		for _, route := range api.Routes() {
			if method != "" && !strings.EqualFold(route.Method, method) {
				continue
			}
			if !strings.Contains(route.Path, path) {
				continue
			}
			operationID := route.OperationID
			if operationID == "" {
				operationID = "-"
			}
			summary := tableCell(route.Summary)
			if summary == "" {
				summary = "-"
			}
			if _, err := fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n", route.Method, tableCell(route.Path), tableCell(operationID), tableCell(route.Access), summary); err != nil {
				return err
			}
		}
		return out.Flush()
	})
	return cmd
}

func tableCell(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
