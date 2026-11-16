// Package dev 实现 tenon dev：与应用一起运行 Vite、templ、Tailwind 和其他工具，修改 Go 代码后重新编译、重启应用，
// 并重新生成客户端。
package dev

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// UIDir 是 Tailwind 在样式入口旁边放置 web/ui 样式源的目录名。
const UIDir = ".web-ui"

const framework = "github.com/go-tenon/tenon"

// DevConfig 是 tenon dev 写给应用的配置：各前端开发服务器的地址（dev_servers），追加在 serve 的 --config 之后。
const DevConfig = ".tenon/dev.yaml"

// Run 在当前目录运行开发模式，直到 ctx 取消；停止后生成正式的代码、样式表和前端构建产物。
func Run(ctx context.Context, cfg *project.Config, out, errOut io.Writer) error {
	d := dev{cfg: cfg, out: shared(out), errOut: shared(errOut)}
	if err := d.check(); err != nil {
		return err
	}
	servers, err := devServers(cfg.Frontends)
	if err != nil {
		return err
	}
	d.servers = servers
	if err := d.prepare(); err != nil {
		return err
	}
	if cfg.Templ != nil {
		fmt.Fprintf(d.out, "dev: 打开 http://localhost:%d（代理 %s）\n", cfg.Templ.ProxyPort, cfg.URL)
	} else {
		fmt.Fprintf(d.out, "dev: 打开 %s\n", cfg.URL)
	}
	err = concurrently(ctx, d.runs()...)
	// 停止后恢复正式的生成结果：templ 的 watch 模式生成的代码读取 _templ.txt，工具的输出也没有压缩。
	return errors.Join(err, d.finish())
}

type dev struct {
	cfg         *project.Config
	out, errOut io.Writer
	// servers 是各前端开发服务器的地址，按前端的名称。
	servers map[string]string
}

