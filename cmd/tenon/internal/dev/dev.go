// Package dev 实现 tenon dev：与应用一起运行前端的开发服务器（TanStack Start 的 Vite）和其他工具，
// 修改 Go 代码后重新编译、重启应用，并重新生成客户端。
package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/cmd/tenon/internal/agents"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
	"github.com/go-tenon/tenon/frontend"
)

// DevConfig 是 tenon dev 写给应用的配置：各前端开发服务器的地址（dev_servers），追加在 serve 的 --config 之后。
const DevConfig = ".tenon/dev.yaml"

// Run 在当前目录运行开发模式，直到 ctx 取消；停止后运行工具的构建命令。clients 是同时运行的客户端，
// 例如 Electron 桌面端，它们的页面来自前端的开发服务器。
//
// 各进程的输出同时写进 out、errOut 和 LogFile。子进程的输出因此是管道：out 是终端时设置 FORCE_COLOR，
// 应用的日志和前端工具的输出照样按终端着色，写进 LogFile 时去掉颜色。
func Run(ctx context.Context, cfg *project.Config, clients []project.Client, out, errOut io.Writer) (err error) {
	log, err := openLog()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, log.Close()) }()
	if isTerminal(out) && os.Getenv("NO_COLOR") == "" {
		if err := os.Setenv("FORCE_COLOR", "1"); err != nil {
			return err
		}
	}
	d := dev{cfg: cfg, clients: clients,
		out: &lockedWriter{w: io.MultiWriter(out, log)}, errOut: &lockedWriter{w: io.MultiWriter(errOut, log)}}
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
	fmt.Fprintf(d.out, "dev: 打开 %s\n", cfg.URL)
	err = concurrently(ctx, d.runs()...)
	// 停止后运行工具的构建命令：监听模式的输出通常没有压缩。
	return errors.Join(err, d.finish())
}

// dev 是一次 tenon dev 的运行。
type dev struct {
	cfg         *project.Config
	clients     []project.Client // 同时运行的客户端
	out, errOut io.Writer
	servers     map[string]string // 各前端开发服务器的地址，按前端的名称。
}

// lockedWriter 让多个进程的输出逐次写入 w，不交错。
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// Write 加锁后把 p 写入 w。
func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// check 确认用到的命令都已安装：go；有前端时 bun；其他工具。
// 有前端时 serve 的参数中要有 --config，前端的地址追加在它之后。
func (d dev) check() error {
	need := []string{"go"}
	if len(d.cfg.Frontends) > 0 {
		need = append(need, "bun")
		if !project.HasConfigFlag(d.cfg.Serve) {
			return errors.Newf("dev: serve in %s has no --config; tenon dev appends --config %s to pass the frontend dev servers", project.File, DevConfig)
		}
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
			}
			return errors.Newf("dev: %s not found in PATH", name)
		}
	}
	return nil
}

