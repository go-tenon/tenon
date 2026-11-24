package httpx

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
)

// ListenAndServe 监听 cfg.Addr，并按 cfg 运行 handler，直到 ctx 取消。
func ListenAndServe(ctx context.Context, cfg Config, handler http.Handler, opts ...ServeOption) error {
	if err := cfg.Validate(); err != nil {
		return errors.Wrap(err, "httpx: config")
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return errors.Wrap(err, "httpx: listen")
	}
	return Serve(ctx, cfg, ln, handler, opts...)
}

// Serve 在 ln 上按 cfg 运行 handler（不使用 cfg.Addr 监听，但 cfg 仍需合法），直到 ctx 取消或监听异常。
//
// Serve 返回时 ln 已关闭，包括配置不合法、提前返回的情况。
//
// 排空开始和服务器停止记录到 slog.Default()，用 Logger 选项改变。
// 停止时：先关闭 DrainStarted 返回的通道（Ready 随即返回 503），然后在 cfg.ShutdownTimeout 内排空请求；
// 超时则取消请求 context 并强制关闭。调用方在 Serve 返回后关闭数据库等请求依赖。
func Serve(ctx context.Context, cfg Config, ln net.Listener, handler http.Handler, opts ...ServeOption) error {
	if err := cfg.Validate(); err != nil {
		_ = ln.Close()
		return errors.Wrap(err, "httpx: config")
	}
	var o serveOptions
	for _, opt := range opts {
		opt.applyServe(&o)
	}
	logger := componentLogger(o.logger)
	// 所有请求 context 派生自 requestRoot，排空超时或结束后统一取消；
	// draining 在排空开始时关闭，供 SSE 等长连接主动结束，见 DrainStarted。
	draining := make(chan struct{})
	srv := cfg.server(drainClose(draining, handler))
	var conns connTracker
	srv.ConnState = conns.track

	requestRoot, cancelRequests := context.WithCancel(context.WithValue(context.Background(), drainKey{}, draining))
	defer cancelRequests()
	srv.BaseContext = func(net.Listener) context.Context { return requestRoot }
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	for _, fn := range o.onReady {
		fn()
	}
	var serveErr error
	returned := false
	select {
	case serveErr = <-errc:
		returned = true
	case <-ctx.Done():
	}
	close(draining)
	logger.Info("http server draining")
	if !returned && cfg.DrainDelay > 0 && (o.skipDrainDelay == nil || !o.skipDrainDelay()) {
		logger.Info("http server waiting for load balancer drain", "delay", cfg.DrainDelay)
		timer := time.NewTimer(cfg.DrainDelay)
		select {
		case serveErr = <-errc:
			returned = true
		case <-timer.C:
		}
		timer.Stop()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	// Shutdown 开始之后才读到的请求，net/http 直接关闭连接。所以先停止接收新连接，
	// 等已经接收的连接读到第一个请求，并给反向代理短暂宽限期消费 Connection: close 主动关闭旧的 keep-alive 连接，再开始 Shutdown。
	if !returned {
		_ = ln.Close()
		serveErr = <-errc
		if errors.Is(serveErr, net.ErrClosed) {
			serveErr = nil
		}
		conns.waitFresh(shutdownCtx, cfg.ReadHeaderTimeout)
		conns.waitIdleDrain(shutdownCtx, min(keepaliveDrainGrace, cfg.ShutdownTimeout/2))
	}
	shutdownErr := srv.Shutdown(shutdownCtx)
	cancel()
	if shutdownErr != nil {
		cancelRequests()
		shutdownErr = errors.Join(shutdownErr, srv.Close())
	}
	cancelRequests()
	logger.Info("http server stopped")
	return errors.Join(errors.Wrap(serveErr, "httpx: serve"), errors.Wrap(shutdownErr, "httpx: shutdown"))
}

// keepaliveDrainGrace 是关闭监听之后、调用 Shutdown 之前，等待反向代理按 Connection: close 主动回收旧 keep-alive 连接的宽限上限。
const keepaliveDrainGrace = 500 * time.Millisecond

// drainKey 是排空通道在 context 中的键。
type drainKey struct{}

// DrainStarted 返回一个在服务器开始排空时关闭的通道。
// 长连接（例如 SSE）应当监听它并主动结束，否则 Serve 要等满 ShutdownTimeout 才能退出；
// 浏览器会自动重连到其他实例。请求不是由 Serve 处理时返回 nil，从它接收会一直阻塞。
func DrainStarted(ctx context.Context) <-chan struct{} {
	ch, _ := ctx.Value(drainKey{}).(chan struct{})
	return ch
}

// drainClose 包装 next：排空开始后在写出 HTTP/1.x 响应头时自动写入 Connection: close，
// 包括排空前已进入处理器、排空后才写响应头的在途请求，促使反向代理及时关闭旧进程的 keep-alive 连接。
// 带 Upgrade 请求头的连接升级请求（例如 WebSocket）原样交给下一个处理器。
func drainClose(draining <-chan struct{}, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 || r.Header.Get("Upgrade") != "" {
			next.ServeHTTP(w, r)
			return
		}
		dw := &drainCloseWriter{ResponseWriter: w, draining: draining}
		next.ServeHTTP(dw, r)
	})
}

