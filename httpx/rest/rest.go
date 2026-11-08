package rest

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// Action 是单文件 action 的约定：在 Register 中调用 huma.Register 注册自己。
type Action interface {
	Register(api huma.API)
}

// Mount 依次注册 actions，api 可以是 huma.API 或 *huma.Group。
func Mount(api huma.API, actions ...Action) {
	for _, a := range actions {
		a.Register(api)
	}
}

// NewErrorWithContextFunc 是 huma.NewErrorWithContext 的函数类型。
type NewErrorWithContextFunc = func(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError

// LogServerErrors 包装 next（通常是 huma.NewErrorWithContext），
// 在状态码 >= 500 时用请求的 context 输出一条 Error 日志，响应内容不变：
//
//	huma.NewErrorWithContext = rest.LogServerErrors(huma.NewErrorWithContext)
//
// 日志的 err 字段以 %+v 格式化，cockroachdb/errors 等库的错误会带上调用栈；
// 因为使用请求的 context，request_id 等字段也会自动带上。日志使用 slog.Default()。
//
// 注意 fmt.Errorf("...: %w", err) 的结果不实现 fmt.Formatter，%+v 时不会输出内层的调用栈，
// 包装错误请用 cockroachdb/errors 的 errors.Wrap / errors.Wrapf。
//
// huma 只对 handler 返回的普通 error 调用 huma.NewErrorWithContext；
// handler 直接返回 huma.Error500InternalServerError 等已构造好的错误时不经过这里，
// 因此遇到非预期错误时请直接 return err。
func LogServerErrors(next NewErrorWithContextFunc) NewErrorWithContextFunc {
	return func(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
		if status >= 500 {
			var c context.Context = context.Background()
			if ctx != nil {
				c = ctx.Context()
			}
			attrs := []any{"status", status}
			// 逐个以 %+v 格式化：errors.Join 的结果不实现 fmt.Formatter，会丢失调用栈。
			var details []string
			for _, err := range errs {
				if err != nil {
					details = append(details, fmt.Sprintf("%+v", err))
				}
			}
			if len(details) > 0 {
				attrs = append(attrs, "err", strings.Join(details, "\n"))
			}
			slog.ErrorContext(c, msg, attrs...)
		}
		return next(ctx, status, msg, errs...)
	}
}