// shared 让多个子进程可以同时写 w。*os.File 直接交给子进程，它们能识别终端、输出颜色。
func shared(w io.Writer) io.Writer {
	if _, ok := w.(*os.File); ok {
		return w
	}
	return &lockedWriter{w: w}
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// check 确认用到的命令都已安装：go；有前端时 bun；有 templ 时 go.mod 中的 templ 工具；Tailwind 和其他工具。
// 有前端时 serve 的参数中要有 --config，前端的地址追加在它之后。
func (d dev) check() error {
	need := []string{"go"}
	if len(d.cfg.Frontends) > 0 {
		need = append(need, "bun")
		if !project.HasConfigFlag(d.cfg.Serve) {
			return errors.Newf("dev: serve in %s has no --config; tenon dev appends --config %s to pass the frontend dev servers", project.File, DevConfig)
		}
	}
	if d.cfg.Tailwind != nil {
		need = append(need, "tailwindcss")
	}
	for _, t := range d.cfg.Tools {
		need = append(need, t.Watch[0])
		if len(t.Build) > 0 {
			need = append(need, t.Build[0])
		}
	}
	for _, name := range need {
		if _, err := exec.LookPath(name); err != nil {
			switch name {
			case "bun":
				return errors.New("dev: bun not found in PATH, install it from https://bun.sh")
			case "tailwindcss":
				return errors.New("dev: tailwindcss not found in PATH, download the standalone CLI from https://github.com/tailwindlabs/tailwindcss/releases")
			}
			return errors.Newf("dev: %s not found in PATH", name)
		}
	}
	if d.cfg.Templ != nil {
		if out, err := exec.Command("go", "tool", "-n", "templ").CombinedOutput(); err != nil {
			return errors.Newf("dev: templ is not a tool of this module, run: go get -tool github.com/a-h/templ/cmd/templ\n%s", out)
		}
	}
	return nil
}

// prepare 在启动监听进程之前运行一次：把框架的前端包复制到 .tenon/js（见 SyncJS），前端包的 package.json 变了
// 或者缺少依赖（见 NeedsInstall）时在根目录 bun install，确保每个前端的 dist 存在（见 EnsureDist），
// 写入前端开发服务器的地址（见 DevConfig），把框架 web/ui 的样式源复制到 Tailwind 入口旁边。
func (d dev) prepare() error {
	if _, err := GenerateTheme("."); err != nil {
		return err
	}
	if len(d.cfg.Frontends) > 0 {
		changed, err := SyncJS(".")
		if err != nil {
			return err
		}
		if changed || NeedsInstall(".") {
			if err := d.exec(".", "bun", "install"); err != nil {
				return err
			}
		}
		if err := writeDevConfig(d.servers); err != nil {
			return err
		}
	}
	if err := EnsureDist(d.cfg); err != nil {
		return err
	}
	if t := d.cfg.Tailwind; t != nil {
		return copyUI(filepath.Join(filepath.Dir(t.Input), UIDir))
	}
	return nil
}

// Placeholder 是前端还没有构建时 dist/index.html 的内容。
const Placeholder = "<!doctype html><p>前端还没有构建：运行 bun run dev 或 bun run build。</p>\n"

// EnsureDist 在前端的 dist 不存在时（例如刚 clone 的项目，dist 被 .gitignore 忽略）写入占位的 index.html，
// 嵌入 dist 的代码才能编译。不运行 vite build：它可能依赖还没生成的客户端，开发时页面也来自 Vite 的开发服务器，
// 构建发布时 build 会先构建前端。
func EnsureDist(cfg *project.Config) error {
	for _, f := range cfg.Frontends {
		dist := filepath.Join(f.Dir, "dist")
		if _, err := os.Stat(dist); !errors.Is(err, fs.ErrNotExist) {
			if err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(dist, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte(Placeholder), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// devServers 为每个前端选一个空闲端口，返回开发服务器的地址。
func devServers(frontends []project.Frontend) (map[string]string, error) {
	servers := map[string]string{}
	for _, f := range frontends {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, errors.Wrap(err, "dev: pick a port for the frontend dev server")
		}
		_, port, _ := net.SplitHostPort(ln.Addr().String())
		_ = ln.Close()
		servers[f.Name] = "http://localhost:" + port
	}
	return servers, nil
}

// writeDevConfig 把开发服务器的地址写入 DevConfig，应用读到 dev_servers 后把页面请求转给它们。
func writeDevConfig(servers map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(DevConfig), 0o755); err != nil {
		return errors.Wrap(err, "dev: write dev config")
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(DevConfig), ".gitignore"), []byte("*\n"), 0o644); err != nil {
		return errors.Wrap(err, "dev: write dev config")
	}
	var b strings.Builder
	b.WriteString("# tenon dev 生成：各前端开发服务器的地址。\ndev_servers:\n")
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		fmt.Fprintf(&b, "  %s: %s\n", name, servers[name])
	}
	return errors.Wrap(os.WriteFile(DevConfig, []byte(b.String()), 0o644), "dev: write dev config")
}

// serveArgs 返回运行应用的参数：有前端时在 serve 之后追加 --config DevConfig。
func (d dev) serveArgs() []string {
	if len(d.cfg.Frontends) == 0 {
		return d.cfg.Serve
	}
	return append(slices.Clone(d.cfg.Serve), "--config", DevConfig)
}

func (d dev) exec(dir string, args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = d.out, d.errOut
	return errors.Wrapf(cmd.Run(), "dev: %s", strings.Join(args, " "))
}

// watcher 是一个与应用一起运行的监听进程。
type watcher struct {
	dir  string
	args []string
}

// watchers 返回与应用一起运行的监听进程。
func (d dev) watchers() []watcher {
	var ws []watcher
	for _, f := range d.cfg.Frontends {
		// 前端 package.json 的 dev 脚本运行 vite。端口固定为选好的端口，被占用时直接失败：应用只把请求转给这个地址。
		ws = append(ws, watcher{f.Dir, []string{"bun", "run", "dev", "--port", port(d.servers[f.Name]), "--strictPort"}})
	}
	if t := d.cfg.Templ; t != nil {
		ws = append(ws, watcher{".", []string{"go", "tool", "templ", "generate", "--watch",
			"--proxy=" + d.cfg.URL, "--proxyport=" + strconv.Itoa(t.ProxyPort), "--open-browser=false"}})
	}
	if t := d.cfg.Tailwind; t != nil {
		// --watch=always：标准输入不是终端时也继续监听，否则 tailwindcss 在标准输入关闭时退出。
		ws = append(ws, watcher{".", []string{"tailwindcss", "-i", t.Input, "-o", t.Output, "--watch=always"}})
	}
	for _, t := range d.cfg.Tools {
		ws = append(ws, watcher{".", t.Watch})
	}
	return ws
}

// port 返回地址中的端口。
func port(addr string) string {
	_, p, _ := net.SplitHostPort(strings.TrimPrefix(addr, "http://"))
	return p
}

// builds 返回停止后运行的生成命令。
func (d dev) builds() []watcher {
	var bs []watcher
	if d.cfg.Templ != nil {
		bs = append(bs, watcher{".", []string{"go", "tool", "templ", "generate"}})
	}
	for _, f := range d.cfg.Frontends {
		bs = append(bs, watcher{f.Dir, []string{"bun", "run", "build"}})
	}
	if t := d.cfg.Tailwind; t != nil {
		bs = append(bs, watcher{".", []string{"tailwindcss", "-i", t.Input, "-o", t.Output, "--minify"}})
	}
	for _, t := range d.cfg.Tools {
		if len(t.Build) > 0 {
			bs = append(bs, watcher{".", t.Build})
		}
	}
	return bs
}

func (d dev) runs() []func(context.Context) error {
	runs := []func(context.Context) error{d.serve}
	for _, w := range d.watchers() {
		runs = append(runs, func(ctx context.Context) error { return d.run(ctx, w) })
	}
	if t := d.cfg.Templ; t != nil && t.Static != "" {
		runs = append(runs, func(ctx context.Context) error {
			return watch(ctx, func() string { return snapshot(t.Static, nil) }, 250*time.Millisecond, d.reload)
		})
	}
	// 设计令牌变化后重新生成前端的主题包，Vite 随之更新页面。令牌写错时只打印错误，改对后再生成。
	runs = append(runs, func(ctx context.Context) error {
		file := func(string) string {
			info, err := os.Stat(ThemeFile)
			if err != nil {
				return ""
			}
			return info.ModTime().String() + strconv.FormatInt(info.Size(), 10)
		}
		return watch(ctx, func() string { return file(ThemeFile) }, 250*time.Millisecond, func() {
			if ok, err := GenerateTheme("."); err != nil {
				fmt.Fprintf(d.errOut, "dev: %v\n", err)
			} else if ok {
				fmt.Fprintln(d.out, "dev: 设计令牌有变化，已重新生成 "+ThemeDir)
			}
		})
	})
	return runs
}

// run 运行一个监听进程，ctx 取消时向它的整个进程组发送中断信号。
func (d dev) run(ctx context.Context, w watcher) error {
	cmd := exec.CommandContext(ctx, w.args[0], w.args[1:]...)
	cmd.Dir = w.dir
	cmd.Stdout, cmd.Stderr = d.out, d.errOut
	interruptGroup(cmd)
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.Wrapf(err, "dev: %s", strings.Join(w.args, " "))
}

// reload 通知 templ 的代理刷新浏览器。
func (d dev) reload() {
	url := fmt.Sprintf("http://127.0.0.1:%d/_templ/reload/events", d.cfg.Templ.ProxyPort)
	if res, err := http.Post(url, "", nil); err == nil {
		_ = res.Body.Close()
	}
}

// finish 运行停止后的生成命令。
func (d dev) finish() error {
	for _, b := range d.builds() {
		if err := d.exec(b.dir, b.args...); err != nil {
			return err
		}
	}
	return nil
}

// concurrently 同时运行 fns，任何一个返回时取消其余的，返回第一个不是 context.Canceled 的错误。
func concurrently(ctx context.Context, fns ...func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, len(fns))
	for _, fn := range fns {
		go func() { errs <- fn(ctx) }()
	}
	var first error
	for range fns {
		err := <-errs
		cancel()
		if first == nil && err != nil && !errors.Is(err, context.Canceled) {
			first = err
		}
	}
	return first
}

