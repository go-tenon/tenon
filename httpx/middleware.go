package httpx

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"

	"github.com/ryan053857/mygoravel/i18n"
	"github.com/ryan053857/mygoravel/logging"
)

type requestIDKey struct{}

// RequestID 返回请求标识，未设置时返回空字符串。
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// MiddlewareOptions 配置实例级 HTTP 策略，非正数限额使用默认值。
type MiddlewareOptions struct {
	Logger         *slog.Logger
	Languages      *goi18n.Bundle
	MaxBodyBytes   int64
	RequestTimeout time.Duration
	TrustedProxies []netip.Prefix
}

// Middleware 安装请求关联、访问日志、恢复、限额与语言协商。
// 客户端地址沿可信代理链从右向左解析。
func Middleware(opts MiddlewareOptions) func(http.Handler) http.Handler {
	if opts.Logger != nil {
		opts.Logger = slog.New(logging.NewContextHandler(opts.Logger.Handler()))
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = 1 << 20
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 30 * time.Second
	}
	opts.TrustedProxies = append([]netip.Prefix(nil), opts.TrustedProxies...)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.Clone(r.Context())
			r.RemoteAddr = clientAddress(r, opts.TrustedProxies)
			id := rand.Text()
			ctx := context.WithValue(r.Context(), requestIDKey{}, id)
			ctx = logging.WithAttrs(ctx, slog.String(logging.AttrRequestID, id))
			ctx, cancel := context.WithTimeout(ctx, opts.RequestTimeout)
			defer cancel()

			r = r.WithContext(ctx)
			w.Header().Set("X-Request-ID", id)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			rw := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			defer func() {
				if opts.Logger != nil {
					status := rw.Status()
					if status == 0 {
						status = http.StatusOK
					}
					opts.Logger.InfoContext(ctx, "http request", "method", r.Method, "status", status, "duration", time.Since(start), "client", r.RemoteAddr)
				}
			}()
			defer func() {
				if recovered := recover(); recovered != nil {
					if recovered == http.ErrAbortHandler {
						panic(recovered)
					}
					if opts.Logger != nil {
						opts.Logger.ErrorContext(ctx, "request panic", "stack", string(debug.Stack()))
					}
					if rw.Status() == 0 {
						WriteProblem(rw, 500, "error.internal", id)
					} else {
						panic(http.ErrAbortHandler)
					}
				}

			}()
			if r.ContentLength > opts.MaxBodyBytes {
				WriteProblem(rw, 413, "error.body_too_large", id)
				return
			}
			r.Body = http.MaxBytesReader(rw, r.Body, opts.MaxBodyBytes)
			if opts.Languages != nil {
				locale := i18n.MatchAcceptLanguage(opts.Languages, r.Header.Get("Accept-Language"))
				ctx = i18n.WithLocale(ctx, locale)
				w.Header().Set("Content-Language", locale)
			}
			r = r.WithContext(ctx)
			next.ServeHTTP(rw, r)
		})
	}
}

// WriteProblem 输出 HTTP 边界的通用 Problem Details 响应。
func WriteProblem(w http.ResponseWriter, status int, code, id string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	detail := http.StatusText(status)
	if status >= 500 {
		detail = "An unexpected error occurred"
	}
	_ = json.NewEncoder(w).Encode(struct {
		Status    int    `json:"status"`
		Title     string `json:"title"`
		Detail    string `json:"detail"`
		Code      string `json:"code"`
		RequestID string `json:"request_id"`
	}{status, http.StatusText(status), detail, code, id})
}

func clientAddress(r *http.Request, proxies []netip.Prefix) string {
	trusted := func(a netip.Addr) bool {
		for _, p := range proxies {
			if p.Contains(a.Unmap()) {
				return true
			}
		}
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !trusted(peer) {
		return r.RemoteAddr
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for j := len(parts) - 1; j >= 0; j-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(parts[j]))
		if err != nil {
			return r.RemoteAddr
		}
		if !trusted(addr) || j == 0 {
			return addr.String()
		}
	}
	return r.RemoteAddr
}
