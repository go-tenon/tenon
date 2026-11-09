package fault

// Kind 是供传输边界映射状态的错误分类。客户端使用 Code 区分业务失败。
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
	default:
		return "internal"
	}
}
