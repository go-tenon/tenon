package frontend

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/errors"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// 构建产物（项目的 apps/dist，go tool tenon build 生成）中的路径。
const (
	// DistDir 是嵌入的构建产物的目录：New 从传入的文件系统的这个子目录读取。
	DistDir = "dist"
	// ManifestFile 是前端的清单，内容是 []Entry。
	ManifestFile = "frontends.json"
	// ExecutableFile 是服务端渲染的 Bun 可执行文件：Bun 加上全部 SSR 前端的服务端入口。只在有 SSR 前端时存在。
	ExecutableFile = "ssr"
	// ShellFile 是只在浏览器中渲染的前端的页面外壳，在前端的目录中：只含根布局，页面在浏览器中渲染。
	ShellFile = "_shell.html"
)

// ErrUnsupported 表示当前系统不能从内存执行 SSR 可执行文件（只支持 Linux）。
var ErrUnsupported = errors.New("frontend: running the SSR executable requires Linux")

// 传给 SSR 进程的环境变量。socket 名字以 @ 开头，表示 Linux 的抽象 unix socket（不在文件系统中）：
// Bun 使用时要把开头的 @ 换成 \0，环境变量中不能出现 NUL 字节。
const (
	// EnvFrontend 是这个进程渲染的前端的名称，可执行文件据此加载对应的服务端入口。
	EnvFrontend = "TENON_FRONTEND"
	// EnvSocket 是 SSR 进程监听的 socket，Go 把页面请求转到这里。
	EnvSocket = "SSR_SOCKET"
	// EnvAPISocket 是 Go 为 SSR 进程提供的内部接口入口，渲染时经它调用接口。
	EnvAPISocket = "API_SOCKET"
)

// 请求和响应中与 SSR 进程约定的头，以及页面中的占位 nonce。
const (
	// HeaderNonce 是 Go 为这次页面请求生成的 CSP nonce，SSR 进程把它写到内联脚本上。
	HeaderNonce = "X-CSP-Nonce"
	// HeaderRoute 是 SSR 进程在响应中报告的前端路由模板，例如 /articles/$id。
	// Go 用它命名这次请求的 span 和访问日志中的路由，然后从响应中删除。
	HeaderRoute = "X-Tenon-Route"
	// PrerenderNonce 是构建时写在页面外壳和预渲染页面中的占位 nonce，Go 返回页面时换成本次请求的。
	PrerenderNonce = "__tenon_csp_nonce__"
)

// Entry 是清单中的一个前端，来自前端 package.json 的 tenon 字段。
type Entry struct {
	Name string `json:"name"` // 前端的名称，即 apps 中的目录名，也是 DistDir 中静态文件的子目录名。
	Base string `json:"base"` // 挂载的路径，以 / 开头和结尾，例如 /admin/。
	SSR  bool   `json:"ssr"`  // 是否在服务端渲染。false 时页面只在浏览器中渲染，静态文件之外的路径返回 ShellFile。
}

// Options 配置 Host。
type Options struct {
	API           http.Handler                               // SSR 进程渲染页面时调用的接口，通常就是对外的那一组接口路由。它在内部 socket 上提供，只接受与本进程同一用户的连接。nil 时不提供，例如只用 Render 生成 PDF 的队列 worker。
	DevServers    map[string]string                          // 各前端开发服务器的地址，按前端的名称，只在 tenon dev 时有值：不启动 SSR 进程，页面请求（包括热更新的 WebSocket）转给它们，不设置 CSP。
	CSP           func(r *http.Request, nonce string) string // 返回页面请求 r 的 Content-Security-Policy，nonce 是这次请求的，写在 HTML 响应上；按 r 的路径可以给不同的页面不同的策略。nil 时使用 DefaultCSP。
	Env           []string                                   // SSR 进程额外的环境变量，形如 KEY=value；进程继承 Go 程序的环境。
	Logger        *slog.Logger                               // 记录 SSR 进程的输出和启停，默认 slog.Default()；每一行带 component=ssr 和 frontend=<名称>。
	MeterProvider metric.MeterProvider                       // 记录 ssr.restarts（SSR 进程意外退出后重启的次数，属性 frontend 是前端的名称），nil 时不记录。
	ReadyTimeout  time.Duration                              // Run 等待 SSR 进程就绪的时间，默认 30 秒；超过时 Run 返回错误。
	StopTimeout   time.Duration                              // 停止时等待 SSR 进程退出的时间，默认 10 秒；超过时强制结束。
}

// Host 托管应用的全部前端：按挂载路径分发页面请求，提供静态文件，管理 SSR 前端的进程。
// 用 New 创建，可以并发使用。
type Host struct {
	opts     Options
	logger   *slog.Logger
	dist     fs.FS
	apps     []*app // 按挂载路径从长到短排列，分发时取第一个匹配的
	dev      bool
	api      string // 内部接口入口的抽象 socket，@ 开头
	restarts metric.Int64Counter
	handler  http.Handler
	started  atomic.Bool
}

