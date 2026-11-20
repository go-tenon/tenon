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
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/i18n"
)

const (
	configFlag = "config"
	// configEnv 是没有传入 --config 时读取的环境变量，格式与 --config 相同：逗号分隔的配置文件。
	configEnv = "APP_CONFIG"
)

// 配置文件的默认位置。
const (
	DefaultConfigFile = "config/app.yaml"   // 没有传入 --config、也没有设置 APP_CONFIG 时加载的配置文件
	LocalConfigFile   = "config/local.yaml" // 用默认的配置文件时，它存在就叠加在后面：本机的覆盖，不提交
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
// 根命令定义了所有子命令共用的 --config 选项：可以重复传入多个配置文件，后面的覆盖前面的。
// 没有传入时读取环境变量 APP_CONFIG；它也没有设置时是 config/app.yaml，config/local.yaml 存在时叠加在它之后，
// 与 tenon dev 运行应用时相同。未知命令、未知选项和多余参数标记为 ExitUsage。
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
	root.PersistentFlags().StringSlice(configFlag, []string{DefaultConfigFile}, "配置文件，可以传入多次，后面的覆盖前面的；没有传入时读取环境变量 "+configEnv+"，都没有时是 "+DefaultConfigFile+"，"+LocalConfigFile+" 存在时叠加在它之后")
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
// 配置文件依次取自 --config、环境变量 APP_CONFIG（可以写在 .env 中）和默认的 config/app.yaml；
// 用默认的文件时，config/local.yaml 存在就叠加在它之后，本机的覆盖不用每次写 --config。
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
	v := os.Getenv(configEnv)
	switch {
	case cmd.Flags().Changed(configFlag):
	case v != "":
		if files = splitList(v); len(files) == 0 {
			return zero, AsExitError(ExitConfig, errors.Newf("command: %s=%q lists no config files", configEnv, v))
		}
	default:
		if _, err := os.Stat(LocalConfigFile); err == nil {
			files = append(files, LocalConfigFile)
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

// ExecuteOption 是 Execute 的选项。
type ExecuteOption func(*executeOptions)

// executeOptions 是 Execute 的设置。
type executeOptions struct {
	languages *goi18n.Bundle // 命令使用的消息目录
}

// Languages 设置命令使用的消息目录，通常与 HTTP 的 httpx.Languages 相同：i18n.MustNew("zh-CN", locales.FS)。
// 命令的 context 带有它的默认语言（i18n.WithLocale），失败时业务错误按它翻译。没有设置时使用框架内置的中英文消息，
// 默认语言是 zh-CN。
func Languages(b *goi18n.Bundle) ExecuteOption {
	return func(o *executeOptions) { o.languages = b }
}

// Execute 执行根命令：SIGINT、SIGTERM 会取消命令的 context，context 带有消息目录的默认语言（见 Languages）；
// 失败时把 ErrorText 写到标准错误，并以 ExitCode(err) 结束进程。
func Execute(root *cobra.Command, opts ...ExecuteOption) {
	o := executeOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	if o.languages == nil {
		o.languages = i18n.MustNew(i18n.LocaleZhCN)
	}
	assignGroups(root)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	ctx = i18n.WithLocale(ctx, o.languages, i18n.Match(o.languages))
	err := root.ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(root.ErrOrStderr(), ErrorText(ctx, err))
		os.Exit(ExitCode(err))
	}
}

// ErrorText 返回命令失败时显示的文字。业务错误（errorx 定义的、不是系统故障的错误）显示按 ctx 中的语言翻译的提示，
// 后面附上错误码，例如“这个邮箱已经被使用（user.email_taken）”，指出了字段时写在错误码之前（见 errorx.Field），其他语言用半角括号；没有译文的业务错误、系统故障和其他错误显示 err 本身。
func ErrorText(ctx context.Context, err error) string {
	code := errorx.CodeOf(err)
	if code == "" || errorx.KindOf(err).Systemic() {
		return err.Error()
	}
	msg, _ := i18n.ErrorMessage(ctx, err)
	if msg == "" || msg == code {
		return err.Error()
	}
	field := errorx.FieldOf(err)
	if strings.HasPrefix(i18n.Locale(ctx), "zh") {
		if field != "" {
			return msg + "（字段 " + field + "，" + code + "）"
		}
		return msg + "（" + code + "）"
	}
	if field != "" {
		return msg + " (field " + field + ", " + code + ")"
	}
	return msg + " (" + code + ")"
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
