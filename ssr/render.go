package ssr

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/cockroachdb/errors"
)

// PrintPath 是 SSR 进程渲染打印模板的地址前缀：POST /__tenon/print/{name}，请求体是模板的数据（JSON），
// 响应是一个完整的 HTML 页面。只有 Render 能调用它，对外的页面请求返回 404。
const PrintPath = "/__tenon/print/"

// ErrUnknownTemplate 表示 SSR 进程中没有这个打印模板。
var ErrUnknownTemplate = errors.New("ssr: unknown print template")

// maxRendered 是 Render 读取的 HTML 的上限。
const maxRendered = 64 << 20

// Render 用 SSR 进程中名为 name 的打印模板渲染 data，返回完整的 HTML 页面，通常交给 pdf.Renderer 生成 PDF。
// data 编码为 JSON 传给模板；模板只用传入的数据渲染，不调用接口，所以队列任务中没有用户的 cookie 也能使用。
// 请求 ID 和链路照常转交。SSR 进程没有就绪（Run 之外、重启期间）时返回错误。
func (h *Host) Render(ctx context.Context, name string, data any) ([]byte, error) {
	if name == "" || strings.Contains(name, "/") {
		return nil, errors.Newf("ssr: invalid print template name %q", name)
	}
	body, err := json.Marshal(data)
	if err != nil {
		return nil, errors.Wrap(err, "ssr: encode print data")
	}
	base := "http://ssr"
	if h.dev != nil {
		base = strings.TrimSuffix(h.dev.String(), "/")
	} else if !h.ready.Load() {
		return nil, errors.New("ssr: process is not ready")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+PrintPath+url.PathEscape(name), bytes.NewReader(body))
	if err != nil {
		return nil, errors.Wrap(err, "ssr: render")
	}
	req.Header.Set("Content-Type", "application/json")
	forward(ctx, req.Header)
	res, err := h.client.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "ssr: render %s", name)
	}
	defer res.Body.Close()
	html, err := io.ReadAll(io.LimitReader(res.Body, maxRendered))
	if err != nil {
		return nil, errors.Wrapf(err, "ssr: read rendered %s", name)
	}
	switch {
	case res.StatusCode == http.StatusNotFound:
		return nil, errors.Wrapf(ErrUnknownTemplate, "ssr: %s", name)
	case res.StatusCode != http.StatusOK:
		return nil, errors.Newf("ssr: render %s: %s: %s", name, res.Status, bytes.TrimSpace(html[:min(len(html), 512)]))
	}
	return html, nil
}

// renderClient 返回 Render 使用的 HTTP 客户端：生产时经 unix socket 连接 SSR 进程，开发时连接开发服务器。
func (h *Host) renderClient() *http.Client {
	if h.dev != nil {
		return &http.Client{}
	}
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dialSocket(ctx, h.socket) },
	}}
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
