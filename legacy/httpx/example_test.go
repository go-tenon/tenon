package httpx_test

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx"
)

func Example() {
	cfg := httpx.DefaultConfig() // 通常嵌在产品配置中，由 config.Load 覆盖

	r := chi.NewRouter()
	r.Use(
		httpx.RealIP(cfg.TrustedProxies...),
		httpx.RequestID,
		httpx.AccessLog(nil),
		httpx.Recover(nil),
		httpx.SecureHeaders,
	)
	r.Get("/hello", func(w http.ResponseWriter, r *http.Request) {
		slog.InfoContext(r.Context(), "saying hello") // 自动带 request_id
		_, _ = w.Write([]byte("hello"))
	})

	srv := httpx.NewServer(cfg, r)
	srv.ReadyCheck = func(ctx context.Context) error {
		return nil // 例如 db.PingContext(ctx)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.Run(ctx); err != nil {
		slog.Error("http server", "err", err)
		os.Exit(1)
	}
}

// 内层中间件（如鉴权）产生的字段，用 WithLogAttrs 挂载，
// 才能同时出现在业务日志和访问日志中。
func ExampleWithLogAttrs() {
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := "u-42" // 实际从 session 或 token 解析
			ctx := httpx.WithLogAttrs(r.Context(), slog.String("user_id", userID))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	_ = authenticate
}