// drainCloseWriter 拦截 HTTP/1.x 响应头写出时机：若此时已进入排空，在写响应头前补上 Connection: close。
type drainCloseWriter struct {
	http.ResponseWriter
	draining <-chan struct{}
	wrote    bool
}

// markClose 在首次写出最终响应前检查是否已开始排空，是则写入 Connection: close。
func (w *drainCloseWriter) markClose() {
	if w.wrote {
		return
	}
	w.wrote = true
	select {
	case <-w.draining:
		w.Header().Set("Connection", "close")
	default:
	}
}

// WriteHeader 写出状态码；1xx 信息响应直接透传，最终响应写出前按需附带 Connection: close。
func (w *drainCloseWriter) WriteHeader(code int) {
	if code >= 100 && code < 200 {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.markClose()
	w.ResponseWriter.WriteHeader(code)
}

// Write 写出响应体前按需附带 Connection: close。
func (w *drainCloseWriter) Write(p []byte) (int, error) {
	w.markClose()
	return w.ResponseWriter.Write(p)
}

// FlushError 供 http.ResponseController.Flush 使用，刷新前按需附带 Connection: close。
func (w *drainCloseWriter) FlushError() error {
	w.markClose()
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Flush 实现 http.Flusher。
func (w *drainCloseWriter) Flush() { _ = w.FlushError() }

// Hijack 实现 http.Hijacker，将连接接管转发给底层 ResponseWriter。
func (w *drainCloseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// Unwrap 返回底层 ResponseWriter，透传 http.ResponseController 的扩展能力。
func (w *drainCloseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// connTracker 追踪尚未读到第一个请求的连接（fresh）与尚未关闭的连接（open）。
type connTracker struct {
	mu    sync.Mutex
	fresh map[net.Conn]struct{}
	open  map[net.Conn]struct{}
}

// track 随连接状态的变化更新 fresh 与 open 集合。
func (t *connTracker) track(c net.Conn, state http.ConnState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch state {
	case http.StateNew:
		if t.fresh == nil {
			t.fresh = make(map[net.Conn]struct{})
		}
		if t.open == nil {
			t.open = make(map[net.Conn]struct{})
		}
		t.fresh[c] = struct{}{}
		t.open[c] = struct{}{}
	case http.StateClosed, http.StateHijacked:
		delete(t.fresh, c)
		delete(t.open, c)
	default:
		delete(t.fresh, c)
	}
}

// freshLen 返回还没读到第一个请求的连接数。
func (t *connTracker) freshLen() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.fresh)
}

// openLen 返回尚未关闭的连接数。
func (t *connTracker) openLen() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.open)
}

// waitFresh 等待全部新连接读到第一个请求，最多等 limit：超过请求头读取时限的连接会被服务器关闭。
func (t *connTracker) waitFresh(ctx context.Context, limit time.Duration) {
	t.waitZero(ctx, limit, t.freshLen)
}

// waitIdleDrain 等待已打开的旧连接按 Connection: close 自行关闭，最多等 limit；全部关闭时立即返回。
func (t *connTracker) waitIdleDrain(ctx context.Context, limit time.Duration) {
	if limit <= 0 {
		return
	}
	t.waitZero(ctx, limit, t.openLen)
}

// waitZero 轮询 count 归零或超时退出。
func (t *connTracker) waitZero(ctx context.Context, limit time.Duration, count func() int) {
	if count() == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for count() > 0 {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
