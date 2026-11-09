package web

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/a-h/templ"
)

// Render 以 status 输出 HTML 组件 c。
//
// 组件先渲染到缓冲区，成功后才写入响应头与内容；渲染失败时不写入任何内容并返回错误，
// 调用方（通常是 HandlerFunc）仍可输出完整的错误页，而不是半个页面。
func Render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) error {
	var buf bytes.Buffer
	if err := c.Render(r.Context(), &buf); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}

// Page 输出页面：IsPartial 为 true 时只渲染 content，否则把 content 作为 layout 的子组件
// （布局模板中用 { children... } 引用）渲染整页。
//
// 同一 URL 既能直接打开、又能被 hx-get 局部加载时使用；只返回片段的接口直接用 Render。
// 响应会带上 Vary: HX-Request，避免浏览器或 CDN 缓存把片段当成整页返回。
//
//	return web.Page(w, r, http.StatusOK, views.Layout("订单"), OrderList(orders))
func Page(w http.ResponseWriter, r *http.Request, status int, layout, content templ.Component) error {
	w.Header().Add("Vary", "HX-Request")
	if IsPartial(r) {
		return Render(w, r, status, content)
	}
	return Render(w, r, status, templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return layout.Render(templ.WithChildren(ctx, content), w)
	}))
}
