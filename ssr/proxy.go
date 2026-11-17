package ssr

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/propagation"

	"github.com/go-tenon/tenon/logging"
)

// DefaultCSP 是 HTML 响应默认的 Content-Security-Policy：脚本只能来自本站，内联脚本必须带这次请求的 nonce；
// 样式允许内联（组件库用 style 属性）；不允许被其他网站嵌入。
func DefaultCSP(nonce string) string {
	return "default-src 'self'; script-src 'self' 'nonce-" + nonce + "'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'"
}

type nonceKey struct{}

// pageProxy 把页面请求经 unix socket 转给 SSR 进程，流式转发，不缓冲。
func (h *Host) pageProxy() http.Handler {
	proxy := &httputil.ReverseProxy{
		Transport: &http.Transport{
			DialContext:         func(ctx context.Context, _, _ string) (net.Conn, error) { return dialSocket(ctx, h.socket) },
			MaxIdleConnsPerHost: 64,
		},
		FlushInterval: -1,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme, r.Out.URL.Host = "http", "ssr"
			r.Out.Host = r.In.Host
			r.SetXForwarded()
			ctx := r.In.Context()
			forward(ctx, r.Out.Header)
			nonce, _ := ctx.Value(nonceKey{}).(string)
			r.Out.Header.Set(HeaderNonce, nonce)
		},
		ModifyResponse: func(res *http.Response) error {
			route(res)
			if isHTML(res) {
				nonce, _ := res.Request.Context().Value(nonceKey{}).(string)
				res.Header.Set("Content-Security-Policy", h.opts.CSP(nonce))
			}
			return nil
		},
		ErrorHandler: h.unavailable,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nonceKey{}, base64.RawStdEncoding.EncodeToString(b))))
	})
}

// devProxy 把页面请求转给开发服务器，包括热更新的 WebSocket。开发服务器注入的内联脚本没有 nonce，所以不设置 CSP。
func (h *Host) devProxy() http.Handler {
	return &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(h.dev)
			r.Out.Host = r.In.Host
			r.SetXForwarded()
			forward(r.In.Context(), r.Out.Header)
		},
		ModifyResponse: func(res *http.Response) error {
			route(res)
			return nil
		},
		ErrorHandler: h.unavailable,
	}
}

// forward 转交请求 ID 和链路：SSR 进程调用接口时原样带回，页面和它调用的接口在同一条链路中、日志带同一个请求 ID。
func forward(ctx context.Context, header http.Header) {
	if id := logging.RequestID(ctx); id != "" {
		header.Set("X-Request-ID", id)
	}
	header.Del("Traceparent")
	header.Del("Tracestate")
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(header))
}

// route 用 SSR 进程报告的前端路由模板替换 chi 的路由模板，httpx.Telemetry 据此命名 span（例如 GET /articles/$id），
// 而不是挂载页面处理器的 /*。路由模板不对外暴露。
func route(res *http.Response) {
	r := res.Header.Get(HeaderRoute)
	res.Header.Del(HeaderRoute)
	if r == "" || !strings.HasPrefix(r, "/") {
		return
	}
	if rctx := chi.RouteContext(res.Request.Context()); rctx != nil {
		rctx.RoutePatterns = []string{r}
	}
}

func isHTML(res *http.Response) bool {
	return strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "text/html")
}

// unavailable 在 SSR 进程不可用（重启中）时返回 502 页面；客户端已经断开时什么都不做。
func (h *Host) unavailable(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	h.logger.WarnContext(r.Context(), "ssr unavailable", "error", err)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write([]byte(unavailablePage))
}

const unavailablePage = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">` +
	`<title>页面暂时不可用</title><body style="font-family:system-ui,sans-serif;margin:4rem auto;max-width:32rem;padding:0 1rem">` +
	`<h1 style="font-size:1.25rem">页面暂时不可用</h1><p>页面服务正在重启，请稍后刷新。</p></body></html>`
