package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/cockroachdb/errors"

	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/i18n"
)

// ErrorHandler 渲染 HandlerFunc 返回的错误。产品在 main 中替换为自己的错误页：
//
//	web.ErrorHandler = func(w http.ResponseWriter, r *http.Request, status int, err error) {
//		_ = web.Render(w, r, status, views.ErrorPage(status))
//	}
//
// status 与给用户看的提示由 Message 解析。默认实现输出纯文本提示。
var ErrorHandler = func(w http.ResponseWriter, r *http.Request, status int, err error) {
	http.Error(w, Message(r, status, err), status)
}

// Message 返回错误给用户看的提示（按请求的语言翻译）：登记过的领域错误（httpx.DefineError）为其提示；
// 实现了 GetStatus() 的 4xx 错误（如 huma.Error404NotFound("…")）为其 Error()；其他为通用提示。
func Message(r *http.Request, status int, err error) string {
	if info, ok := httpx.Describe(r.Context(), err); ok {
		return info.Message
	}
	var se interface{ GetStatus() int }
	if status < 500 && errors.As(err, &se) {
		return err.Error()
	}
	return i18n.T(r.Context(), "error.internal")
}

// HandlerFunc 是返回 error 的 http handler。返回非 nil 错误时：
//
//   - 状态码取自登记的领域错误（httpx.DefineError），或错误链中第一个实现了 GetStatus() int 的错误，都没有则为 500；
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

	status := StatusOf(r.Context(), err)
	if status >= 500 {
		slog.ErrorContext(r.Context(), "request failed", "status", status, "err", fmt.Sprintf("%+v", err))
	}
	ErrorHandler(w, r, status, err)
}

// StatusOf 返回错误对应的 HTTP 状态码，见 HandlerFunc。
func StatusOf(ctx context.Context, err error) int {
	if info, ok := httpx.Describe(ctx, err); ok {
		return info.Status
	}
	var se interface{ GetStatus() int }
	if errors.As(err, &se) {
		return se.GetStatus()
	}
	return http.StatusInternalServerError
}
