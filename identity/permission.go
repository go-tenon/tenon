package identity

// Permission 是权限目录中的一项：权限名和给人看的说明。
// 应用的全部权限在代码中声明一次（例如 authz.Catalog），交给 rest.Options.Permissions 后写入 OpenAPI 的
// x-tenon-permissions，客户端据此生成权限常量。
type Permission struct {
	Name        string `json:"name"`                  // 以冒号分隔的权限名，例如 articles:write
	Description string `json:"description,omitempty"` // 在角色编辑界面中显示的说明
}
