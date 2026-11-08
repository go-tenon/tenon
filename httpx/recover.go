package httpx

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/shellphy-kit/tools/support"
)

// Recover 返回把 handler 中的 panic 转为 500 响应的中间件。
//
// panic 的值与调用栈以 Error 级别记录（err 字段为 *support.PanicError），
// 若响应尚未开始写入，则返回 500 Internal Server Error。
//
// http.ErrAbortHandler 是标准库约定的“中止响应”信号，会被重新抛出交给 net/http 处理，
// 不记录日志。WebSocket 等已升级的连接不再写入响应。
//
// log 为 nil 时在每次请求时使用 slog.Default()。
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			err := support.Catch(func() error {
				next.ServeHTTP(ww, r)
				return nil
			})

			var pe *support.PanicError
			if !errors.As(err, &pe) {
				return
			}
			if pe.Value == http.ErrAbortHandler {
				panic(http.ErrAbortHandler)
			}

			l := log
			if l == nil {
				l = slog.Default()
			}
			l.ErrorContext(r.Context(), "panic recovered", "err", err)

			if ww.Status() == 0 && !isUpgrade(r) {
				http.Error(ww, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		})
	}
}

func isUpgrade(r *http.Request) bool {
	for _, v := range r.Header.Values("Connection") {
		for token := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}
