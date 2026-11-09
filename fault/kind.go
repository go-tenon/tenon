package fault

import "net/http"

// Kind 是错误分类，供边界映射；不是业务错误码。客户端分支应使用 Code。
type Kind int

const (
	InvalidArgument  Kind = iota + 1 // 422
	Unauthenticated                  // 401
	PermissionDenied                 // 403
	NotFound                         // 404
	Conflict                         // 409
	Unavailable                      // 503
	Internal                         // 500
)

// String 返回稳定的 kind 名称。
func (k Kind) String() string {
	switch k {
	case InvalidArgument:
		return "invalid_argument"
	case Unauthenticated:
		return "unauthenticated"
	case PermissionDenied:
		return "permission_denied"
	case NotFound:
		return "not_found"
	case Conflict:
		return "conflict"
	case Unavailable:
		return "unavailable"
	case Internal:
		return "internal"
	default:
		return "internal"
	}
}

// HTTPStatus 返回第一期 HTTP 默认映射。语法解码失败（400）由 HTTP 边界自行处理，
// 不进入领域 Kind。
func (k Kind) HTTPStatus() int {
	switch k {
	case InvalidArgument:
		return http.StatusUnprocessableEntity
	case Unauthenticated:
		return http.StatusUnauthorized
	case PermissionDenied:
		return http.StatusForbidden
	case NotFound:
		return http.StatusNotFound
	case Conflict:
		return http.StatusConflict
	case Unavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
