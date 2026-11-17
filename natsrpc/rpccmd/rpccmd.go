// Package rpccmd 提供 rpc 命令：rpc asyncapi 输出 RPC 端点的 AsyncAPI 文档，rpc endpoints 列出端点。
// 它们从应用构造的 natsrpc.Server 中读取登记的端点，离线运行，不连接 NATS。
package rpccmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/natsrpc"
)

// New 返回 rpc 命令，加入应用的根命令。server 构造登记了全部端点的 Server，通常用零值的配置和依赖调用
// RPC 运行部分也在用的那个函数：
//
//	root.AddCommand(rpccmd.New(bootstrap.DocRPC))
//
// 登记端点时只保存依赖，处理请求时才使用，用零值构造才能成功；违反时命令 panic，堆栈指向使用依赖的地方。
func New(server func() *natsrpc.Server) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rpc",
		Short: "RPC 端点的接口描述",
		Args:  command.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "asyncapi",
		Short: "输出 RPC 端点的 AsyncAPI 3.0 文档",
		Args:  command.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := server().AsyncAPI()
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(append(data, '\n'))
			return err
		},
	}, &cobra.Command{
		Use:   "endpoints",
		Short: "列出 RPC 端点、subject 和需要的权限",
		Args:  command.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			srv := server()
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "SUBJECT\tPERMISSIONS\tHINTS\tDESCRIPTION")
			for _, e := range srv.Endpoints() {
				perms := strings.Join(e.Permissions, ",")
				if e.Public {
					perms = "(public)"
				}
				hints := "-"
				if e.Idempotent {
					hints = "idempotent"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", srv.Subject(e.Name), perms, hints, e.Description)
			}
			return w.Flush()
		},
	})
	return cmd
}
