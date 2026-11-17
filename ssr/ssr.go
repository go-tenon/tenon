package ssr

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/errors"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// ErrUnsupported 表示当前系统不能从内存执行 SSR 可执行文件（只支持 Linux）。
var ErrUnsupported = errors.New("ssr: running the SSR executable requires Linux")

// 传给 SSR 进程的环境变量。socket 名字以 @ 开头，表示 Linux 的抽象 unix socket（不在文件系统中）：
// Bun 使用时要把开头的 @ 换成 \0，环境变量中不能出现 NUL 字节。
const (
	// EnvSocket 是 SSR 进程监听的 socket，Go 把页面请求转到这里。
	EnvSocket = "SSR_SOCKET"
	// EnvAPISocket 是 Go 为 SSR 进程提供的内部接口入口，渲染时经它调用接口。
	EnvAPISocket = "API_SOCKET"
)

// 请求和响应中与 SSR 进程约定的头。
const (
	// HeaderNonce 是 Go 为这次页面请求生成的 CSP nonce，SSR 进程把它写到内联脚本上。
	HeaderNonce = "X-CSP-Nonce"
	// HeaderRoute 是 SSR 进程在响应中报告的前端路由模板，例如 /articles/$id。
	// Go 用它命名这次请求的 span 和访问日志中的路由，然后从响应中删除。
	HeaderRoute = "X-Tenon-Route"
)

// Options 配置 Host。
type Options struct {
	// Executable 是 bun build --compile 编译出的 SSR 可执行文件，通常用 go:embed 嵌入。DevServer 为空时必填。
	Executable []byte
	// API 是 SSR 进程渲染页面时调用的接口，通常就是对外的那一组接口路由。
	// 它在内部 socket 上提供，只接受与本进程同一用户的连接。nil 时不提供，例如只用 Render 生成 PDF 的队列 worker。
	API http.Handler
	// DevServer 是前端开发服务器的地址，例如 http://localhost:5173，只在 tenon dev 时设置：
	// 不启动 SSR 进程，页面请求（包括热更新的 WebSocket）转给它，不设置 CSP。
	DevServer string
	// CSP 返回 nonce 对应的 Content-Security-Policy，写在 HTML 响应上；nil 时使用 DefaultCSP。
	CSP func(nonce string) string
	// Env 是 SSR 进程额外的环境变量，形如 KEY=value；进程继承 Go 程序的环境。
	Env []string
	// Logger 记录 SSR 进程的输出和启停，默认 slog.Default()；输出的每一行带 component=ssr。
	Logger *slog.Logger
	// MeterProvider 记录 ssr.restarts（SSR 进程意外退出后重启的次数），nil 时不记录。
	MeterProvider metric.MeterProvider
	// ReadyTimeout 是 Run 等待 SSR 进程就绪的时间，默认 30 秒；超过时 Run 返回错误。
	ReadyTimeout time.Duration
	// StopTimeout 是停止时等待 SSR 进程退出的时间，默认 10 秒；超过时强制结束。
	StopTimeout time.Duration
}

// Host 托管 SSR 进程并代理页面请求。用 New 创建，可以并发使用。
type Host struct {
	opts     Options
	logger   *slog.Logger
	dev      *url.URL
	socket   string // SSR 进程监听的抽象 socket，@ 开头
	api      string // 内部接口入口的抽象 socket，@ 开头
	restarts metric.Int64Counter
	handler  http.Handler
	client   *http.Client // Render 用
	ready    atomic.Bool
	pid      atomic.Int64
	started  atomic.Bool
}

