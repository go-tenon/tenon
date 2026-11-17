// Command tenon 是 Tenon 应用的开发工具：init 生成新项目，add frontend 加入新的前端，dev 运行应用和前端工具，
// build 构建前端和二进制，generate 生成客户端。
// 它读取项目根目录的 tenon.json（可选，缺省时全部用约定的默认值）。应用把它加入 go.mod 的工具，版本随框架固定：
//
//	go get -tool github.com/go-tenon/tenon/cmd/tenon
//	go tool tenon dev
//
// 前端开发者在 package.json 中写 "dev": "go tool tenon dev"，用 bun run dev 开始。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/cmd/tenon/internal/dev"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
	"github.com/go-tenon/tenon/cmd/tenon/internal/scaffold"
)

const framework = "github.com/go-tenon/tenon"

func initCommand() *cobra.Command {
	var opts scaffold.Options
	cmd := &cobra.Command{
		Use:   "init <dir>",
		Short: "生成新项目：Go 应用加 TanStack Start 的前端，生成后即可运行",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			base := filepath.Base(dir)
			if opts.Module == "" {
				opts.Module = base
			}
			if opts.Name == "" {
				opts.Name = base
			}
			version, local := tenonVersion()
			if opts.TenonDir == "" {
				opts.TenonDir = local
			}
			if opts.TenonDir != "" {
				abs, err := filepath.Abs(opts.TenonDir)
				if err != nil {
					return err
				}
				opts.TenonDir, version = abs, "v0.0.0"
			}
			if version == "" || version == "(devel)" {
				return fmt.Errorf("cannot tell which version of %s to use; run go run %s/cmd/tenon@latest init, or pass --tenon <checkout>", framework, framework)
			}
			opts.TenonVersion = version
			opts.Slug = scaffold.Slug(base)
			if err := scaffold.Write(dir, opts); err != nil {
				return err
			}
			// 生成客户端要编译应用，嵌入的 dist 要先存在。
			if err := dev.EnsureDist(&project.Config{Frontends: []project.Frontend{{Name: "web", Dir: filepath.Join(dir, "apps", "web")}}}); err != nil {
				return err
			}
			for _, s := range [][]string{{"go", "mod", "tidy"}, {"go", "run", "./cmd/api", "client", "--ts", project.ClientSrc}} {
				if err := run(cmd, dir, s[0], s[1:]...); err != nil {
					return err
				}
			}
			// 前端依赖框架的前端包和设计令牌生成的主题包，bun install 之前先准备好它们。
			if _, err := dev.SyncJS(dir); err != nil {
				return err
			}
			_, err := exec.LookPath("bun")
			hasBun := err == nil
			if hasBun {
				if err := run(cmd, dir, "bun", "install"); err != nil {
					return err
				}
			}
			next := "  cd " + dir + "\n"
			if !hasBun {
				next += "  # 先安装 Bun：https://bun.sh\n  bun install\n"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\n已生成 %s。接着运行：\n\n%s  bun run dev\n\n然后打开 http://localhost:8080。\n", dir, next)
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Module, "module", "", "Go 模块路径，默认是目录名")
	cmd.Flags().StringVar(&opts.Name, "name", "", "应用的名称，默认是目录名")
	cmd.Flags().StringVar(&opts.TenonDir, "tenon", "", "本地的框架目录，go.mod 用 replace 指向它（开发框架本身时使用）")
	return cmd
}

// tenonVersion 返回编译这个程序时框架的版本；框架被 replace 到本地目录时还返回这个目录。
func tenonVersion() (version, dir string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", ""
	}
	for _, m := range append([]*debug.Module{&info.Main}, info.Deps...) {
		if m.Path != framework {
			continue
		}
		if m.Replace != nil && m.Replace.Version == "" {
			return m.Version, m.Replace.Path
		}
		return m.Version, ""
	}
	return "", ""
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := root().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tenon:", err)
		os.Exit(1)
	}
}

