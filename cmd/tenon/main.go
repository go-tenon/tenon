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
	"runtime/debug"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/cmd/tenon/internal/dev"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
	"github.com/go-tenon/tenon/cmd/tenon/internal/scaffold"
	"github.com/go-tenon/tenon/frontend"
)

const framework = "github.com/go-tenon/tenon"

// initCommand 返回 tenon init 命令：生成新项目。
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
			// 生成客户端要编译应用，嵌入的 apps/dist 中要先有前端的清单。
			cfg, err := project.Load(dir)
			if err != nil {
				return err
			}
			if err := dev.WriteManifest(dir, cfg); err != nil {
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
			_, err = exec.LookPath("bun")
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

// main 执行 tenon 命令，收到中断信号时取消。
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := root().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tenon:", err)
		os.Exit(1)
	}
}

// root 返回 tenon 的根命令。
func root() *cobra.Command {
	root := &cobra.Command{
		Use:           "tenon",
		Short:         "Tenon 应用的开发工具",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	add := &cobra.Command{Use: "add", Short: "给项目加入新的部分"}
	add.AddCommand(addFrontendCommand(), addClientCommand())
	root.AddCommand(initCommand(), add, devCommand(), buildCommand(), generateCommand())
	return root
}

// addFrontendCommand 返回 tenon add frontend 命令：在 apps/<name> 中生成一个前端，加入 Bun workspace。
func addFrontendCommand() *cobra.Command {
	var base, layout, login, preset, render string
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
			scope, err := rootPackageName("add frontend")
			if err != nil {
				return err
			}
			existing, err := project.Load(".")
			if err != nil {
				return err
			}
			if i := slices.IndexFunc(existing.Frontends, func(f project.Frontend) bool { return f.Base == base && f.Name != name }); i >= 0 {
				return fmt.Errorf("%s is already mounted at %s; choose another --base", existing.Frontends[i].Dir, base)
			}
			f, err := scaffold.Frontend{Scope: scope, Name: name, Base: base, Title: name, Layout: layout, Login: login, Preset: preset, Render: render}.Resolve()
			if err != nil {
				return err
			}
			if err := scaffold.WriteFrontend(".", f); err != nil {
				return err
			}
			if err := scaffold.AddWorkspace(".", path.Join(project.AppsDir, name)); err != nil {
				return err
			}
			cfg, err := project.Load(".")
			if err != nil {
				return err
			}
			if err := dev.WriteManifest(".", cfg); err != nil {
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
			rendering := "页面只在浏览器中渲染"
			if f.SSR() {
				rendering = "页面在服务端渲染"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\n已生成 apps/%s，挂在 %s，%s（apps/%s/package.json 的 tenon 字段）。运行 bun run dev 打开它。\n",
				name, base, rendering, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "前端挂载的路径，以 / 开头和结尾，默认 /<name>/")
	cmd.Flags().StringVar(&layout, "layout", "rail", "布局：rail（双栏导航）；frame（顶栏加侧栏菜单、多标签）；site（官网和内容站）")
	cmd.Flags().StringVar(&login, "login", "", "登录页：centered（居中的卡片）；split（左右分栏）；none（没有登录）。默认按布局：frame、rail 是 centered，site 是 none")
	cmd.Flags().StringVar(&preset, "preset", "", "样式预设：compact（紧凑）；comfortable（舒展）；spacious（宽松）。默认按布局：frame 是 compact，rail 是 comfortable，site 是 spacious")
	cmd.Flags().StringVar(&render, "render", "", "渲染方式：csr（只在浏览器中渲染）；ssr（服务端渲染）。默认按布局：frame、rail 是 csr，site 是 ssr")
	return cmd
}

// rootPackageName 返回项目根目录 package.json 的 name，它是前端和客户端的 npm 包名的作用域。command 是出错时提示的命令。
func rootPackageName(command string) (string, error) {
	var pkg struct {
		Name string `json:"name"`
	}
	data, err := os.ReadFile("package.json")
	if err == nil {
		err = json.Unmarshal(data, &pkg)
	}
	if err != nil || pkg.Name == "" {
		return "", fmt.Errorf("run %s in the project root: package.json with a name is required (%v)", command, err)
	}
	return pkg.Name, nil
}

// addClientCommand 返回 tenon add client 命令：在 clients/<name> 中生成一个客户端，加入 Bun workspace。
func addClientCommand() *cobra.Command {
	var kind, frontendName, title string
	cmd := &cobra.Command{
		Use:   "client <name>",
		Short: "在 clients/<name> 中生成一个客户端（例如 Electron 桌面端），加入 Bun workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !project.Name.MatchString(name) {
				return fmt.Errorf("client name %q must match %s", name, project.Name)
			}
			if !slices.Contains(project.Kinds, kind) {
				return fmt.Errorf("--kind %q is not one of %v", kind, project.Kinds)
			}
			scope, err := rootPackageName("add client")
			if err != nil {
				return err
			}
			cfg, err := project.Load(".")
			if err != nil {
				return err
			}
			if frontendName == "" {
				return fmt.Errorf("--frontend is required: the %s client shows one of the frontends in %s", kind, project.AppsDir)
			}
			f, ok := cfg.FindFrontend(frontendName)
			if !ok {
				return fmt.Errorf("--frontend %q is not in %s", frontendName, project.AppsDir)
			}
			if title == "" {
				title = name
			}
			consent, err := scaffold.WriteClient(".", scaffold.Client{
				Scope: scope, Name: name, Kind: kind, Frontend: f.Name, Base: f.Base, SSR: f.SSR, Title: title,
			})
			if err != nil {
				return err
			}
			for _, member := range append(dev.ClientWorkspaces(kind), path.Join(project.ClientsDir, name)) {
				if err := scaffold.AddWorkspace(".", member); err != nil {
					return err
				}
			}
			if _, err := dev.SyncJS("."); err != nil {
				return err
			}
			if _, err := exec.LookPath("bun"); err == nil {
				if err := run(cmd, ".", "bun", "install"); err != nil {
					return err
				}
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "\n已生成 %s/%s：Electron 桌面端，窗口中显示 %s（%s）。\n", project.ClientsDir, name, f.Dir, f.Base)
			if consent {
				fmt.Fprintf(w, "已在 %s/src/routes/oauth/consent.tsx 加上授权页：用户在浏览器中登录桌面端时在这里确认。\n", f.Dir)
			}
			fmt.Fprintf(w, "接下来：\n"+
				"  1. 服务端用 oauthserver 登记这个客户端：ID %s，FirstParty，回调地址 http://127.0.0.1/callback，ConsentURL 是 %soauth/consent（见文档“桌面端”）；\n"+
				"  2. 把 %s/%s/src/main.ts 中服务器的地址改成线上的地址；\n"+
				"  3. go tool tenon dev --client %s 运行它，go tool tenon build --client %s 打包。\n",
				name, f.Base, project.ClientsDir, name, name, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", fmt.Sprintf("客户端的种类：%s", strings.Join(project.Kinds, "、")))
	cmd.Flags().StringVar(&frontendName, "frontend", "", "窗口中显示的前端，即 apps 中的目录名；它要有登录页")
	cmd.Flags().StringVar(&title, "title", "", "应用的名称：窗口标题和安装包的名称，默认是 <name>")
	return cmd
}

// devCommand 返回 tenon dev 命令：运行应用和前端工具，修改代码后重新编译、重启并生成客户端。
func devCommand() *cobra.Command {
	var names []string
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "运行应用和前端工具，修改代码后重新编译、重启并生成客户端",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := project.Load(".")
			if err != nil {
				return err
			}
			var clients []project.Client
			for _, name := range names {
				c, ok := cfg.FindClient(name)
				if !ok {
					return fmt.Errorf("--client %q is not in %s", name, project.ClientsDir)
				}
				clients = append(clients, c)
			}
			return dev.Run(cmd.Context(), cfg, clients, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringSliceVar(&names, "client", nil, "同时运行的客户端（clients 中的目录名），可以写多个")
	return cmd
}

// buildCommand 返回 tenon build 命令：构建每个前端，把它们的静态文件放进 apps/dist；有服务端渲染的前端时，
// 把它们编进一个 Bun 可执行文件（apps/dist/ssr）；最后编译嵌入 apps/dist 的应用。
func buildCommand() *cobra.Command {
	var out, arch, client string
	cmd := &cobra.Command{
		Use:   "build",
		Short: "构建每个前端（有服务端渲染的前端时连同 Bun），再编译嵌入它们的应用；--client 打包一个客户端",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := project.Load(".")
			if err != nil {
				return err
			}
			if client != "" {
				c, ok := cfg.FindClient(client)
				if !ok {
					return fmt.Errorf("--client %q is not in %s", client, project.ClientsDir)
				}
				f, _ := cfg.FindFrontend(c.Frontend)
				if _, err := dev.SyncJS("."); err != nil {
					return err
				}
				if err := run(cmd, ".", "bun", "install", "--frozen-lockfile"); err != nil {
					return err
				}
				return dev.BuildClient(cmd.Context(), c, f, cmd.OutOrStdout(), cmd.ErrOrStderr())
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
				// apps/dist 每次从头生成：改成只在浏览器中渲染的前端不留下上次的 SSR 可执行文件。
				if err := os.RemoveAll(filepath.FromSlash(dev.DistDir)); err != nil {
					return err
				}
				var ssr []project.Frontend
				for _, f := range cfg.Frontends {
					if err := run(cmd, f.Dir, "bun", "run", "build"); err != nil {
						return err
					}
					if err := dev.CopyClient(f); err != nil {
						return err
					}
					if f.SSR {
						ssr = append(ssr, f)
					}
				}
				if len(ssr) > 0 {
					// 可执行文件的架构与 Go 程序相同：tenon-start compile 读取 GOARCH。
					goarch := arch
					if goarch == "" {
						out, err := exec.CommandContext(cmd.Context(), "go", "env", "GOARCH").Output()
						if err != nil {
							return fmt.Errorf("go env GOARCH: %w", err)
						}
						goarch = strings.TrimSpace(string(out))
					}
					exe, err := filepath.Abs(filepath.Join(filepath.FromSlash(dev.DistDir), frontend.ExecutableFile))
					if err != nil {
						return err
					}
					args := []string{"x", "tenon-start", "compile", "--out", exe}
					for _, f := range ssr {
						entry, err := filepath.Abs(filepath.Join(f.Dir, "dist", "server", "server.js"))
						if err != nil {
							return err
						}
						args = append(args, f.Name+"="+entry)
					}
					// tenon-start 是前端的依赖，在第一个 SSR 前端的目录中运行它。
					c := exec.CommandContext(cmd.Context(), "bun", args...)
					c.Dir = ssr[0].Dir
					c.Env = append(slices.Clone(env), "GOARCH="+goarch)
					c.Stdout, c.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
					if err := c.Run(); err != nil {
						return fmt.Errorf("tenon-start compile: %w", err)
					}
				}
				if err := dev.WriteManifest(".", cfg); err != nil {
					return err
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
	cmd.Flags().StringVar(&client, "client", "", "只打包这个客户端（clients 中的目录名）：网页宿主先按 SPA 模式构建它的前端，安装包在客户端的 release 目录中")
	cmd.Flags().StringVar(&arch, "arch", "", "目标架构 amd64 或 arm64（Linux），默认与本机相同。"+
		"不要用 GOARCH 环境变量：go tool tenon 会按它把 tenon 本身编译成目标架构，在本机无法运行")
	return cmd
}

// generateCommand 返回 tenon generate 命令：编译应用，生成 TypeScript 和 Go 客户端。
func generateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "generate",
		Short: "编译应用，为每个 API 生成 TypeScript 客户端，按 tenon.json 生成 Go 客户端",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := project.Load(".")
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
			if err := dev.WriteManifest(".", cfg); err != nil {
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

// run 在目录 dir 中执行命令 name，输出写到 cmd 的输出，随 cmd 的 context 取消。
func run(cmd *cobra.Command, dir, name string, args ...string) error {
	c := exec.CommandContext(cmd.Context(), name, args...)
	c.Dir = dir
	c.Stdout, c.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s %v: %w", name, args, err)
	}
	return nil
}
