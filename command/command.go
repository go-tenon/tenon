package command

import (
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/config"
)

const (
	configFlag = "config"
	// configEnv 是没有传入 --config 时读取的环境变量，格式与 --config 相同：逗号分隔的配置文件。
	configEnv = "APP_CONFIG"
)

// 命令的分组。Execute 按子命令的 Annotations[GroupKey] 把根命令的子命令分组显示在帮助中，没有标注的归入 GroupApp，
// help 和 completion 归入 GroupOps。
const (
	GroupKey = "tenon.group" // Annotations 中分组的键，用 InGroup 设置
	GroupRun = "run"         // 常驻进程：serve
	GroupOps = "ops"         // 运维：migrate、config、doctor、queue、schedules、health 等
	GroupApp = "app"         // 应用自己的命令，例如 article publish
	GroupDev = "dev"         // 开发：openapi、routes、client、rpc 等离线生成文档和代码的命令
)

// InGroup 把 cmd 标注为分组 id，返回 cmd。挂在根命令下、用 Execute 执行时，帮助按分组列出命令。
func InGroup(cmd *cobra.Command, id string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[GroupKey] = id
	return cmd
}

// Root 返回应用的根命令，用 AddCommand 装配需要的命令。
// 根命令定义了所有子命令共用的 --config 选项：可以重复传入多个配置文件，
// 后面的覆盖前面的。没有传入时读取环境变量 APP_CONFIG，它也没有设置时为 config/app.yaml。未知命令、未知选项和多余参数标记为 ExitUsage。
func Root(name, short string) *cobra.Command {
	root := &cobra.Command{
		Use:           name,
		Short:         short,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          NoArgs,
		RunE:          func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return AsExitError(ExitUsage, err) })
	root.PersistentFlags().StringSlice(configFlag, []string{"config/app.yaml"}, "配置文件，可以传入多次，后面的覆盖前面的；没有传入时读取环境变量 "+configEnv)
	return root
}

// Run 构造一个加载配置后执行 run 的命令，适合只需要配置的一次性任务，例如发送一次报表。
// run 的 ctx 在进程收到 SIGINT、SIGTERM 时被取消。常驻进程用 Serve，需要应用资源的命令用 Bind。
func Run[C any](use, short string, run func(ctx context.Context, cfg C) error) *cobra.Command {
	return leaf(use, short, func(cmd *cobra.Command) error {
		cfg, err := LoadConfig[C](cmd)
		if err != nil {
			return err
		}
		return run(cmd.Context(), cfg)
	})
}

// LoadConfig 加载并校验配置文件，失败时错误带 ExitConfig 退出码。
// 加载前先读取工作目录下的 .env（不存在时忽略），进程中已经设置的变量优先。
// 配置文件依次取自 --config、环境变量 APP_CONFIG（可以写在 .env 中）和默认的 config/app.yaml。
// cmd 必须挂在 Root 返回的根命令下。
func LoadConfig[C any](cmd *cobra.Command) (C, error) {
	var zero C
	files, err := cmd.Flags().GetStringSlice(configFlag)
	if err != nil {
		return zero, errors.Wrap(err, "command: --config is defined by command.Root")
	}
	if err := config.LoadEnv(".env"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return zero, AsExitError(ExitConfig, err)
	}
	if v := os.Getenv(configEnv); v != "" && !cmd.Flags().Changed(configFlag) {
		if files = splitList(v); len(files) == 0 {
			return zero, AsExitError(ExitConfig, errors.Newf("command: %s=%q lists no config files", configEnv, v))
		}
	}
	cfg, err := config.Load[C](files...)
	return cfg, AsExitError(ExitConfig, err)
}

// splitList 按逗号拆分 s，去掉空白和空项。
func splitList(s string) []string {
	var out []string
	for f := range strings.SplitSeq(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// Execute 执行根命令：SIGINT、SIGTERM 会取消命令的 context；
// 失败时把错误写到标准错误，并以 ExitCode(err) 结束进程。
func Execute(root *cobra.Command) {
	assignGroups(root)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := root.ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(root.ErrOrStderr(), err)
		os.Exit(ExitCode(err))
	}
}

// groupTitles 是各分组在帮助中的标题，按显示的顺序排列。
var groupTitles = []cobra.Group{
	{ID: GroupRun, Title: "运行："},
	{ID: GroupOps, Title: "运维："},
	{ID: GroupApp, Title: "应用："},
	{ID: GroupDev, Title: "开发："},
}

// assignGroups 按 InGroup 的标注设置 root 子命令的 GroupID，没有标注的归入 GroupApp，help 和 completion 归入 GroupOps；
// 只添加用到的分组。已经设置了 GroupID 的命令保持原样。
func assignGroups(root *cobra.Command) {
	used := map[string]bool{GroupOps: true}
	for _, cmd := range root.Commands() {
		if cmd.GroupID == "" {
			cmd.GroupID = cmp.Or(cmd.Annotations[GroupKey], GroupApp)
		}
		used[cmd.GroupID] = true
	}
	for _, g := range groupTitles {
		if used[g.ID] && !root.ContainsGroup(g.ID) {
			root.AddGroup(&g)
		}
	}
	root.SetHelpCommandGroupID(GroupOps)
	root.SetCompletionCommandGroupID(GroupOps)
}

// leaf 构造不接受位置参数的命令。
func leaf(use, short string, run func(cmd *cobra.Command) error) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return run(cmd) },
	}
}

// group 构造只包含子命令的命令；直接执行时输出帮助。
func group(use, short string, children ...*cobra.Command) *cobra.Command {
	cmd := leaf(use, short, func(cmd *cobra.Command) error { return cmd.Help() })
	cmd.AddCommand(children...)
	return cmd
}

// NoArgs 是拒绝位置参数的 cobra.PositionalArgs，出错时带 ExitUsage 退出码。
// 自定义命令用它代替 cobra.NoArgs，多余参数就会以退出码 2 失败。
func NoArgs(cmd *cobra.Command, args []string) error {
	return AsExitError(ExitUsage, cobra.NoArgs(cmd, args))
}

// ExactArgs 是要求恰好 n 个位置参数的 cobra.PositionalArgs，出错时带 ExitUsage 退出码。
func ExactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		return AsExitError(ExitUsage, cobra.ExactArgs(n)(cmd, args))
	}
}