// watch 每隔 every 取一次 state，变化并且稳定下来（连续两次相同）后调用 changed。
func watch(ctx context.Context, state func() string, every time.Duration, changed func()) error {
	return watchFrom(ctx, state(), state, every, changed)
}

// watchFrom 与 watch 相同，但以调用方事先记下的 last 作为初始状态：记下之后、开始轮询之前的变化同样会报告。
func watchFrom(ctx context.Context, last string, state func() string, every time.Duration, changed func()) error {
	t := time.NewTicker(every)
	defer t.Stop()
	pending := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			cur := state()
			switch {
			case cur != last:
				last, pending = cur, true
			case pending:
				pending = false
				changed()
			}
		}
	}
}

// snapshot 返回 dir 中文件的路径、大小和修改时间；include 不为 nil 时只算它接受的文件。目录不存在时为空。
func snapshot(dir string, include func(path string) bool) string {
	var b strings.Builder
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && project.SkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if include != nil && !include(path) {
			return nil
		}
		if info, err := d.Info(); err == nil {
			fmt.Fprintf(&b, "%s %d %d\n", path, info.Size(), info.ModTime().UnixNano())
		}
		return nil
	})
	return b.String()
}

// goSource 报告 path 是否影响应用的编译：.go（测试除外）、go.mod、go.sum。
func goSource(path string) bool {
	base := filepath.Base(path)
	return strings.HasSuffix(base, ".go") && !strings.HasSuffix(base, "_test.go") || base == "go.mod" || base == "go.sum"
}

