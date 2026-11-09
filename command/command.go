package command

import (
	"context"
	"io"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"
)

// Exit codes.
const (
	ExitOK          = 0
	ExitUsage       = 2
	ExitConfig      = 3
	ExitUnavailable = 4
	ExitInternal    = 5
)

// IO 绑定命令输入输出；stdout 给结果，stderr 给诊断。
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

func bindIO(cmd *cobra.Command, io IO) {
	if io.In != nil {
		cmd.SetIn(io.In)
	}
	if io.Out != nil {
		cmd.SetOut(io.Out)
	}
	if io.Err != nil {
		cmd.SetErr(io.Err)
	}
}

// RunE 是带 context 的命令函数。
type RunE func(ctx context.Context, cmd *cobra.Command, args []string) error

func wrap(run RunE) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		return run(cmd.Context(), cmd, args)
	}
}

// Root 返回应用根命令。
func Root(name, short string, io IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:           name,
		Short:         short,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	bindIO(cmd, io)
	return cmd
}

// NewServe 构造 serve 命令；run 负责打开资源并启动 HTTP。
func NewServe(run RunE) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "启动 HTTP API",
		RunE:  wrap(run),
	}
	return cmd
}

// NewMigrate 构造 migrate 根命令，并挂 up/status/down。
func NewMigrate(up, status, down RunE) *cobra.Command {
	root := &cobra.Command{Use: "migrate", Short: "数据库迁移"}
	root.AddCommand(&cobra.Command{Use: "up", Short: "应用迁移", RunE: wrap(up)})
	root.AddCommand(&cobra.Command{Use: "status", Short: "迁移状态", RunE: wrap(status)})
	root.AddCommand(&cobra.Command{Use: "down", Short: "回滚一步", RunE: wrap(down)})
	return root
}

// NewOpenAPI 构造离线 OpenAPI 输出命令。
func NewOpenAPI(run RunE) *cobra.Command {
	return &cobra.Command{Use: "openapi", Short: "离线输出 OpenAPI", RunE: wrap(run)}
}

// NewConfig 构造 config check/show。
func NewConfig(check, show RunE) *cobra.Command {
	root := &cobra.Command{Use: "config", Short: "配置工具"}
	root.AddCommand(&cobra.Command{Use: "check", Short: "离线校验配置", RunE: wrap(check)})
	root.AddCommand(&cobra.Command{Use: "show", Short: "输出脱敏配置", RunE: wrap(show)})
	return root
}

// NewDoctor 构造外部依赖探测命令。
func NewDoctor(run RunE) *cobra.Command {
	return &cobra.Command{Use: "doctor", Short: "探测外部依赖", RunE: wrap(run)}
}

// AsExitError 将错误映射为退出码包装。
func AsExitError(code int, err error) error {
	if err == nil {
		return nil
	}
	return &ExitError{Code: code, Err: err}
}

// ExitError 携带进程退出码。
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode 从错误链提取退出码。
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitInternal
}
