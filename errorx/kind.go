package errorx

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

// parseKind 是 String 的逆运算，未知名称返回 Internal。
func parseKind(s string) Kind {
	for k := InvalidArgument; k <= Internal; k++ {
		if k.String() == s {
			return k
		}
	}
	return Internal
}
