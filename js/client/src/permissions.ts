// 权限的匹配：规则与服务端的 identity.Grants 相同，前端按权限显示菜单、页面和按钮时使用。

/**
 * grants 报告 granted 中是否有权限匹配 perm：同名的权限；* 匹配全部权限；以 :* 结尾的权限匹配同一前缀下的每个权限，
 * 例如 articles:* 匹配 articles:write；以 *: 开头的权限匹配同一动作结尾的每个权限，例如 *:read 匹配 articles:read。
 * 账号的权限中可以写通配符，接口返回的权限原样交给它判断。
 */
export function grants(granted: Iterable<string>, perm: string): boolean {
  if (perm === "") return false
  for (const g of granted) {
    if (g === perm || g === "*") return true
    if (g.endsWith(":*") && g.length > 2 && perm.startsWith(g.slice(0, -1))) return true
    if (g.startsWith("*:") && g.length > 2 && perm.length > g.length - 1 && perm.endsWith(g.slice(1))) return true
  }
  return false
}