// prepare 在启动监听进程之前运行一次：把框架的文档复制到 .tenon/docs（见 agents.SyncDocs），把框架的前端包复制到
// .tenon/js（见 SyncJS），前端包的 package.json 变了或者缺少依赖（见 NeedsInstall）时在根目录 bun install，
// 写入前端的清单（见 WriteManifest）和前端开发服务器的地址（见 DevConfig）。
func (d dev) prepare() error {
	fw, err := project.FrameworkDir(".")
	if err != nil {
		return err
	}
	if err := agents.SyncDocs(".", fw); err != nil {
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
	return WriteManifest(".", d.cfg)
}

// DistDir 是构建产物的目录，apps 包用 go:embed 嵌入它（frontend.New 读取）：前端的清单、每个前端的静态文件，
// 以及有 SSR 前端时的 Bun 可执行文件。它由 tenon build 生成；tenon dev、generate 只写清单。
const DistDir = project.AppsDir + "/" + frontend.DistDir

// DistIgnore 是提交在 DistDir 中的 .gitignore：目录中的其他文件都不提交。它让刚 clone 的项目在还没有生成清单时，
// apps 包的 embed 指令也有文件可嵌入，go build、go vet 照常通过；项目根目录的 .gitignore 用 !apps/dist/ 放行这个目录。
const DistIgnore = "# 构建产物，由 tenon build、tenon dev 生成，不提交。这个文件让 go:embed all:dist 在还没有生成时也能编译。\n*\n!.gitignore\n"

// WriteDistIgnore 在项目 root 的 DistDir 中写入 DistIgnore，目录不存在时创建它。
func WriteDistIgnore(root string) error {
	dir := filepath.Join(root, filepath.FromSlash(DistDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.Wrap(err, "dev: write apps/dist/.gitignore")
	}
	return errors.Wrap(os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(DistIgnore), 0o644), "dev: write apps/dist/.gitignore")
}

// WriteManifest 把项目 root 中的前端（名称、挂载路径、是否服务端渲染）写入 DistDir 中的清单，并写入 DistIgnore。
// 应用按清单分发页面请求。没有前端时什么也不做。
func WriteManifest(root string, cfg *project.Config) error {
	if len(cfg.Frontends) == 0 {
		return nil
	}
	if err := WriteDistIgnore(root); err != nil {
		return err
	}
	entries := make([]frontend.Entry, len(cfg.Frontends))
	for i, f := range cfg.Frontends {
		entries[i] = frontend.Entry{Name: f.Name, Base: f.Base, SSR: f.SSR}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return errors.Wrap(err, "dev: encode frontend manifest")
	}
	dir := filepath.Join(root, filepath.FromSlash(DistDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.Wrap(err, "dev: write frontend manifest")
	}
	return errors.Wrap(os.WriteFile(filepath.Join(dir, frontend.ManifestFile), append(data, '\n'), 0o644), "dev: write frontend manifest")
}

// CopyClient 把当前目录中前端 f 的浏览器构建（vite build 生成的 dist/client）复制到 DistDir/<名称>，不复制 source map。
// 只在浏览器中渲染的前端要有页面外壳（frontend.ShellFile）。
func CopyClient(f project.Frontend) error {
	src := filepath.Join(f.Dir, "dist", "client")
	return errors.Wrapf(copyBuild(src, filepath.Join(filepath.FromSlash(DistDir), f.Name), !f.SSR), "dev: %s", f.Dir)
}

// copyBuild 把前端的浏览器构建 src 复制到 dst，不复制 source map；shell 为 true 时 src 中必须有页面外壳。
func copyBuild(src, dst string, shell bool) error {
	if shell {
		if err := ensureShell(src); err != nil {
			return err
		}
	}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if strings.HasSuffix(d.Name(), ".map") {
			return nil // source map 等于源码，不嵌入
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	return errors.Wrapf(err, "copy %s", src)
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

// exec 在目录 dir 中执行命令 args，输出写到 dev 的输出。
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
	env  []string // 追加的环境变量
}

// watchers 返回与应用一起运行的监听进程。
func (d dev) watchers() []watcher {
	var ws []watcher
	for _, f := range d.cfg.Frontends {
		// 前端 package.json 的 dev 脚本运行 vite。端口固定为选好的端口，被占用时直接失败：应用只把请求转给这个地址。
		// 服务端渲染时没有内部 socket，经 API_ORIGIN 调用应用的接口。
		ws = append(ws, watcher{f.Dir, []string{"bun", "run", "dev", "--port", port(d.servers[f.Name]), "--strictPort"},
			[]string{"API_ORIGIN=" + d.cfg.URL}})
	}
	for _, t := range d.cfg.Tools {
		ws = append(ws, watcher{".", t.Watch, nil})
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
	for _, t := range d.cfg.Tools {
		if len(t.Build) > 0 {
			bs = append(bs, watcher{".", t.Build, nil})
		}
	}
	return bs
}

// runs 返回 tenon dev 同时运行的各项：应用本身、每个监视的工具和每个客户端。客户端正常退出（例如关闭了窗口）时
// 开发模式继续运行；客户端出错时整体停止。
func (d dev) runs() []func(context.Context) error {
	runs := []func(context.Context) error{d.serve}
	for _, w := range d.watchers() {
		runs = append(runs, func(ctx context.Context) error { return d.run(ctx, w) })
	}
	for _, c := range d.clients {
		w := d.clientWatcher(c)
		runs = append(runs, func(ctx context.Context) error {
			if err := d.run(ctx, w); err != nil {
				return err
			}
			fmt.Fprintf(d.out, "dev: %s 已退出，应用和前端继续运行\n", c.Dir)
			<-ctx.Done()
			return ctx.Err()
		})
	}
	return runs
}

// run 运行一个监听进程，ctx 取消时向它的整个进程组发送中断信号。
func (d dev) run(ctx context.Context, w watcher) error {
	cmd := exec.CommandContext(ctx, w.args[0], w.args[1:]...)
	cmd.Dir = w.dir
	if len(w.env) > 0 {
		cmd.Env = append(os.Environ(), w.env...)
	}
	cmd.Stdout, cmd.Stderr = d.out, d.errOut
	interruptGroup(cmd)
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.Wrapf(err, "dev: %s", strings.Join(w.args, " "))
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