// app 是一个前端。
type app struct {
	Entry
	logger  *slog.Logger
	files   fs.FS        // 静态文件：DistDir/<名称>；开发模式下为 nil
	dev     *url.URL     // 开发服务器，只在开发模式下有值
	socket  string       // SSR 进程监听的抽象 socket，@ 开头；只在生产模式的 SSR 前端有值
	handler http.Handler // 静态文件之外的请求：SSR 进程、开发服务器，或者页面外壳
	client  *http.Client // Render 用，只在 SSR 前端和开发模式下有值
	ready   atomic.Bool
	pid     atomic.Int64
}

// New 读取 apps 中 DistDir 下的清单，为每个前端准备处理器。apps 通常是项目 apps 包中 go:embed 嵌入的 dist 目录。
// 不是开发模式时检查构建产物：有 SSR 前端时需要 ExecutableFile，其余前端需要自己的 ShellFile。
func New(apps fs.FS, opts Options) (*Host, error) {
	h := &Host{opts: opts, logger: opts.Logger, dev: len(opts.DevServers) > 0}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	if h.opts.CSP == nil {
		h.opts.CSP = func(_ *http.Request, nonce string) string { return DefaultCSP(nonce) }
	}
	if h.opts.ReadyTimeout <= 0 {
		h.opts.ReadyTimeout = 30 * time.Second
	}
	if h.opts.StopTimeout <= 0 {
		h.opts.StopTimeout = 10 * time.Second
	}
	mp := opts.MeterProvider
	if mp == nil {
		mp = noop.NewMeterProvider()
	}
	restarts, err := mp.Meter("github.com/go-tenon/tenon/frontend").Int64Counter("ssr.restarts",
		metric.WithDescription("Times an SSR process exited unexpectedly and was restarted."))
	if err != nil {
		return nil, errors.Wrap(err, "frontend: create ssr.restarts")
	}
	h.restarts = restarts
	if h.dist, err = fs.Sub(apps, DistDir); err != nil {
		return nil, errors.Wrap(err, "frontend: open dist")
	}
	data, err := fs.ReadFile(h.dist, ManifestFile)
	if err != nil {
		return nil, errors.Wrapf(err, "frontend: read %s/%s (run go tool tenon dev or go tool tenon build)", DistDir, ManifestFile)
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, errors.Wrapf(err, "frontend: parse %s", ManifestFile)
	}
	id := rand.Text()
	h.api = "@tenon-api-" + id
	for _, e := range entries {
		if !strings.HasPrefix(e.Base, "/") || !strings.HasSuffix(e.Base, "/") {
			return nil, errors.Newf("frontend %s: base %q must start and end with /", e.Name, e.Base)
		}
		a := &app{Entry: e, logger: h.logger.With("component", "ssr", "frontend", e.Name)}
		if h.dev {
			addr := opts.DevServers[e.Name]
			u, err := url.Parse(addr)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return nil, errors.Newf("frontend %s: invalid dev server %q", e.Name, addr)
			}
			a.dev = u
			a.handler = h.devProxy(a)
			a.client = &http.Client{}
			h.apps = append(h.apps, a)
			continue
		}
		if a.files, err = fs.Sub(h.dist, e.Name); err != nil {
			return nil, errors.Wrapf(err, "frontend %s: open files", e.Name)
		}
		if e.SSR {
			if _, err := fs.Stat(h.dist, ExecutableFile); err != nil {
				return nil, errors.Wrapf(err, "frontend %s: server rendering needs %s/%s (run go tool tenon build)", e.Name, DistDir, ExecutableFile)
			}
			a.socket = "@tenon-ssr-" + e.Name + "-" + id
			a.handler = h.pageProxy(a)
			a.client = &http.Client{Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dialSocket(ctx, a.socket) },
			}}
		} else {
			if _, err := fs.Stat(a.files, ShellFile); err != nil {
				return nil, errors.Wrapf(err, "frontend %s: missing %s (run go tool tenon build)", e.Name, path.Join(DistDir, e.Name, ShellFile))
			}
			a.handler = h.shell(a)
		}
		h.apps = append(h.apps, a)
	}
	slices.SortStableFunc(h.apps, func(a, b *app) int { return len(b.Base) - len(a.Base) })
	h.handler = private(http.HandlerFunc(h.serveHTTP))
	return h, nil
}

// Handler 返回全部前端的页面处理器，挂在接口路由之后，例如 router.Handle("/*", pages.Handler())。
// 请求交给挂载路径最长匹配的前端；不在任何前端之下的路径返回 404。
func (h *Host) Handler() http.Handler { return h.handler }

