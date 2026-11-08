// Package views 存放各页面共用的布局、错误页与静态资源。
package views

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/shellphy-kit/tools/httpx/web"
)

//go:embed static
var staticFS embed.FS

// Static 是本服务自己的静态资源，Vendor 是 web 包内置的 htmx。在 main 中挂载：
//
//	r.Handle("/static/*", views.Static)
//	r.Handle("/vendor/*", views.Vendor)
var (
	Static = mustAssets("/static/", mustSub(staticFS, "static"))
	Vendor = mustAssets("/vendor/", web.Vendor)
)

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

func mustAssets(prefix string, fsys fs.FS) *web.Assets {
	a, err := web.NewAssets(prefix, fsys)
	if err != nil {
		panic(err)
	}
	return a
}

// ErrorHandler 渲染错误页，在 main 中赋值给 web.ErrorHandler。
// 4xx 显示错误信息（如 huma.Error404NotFound 的消息），5xx 只显示通用提示。
func ErrorHandler(w http.ResponseWriter, r *http.Request, status int, err error) {
	msg := "服务器开小差了，请稍后再试"
	if status < 500 {
		msg = err.Error()
	}
	if renderErr := web.Page(w, r, status, Layout(http.StatusText(status)), errorContent(status, msg)); renderErr != nil {
		http.Error(w, http.StatusText(status), status)
	}
}
