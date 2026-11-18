package command

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/cleanup"
)

// Part 是常驻进程中的一个运行部分，例如 HTTP 服务、RPC 服务、队列 worker、定时任务调度器。
type Part struct {
	Name       string // serve 命令中的名字，例如 http、worker
	Short      string // 一句话说明，出现在 serve --help 中
	Standalone bool   // 只单独运行：serve 不带参数运行全部部分时不包含它，例如单独承接 WebSocket 的部分
	// Start 装配这个部分并用 d.Listen 取得监听端口，返回运行它的函数；run 一直运行到 ctx 被取消。
	Start func(d *Daemon) (run func(context.Context) error, err error)
}

// AllParts 是 serve 中表示全部部分的名字：serve all 与不带参数的 serve 相同，方便在 systemd 的模板单元中使用。
const AllParts = "all"

// Serve 构造 serve 命令，运行常驻进程。进程只有两种形态：不带参数（或者 AllParts）时运行 parts 中全部不是 Standalone 的部分，
// 带一个名字时只运行这一个部分，例如 serve worker。
//
// 执行时加载配置，用 open 打开各部分共用的资源，再用打开的资源调用 parts，按顺序调用选中部分的 Start；
// 全部装配成功后用 Daemon.Run 同时运行它们，直到收到停止信号，最后按相反顺序关闭 open 登记的资源。
// 任何一个部分装配失败时，没有部分开始运行。
//
// 构造命令时，parts 收到指向零值 D 的指针，只用来列出部分的名字和说明，所以 parts 中只取方法值，不使用依赖。
// 名字为空、重复、含空白或者是 AllParts，以及 Start 为 nil 时 panic。
func Serve[C, D any](open func(ctx context.Context, cfg C, stack *cleanup.Stack) (*D, error), parts func(*D) []Part) *cobra.Command {
	declared := parts(new(D))
	names := make([]string, 0, len(declared))
	var list strings.Builder
	for _, p := range declared {
		if p.Name == "" || p.Name == AllParts || strings.ContainsAny(p.Name, " \t") || slices.Contains(names, p.Name) || p.Start == nil {
			panic(fmt.Sprintf("command: invalid or duplicate part %q", p.Name))
		}
		names = append(names, p.Name)
		short := p.Short
		if p.Standalone {
			short += "（只单独运行）"
		}
		fmt.Fprintf(&list, "\n  %-12s %s", p.Name, short)
	}
	cmd := &cobra.Command{
		Use:   "serve [part]",
		Short: "运行常驻进程：不带参数时运行全部部分，带参数时只运行这一个部分",
		Long:  "运行常驻进程：不带参数（或者 all）时运行全部部分，带参数时只运行这一个部分。\n\n部分：" + list.String(),
		Args: func(_ *cobra.Command, args []string) error {
			switch {
			case len(args) > 1:
				return AsExitError(ExitUsage, errors.Newf("serve runs all parts or exactly one, got %d", len(args)))
			case len(args) == 1 && args[0] != AllParts && !slices.Contains(names, args[0]):
				return AsExitError(ExitUsage, errors.Newf("unknown part %q, want one of %s", args[0], strings.Join(names, ", ")))
			}
			return nil
		},
		ValidArgs: append(slices.Clone(names), AllParts),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			cfg, err := LoadConfig[C](cmd)
			if err != nil {
				return err
			}
			d, err := NewDaemon(nil)
			if err != nil {
				return err
			}
			defer d.Stop()

			var stack cleanup.Stack
			defer func() { err = stack.CloseWith(context.Background(), err) }()
			deps, err := open(cmd.Context(), cfg, &stack)
			if err != nil {
				return err
			}

			// 选出要运行的部分：点名的那一个，或者全部不是 Standalone 的部分。
			name := AllParts
			if len(args) == 1 {
				name = args[0]
			}
			var selected []Part
			for _, p := range parts(deps) {
				if p.Name == name || (name == AllParts && !p.Standalone) {
					selected = append(selected, p)
				}
			}
			if len(selected) == 0 {
				return errors.Newf("command: serve %s: no part to run", name)
			}

			// 先装配全部部分，再同时运行：端口被占用这类错误在任何部分开始运行之前返回。
			runs := make([]func(context.Context) error, len(selected))
			for i, p := range selected {
				if runs[i], err = p.Start(d); err != nil {
					return errors.Wrapf(err, "command: start %s", p.Name)
				}
				if runs[i] == nil {
					return errors.Newf("command: start %s returned no run function", p.Name)
				}
			}
			return d.Run(cmd.Context(), runs...)
		},
	}
	return InGroup(cmd, GroupRun)
}