// New 检查 opts 并创建 Host。DevServer 为空时 Executable 必填。
func New(opts Options) (*Host, error) {
	h := &Host{opts: opts, logger: opts.Logger}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	h.logger = h.logger.With("component", "ssr")
	if h.opts.CSP == nil {
		h.opts.CSP = DefaultCSP
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
	restarts, err := mp.Meter("github.com/go-tenon/tenon/ssr").Int64Counter("ssr.restarts",
		metric.WithDescription("Times the SSR process exited unexpectedly and was restarted."))
	if err != nil {
		return nil, errors.Wrap(err, "ssr: create ssr.restarts")
	}
	h.restarts = restarts
	if opts.DevServer != "" {
		u, err := url.Parse(opts.DevServer)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, errors.Newf("ssr: invalid dev server %q", opts.DevServer)
		}
		h.dev = u
		h.handler = private(h.devProxy())
		h.client = h.renderClient()
		return h, nil
	}
	if len(opts.Executable) == 0 {
		return nil, errors.New("ssr: Options.Executable is required")
	}
	id := rand.Text()
	h.socket = "@tenon-ssr-" + id
	h.api = "@tenon-api-" + id
	h.handler = private(h.pageProxy())
	h.client = h.renderClient()
	return h, nil
}

// Handler 返回页面的处理器，挂在接口路由之后，例如 router.Handle("/*", pages.Handler())。
func (h *Host) Handler() http.Handler { return h.handler }

// Ready 报告 SSR 进程是否可以接收请求；开发模式总是 true。可以放进就绪检查。
func (h *Host) Ready() bool { return h.dev != nil || h.ready.Load() }

// Run 运行 serve（对外的 HTTP 服务，例如 httpx.ListenAndServe），并在它前后管理 SSR 进程：
//
//  1. 在内部 socket 上提供 Options.API（不为 nil 时），启动 SSR 进程，等它就绪（超过 ReadyTimeout 返回错误）；
//  2. 调用 serve(ctx)，SSR 进程意外退出时按退避重启，期间页面请求返回 502；
//  3. serve 返回后（ctx 取消，请求已经排空），停止 SSR 进程，再关闭内部入口。
//
// 开发模式只调用 serve。一个 Host 只能 Run 一次。返回值合并了 serve 和停止过程的错误。
func (h *Host) Run(ctx context.Context, serve func(context.Context) error) error {
	if !h.started.CompareAndSwap(false, true) {
		return errors.New("ssr: Host.Run called twice")
	}
	if h.dev != nil {
		return serve(ctx)
	}
	closeInternal := func() error { return nil }
	if h.opts.API != nil {
		ln, err := listenPeer(h.api)
		if err != nil {
			return errors.Wrap(err, "ssr: listen internal API")
		}
		internal := &http.Server{Handler: h.opts.API, ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = internal.Serve(ln) }()
		closeInternal = func() error {
			sctx, cancel := context.WithTimeout(context.Background(), h.opts.StopTimeout)
			defer cancel()
			return errors.Wrap(internal.Shutdown(sctx), "ssr: shut down internal API")
		}
	}

	proc, err := h.prepare()
	if err != nil {
		return errors.Join(err, closeInternal())
	}
	procCtx, stopProc := context.WithCancel(context.Background())
	procErr := make(chan error, 1)
	procDone := make(chan struct{})
	go func() {
		procErr <- h.supervise(procCtx, proc)
		close(procDone)
	}()
	stop := func() error {
		stopProc()
		return errors.Join(<-procErr, closeInternal())
	}

	if err := h.waitReady(ctx, procDone); err != nil {
		return errors.Join(err, stop())
	}
	serveErr := serve(ctx)
	return errors.Join(serveErr, stop())
}

// waitReady 等待 SSR 进程的 socket 可以连接；监控在此之前结束（无法启动进程）时立即返回。
func (h *Host) waitReady(ctx context.Context, supervisorDone <-chan struct{}) error {
	deadline := time.Now().Add(h.opts.ReadyTimeout)
	for !h.ready.Load() {
		if time.Now().After(deadline) {
			return errors.Newf("ssr: process not ready after %s", h.opts.ReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-supervisorDone:
			return errors.New("ssr: process supervisor stopped before the process was ready")
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
