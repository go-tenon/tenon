// 菜单与面包屑：由路由的元数据生成，FrameLayout 把 MenuItem 渲染为侧栏菜单。

import type { StaticDataRouteOption } from "@tanstack/react-router"
import "./meta"

/** 生成菜单需要的路由信息，取自 router.routesById。 */
export interface RouteInfo {
  id: string
  /** 完整路径，例如 /articles/ 或 /articles/$id。 */
  path: string
  /** 父路由的 id；根路由没有。 */
  parent?: string
  staticData?: StaticDataRouteOption
}

/** 一个菜单项。有 children 的是分组。 */
export interface MenuItem {
  id: string
  title: string
  icon?: string
  /** 页面的路径；分组的路径是它自己的路由的路径。上级路由的路径参数已经填上当前地址中的值。 */
  path: string
  /** 填进 path 的路径参数，例如租户布局路由 /t/$tenant 的 { tenant: "acme" }；没有时不设置。 */
  params?: Record<string, string>
  /** 徽标：数字、文字，或者 true 表示小红点，见 useMenuBadge。 */
  badge?: number | string | boolean
  /** 点击菜单项跳转时从当前地址保留的查询参数名（继承自模块父路由）。 */
  preserveSearch?: readonly string[]
  children?: MenuItem[]
}

/** 一级面包屑。 */
export interface Crumb {
  title: string
  path: string
}

/** 去掉末尾的 /：/articles/ 与 /articles 是同一个页面。 */
export function normalizePath(path: string): string {
  return path.length > 1 && path.endsWith("/") ? path.slice(0, -1) : path || "/"
}

/** 从 TanStack Router 的路由读出 RouteInfo。 */
export function routeInfos(routesById: Record<string, unknown>): RouteInfo[] {
  return Object.values(routesById).map((r) => {
    const route = r as {
      id: string
      fullPath: string
      parentRoute?: { id: string }
      options?: { staticData?: StaticDataRouteOption }
    }
    return { id: route.id, path: route.fullPath, parent: route.parentRoute?.id, staticData: route.options?.staticData }
  })
}

/** path 中的路径参数名，例如 /t/$tenant/items/$id 是 ["tenant", "id"]；通配的 $ 是 "*"。 */
function pathParams(path: string): string[] {
  return path.split("/").flatMap((seg) => (seg === "$" ? ["*"] : seg.startsWith("$") ? [seg.slice(1)] : []))
}

/**
 * 生成菜单：带 title 的路由按路由树嵌套（没有标题的布局路由被跳过），按 order 再按路径排序。
 * menu 为 false、或 can 返回 false 的路由连同下级不出现；过滤后没有下级的分组也不出现。
 * 能进菜单的下级页面才让上级成为分组：列表路由下的详情页（/articles/$id）属于列表的菜单项，进入详情页时列表保持选中。
 *
 * 路径参数：路由自己引入的参数（例如详情页 /articles/$id 的 id）标识一条数据，这样的页面不出现；上级路由引入的参数
 * （例如租户布局路由 /t/$tenant 下的页面 /t/$tenant/inbox 的 tenant）从 params 取当前地址中的值填进链接，
 * 当前地址中没有这个参数（例如还没有进入租户）时页面不出现。
 */
