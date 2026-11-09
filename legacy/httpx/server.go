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

// 健康检查路径。Server 在路由之前处理这两个路径，因此它们不经过中间件，也不产生访问日志。
const (
	// LivenessPath 只要进程能处理请求就返回 200。
	LivenessPath = "/healthz"
	// ReadinessPath 在正常服务时返回 200；停机过程中或 ReadyCheck 失败时返回 503。
	ReadinessPath = "/readyz"
)

// Server 运行 HTTP 服务，负责超时设置、健康检查与优雅停机。
// 用 NewServer 创建，每个 Server 只能运行一次。
type Server struct {
	// ReadyCheck 可选，在每次请求 /readyz 时调用（例如检查数据库连接），
	// 返回错误时 /readyz 返回 503。请让它尽快返回。
	ReadyCheck func(ctx context.Context) error

	cfg      Config
	handler  http.Handler
	draining atomic.Bool
}

// NewServer 返回以 cfg 配置、由 h 处理请求的 Server。
func NewServer(cfg Config, h http.Handler) *Server {
	return &Server{cfg: cfg, handler: h}
}

// Run 监听 cfg.Addr 并开始服务，直到 ctx 被取消或服务出错。
//
// ctx 取消后依次：/readyz 改为返回 503 → 等待 ShutdownDelay →
// 停止接受新连接并等待进行中的请求完成（最长 ShutdownTimeout，超时后强制关闭连接）。
// 正常停机返回 nil。
//
// 通常配合 signal.NotifyContext 使用：
//
//	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//	defer stop()
//	err := srv.Run(ctx)
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return errors.Wrap(err, "httpx")
	}
	return s.Serve(ctx, ln)
}

// Serve 与 Run 相同，但使用调用方提供的 listener，返回时 ln 已关闭。
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	log := slog.Default()
	srv := &http.Server{
		Handler:           s.healthHandler(s.handler),
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout,
		ReadTimeout:       s.cfg.ReadTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("http server started", "addr", ln.Addr().String())

	select {
	case err := <-errc:
		return errors.Wrap(err, "httpx")
	case <-ctx.Done():
	}

	s.draining.Store(true)
	log.Info("http server shutting down", "delay", s.cfg.ShutdownDelay, "timeout", s.cfg.ShutdownTimeout)
	if d := s.cfg.ShutdownDelay; d > 0 {
		time.Sleep(d)
	}

	sctx := context.Background()
	if t := s.cfg.ShutdownTimeout; t > 0 {
		var cancel context.CancelFunc
		sctx, cancel = context.WithTimeout(sctx, t)
		defer cancel()
	}
	if err := srv.Shutdown(sctx); err != nil {
		_ = srv.Close()
		<-errc
		return errors.Wrap(err, "httpx: shutdown")
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return errors.Wrap(err, "httpx")
	}
	log.Info("http server stopped")
	return nil
}

func (s *Server) healthHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case LivenessPath:
			writeHealth(w, http.StatusOK, "ok")
		case ReadinessPath:
			switch {
			case s.draining.Load():
				writeHealth(w, http.StatusServiceUnavailable, "shutting down")
			case s.ReadyCheck != nil:
				if err := s.ReadyCheck(r.Context()); err != nil {
					slog.WarnContext(r.Context(), "readiness check failed", "err", err)
					writeHealth(w, http.StatusServiceUnavailable, "not ready")
					return
				}
				fallthrough
			default:
				writeHealth(w, http.StatusOK, "ok")
			}
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func writeHealth(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body + "\n"))
}
