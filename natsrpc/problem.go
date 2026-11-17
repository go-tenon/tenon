package natsrpc

import (
	"net/http"

	"github.com/go-tenon/tenon/errorx"
)

// 协议规定的请求头和响应头，见 docs/rpc/protocol.md。NATS 的请求头区分大小写。
const (
	HeaderAuthorization   = "Authorization"
	HeaderAcceptLanguage  = "Accept-Language"
	HeaderRequestID       = "X-Request-ID"
	HeaderTimeout         = "X-Timeout"
	HeaderContentLanguage = "Content-Language"
	HeaderRetryAfter      = "Retry-After"
	// HeaderErrorCode 和 HeaderError 是 NATS 服务协议规定的错误响应头：状态码和它的标准描述。
	HeaderErrorCode = "Nats-Service-Error-Code"
	HeaderError     = "Nats-Service-Error"
)

// Problem 是失败的响应体，与 HTTP 接口的错误响应相同（Problem Details）。
type Problem struct {
	Title  string         `json:"title"`
	Status int            `json:"status"`
	Detail string         `json:"detail,omitempty"` // 展示给用户的文案，按 Accept-Language 翻译，可能随语言和版本变化，不用于判断。
	Code   string         `json:"code,omitempty"`   // 稳定的错误码，只有业务错误包含。
	Params map[string]any `json:"params,omitempty"` // 业务错误的公开参数。
	Errors []FieldError   `json:"errors,omitempty"` // 字段错误的明细，只有请求格式错误包含。
}

// FieldError 是一个字段的错误。
type FieldError struct {
	Message  string `json:"message"`         // 按请求的语言翻译的提示。
	Location string `json:"location"`        // 出错的位置：body. 之后是请求体中的字段路径，例如 body.items[0].name。
	Value    any    `json:"value,omitempty"` // 服务端收到的值。
	Code     string `json:"code,omitempty"`  // 违反的规则，例如 validation.min_length。
}

// statusOf 返回错误的分类对应的状态码，与 HTTP 接口相同（httpx.StatusOf）。
func statusOf(kind errorx.Kind) int {
	switch kind {
	case errorx.InvalidArgument:
		return http.StatusUnprocessableEntity
	case errorx.Unauthenticated:
		return http.StatusUnauthorized
	case errorx.PermissionDenied:
		return http.StatusForbidden
	case errorx.NotFound:
		return http.StatusNotFound
	case errorx.Conflict:
		return http.StatusConflict
	case errorx.ResourceExhausted:
		return http.StatusTooManyRequests
	case errorx.Unavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// kindOf 是 statusOf 的反向：调用方据此把状态码还原为错误的分类。400 是请求格式错误，归入 InvalidArgument；
// 504 是处理超时，归入 Unavailable；不认识的状态码归入 Internal。
func kindOf(status int) errorx.Kind {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return errorx.InvalidArgument
	case http.StatusUnauthorized:
		return errorx.Unauthenticated
	case http.StatusForbidden:
		return errorx.PermissionDenied
	case http.StatusNotFound:
		return errorx.NotFound
	case http.StatusConflict:
		return errorx.Conflict
	case http.StatusTooManyRequests:
		return errorx.ResourceExhausted
	case http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return errorx.Unavailable
	default:
		return errorx.Internal
	}
}
