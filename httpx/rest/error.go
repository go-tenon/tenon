package rest

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/mygoravel/fault"
)

// ErrorModel 是 Problem Details 风格响应，增加稳定 code。
type ErrorModel struct {
	huma.ErrorModel
	Code string `json:"code,omitempty" doc:"稳定业务错误码，如 order.not_found"`
}

// Status 实现 huma.StatusError。
func (e *ErrorModel) GetStatus() int { return e.Status }

// FromFault 将 fault 错误转为对外 StatusError；未知错误为 500 通用提示。
func FromFault(err error) huma.StatusError {
	if err == nil {
		return nil
	}
	var se huma.StatusError
	if ok := asStatusError(err, &se); ok {
		return se
	}
	status := fault.HTTPStatus(err)
	code := fault.CodeOf(err)
	detail := "An unexpected error occurred"
	if code != "" && status < 500 {
		detail = code
	}
	if code == "" {
		if status >= 500 {
			code = "error.internal"
		} else {
			code = "error.request"
		}
	}
	model := &ErrorModel{
		ErrorModel: huma.ErrorModel{
			Status: status,
			Title:  http.StatusText(status),
			Detail: detail,
		},
		Code: code,
	}
	return model
}

func asStatusError(err error, dest *huma.StatusError) bool {
	if se, ok := err.(huma.StatusError); ok {
		*dest = se
		return true
	}
	return false
}

// StripErrorValues 删除校验错误详情中的 value，避免回显输入（含秘密）。
func StripErrorValues(ctx huma.Context, status string, v any) (any, error) {
	switch e := v.(type) {
	case *huma.ErrorModel:
		stripDetails(e.Errors)
		return e, nil
	case *ErrorModel:
		stripDetails(e.Errors)
		return e, nil
	default:
		return v, nil
	}
}

func stripDetails(details []*huma.ErrorDetail) {
	for _, d := range details {
		if d != nil {
			d.Value = nil
		}
	}
}