// serveHTTP 把请求交给挂载路径匹配的前端：生产模式下先找静态文件，找不到时交给前端的处理器。
func (h *Host) serveHTTP(w http.ResponseWriter, r *http.Request) {
	for _, a := range h.apps {
		rel, ok := within(r.URL.Path, a.Base)
		if !ok {
			continue
		}
		if a.dev != nil {
			a.handler.ServeHTTP(w, r)
			return
		}
		r = withPage(r)
		if h.static(w, r, a, rel) {
			return
		}
		a.handler.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

// within 返回路径 p 在挂载路径 base 之下的部分，以 / 开头；p 等于去掉结尾 / 的 base 时是 /。
func within(p, base string) (string, bool) {
	if p == strings.TrimSuffix(base, "/") {
		return "/", true
	}
	if !strings.HasPrefix(p, base) {
		return "", false
	}
	return "/" + p[len(base):], true
}

// Ready 报告全部 SSR 进程是否可以接收请求；开发模式和没有 SSR 前端时总是 true。可以放进就绪检查。
func (h *Host) Ready() bool {
	for _, a := range h.apps {
		if a.socket != "" && !a.ready.Load() {
			return false
		}
	}
	return true
}

// Run 运行 serve（对外的 HTTP 服务，例如 httpx.ListenAndServe），并在它前后管理 SSR 进程：
//
//  1. 在内部 socket 上提供 Options.API（不为 nil 时），为每个 SSR 前端启动一个进程，等它们全部就绪
//     （超过 ReadyTimeout 返回错误）；
//  2. 调用 serve(ctx)，SSR 进程意外退出时按退避重启，期间这个前端的页面请求返回 502；
//  3. serve 返回后（ctx 取消，请求已经排空），停止 SSR 进程，再关闭内部入口。
//
// 开发模式和没有 SSR 前端时只调用 serve。一个 Host 只能 Run 一次。返回值合并了 serve 和停止过程的错误。
func (h *Host) Run(ctx context.Context, serve func(context.Context) error) error {
	if !h.started.CompareAndSwap(false, true) {
		return errors.New("frontend: Host.Run called twice")
	}
	var ssr []*app
	for _, a := range h.apps {
		if a.socket != "" {
			ssr = append(ssr, a)
		}
	}
	if len(ssr) == 0 {
		return serve(ctx)
	}
	closeInternal := func() error { return nil }
	if h.opts.API != nil {
		ln, err := listenPeer(h.api)
		if err != nil {
			return errors.Wrap(err, "frontend: listen internal API")
		}
		internal := &http.Server{Handler: h.opts.API, ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = internal.Serve(ln) }()
		closeInternal = func() error {
			sctx, cancel := context.WithTimeout(context.Background(), h.opts.StopTimeout)
			defer cancel()
			return errors.Wrap(internal.Shutdown(sctx), "frontend: shut down internal API")
		}
	}

	exe, err := h.load()
	if err != nil {
		return errors.Join(err, closeInternal())
	}
	// 每个 SSR 前端一个监控：procCtx 取消时停止进程，dones[i] 在监控结束时关闭。
	procCtx, stopProcs := context.WithCancel(context.Background())
	errs := make([]chan error, len(ssr))
	dones := make([]chan struct{}, len(ssr))
	for i, a := range ssr {
		errs[i], dones[i] = make(chan error, 1), make(chan struct{})
		go func() {
			errs[i] <- h.supervise(procCtx, a, exe)
			close(dones[i])
		}()
	}
	stop := func() error {
		stopProcs()
		var all []error
		for _, c := range errs {
			all = append(all, <-c)
		}
		return errors.Join(append(all, exe.close(), closeInternal())...)
	}

	deadline := time.Now().Add(h.opts.ReadyTimeout)
	for i, a := range ssr {
		if err := h.waitReady(ctx, a, dones[i], deadline); err != nil {
			return errors.Join(err, stop())
		}
	}
	serveErr := serve(ctx)
	return errors.Join(serveErr, stop())
}

// waitReady 等待前端 a 的 SSR 进程的 socket 可以连接；它的监控在此之前结束（无法启动进程）时立即返回。
func (h *Host) waitReady(ctx context.Context, a *app, supervisorDone <-chan struct{}, deadline time.Time) error {
	for !a.ready.Load() {
		if time.Now().After(deadline) {
			return errors.Newf("frontend %s: ssr process not ready after %s", a.Name, h.opts.ReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-supervisorDone:
			return errors.Newf("frontend %s: ssr process supervisor stopped before the process was ready", a.Name)
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil
}

// dialSocket 连接抽象 socket。
func dialSocket(ctx context.Context, name string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", name)
}
