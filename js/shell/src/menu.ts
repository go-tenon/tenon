// 菜单与面包屑：由路由的元数据生成，与组件库无关。外观层把 MenuItem 渲染为 ProLayout 的菜单或 shadcn 的侧栏。

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
  /** 页面的路径；分组的路径是它自己的路由的路径。 */
  path: string
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

/**
 * 生成菜单：带 title 的路由按路由树嵌套（没有标题的布局路由被跳过），按 order 再按路径排序。
 * menu 为 false、带路径参数、或 can 返回 false 的路由连同下级不出现；过滤后没有下级的分组也不出现。
 */
export function buildMenu(routes: RouteInfo[], can: (permission: string) => boolean = () => true): MenuItem[] {
  const byId = new Map(routes.map((r) => [r.id, r]))
  const titled = (r: RouteInfo | undefined) => Boolean(r?.staticData?.title)
  const parentOf = (r: RouteInfo): string | undefined => {
    for (let p = r.parent ? byId.get(r.parent) : undefined; p; p = p.parent ? byId.get(p.parent) : undefined) {
      if (titled(p)) return p.id
    }
    return undefined
  }
  const children = new Map<string | undefined, RouteInfo[]>()
  for (const r of routes) {
    if (!titled(r)) continue
    const p = parentOf(r)
    children.set(p, [...(children.get(p) ?? []), r])
  }
  const visible = (r: RouteInfo) => {
    const d = r.staticData ?? {}
    return d.menu !== false && !r.path.includes("$") && (!d.permission || can(d.permission))
  }
  const order = (a: RouteInfo, b: RouteInfo) =>
    (a.staticData?.order ?? 0) - (b.staticData?.order ?? 0) ||
    normalizePath(a.path).localeCompare(normalizePath(b.path))
  const build = (parent: string | undefined): MenuItem[] => {
    const items: MenuItem[] = []
    for (const r of (children.get(parent) ?? []).filter(visible).sort(order)) {
      const item: MenuItem = { id: r.id, title: r.staticData?.title ?? "", path: normalizePath(r.path) }
      if (r.staticData?.icon) item.icon = r.staticData.icon
      if (children.has(r.id)) {
        const sub = build(r.id)
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
