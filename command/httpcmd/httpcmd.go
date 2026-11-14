// Package httpcmd 提供与 HTTP 路由有关的命令：openapi 输出 JSON API 的 OpenAPI 文档，
// routes 输出 JSON API 和页面的路由表，client 生成 TypeScript 和 Go 客户端（tenon generate 调用它）。
// 它们都从应用构造的路由中读取信息，不读取配置，不启动服务，不调用处理器。Commands 一次返回这三个命令。
package httpcmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/httpx/rest/clientgen"
)

// Commands 返回 openapi、routes、client 三个命令，加入应用的根命令。router 构造应用的完整路由，
// 通常用零值的配置和依赖调用 serve 也在用的那个函数：
//
//	root.AddCommand(httpcmd.Commands(func() chi.Routes { return routes(Config{}, nil) })...)
//
// 注册路由时只保存依赖、不调用依赖，用零值构造才不会出错；违反时命令 panic，堆栈指向使用依赖的地方。
func Commands(router func() chi.Routes) []*cobra.Command {
	return []*cobra.Command{OpenAPI(router), Routes(router), clientgen.Command(router)}
}

// OpenAPI 构造 openapi 命令：调用 router 构造路由，用 rest.Lookup 找到其中的 API，
// 把 OpenAPI 文档输出到标准输出。路由中有多个 API 时用 --base-path 选择。
//
// 默认输出 OpenAPI 3.1；--spec 3.0 输出转换后的 3.0.3（不含 webhooks），供只支持 3.0 的客户端生成器使用，例如 oapi-codegen。
func OpenAPI(router func() chi.Routes) *cobra.Command {
	var spec, basePath string
	cmd := leaf("openapi", "输出 OpenAPI 文档", func(cmd *cobra.Command) error {
		if spec != "3.1" && spec != "3.0" {
			return command.AsExitError(command.ExitUsage, fmt.Errorf("--spec must be 3.1 or 3.0, got %q", spec))
		}
		api, err := rest.Lookup(router(), basePath)
		if err != nil {
			return command.AsExitError(command.ExitUsage, err)
		}
		data, err := api.OpenAPIJSON()
		if spec == "3.0" {
			data, err = api.OpenAPI30JSON()
		}
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(append(data, '\n'))
		return err
	})
	cmd.Flags().StringVar(&spec, "spec", "3.1", "OpenAPI 版本：3.1 或 3.0")
	cmd.Flags().StringVar(&basePath, "base-path", "", "路由中有多个 API 时，按 BasePath 选择一个，例如 /api/v1")
	return cmd
}

// Routes 构造 routes 命令：调用 router 构造路由，用 httpx.Routes 输出路由表。
// 支持 --method、--path 过滤；Huma 的文档端点默认隐藏，--all 时以 docs 显示。
func Routes(router func() chi.Routes) *cobra.Command {
	var method, path string
	var all bool
	cmd := leaf("routes", "查看应用的全部路由", func(cmd *cobra.Command) error {
		out := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(out, "METHOD\tPATH\tOPERATION ID\tACCESS\tSUMMARY")
		for _, route := range httpx.Routes(router()) {
			if method != "" && route.Method != httpx.AnyMethod && !strings.EqualFold(route.Method, method) ||
				!strings.Contains(route.Path, path) ||
				route.Access == httpx.AccessDocs && !all {
				continue
			}
			fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n", route.Method, cell(route.Path),
				orDash(cell(route.OperationID)), cell(access(route)), orDash(cell(route.Summary)))
		}
		return out.Flush()
	})
	cmd.Flags().StringVar(&method, "method", "", "按 HTTP 方法过滤（不区分大小写），响应任意方法的 * 路由总是列出")
	cmd.Flags().StringVar(&path, "path", "", "按路径包含的文本过滤")
	cmd.Flags().BoolVar(&all, "all", false, "同时列出 /docs、/openapi.json 等文档端点")
	return cmd
}

func access(route httpx.Route) string {
	if len(route.Permissions) == 0 {
		return route.Access
	}
	return route.Access + ": " + strings.Join(route.Permissions, ", ")
}

// cell 把换行、制表符等空白合并为单个空格，保持每行一条记录。
func cell(value string) string { return strings.Join(strings.Fields(value), " ") }

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
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
