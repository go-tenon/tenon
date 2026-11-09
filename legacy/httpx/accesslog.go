package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/ryan053857/go-kit/logger"
)

// AccessLog 返回为每个请求输出一条 "http request" 日志的中间件，字段为：
//
//	method, path, query, route, status, bytes, duration_ms, ip, user_agent
//
// query 为原始查询字符串（不含 ?），没有时为空。
// route 为 chi 匹配到的路由模板（如 /users/{id}），基数低，适合做聚合统计；
// 未匹配任何路由时为空。
// 状态码 >= 500 时级别为 Error，其余为 Info。
//
// 日志使用请求的 context 输出，因此会带上 RequestID 等外层中间件挂载的字段；
// 内层（如鉴权中间件）需要补充到访问日志的字段，请用 WithLogAttrs 挂载。
//
// log 为 nil 时在每次请求时使用 slog.Default()。
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			extra := &logAttrs{}
			ctx := context.WithValue(r.Context(), logAttrsKey{}, extra)
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				status := ww.Status()
				if status == 0 {
					status = http.StatusOK
				}
				route := ""
				if rc := chi.RouteContext(ctx); rc != nil {
					route = rc.RoutePattern()
				}
				attrs := []slog.Attr{
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("query", r.URL.RawQuery),
					slog.String("route", route),
					slog.Int("status", status),
					slog.Int("bytes", ww.BytesWritten()),
					slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
					slog.String("ip", ClientIP(r)),
					slog.String("user_agent", r.UserAgent()),
				}
				attrs = append(attrs, extra.get()...)

				level := slog.LevelInfo
				if status >= http.StatusInternalServerError {
					level = slog.LevelError
				}
				l := log
				if l == nil {
					l = slog.Default()
				}
				l.LogAttrs(r.Context(), level, "http request", attrs...)
			}()

			next.ServeHTTP(ww, r.WithContext(ctx))
		})
	}
}

// WithLogAttrs 返回通过 logger.WithAttrs 挂上 attrs 的 context，
// 同时把 attrs 补充到本请求的访问日志中。
//
// 外层中间件无法看到内层修改过的 context，所以内层产生的请求级字段（如鉴权得到的 user_id）
// 需要用本函数挂载，才能同时出现在业务日志与访问日志里：
//
//	ctx := httpx.WithLogAttrs(r.Context(), slog.String("user_id", id))
//	next.ServeHTTP(w, r.WithContext(ctx))
//
// 请求未经过 AccessLog 时，效果与 logger.WithAttrs 相同。
func WithLogAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	if la, ok := ctx.Value(logAttrsKey{}).(*logAttrs); ok {
		la.add(attrs)
	}
	return logger.WithAttrs(ctx, attrs...)
}

type logAttrsKey struct{}

// logAttrs 收集内层补充给访问日志的字段。handler 可能在其他 goroutine 中调用 WithLogAttrs，因此加锁。
type logAttrs struct {
	mu    sync.Mutex
	attrs []slog.Attr
}

func (l *logAttrs) add(attrs []slog.Attr) {
	l.mu.Lock()
	l.attrs = append(l.attrs, attrs...)
	l.mu.Unlock()
}

func (l *logAttrs) get() []slog.Attr {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.attrs
}
