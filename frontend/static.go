package frontend

import (
	"bytes"
	"html"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// static 在前端 a 的静态文件中查找 rel（前端内部的路径，以 / 开头），找到时返回它并报告 true：
// 依次查找文件本身和目录下的 index.html（预渲染的页面）。assets 下找不到的文件返回 404：
// 它们是构建出的脚本和样式，返回页面会让浏览器把 HTML 当作脚本执行。只处理 GET 和 HEAD。
func (h *Host) static(w http.ResponseWriter, r *http.Request, a *app, rel string) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	name := strings.TrimPrefix(rel, "/")
	candidates := []string{name + "index.html"}
	if name != "" && !strings.HasSuffix(name, "/") {
		candidates = []string{name, name + "/index.html"}
	}
	for _, c := range candidates {
		if !fs.ValidPath(c) {
			continue
		}
		f, err := a.files.Open(c)
		if err != nil {
			continue
		}
		info, err := f.Stat()
		if err != nil || info.IsDir() {
			_ = f.Close()
			continue
		}
		h.serveFile(w, r, a, c, f)
		_ = f.Close()
		return true
	}
	if strings.HasPrefix(name, "assets/") {
		http.NotFound(w, r)
		return true
	}
	return false
}

// serveFile 返回静态文件 f：assets 下的文件名带内容哈希，永久缓存；其余文件每次协商。
// HTML（页面外壳和预渲染的页面）中的占位 nonce 换成本次请求的，启用浏览器观测时注入公开配置，并带上对应的 Content-Security-Policy。
func (h *Host) serveFile(w http.ResponseWriter, r *http.Request, a *app, name string, f fs.File) {
	if strings.HasSuffix(name, ".html") {
		data, err := io.ReadAll(f)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		h.serveHTML(w, r, a, data)
		return
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, time.Time{}, rs)
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// serveHTML 返回构建时生成的页面，把其中的占位 nonce 换成本次请求的，并在启用浏览器观测时注入配置 meta 标签。
func (h *Host) serveHTML(w http.ResponseWriter, r *http.Request, a *app, page []byte) {
	nonce := requestNonce(r)
	page = bytes.ReplaceAll(page, []byte(PrerenderNonce), []byte(nonce))
	page = injectTelemetry(page, a.telemetry)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy", h.csp(r))
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(page))
}

// injectTelemetry 在 HTML 文档的 </head> 之前插入浏览器观测配置 meta 标签；未配置或已存在同名标签时原样返回。
func injectTelemetry(page []byte, telemetryJSON string) []byte {
	if telemetryJSON == "" || bytes.Contains(page, []byte(`name="`+MetaTelemetry+`"`)) {
		return page
	}
	tag := []byte(`<meta name="` + MetaTelemetry + `" content="` + html.EscapeString(telemetryJSON) + `">`)
	if idx := bytes.Index(bytes.ToLower(page), []byte("</head>")); idx >= 0 {
		out := make([]byte, 0, len(page)+len(tag))
		out = append(out, page[:idx]...)
		out = append(out, tag...)
		out = append(out, page[idx:]...)
		return out
	}
	out := make([]byte, 0, len(page)+len(tag))
	out = append(out, tag...)
	out = append(out, page...)
	return out
}

// shell 返回只在浏览器中渲染的前端 a 的处理器：静态文件之外的 GET 和 HEAD 请求都返回页面外壳，
// 由浏览器中的路由渲染页面；其他方法返回 405。
func (h *Host) shell(a *app) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		page, err := fs.ReadFile(a.files, ShellFile)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		h.serveHTML(w, r, a, page)
	})
}
