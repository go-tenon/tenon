package identity

// 权限适用域常量。
const (
	DomainPlatform = "platform" // 平台域专用权限，租户自定义角色不能授予
	DomainTenant   = "tenant"   // 租户域权限
)

// Permission 是权限目录中的一项：权限名、适用域和给人看的说明。
// 应用的全部权限在代码中声明一次（例如 authz.Catalog），交给 rest.Options.Permissions 后写入 OpenAPI 的
// x-tenon-permissions，客户端据此生成权限常量。
type Permission struct {
	Name        string `json:"name"`                  // 以冒号分隔的权限名，例如 articles:write
	Description string `json:"description,omitempty"` // 在角色编辑界面中显示的说明
	Domain      string `json:"domain,omitempty"`      // 适用域：空字符串表示通用，DomainPlatform 表示仅平台可用，DomainTenant 表示仅租户可用
}
