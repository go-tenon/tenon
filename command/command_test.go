package command_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/i18n"
)

type settings struct {
	Name string `yaml:"name"`
}

func (s settings) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("name is required")
	}
	return nil
}

// writeConfig 在临时目录中写入配置文件并返回路径。
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func execute(root *cobra.Command, args ...string) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

const minimalConfig = "name: blog\n"

// newRoot 返回装配了 cmds 的根命令。
func newRoot(cmds ...*cobra.Command) *cobra.Command {
	root := command.Root("api", "test")
	root.AddCommand(cmds...)
	return root
}

func TestRootHasNoCommandsByDefault(t *testing.T) {
	t.Parallel()
	root := command.Root("api", "test")
	if len(root.Commands()) != 0 {
		t.Fatalf("commands = %v", root.Commands())
	}
	out, _, err := execute(root)
	if err != nil || !strings.Contains(out, "--config") {
		t.Fatalf("help out=%q err=%v", out, err)
	}
}

func TestRunReceivesConfigAndContext(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, minimalConfig)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got settings
	root := newRoot(command.Run("work", "worker", func(runCtx context.Context, cfg settings) error {
		if runCtx != ctx {
			t.Fatal("run did not receive the command context")
		}
		got = cfg
		return nil
	}))
	root.SetArgs([]string{"work", "--config", path})
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	if got.Name != "blog" {
		t.Fatalf("cfg = %+v", got)
	}
}

func TestConfigErrorsExitWithConfigCode(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	invalid := writeConfig(t, "name: ''\n")
	for _, args := range [][]string{
		{"config", "check", "--config", missing},
		{"config", "check", "--config", invalid},
		{"config", "show", "--config", missing},
		{"serve", "--config", missing},
	} {
		root := newRoot(
			command.Config[settings](),
			command.Run("serve", "serve", func(context.Context, settings) error {
				t.Fatal("serve ran with invalid config")
				return nil
			}),
		)
		if _, _, err := execute(root, args...); command.ExitCode(err) != command.ExitConfig {
			t.Errorf("args=%v exit=%d err=%v", args, command.ExitCode(err), err)
		}
	}
}

func TestConfigCheckAndShow(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, minimalConfig)
	root := func() *cobra.Command { return newRoot(command.Config[settings]()) }

	if out, _, err := execute(root(), "config", "check", "--config", path); err != nil || out != "" {
		t.Fatalf("check out=%q err=%v", out, err)
	}
	out, _, err := execute(root(), "config", "show", "--config", path)
	if err != nil || !strings.Contains(out, "name: blog\n") || strings.Contains(out, "Name") {
		t.Fatalf("yaml out=%q err=%v", out, err)
	}
	out, _, err = execute(root(), "config", "show", "--format", "json", "--config", path)
	if err != nil || !strings.Contains(out, `"Name": "blog"`) {
		t.Fatalf("json out=%q err=%v", out, err)
	}
	if _, _, err := execute(root(), "config", "show", "--format", "toml", "--config", path); command.ExitCode(err) != command.ExitUsage {
		t.Fatalf("invalid format exit=%d err=%v", command.ExitCode(err), err)
	}
}

func TestLoadConfigInCustomCommand(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, minimalConfig)
	root := newRoot(&cobra.Command{Use: "hello", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := command.LoadConfig[settings](cmd)
		if err != nil {
			return err
		}
		cmd.Println("hello", cfg.Name)
		return nil
	}})
	out, _, err := execute(root, "hello", "--config", path)
	if err != nil || out != "hello blog\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}

	orphan := &cobra.Command{Use: "orphan"}
	if _, err := command.LoadConfig[settings](orphan); err == nil {
		t.Fatal("LoadConfig without --config flag succeeded")
	}
}

func TestUsageErrors(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"unknown"},
		{"--unknown"},
		{"serve", "extra"},
		{"serve", "--unknown"},
		{"config", "extra"},
		{"config", "check", "extra"},
		{"hello", "extra"},
	} {
		root := newRoot(
			command.Run("serve", "serve", func(context.Context, settings) error {
				t.Fatal("invalid arguments ran serve")
				return nil
			}),
			command.Config[settings](),
			&cobra.Command{Use: "hello", Args: command.NoArgs, RunE: func(*cobra.Command, []string) error {
				t.Fatal("invalid arguments ran hello")
				return nil
			}},
		)
		if _, _, err := execute(root, args...); command.ExitCode(err) != command.ExitUsage {
			t.Errorf("args=%v exit=%d err=%v", args, command.ExitCode(err), err)
		}
	}
}

