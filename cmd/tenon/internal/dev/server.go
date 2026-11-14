package dev

import (
	"context"
	"fmt"
	"maps"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// pollInterval 是检查 Go 源文件变化的间隔。
var pollInterval = 300 * time.Millisecond

// stopTimeout 是平滑停止应用的时限，超过后强制结束。
const stopTimeout = 10 * time.Second

// serve 编译并运行应用，Go 源文件变化后重新编译、重启，直到 ctx 取消。
// 编译失败只打印错误：已经在运行的应用继续运行，等下一次修改。
func (d dev) serve(ctx context.Context) error {
	dir, err := os.MkdirTemp("", "devcmd-")
	if err != nil {
		return errors.Wrap(err, "devcmd: temp dir")
	}
	defer func() { _ = os.RemoveAll(dir) }()
	bin := filepath.Join(dir, "app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	var app *exec.Cmd
	exited := make(chan error, 1)
	defer func() { d.stop(app, exited) }()
	restart := func() {
		if !d.build(ctx, bin) {
			return
		}
		d.stop(app, exited)
		app, exited = d.start(bin)
		if app == nil {
			return
		}
		if d.cfg.Templ != nil {
			go d.reloadWhenUp(ctx)
		}
		d.generate(ctx, bin)
	}
	d.waitFrontends(ctx)
	restart()

	changes := make(chan struct{}, 1)
	every := pollInterval
	watching := make(chan struct{})
	defer func() { <-watching }()
	go func() {
		defer close(watching)
		_ = watch(ctx, func() string { return snapshot(".", goSource) }, every, func() {
			select {
			case changes <- struct{}{}:
			default:
			}
		})
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changes:
			fmt.Fprintln(d.out, "dev: Go 代码有变化，重新编译")
			d.waitFrontends(ctx)
			restart()
		case err := <-exited:
			// 应用自己退出（例如启动时配置错误）：等下一次修改后再启动。
			fmt.Fprintf(d.errOut, "dev: 应用已退出：%v，修改代码后重新启动\n", err)
			app = nil
		}
	}
}

// build 编译应用到 bin，失败时打印错误并返回 false。
func (d dev) build(ctx context.Context, bin string) bool {
	// -mod=mod：新导入的包带来新的依赖时自动补全 go.mod，开发中途即可使用新依赖。
	cmd := exec.CommandContext(ctx, "go", "build", "-mod=mod", "-o", bin, d.cfg.Main)
	cmd.Stdout, cmd.Stderr = d.out, d.errOut
	if err := cmd.Run(); err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(d.errOut, "dev: 编译失败：%v\n", err)
		}
		return false
	}
	return true
}

// start 运行编译出的应用；exited 在它退出时收到结果。
func (d dev) start(bin string) (*exec.Cmd, chan error) {
	cmd := exec.Command(bin, d.serveArgs()...)
	cmd.Stdout, cmd.Stderr = d.out, d.errOut
	cmd.Env = os.Environ()
	if d.cfg.Templ != nil {
		// 与 templ generate --watch 的 --cmd 相同：生成的代码从 _templ.txt 读取文本，只改文字时直接生效，跳过重新编译。
		root, _ := filepath.Abs(".")
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = resolved
		}
		cmd.Env = append(cmd.Env, "TEMPL_DEV_MODE=true", "TEMPL_DEV_MODE_WATCH_ROOT="+root)
	}
	ownGroup(cmd)
	exited := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(d.errOut, "dev: 启动应用失败：%v\n", err)
		return nil, exited
	}
	go func() { exited <- cmd.Wait() }()
	return cmd, exited
}

// stop 平滑停止应用：发送 SIGTERM，stopTimeout 之后强制结束。应用已经退出时直接返回。
func (d dev) stop(app *exec.Cmd, exited chan error) {
	if app == nil {
		return
	}
	terminate(app)
	var err error
	select {
	case err = <-exited:
	case <-time.After(stopTimeout):
		kill(app)
		err = <-exited
	}
	_ = err // 停止是 dev 自己要求的，退出状态不重要
}

// frontendWait 是等一个前端的开发服务器能访问的时限。
var frontendWait = 30 * time.Second

// waitFrontends 等每个前端的开发服务器能访问：应用把页面请求转给它们，太早打开会看到连接错误。
func (d dev) waitFrontends(ctx context.Context) {
	for _, f := range d.cfg.Frontends {
		waitHTTP(ctx, d.servers[f.Name], frontendWait)
	}
}

// generate 用编译出的程序生成客户端，失败时只打印错误。
func (d dev) generate(ctx context.Context, bin string) {
	for _, args := range Generations(d.cfg) {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Stdout, cmd.Stderr = d.out, d.errOut
		if err := cmd.Run(); err != nil && ctx.Err() == nil {
			fmt.Fprintf(d.errOut, "dev: 生成客户端失败：%v\n", err)
		}
	}
}

// Generations 返回生成客户端时运行应用的参数，每一项运行一次：有 packages/client 时为全部 API 生成 TypeScript 客户端，
// tenon.json 中设置了 go 的 API 各生成一个 Go 客户端。不生成任何客户端时为空。
func Generations(cfg *project.Config) [][]string {
	bases := slices.Sorted(maps.Keys(cfg.APIs))
	var runs [][]string
	if cfg.Client {
		args := []string{"client", "--ts", project.ClientSrc}
		for _, base := range bases {
			if name := cfg.APIs[base].Name; name != "" {
				args = append(args, "--name", base+"="+name)
			}
		}
		runs = append(runs, args)
	}
	for _, base := range bases {
		if out := cfg.APIs[base].Go; out != "" {
			runs = append(runs, []string{"client", "--base-path", base, "--out", out})
		}
	}
	return runs
}

// waitHTTP 等 url 能访问（任何响应都算），最多等 limit。
func waitHTTP(ctx context.Context, url string, limit time.Duration) {
	client := &http.Client{Timeout: time.Second}
	for deadline := time.Now().Add(limit); time.Now().Before(deadline) && ctx.Err() == nil; time.Sleep(100 * time.Millisecond) {
		if res, err := client.Get(url); err == nil {
			_ = res.Body.Close()
			return
		}
	}
}

// reloadWhenUp 等应用开始监听后刷新浏览器，最多等 10 秒。
func (d dev) reloadWhenUp(ctx context.Context) {
	host := hostPort(d.cfg.URL)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && ctx.Err() == nil; time.Sleep(100 * time.Millisecond) {
		if c, err := net.DialTimeout("tcp", host, time.Second); err == nil {
			_ = c.Close()
			d.reload()
			return
		}
	}
}

// hostPort 返回 raw 的主机和端口，没有端口时按协议补上。
func hostPort(raw string) string {
	u, err := neturl.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Port() != "" {
		return u.Host
	}
	port := "80"
	if u.Scheme == "https" {
		port = "443"
	}
	return net.JoinHostPort(u.Hostname(), port)
}
