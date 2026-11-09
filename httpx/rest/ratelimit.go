package rest

import (
	"net"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/httprate"
)

// RateLimit 返回 huma 中间件：每个客户端 IP 在 window 内最多请求 limit 次，超出时响应 429
// 并带 Retry-After 头。用于登录、注册、找回密码等容易被暴力尝试的接口：
//
//	authAPI := huma.NewGroup(v1, "/auth")
//	authAPI.UseMiddleware(rest.RateLimit(api, 10, time.Minute))
//
// 同一个中间件挂在多个接口上时共用计数。计数保存在进程内存中（github.com/go-chi/httprate），
// 多实例部署时各实例分别计数。客户端 IP 取自 RemoteAddr，部署在代理之后时需挂载 httpx.RealIP。
// 普通 chi 路由（如登录页的表单提交）直接使用 httprate.LimitBy。
func RateLimit(api huma.API, limit int, window time.Duration) func(huma.Context, func(huma.Context)) {
	l := httprate.NewRateLimiter(limit, window)
	return func(ctx huma.Context, next func(huma.Context)) {
		key := ctx.RemoteAddr()
		if host, _, err := net.SplitHostPort(key); err == nil {
			key = host
		}
		w := headerWriter{http.Header{}}
		r := (&http.Request{}).WithContext(ctx.Context())
		limited := l.OnLimit(w, r, key)
		for k, v := range w.h {
			ctx.SetHeader(k, v[0])
		}
		if limited {
			_ = huma.WriteErr(api, ctx, http.StatusTooManyRequests, "too many requests")
			return
		}
		next(ctx)
	}
}

// headerWriter 只用于接收 httprate 设置的响应头。
type headerWriter struct{ h http.Header }

func (w headerWriter) Header() http.Header       { return w.h }
func (headerWriter) Write(b []byte) (int, error) { return len(b), nil }
func (headerWriter) WriteHeader(int)             {}
