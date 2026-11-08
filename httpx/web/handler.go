package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// ErrorHandler 渲染 HandlerFunc 返回的错误。产品在 main 中替换为自己的错误页：
//
//	web.ErrorHandler = func(w http.ResponseWriter, r *http.Request, status int, err error) {
//		_ = web.Render(w, r, status, views.ErrorPage(status))
//	}
//
// status 由错误的 GetStatus() 方法决定（例如 huma.Error404NotFound），没有则为 500。
// 默认实现输出状态码对应的纯文本。
var ErrorHandler = func(w http.ResponseWriter, r *http.Request, status int, err error) {
	http.Error(w, http.StatusText(status), status)
}

// HandlerFunc 是返回 error 的 http handler。返回非 nil 错误时：
//
//   - 状态码取自错误链中第一个实现了 GetStatus() int 的错误，没有则为 500；
//   - 状态码 >= 500 时以 %+v 格式记录 Error 日志（cockroachdb/errors 的错误会带调用栈）；
//   - 调用 ErrorHandler 输出响应。
//
// 返回错误前不要写入响应。
//
//	r.Method(http.MethodGet, "/orders", web.HandlerFunc(a.serve))
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

func (f HandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := f(w, r)
	if err == nil {
		return
	}

	status := http.StatusInternalServerError
	var se interface{ GetStatus() int }
	if errors.As(err, &se) {
		status = se.GetStatus()
	}
	if status >= 500 {
		slog.ErrorContext(r.Context(), "request failed", "status", status, "err", fmt.Sprintf("%+v", err))
	}
	ErrorHandler(w, r, status, err)
}
