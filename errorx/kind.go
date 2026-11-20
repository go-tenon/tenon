package errorx

// Kind 是与传输方式无关的错误分类。HTTP、命令行、队列等边界各自把它映射为
// 状态码、退出码或重试策略；客户端使用 Code 区分具体的业务失败。
type Kind int

// 错误的分类，括号中是 HTTP 的状态码。
const (
	InvalidArgument   Kind = iota + 1 // 输入不合法（422）
	Unauthenticated                   // 没有登录或凭据无效（401）
	PermissionDenied                  // 没有权限（403）
	NotFound                          // 资源不存在（404）
	Conflict                          // 与现有状态冲突，例如重复（409）
	ResourceExhausted                 // 超出限额或频率（429）
	Unavailable                       // 依赖暂时不可用，可以重试（503）
	Internal                          // 系统内部错误（500）
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
	case ResourceExhausted:
		return "resource_exhausted"
	case Unavailable:
		return "unavailable"
	default:
		return "internal"
	}
}

// Systemic 报告分类是否代表系统故障（Unavailable、Internal），与可预期的业务结果相对。
// 各入口据此决定是否记录错误日志、是否向调用者隐藏细节、长连接是否保持，
// 不必借用 HTTP 状态码判断：errorx.KindOf(err).Systemic()。
func (k Kind) Systemic() bool { return k == Unavailable || k == Internal }
