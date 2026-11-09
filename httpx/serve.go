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
	// ShutdownTimeout 是 Shutdown 排空上限；0 表示不等待（立即 Close）。
	ShutdownTimeout time.Duration
	// Logger 可选；nil 时不写启动/停机日志。
	Logger *slog.Logger
	// OnDraining 在开始排空前调用（例如撤销 ready）；可空。
	OnDraining func()
}

// Serve 在 ln 上运行 srv，直到 ctx 取消或 Serve 返回错误。
//
// ctx 取消后：先调用 OnDraining，再 Shutdown；若超时则 Close。
// 必须等 Serve 返回后，调用方才应关闭数据库等依赖（由 cleanup 顺序保证）。
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

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	if opts.Logger != nil {
		opts.Logger.Info("http server started", "addr", ln.Addr().String())
	}

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.Wrap(err, "httpx: serve")
	case <-ctx.Done():
	}

	if opts.OnDraining != nil {
		opts.OnDraining()
	}
	if opts.Logger != nil {
		opts.Logger.Info("http server shutting down", "timeout", opts.ShutdownTimeout)
	}

	sctx := context.Background()
	var cancel context.CancelFunc
	if opts.ShutdownTimeout > 0 {
		sctx, cancel = context.WithTimeout(sctx, opts.ShutdownTimeout)
		defer cancel()
	}
	if err := srv.Shutdown(sctx); err != nil {
		_ = srv.Close()
		serveErr := <-errc
		if !errors.Is(serveErr, http.ErrServerClosed) && serveErr != nil {
			return errors.Join(errors.Wrap(err, "httpx: shutdown"), serveErr)
		}
		return errors.Wrap(err, "httpx: shutdown")
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.Wrap(err, "httpx: serve")
	}
	if opts.Logger != nil {
		opts.Logger.Info("http server stopped")
	}
	return nil
}

// Draining 是可在就绪检查中观察的排空标志。
type Draining struct{ v atomic.Bool }

// Set 标记开始排空。
func (d *Draining) Set() { d.v.Store(true) }

// Load 报告是否正在排空。
func (d *Draining) Load() bool { return d.v.Load() }