func root() *cobra.Command {
	root := &cobra.Command{
		Use:           "tenon",
		Short:         "Tenon 应用的开发工具",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	add := &cobra.Command{Use: "add", Short: "给项目加入新的部分"}
	add.AddCommand(addFrontendCommand())
	root.AddCommand(initCommand(), add, devCommand(), buildCommand(), generateCommand())
	return root
}

// moduleLine 是 go.mod 中的 module 行。
var moduleLine = regexp.MustCompile(`(?m)^module\s+(\S+)`)

func addFrontendCommand() *cobra.Command {
	var base, style, landing string
	cmd := &cobra.Command{
		Use:   "frontend <name>",
		Short: "在 apps/<name> 中生成一个 TanStack Start 前端，加入 Bun workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !project.Name.MatchString(name) {
				return fmt.Errorf("frontend name %q must match %s", name, project.Name)
			}
			if base == "" {
				base = "/" + name + "/"
			}
			if !strings.HasPrefix(base, "/") || !strings.HasSuffix(base, "/") {
				return fmt.Errorf("--base %q must start and end with /", base)
			}
			var pkg struct {
				Name string `json:"name"`
			}
			data, err := os.ReadFile("package.json")
			if err == nil {
				err = json.Unmarshal(data, &pkg)
			}
			if err != nil || pkg.Name == "" {
				return fmt.Errorf("run add frontend in the project root: package.json with a name is required (%v)", err)
			}
			goMod, err := os.ReadFile("go.mod")
			m := moduleLine.FindSubmatch(goMod)
			if err != nil || m == nil {
				return fmt.Errorf("run add frontend in the project root: go.mod with a module line is required (%v)", err)
			}
			if !slices.Contains(scaffold.Styles, style) {
				return fmt.Errorf("--style %q is not one of %v", style, scaffold.Styles)
			}
			if landing != "" && style != "site" {
				return fmt.Errorf("--landing needs --style site")
			}
			if landing != "" && !slices.Contains(scaffold.Landings, landing) {
				return fmt.Errorf("--landing %q is not one of %v", landing, scaffold.Landings)
			}
			f := scaffold.Frontend{Scope: pkg.Name, Name: name, Base: base, Title: name, Style: style, Landing: landing}
			if err := scaffold.WriteFrontend(".", f); err != nil {
				return err
			}
			if err := scaffold.AddWorkspace(".", path.Join(project.AppsDir, name)); err != nil {
				return err
			}
			if err := dev.EnsureDist(&project.Config{Frontends: []project.Frontend{{Name: name, Dir: path.Join(project.AppsDir, name)}}}); err != nil {
				return err
			}
			if _, err := dev.SyncJS("."); err != nil {
				return err
			}
			if _, err := exec.LookPath("bun"); err == nil {
				if err := run(cmd, ".", "bun", "install"); err != nil {
					return err
				}
			}
			pattern := base + "*"
			fmt.Fprintf(cmd.OutOrStdout(), `
已生成 apps/%[1]s，挂在 %[4]s。在 cmd/api/main.go 中托管它的 SSR 进程：

	%[3]sPages, err := ssr.New(ssr.Options{Runtime: runtime, Bundle: %[3]s.SSR, API: api, DevServer: cfg.DevServers[%[1]q]})

把 %[3]sPages.Handler() 挂在 %[2]q（放在挂载 /* 的那一行之前），并加入 ssr.Run 的参数：
ssr.Run(ctx, serve, webPages, %[3]sPages)。导入 %[5]q。
`, name, pattern, f.Package(), base, string(m[1])+"/apps/"+name)
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "前端挂载的路径，以 / 开头和结尾，默认 /<name>/")
	cmd.Flags().StringVar(&style, "style", "console", "样式预设：console（SaaS 控制台）；admin（运营后台，高密度、多标签）；site（官网和内容站，服务端渲染）")
	cmd.Flags().StringVar(&landing, "landing", "", "落地页的风格，只用于 --style site：classic（默认，朴素）；dusk（暗色、光晕）；ink（黑白、网格）；mist（柔和渐变、毛玻璃）")
	return cmd
}

func load() (*project.Config, error) { return project.Load(".") }

func devCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "dev",
		Short: "运行应用和前端工具，修改代码后重新编译、重启并生成客户端",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := load()
			if err != nil {
				return err
			}
			return dev.Run(cmd.Context(), cfg, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

func buildCommand() *cobra.Command {
	var out, arch string
	cmd := &cobra.Command{
		Use:   "build",
		Short: "把每个前端编译成 SSR 代码，构建共用的运行时，再编译应用（嵌入它们）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := load()
			if err != nil {
				return err
			}
			env := os.Environ()
			switch arch {
			case "":
			case "amd64", "arm64":
				env = append(env, "GOOS=linux", "GOARCH="+arch)
			default:
				return fmt.Errorf("--arch %q: must be amd64 or arm64", arch)
			}
			if len(cfg.Frontends) > 0 {
				if _, err := dev.SyncJS("."); err != nil {
					return err
				}
				if err := run(cmd, ".", "bun", "install", "--frozen-lockfile"); err != nil {
					return err
				}
				// 共用的运行时的架构与 Go 程序相同：tenon-start runtime 读取 GOARCH。
				goarch := arch
				if goarch == "" {
					out, err := exec.CommandContext(cmd.Context(), "go", "env", "GOARCH").Output()
					if err != nil {
						return fmt.Errorf("go env GOARCH: %w", err)
					}
					goarch = strings.TrimSpace(string(out))
				}
				for _, f := range cfg.Frontends {
					c := exec.CommandContext(cmd.Context(), "bun", "run", "build")
					c.Dir = f.Dir
					c.Env = append(slices.Clone(env), "GOARCH="+goarch)
					c.Stdout, c.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
					if err := c.Run(); err != nil {
						return fmt.Errorf("%s: bun run build: %w", f.Dir, err)
					}
				}
				// 几个前端共用一份 Bun 运行时，构建一次。tenon-start 是前端的依赖，在第一个前端的目录中运行它。
				rt, err := filepath.Abs(filepath.Join(project.AppsDir, dev.SSRRuntime))
				if err != nil {
					return err
				}
				c := exec.CommandContext(cmd.Context(), "bun", "x", "tenon-start", "runtime", "--out", rt)
				c.Dir = cfg.Frontends[0].Dir
				c.Env = append(slices.Clone(env), "GOARCH="+goarch)
				c.Stdout, c.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
				if err := c.Run(); err != nil {
					return fmt.Errorf("tenon-start runtime: %w", err)
				}
			}
			if out == "" {
				out = filepath.Join("bin", path.Base(cfg.Main))
			}
			c := exec.CommandContext(cmd.Context(), "go", "build", "-trimpath", "-o", out, cfg.Main)
			c.Env = env
			c.Stdout, c.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
			if err := c.Run(); err != nil {
				return fmt.Errorf("go build: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "输出的二进制，默认 bin/<入口包的目录名>")
	cmd.Flags().StringVar(&arch, "arch", "", "目标架构 amd64 或 arm64（Linux），默认与本机相同。"+
		"不要用 GOARCH 环境变量：go tool tenon 会按它把 tenon 本身编译成目标架构，在本机无法运行")
	return cmd
}

func generateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "generate",
		Short: "编译应用，为每个 API 生成 TypeScript 客户端，按 tenon.json 生成 Go 客户端",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := load()
			if err != nil {
				return err
			}
			runs := dev.Generations(cfg)
			if len(runs) == 0 {
				return fmt.Errorf("nothing to generate: no %s/package.json, and no API in %s sets go", project.ClientDir, project.File)
			}
			tmp, err := os.MkdirTemp("", "tenon-")
			if err != nil {
				return err
			}
			defer func() { _ = os.RemoveAll(tmp) }()
			if err := dev.EnsureDist(cfg); err != nil {
				return err
			}
			// 生成之后通常接着做类型检查，前端依赖的框架前端包要已经就位，包的依赖要已经安装。
			if len(cfg.Frontends) > 0 {
				changed, err := dev.SyncJS(".")
				if err != nil {
					return err
				}
				if _, berr := exec.LookPath("bun"); berr == nil && (changed || dev.NeedsInstall(".")) {
					if err := run(cmd, ".", "bun", "install"); err != nil {
						return err
					}
				}
			}
			bin := filepath.Join(tmp, "app")
			if err := run(cmd, ".", "go", "build", "-mod=mod", "-o", bin, cfg.Main); err != nil {
				return err
			}
			for _, args := range runs {
				if err := run(cmd, ".", bin, args...); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func run(cmd *cobra.Command, dir, name string, args ...string) error {
	c := exec.CommandContext(cmd.Context(), name, args...)
	c.Dir = dir
	c.Stdout, c.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s %v: %w", name, args, err)
	}
	return nil
}
