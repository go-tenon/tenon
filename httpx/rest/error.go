package rest

import (
	"net/http"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/mygoravel/fault"
	"github.com/ryan053857/mygoravel/httpx"
)

// ErrorDetail 包含公开的校验提示和字段位置。
type ErrorDetail struct {
	Message  string `json:"message"`
	Location string `json:"location,omitempty"`
}

// ErrorModel 定义业务错误和校验错误共用的 Problem Details 响应。
type ErrorModel struct {
	Status    int           `json:"status"`
	Title     string        `json:"title"`
	Detail    string        `json:"detail"`
	Code      string        `json:"code"`
	RequestID string        `json:"request_id"`
	Errors    []ErrorDetail `json:"errors,omitempty"`
}

func (e *ErrorModel) Error() string             { return e.Detail }
func (e *ErrorModel) GetStatus() int            { return e.Status }
func (e *ErrorModel) ContentType(string) string { return "application/problem+json" }

// HTTPStatus 将 fault.Kind 映射为 HTTP 状态。
func HTTPStatus(err error) int {
	switch fault.KindOf(err) {
	case fault.InvalidArgument:
		return 422
	case fault.Unauthenticated:
		return 401
	case fault.PermissionDenied:
		return 403
	case fault.NotFound:
		return 404
	case fault.Conflict:
		return 409
	case fault.Unavailable:
		return 503
	default:
		return 500
	}
}

func statusCode(status int) string {
	switch status {
	case 400, 422:
		return "error.validation"
	case 401:
		return "auth.unauthenticated"
	case 403:
		return "auth.forbidden"
	case 404:
		return "error.not_found"
	case 405:
		return "error.method_not_allowed"
	case 413:
		return "error.body_too_large"
	default:
		if status >= 500 {
			return "error.internal"
		}
		return "error.request"
	}
}

// FromFault 转换业务错误，保留原生 Huma 状态错误；nil 输入返回 nil。
func FromFault(err error) huma.StatusError {
	if err == nil {
		return nil
	}
	code := fault.CodeOf(err)
	if code == "" {
		var se huma.StatusError
		if errors.As(err, &se) {
			return se
		}
	}
	status := HTTPStatus(err)
	if code == "" {
		code = statusCode(status)
	}
	detail := code
	if status >= 500 {
		detail = "An unexpected error occurred"
	}
	return &ErrorModel{Status: status, Title: http.StatusText(status), Detail: detail, Code: code}
}

// StripErrorValues 将 Huma 校验错误转换为公开响应，并填入请求标识。
func StripErrorValues(ctx huma.Context, status string, v any) (any, error) {
	var model *ErrorModel
	switch e := v.(type) {
	case *ErrorModel:
		copy := *e
		model = &copy
	case *huma.ErrorModel:
		model = &ErrorModel{Status: e.Status, Title: http.StatusText(e.Status), Code: statusCode(e.Status), Detail: http.StatusText(e.Status)}
		if e.Status >= 500 {
			model.Detail = "An unexpected error occurred"
		} else {
			for _, d := range e.Errors {
				if d != nil {
					model.Errors = append(model.Errors, ErrorDetail{Message: "Invalid value", Location: d.Location})
				}
			}
		}
	default:
		return v, nil
	}
	model.RequestID = httpx.RequestID(ctx.Context())
	return model, nil
}
