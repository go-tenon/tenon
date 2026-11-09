package fault

// Kind 是错误分类，供边界映射；不是业务错误码。客户端分支应使用 Code。
type Kind int

const (
	InvalidArgument Kind = iota + 1
	Unauthenticated
	PermissionDenied
	NotFound
	Conflict
	Unavailable
	Internal
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