export function buildMenu(
  routes: RouteInfo[],
  can: (permission: string) => boolean = () => true,
  badges: ReadonlyMap<string, number | string | boolean> = new Map(),
  params: Readonly<Record<string, string>> = {},
): MenuItem[] {
  const byId = new Map(routes.map((r) => [r.id, r]))
  // fill 返回 r 的链接和填进去的参数；页面不能出现在菜单中时返回 undefined。
  const fill = (r: RouteInfo): { path: string; params?: Record<string, string> } | undefined => {
    const names = pathParams(r.path)
    if (names.length === 0) return { path: r.path }
    const inherited = pathParams((r.parent && byId.get(r.parent)?.path) || "")
    if (names.some((n) => !inherited.includes(n) || params[n] === undefined || params[n] === "")) return undefined
    const used = Object.fromEntries(names.map((n) => [n, params[n]]))
    const path = r.path
      .split("/")
      .map((seg) => (seg.startsWith("$") ? encodeURIComponent(used[seg.slice(1)]) : seg))
      .join("/")
    return { path, params: used }
  }
  const titled = (r: RouteInfo | undefined) => Boolean(r?.staticData?.title)
  // listed：路由按自己的定义能进菜单，即带 title、menu 不是 false、路径参数都来自上级。只有这样的路由是上级的子项。
  const listed = (r: RouteInfo) => {
    if (!titled(r) || r.staticData?.menu === false) return false
    const inherited = pathParams((r.parent && byId.get(r.parent)?.path) || "")
    return pathParams(r.path).every((n) => inherited.includes(n))
  }
  const parentOf = (r: RouteInfo): string | undefined => {
    for (let p = r.parent ? byId.get(r.parent) : undefined; p; p = p.parent ? byId.get(p.parent) : undefined) {
      if (titled(p)) return p.id
    }
    return undefined
  }
  const children = new Map<string | undefined, RouteInfo[]>()
  for (const r of routes) {
    if (!listed(r)) continue
    const p = parentOf(r)
    children.set(p, [...(children.get(p) ?? []), r])
  }
  const visible = (r: RouteInfo) => {
    const d = r.staticData ?? {}
    return d.menu !== false && fill(r) !== undefined && (!d.permission || can(d.permission))
  }
  const order = (a: RouteInfo, b: RouteInfo) =>
    (a.staticData?.order ?? 0) - (b.staticData?.order ?? 0) ||
    normalizePath(a.path).localeCompare(normalizePath(b.path))
  const build = (parent: string | undefined, inheritedSearch?: readonly string[]): MenuItem[] => {
    const items: MenuItem[] = []
    for (const r of (children.get(parent) ?? []).filter(visible).sort(order)) {
      const filled = fill(r) ?? { path: r.path }
      const preserveSearch = r.staticData?.preserveSearch ?? inheritedSearch
      const item: MenuItem = { id: r.id, title: r.staticData?.title ?? "", path: normalizePath(filled.path) }
      if (filled.params) item.params = filled.params
      if (r.staticData?.icon) item.icon = r.staticData.icon
      if (preserveSearch?.length) item.preserveSearch = preserveSearch
      const badge = badges.get(r.id)
      if (badge !== undefined) item.badge = badge
      if (children.has(r.id)) {
        const sub = build(r.id, preserveSearch)
        if (sub.length === 0) continue // 分组中没有可以进入的页面
        item.children = sub
      }
      items.push(item)
    }
    return items
  }
  return build(undefined)
}

/** 菜单中第一个可以进入的页面，例如登录后的默认去处；没有时返回 undefined。 */
export function firstPage(items: readonly MenuItem[]): string | undefined {
  for (const item of items) {
    const path = item.children ? firstPage(item.children) : item.path
    if (path) return path
  }
  return undefined
}

/** 菜单中 path 对应的项（例如点击了面包屑中的分组）下第一个可以进入的页面。 */
export function pageUnder(items: readonly MenuItem[], path: string): string | undefined {
  const want = normalizePath(path)
  for (const item of items) {
    if (item.path === want) return item.children ? firstPage(item.children) : item.path
    const found = item.children && pageUnder(item.children, path)
    if (found) return found
  }
  return undefined
}

/** 由当前匹配到的路由生成面包屑：带 title 的路由，去掉相邻的重复。 */
export function breadcrumbs(matches: readonly { pathname: string; staticData?: StaticDataRouteOption }[]): Crumb[] {
  const out: Crumb[] = []
  for (const m of matches) {
    const title = m.staticData?.title
    if (!title) continue
    const crumb = { title, path: normalizePath(m.pathname) }
    const last = out[out.length - 1]
    if (last?.title === crumb.title && last.path === crumb.path) continue
    out.push(crumb)
  }
  return out
}