func TestExitCode(t *testing.T) {
	t.Parallel()
	unavailable := errorx.Define(errorx.Unavailable, "demo.db")
	for _, tc := range []struct {
		err  error
		want int
	}{
		{nil, 0},
		{command.AsExitError(command.ExitConfig, unavailable.New()), command.ExitConfig},
		{fmt.Errorf("sync: %w", unavailable.Wrap(context.DeadlineExceeded)), command.ExitUnavailable},
		{errorx.Define(errorx.NotFound, "demo.missing").New(), command.ExitFailure},
		{errorx.Define(errorx.Internal, "demo.bug").New(), command.ExitInternal},
		{context.Canceled, command.ExitInternal},
	} {
		if got := command.ExitCode(tc.err); got != tc.want {
			t.Fatalf("ExitCode(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
	if command.AsExitError(command.ExitConfig, nil) != nil {
		t.Fatal("AsExitError(nil) must be nil")
	}
}

// projectDir 创建一个临时项目目录并切换进去，files 的路径相对于该目录。
func projectDir(t *testing.T, files map[string]string) {
	t.Helper()
	t.Setenv("APP_CONFIG", "") // 不受运行测试的环境影响
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
}

// loadedName 用 --config 等参数加载配置，返回其中的 name。
func loadedName(t *testing.T, args ...string) string {
	t.Helper()
	var name string
	root := newRoot(command.Run("name", "print name", func(_ context.Context, cfg settings) error {
		name = cfg.Name
		return nil
	}))
	if _, _, err := execute(root, append([]string{"name"}, args...)...); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return name
}

func TestConfigFiles(t *testing.T) {
	projectDir(t, map[string]string{
		"config/app.yaml":   "name: app\n",
		"config/prod.yaml":  "name: prod\n",
		"config/local.yaml": "name: local\n",
	})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "local"}, // 默认 config/app.yaml，config/local.yaml 存在时叠加在它之后
		{[]string{"--config", "config/app.yaml"}, "app"}, // 传入 --config 时只加载传入的文件
		{[]string{"--config", "config/app.yaml", "--config", "config/prod.yaml"}, "prod"},
		{[]string{"--config", "config/app.yaml,config/prod.yaml,config/local.yaml"}, "local"},
		{[]string{"--config", "config/prod.yaml", "--config", "config/app.yaml"}, "app"},
	} {
		if got := loadedName(t, tc.args...); got != tc.want {
			t.Errorf("args=%v name=%q want %q", tc.args, got, tc.want)
		}
	}
}

// 没有 config/local.yaml 时默认只加载 config/app.yaml。
func TestConfigFilesWithoutLocal(t *testing.T) {
	projectDir(t, map[string]string{"config/app.yaml": "name: app\n"})
	if got := loadedName(t); got != "app" {
		t.Fatalf("name=%q", got)
	}
}

func TestConfigEnv(t *testing.T) {
	projectDir(t, map[string]string{
		"config/app.yaml":   "name: app\n",
		"config/prod.yaml":  "name: prod\n",
		"config/local.yaml": "name: local\n",
	})
	for _, tc := range []struct {
		env  string
		args []string
		want string
	}{
		{"config/app.yaml,config/prod.yaml", nil, "prod"},
		{" config/app.yaml , config/local.yaml ,", nil, "local"},
		{"config/app.yaml,config/prod.yaml", []string{"--config", "config/app.yaml"}, "app"}, // --config 优先
		{"config/app.yaml", nil, "app"},                                                      // APP_CONFIG 中没有 local.yaml 时不叠加
		{"", nil, "local"},
	} {
		t.Setenv("APP_CONFIG", tc.env)
		if got := loadedName(t, tc.args...); got != tc.want {
			t.Errorf("APP_CONFIG=%q args=%v name=%q want %q", tc.env, tc.args, got, tc.want)
		}
	}
}

func TestConfigEnvFromDotEnv(t *testing.T) {
	projectDir(t, map[string]string{
		".env":              "APP_CONFIG=config/app.yaml,config/local.yaml\n",
		"config/app.yaml":   "name: app\n",
		"config/local.yaml": "name: local\n",
	})
	os.Unsetenv("APP_CONFIG")
	if got := loadedName(t); got != "local" {
		t.Fatalf("name=%q", got)
	}
}

func TestEmptyConfigEnvIsConfigError(t *testing.T) {
	projectDir(t, map[string]string{"config/app.yaml": minimalConfig})
	t.Setenv("APP_CONFIG", " , ")
	root := newRoot(command.Config[settings]())
	if _, _, err := execute(root, "config", "check"); command.ExitCode(err) != command.ExitConfig {
		t.Fatalf("exit=%d err=%v", command.ExitCode(err), err)
	}
}

func TestDotEnvInWorkingDirectory(t *testing.T) {
	t.Setenv("TENON_TEST_NAME", "")
	os.Unsetenv("TENON_TEST_NAME")
	projectDir(t, map[string]string{
		".env":            "TENON_TEST_NAME=from-dotenv\n",
		"config/app.yaml": "name: ${TENON_TEST_NAME}\n",
	})
	if got := loadedName(t); got != "from-dotenv" {
		t.Fatalf("name=%q", got)
	}
}

func TestProcessEnvOverridesDotEnv(t *testing.T) {
	t.Setenv("TENON_TEST_NAME", "from-process")
	projectDir(t, map[string]string{
		".env":            "TENON_TEST_NAME=from-dotenv\n",
		"config/app.yaml": "name: ${TENON_TEST_NAME}\n",
	})
	if got := loadedName(t); got != "from-process" {
		t.Fatalf("name=%q", got)
	}
}

func TestInvalidDotEnvIsConfigError(t *testing.T) {
	projectDir(t, map[string]string{
		".env":            "BAD-KEY=value\n",
		"config/app.yaml": minimalConfig,
	})
	root := newRoot(command.Config[settings]())
	if _, _, err := execute(root, "config", "check"); command.ExitCode(err) != command.ExitConfig {
		t.Fatalf("exit=%d err=%v", command.ExitCode(err), err)
	}
}

func TestErrorText(t *testing.T) {
	t.Parallel()
	b, err := i18n.New(i18n.LocaleZhCN, fstest.MapFS{
		"zh-CN.yaml": {Data: []byte("demo.taken: \"“{{.name}}”已被使用\"\n")},
		"en.yaml":    {Data: []byte("demo.taken: \"{{.name}} is taken\"\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	taken := errorx.Define(errorx.Conflict, "demo.taken").New(errorx.Public("name", "acme"))
	zh := i18n.WithLocale(context.Background(), b, i18n.LocaleZhCN)
	en := i18n.WithLocale(context.Background(), b, i18n.LocaleEn)
	for _, tc := range []struct {
		ctx  context.Context
		err  error
		want string
	}{
		{zh, taken, "“acme”已被使用（demo.taken）"},
		{en, taken, "acme is taken (demo.taken)"},
		{zh, fmt.Errorf("create: %w", taken), "“acme”已被使用（demo.taken）"}, // 包装后仍按错误码翻译
		{zh, errorx.Define(errorx.Conflict, "demo.taken").New(errorx.Public("name", "acme"), errorx.Field("slug")), "“acme”已被使用（字段 slug，demo.taken）"},
		{en, errorx.Define(errorx.Conflict, "demo.taken").New(errorx.Public("name", "acme"), errorx.Field("slug")), "acme is taken (field slug, demo.taken)"},
		{zh, errorx.Define(errorx.Conflict, "demo.untranslated").New(), "demo.untranslated"}, // 没有译文
		{zh, errorx.Define(errorx.Unavailable, "demo.taken").New(), "demo.taken"},            // 系统故障显示错误本身
		{zh, errors.New("plain failure"), "plain failure"},
		{context.Background(), taken, "demo.taken"}, // context 中没有语言
	} {
		if got := command.ErrorText(tc.ctx, tc.err); got != tc.want {
			t.Errorf("ErrorText(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// 标准输入不是终端时，ReadSecret 读取第一行，不显示提示，不要求确认。
func TestReadSecretFromPipe(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{}
	var stderr bytes.Buffer
	cmd.SetIn(strings.NewReader("s3cret pass\r\nignored\n"))
	cmd.SetErr(&stderr)
	got, err := command.ReadSecret(cmd, "密码：", "再输入一次：")
	if err != nil || got != "s3cret pass" || stderr.Len() != 0 {
		t.Fatalf("got %q, %v, stderr %q", got, err, stderr.String())
	}
	cmd.SetIn(strings.NewReader("no newline"))
	if got, err := command.ReadSecret(cmd, "密码：", ""); err != nil || got != "no newline" {
		t.Fatalf("got %q, %v", got, err)
	}
}