// copyUI 把框架的 web/ui/css 复制到 dst，替换已有的内容。框架的位置由 go list 给出（通常在模块缓存中）。
func copyUI(dst string) error {
	dir, err := frameworkDir(".")
	if err != nil {
		return err
	}
	if err := copyTree(filepath.Join(dir, "web", "ui", "css"), dst); err != nil {
		return errors.Wrap(err, "dev: copy ui styles")
	}
	return errors.Wrap(os.WriteFile(filepath.Join(dst, ".gitignore"), []byte("*\n"), 0o644), "dev: copy ui styles")
}

// frameworkDir 返回项目 dir 使用的框架源码目录：通常在模块缓存中，replace 到本地时是本地的目录。
func frameworkDir(dir string) (string, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", framework)
	cmd.Dir = dir
	out, err := cmd.Output()
	fw := strings.TrimSpace(string(out))
	if err != nil || fw == "" {
		return "", errors.Newf("dev: cannot locate %s with go list: %v", framework, err)
	}
	return fw, nil
}

// copyTree 把 src 复制到 dst，先删除 dst 中已有的内容。跳过隐藏目录、node_modules 和 dist。
// 复制出的文件可写：模块缓存中的文件是只读的。
func copyTree(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return copyInto(src, dst, nil)
}

// copyInto 把 src 复制到 dst，覆盖同名的文件，跳过 project.SkipDir 中的目录（例如 node_modules），
// 以及 skip 返回 true 的文件（skip 为 nil 时不跳过）。
func copyInto(src, dst string, skip func(name string) bool) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != src && project.SkipDir(d.Name()) {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if skip != nil && skip(d.Name()) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
