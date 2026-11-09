package httpx

import (
	"context"

	"github.com/cockroachdb/errors"

	"github.com/ryan053857/go-kit/i18n"
)

// Error 是登记了 HTTP 状态码与错误码的领域错误，用 DefineError 定义为包级变量：
//
//	var ErrEmailTaken = httpx.DefineError(http.StatusConflict, "account.email_taken")
//
// 错误码同时是多语言消息的 ID：REST 接口与页面的全局错误处理据此决定状态码，
// 并把错误码翻译为当前语言的提示。业务代码直接返回（或包装）它即可，用 errors.Is 判断。
type Error struct {
	status int
	code   string
}

// DefineError 定义一个领域错误。
func DefineError(status int, code string) *Error {
	return &Error{status: status, code: code}
}

// Error 返回错误码。
func (e *Error) Error() string { return e.code }

// HTTPStatus 返回登记的状态码。
func (e *Error) HTTPStatus() int { return e.status }

// Code 返回错误码。
func (e *Error) Code() string { return e.code }

// withData 为领域错误附带消息模板参数，例如 {"Min": 8}。
type withData struct {
	err  *Error
	data map[string]any
}

func (w *withData) Error() string { return w.err.code }
func (w *withData) Unwrap() error { return w.err }

// WithData 返回附带消息模板参数的 err，翻译提示时使用，例如“密码至少 {{.Min}} 位”：
//
//	return httpx.WithData(ErrWeakPassword, map[string]any{"Min": s.cfg.MinPasswordLength})
func WithData(err *Error, data map[string]any) error {
	return &withData{err: err, data: data}
}

// ErrorInfo 是从错误中解析出的响应信息。
type ErrorInfo struct {
	Status  int    // HTTP 状态码
	Code    string // 错误码；未登记的错误为 "error.internal"
	Message string // 按语言翻译后的提示
}

// Describe 解析 err：错误链中有登记的领域错误时返回其状态码与翻译后的提示，否则为 500 与通用提示。
// 翻译使用 ctx 的语言（见 i18n.Middleware）。
func Describe(ctx context.Context, err error) (ErrorInfo, bool) {
	var data map[string]any
	var wd *withData
	if errors.As(err, &wd) {
		data = wd.data
	}
	var e *Error
	if errors.As(err, &e) {
		return ErrorInfo{Status: e.status, Code: e.code, Message: i18n.T(ctx, e.code, data)}, true
	}
	return ErrorInfo{Status: 500, Code: "error.internal", Message: i18n.T(ctx, "error.internal")}, false
}
