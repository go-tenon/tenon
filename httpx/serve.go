package httpx

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/errors"
)

// ServeOptions 控制 Serve 的停机行为。
type ServeOptions struct {
	// ShutdownTimeout 是 Shutdown 排空上限；0 表示立即 Close。
	ShutdownTimeout time.Duration
	// Logger 记录排空开始和服务器停止；nil 表示关闭这些日志。
	Logger *slog.Logger
	// OnDraining 在开始排空前调用（例如撤销 ready）；可空。
	OnDraining func()
}

// Serve 在 ln 上运行 srv，直到 ctx 取消或 Serve 返回错误。
//
// ctx 取消或监听异常后：先调用 OnDraining，再 Shutdown；若超时则取消请求并 Close。
// 调用方在 Serve 返回后关闭数据库等请求依赖。
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, opts ServeOptions) error {
	if srv == nil {
		return errors.New("httpx: nil server")
	}
	if ln == nil {
		return errors.New("httpx: nil listener")
	}
	if ctx == nil {
		return errors.New("httpx: nil context")
	}

	if opts.ShutdownTimeout < 0 {
		return errors.New("httpx: shutdown timeout must not be negative")
	}
	// 请求继承应用的 BaseContext，在排空结束后取消。
	requestRoot, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	baseContext := srv.BaseContext
	var stopCancel func() bool
	srv.BaseContext = func(l net.Listener) context.Context {
		base := context.Background()
		if baseContext != nil {
			base = baseContext(l)
		}
		ctx, cancel := context.WithCancel(base)
		stopCancel = context.AfterFunc(requestRoot, cancel)
		return ctx
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	var serveErr error
	returned := false
	select {
	case serveErr = <-errc:
		returned = true
	case <-ctx.Done():
	}
	if opts.OnDraining != nil {
		opts.OnDraining()
	}
	if opts.Logger != nil {
		opts.Logger.Info("http server draining")
	}
	var shutdownErr error
	if opts.ShutdownTimeout == 0 {
		cancelRequests()
		shutdownErr = srv.Close()
	} else {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), opts.ShutdownTimeout)
		shutdownErr = srv.Shutdown(shutdownCtx)
		cancel()
		if shutdownErr != nil {
			cancelRequests()
			shutdownErr = errors.Join(shutdownErr, srv.Close())
		}
	}
	if !returned {
		serveErr = <-errc
	}
	cancelRequests()
	if stopCancel != nil {
		stopCancel()
	}
	// 本次停机流程产生的 ErrServerClosed 表示正常退出。
	if !returned && errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	if opts.Logger != nil {
		opts.Logger.Info("http server stopped")
	}
	return errors.Join(errors.Wrap(serveErr, "httpx: serve"), errors.Wrap(shutdownErr, "httpx: shutdown"))
}

// Draining 是可在就绪检查中观察的排空标志。
type Draining struct{ v atomic.Bool }

// Set 标记开始排空。
func (d *Draining) Set() { d.v.Store(true) }

// Load 报告是否正在排空。
func (d *Draining) Load() bool { return d.v.Load() }
