package frontend

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"
)

// PrintPath 是 SSR 进程渲染打印模板的地址前缀（不带前端的挂载路径）：POST /__tenon/print/{name}，请求体是模板的数据（JSON），
// 响应是一个完整的 HTML 页面。只有 Render 能调用它，对外的页面请求返回 404。
const PrintPath = "/__tenon/print/"

// ErrUnknownTemplate 表示 SSR 进程中没有这个打印模板。
var ErrUnknownTemplate = errors.New("frontend: unknown print template")

// maxRendered 是 Render 读取的 HTML 的上限。
const maxRendered = 64 << 20

// Render 用 SSR 前端 frontend 中名为 name 的打印模板渲染 data，返回完整的 HTML 页面，通常交给 pdf.Renderer 生成 PDF。
// data 编码为 JSON 传给模板；模板只用传入的数据渲染，不调用接口，所以队列任务中没有用户的 cookie 也能使用。
// 请求 ID 和链路照常转交。没有这个前端、它不在服务端渲染、SSR 进程没有就绪（Run 之外、重启期间）时返回错误。
func (h *Host) Render(ctx context.Context, frontend, name string, data any) ([]byte, error) {
	if name == "" || strings.Contains(name, "/") {
		return nil, errors.Newf("frontend: invalid print template name %q", name)
	}
	i := slices.IndexFunc(h.apps, func(a *app) bool { return a.Name == frontend })
	if i < 0 {
		return nil, errors.Newf("frontend: unknown frontend %q", frontend)
	}
	a := h.apps[i]
	if !a.SSR {
		return nil, errors.Newf("frontend %s: print templates need server rendering (set \"ssr\": true in its package.json)", frontend)
	}
	body, err := json.Marshal(data)
	if err != nil {
		return nil, errors.Wrap(err, "frontend: encode print data")
	}
	base := "http://ssr"
	if a.dev != nil {
		base = strings.TrimSuffix(a.dev.String(), "/")
	} else if !a.ready.Load() {
		return nil, errors.Newf("frontend %s: ssr process is not ready", frontend)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+PrintPath+url.PathEscape(name), bytes.NewReader(body))
	if err != nil {
		return nil, errors.Wrap(err, "frontend: render")
	}
	req.Header.Set("Content-Type", "application/json")
	forward(ctx, req.Header)
	res, err := a.client.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "frontend: render %s", name)
	}
	defer res.Body.Close()
	html, err := io.ReadAll(io.LimitReader(res.Body, maxRendered))
	if err != nil {
		return nil, errors.Wrapf(err, "frontend: read rendered %s", name)
	}
	switch {
	case res.StatusCode == http.StatusNotFound:
		return nil, errors.Wrapf(ErrUnknownTemplate, "frontend: %s", name)
	case res.StatusCode != http.StatusOK:
		return nil, errors.Newf("frontend: render %s: %s: %s", name, res.Status, bytes.TrimSpace(html[:min(len(html), 512)]))
	}
	return html, nil
}

// private 拒绝对外访问只供 Go 调用的地址（打印模板）。
func private(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/__tenon/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
