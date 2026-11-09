package rest

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/i18n"
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

// ErrorModel 是 REST 接口的错误响应（RFC 9457），比 huma.ErrorModel 多一个稳定的错误码：
//
//	{"status": 409, "title": "Conflict", "detail": "该邮箱已注册", "code": "account.email_taken"}
//
// 客户端根据 code 判断错误类型，根据 detail 显示提示（按请求的语言翻译）。
type ErrorModel struct {
	huma.ErrorModel
	Code string `json:"code,omitempty" doc:"错误码，客户端据此判断错误类型，如 account.email_taken"`
}

// 没有登记错误码时，按状态码使用的通用错误码。
var statusCodes = map[int]string{
	http.StatusBadRequest:          "error.validation",
	http.StatusUnauthorized:        "auth.unauthenticated",
	http.StatusForbidden:           "authz.forbidden",
	http.StatusNotFound:            "error.not_found",
	http.StatusUnprocessableEntity: "error.validation",
	http.StatusTooManyRequests:     "error.too_many_requests",
}

// NewErrorWithContext 替换 huma.NewErrorWithContext（app 已替换），使 REST 接口的错误：
//
//   - errs 中有登记的领域错误（httpx.DefineError）时，使用其状态码、错误码与翻译后的提示；
//     handler 直接 return 领域错误即可，不需要转换为 huma 的错误；
//
//   - 否则按状态码给出通用的错误码与提示（参数校验失败、未登录、无权限……），5xx 不暴露内部错误；
//
//   - 状态码 >= 500 时以 %+v 记录 Error 日志（带调用栈与 request_id）；
//
//   - 参数校验的字段级详情（huma.ErrorDetail）保留在 errors 中。
//
//     huma.NewErrorWithContext = rest.NewErrorWithContext
func NewErrorWithContext(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
	c := context.Background()
	if ctx != nil {
		c = ctx.Context()
	}
	model := &ErrorModel{}
	found := false
	for _, err := range errs {
		if err == nil {
			continue
		}
		if info, ok := httpx.Describe(c, err); ok && !found {
			found = true
			status, model.Code, model.Detail = info.Status, info.Code, info.Message
			continue
		}
		var d huma.ErrorDetailer
		if errors.As(err, &d) {
			model.Errors = append(model.Errors, d.ErrorDetail())
		}
	}
	if !found {
		code := statusCodes[status]
		if status >= 500 {
			code = "error.internal"
		}
		model.Code = code
		model.Detail = msg
		if code != "" {
			model.Detail = i18n.T(c, code)
		}
	}
	if status >= 500 {
		attrs := []any{"status", status}
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
	model.Status = status
	model.Title = http.StatusText(status)
	return model
}
