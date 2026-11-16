// 布局约定：一个布局读取导航模型和布局状态，把插槽放到自己的位置上；页面只往插槽中放内容，不关心当前是哪一种布局。
// 侧栏、顶部导航、图标栏等具体的布局由 @go-tenon/admin（antd）和 shadcn registry 按这个约定实现。

import { useMatches } from "@tanstack/react-router"
import { type ReactNode, useCallback, useLayoutEffect, useMemo, useSyncExternalStore } from "react"
import { createPortal } from "react-dom"
import { useApp, useBreadcrumbs, useMenu } from "./app"
import { type Badge, useBadgeOf } from "./badges"
import { type Crumb, firstPage, type MenuItem } from "./menu"
import { createStore, perOwner, type Store } from "./store"

// ———————————————————————————— 导航模型 ————————————————————————————

/** 布局需要的导航信息。 */
export interface Navigation {
  /** 按权限过滤后的菜单树。 */
  menu: MenuItem[]
  /** 当前位置在菜单中的路径：从第一级到最深一级的菜单项 id，用于高亮当前项、展开所在的分组。 */
  active: string[]
  /** 第一级菜单，例如“顶栏 + 侧栏”的顶栏、“图标栏 + 二级面板”的图标栏。 */
  modules: MenuItem[]
  /** 当前所在的第一级菜单项。 */
  module?: MenuItem
  /** 当前第一级下的菜单，例如二级导航、二级面板。 */
  secondary: MenuItem[]
  crumbs: Crumb[]
}

/** 在菜单中找到当前位置：匹配到的路由中，菜单里最深的那一项及其上级。 */
export function activePath(menu: readonly MenuItem[], matched: ReadonlySet<string>): string[] {
  let best: string[] = []
  const walk = (items: readonly MenuItem[], trail: string[]) => {
    for (const item of items) {
      const here = [...trail, item.id]
      if (matched.has(item.id) && here.length > best.length) best = here
      if (item.children) walk(item.children, here)
    }
  }
  walk(menu, [])
  return best
}

/** 当前的导航模型。 */
export function useNavigation(): Navigation {
  const menu = useMenu()
  const crumbs = useBreadcrumbs()
  const matches = useMatches()
  const matched = useMemo(() => new Set(matches.map((m) => m.routeId as string)), [matches])
  return useMemo(() => {
    const active = activePath(menu, matched)
    const module = menu.find((m) => m.id === active[0])
    return { menu, active, modules: menu, module, secondary: module?.children ?? [], crumbs }
  }, [menu, matched, crumbs])
}

/** 底部标签栏中的一项。 */
export interface BottomNavItem {
  id: string
  title: string
  icon?: string
  /** 点击后进入的页面：分组是它下面第一个可以进入的页面。 */
  path: string
  badge?: MenuItem["badge"]
  active: boolean
}

/**
 * 窄屏时底部标签栏的项：第一级菜单，最多 max 项（默认 5）。分组进入它下面第一个可以进入的页面，
 * 没有可以进入的页面的分组不显示。底部标签栏只适合第一级菜单不多的前端，例如用户端。
 */
export function bottomNav(menu: readonly MenuItem[], active: readonly string[], max = 5): BottomNavItem[] {
  const out: BottomNavItem[] = []
  for (const item of menu) {
    if (out.length >= max) break
    const path = item.children ? firstPage(item.children) : item.path
    if (!path) continue
    out.push({
      id: item.id,
      title: item.title,
      icon: item.icon,
      path,
      badge: item.badge,
      active: active[0] === item.id,
    })
  }
  return out
}

/** 当前的底部标签栏，见 bottomNav。 */
export function useBottomNav(max = 5): BottomNavItem[] {
  const { menu, active } = useNavigation()
  return useMemo(() => bottomNav(menu, active, max), [menu, active, max])
}

/**
 * 登记 routeId 对应菜单项的徽标，例如未读通知数；组件卸载、value 为 0 或空时去掉。
 *
 *   useMenuBadge("/_app/notifications", unread)
 */
export function useMenuBadge(routeId: string, value: Badge | undefined | null): void {
  useBadgeOf(useApp(), routeId, value)
}

// ———————————————————————————— 插槽 ————————————————————————————

/** 约定的插槽。布局决定每个插槽放在哪里，可以只放一部分。 */
export type SlotName =
  | "brand" // 品牌：标志和名称
  | "nav" // 主导航，通常由布局自己按 useNavigation 渲染
  | "subnav" // 二级导航
  | "actions" // 顶栏操作，随页面变化
  | "search" // 搜索与命令面板入口
  | "user" // 用户菜单
  | "tenant" // 租户切换
  | "tabs" // 标签栏（多标签）
  | "aside" // 侧边面板，随页面变化
  | "footer" // 页脚

// 插槽用 portal 实现：Slot 登记一个 DOM 元素，Fill 把内容 portal 进去。内容随页面自己的渲染更新，
// 状态只在插槽或内容出现、消失时变化，不会因为渲染而反复通知。
type Slots = Store<{ elements: ReadonlyMap<string, HTMLElement>; counts: ReadonlyMap<string, number> }>

const slotsOf = perOwner<Slots>(() => createStore({ elements: new Map(), counts: new Map() }))

function useSlots(): Slots {
  return slotsOf(useApp())
}

/** 插槽中是否有内容：布局用它决定是否留出位置，例如没有侧边面板时不显示侧边栏。 */
export function useSlotFilled(name: SlotName | (string & {})): boolean {
  const store = useSlots()
  return useSyncExternalStore(store.subscribe, () => (store.get().counts.get(name) ?? 0) > 0)
}

/** 布局中放置插槽。内容按放入的顺序排列；外层是 display: contents 的元素，不影响布局。 */
export function Slot({ name }: { name: SlotName | (string & {}) }): ReactNode {
  const store = useSlots()
  const ref = useCallback(
    (el: HTMLDivElement | null) => {
      const { elements, counts } = store.get()
      const next = new Map(elements)
      if (el) next.set(name, el)
      else if (next.get(name)) next.delete(name)
      store.set({ elements: next, counts })
    },
    [store, name],
  )
  return <div ref={ref} data-slot={name} style={{ display: "contents" }} />
}

/**
 * 往插槽放内容，组件卸载时（例如离开页面）移除。同一个插槽可以有多份内容，按放入的顺序排列。
 *
 *   <Fill name="actions"><Button>写文章</Button></Fill>
 */
export function Fill({ name, children }: { name: SlotName | (string & {}); children: ReactNode }): ReactNode {
  const store = useSlots()
  const element = useSyncExternalStore(store.subscribe, () => store.get().elements.get(name))
  useLayoutEffect(() => {
    const change = (d: number) => {
      const { elements, counts } = store.get()
      const next = new Map(counts)
      next.set(name, (next.get(name) ?? 0) + d)
      store.set({ elements, counts: next })
    }
    change(1)
    return () => change(-1)
  }, [store, name])
  return element ? createPortal(children, element) : null
}

// ———————————————————————————— 布局状态 ————————————————————————————

/** 布局状态。navOpen 之外的字段按用户保存。 */
export interface LayoutState {
  /** 侧栏收起为图标。 */
  collapsed: boolean
  /** 二级面板的宽度（像素）；没有调整过时为空，用布局的默认宽度。 */
  secondaryWidth?: number
  /** 侧边面板打开。 */
  aside: boolean
  /** 内容最大化：隐藏导航和顶栏。 */
  maximized: boolean
  /** 用户选择的布局变体，见 useLayoutVariant。 */
  variant?: string
  /** 密度。 */
  density?: "compact" | "comfortable"
  /** 窄屏时导航抽屉打开。只在当前页面有效，不保存。 */
  navOpen: boolean
}

const defaultLayout: LayoutState = { collapsed: false, aside: false, maximized: false, navOpen: false }

const layoutOf = perOwner(() => createStore<{ key: string; state: LayoutState }>({ key: "", state: defaultLayout }))

/** 布局状态和修改它的函数。保存在 app.storage 中，按账号区分：换个账号登录各是各的。 */
export function useLayoutState(): [LayoutState, (changes: Partial<LayoutState>) => void] {
  const app = useApp()
  const store = layoutOf(app)
  const account = useSyncExternalStore(app.session.subscribe, () => app.session.identity()?.account ?? "")
  const key = `layout:${account}`
  const current = useSyncExternalStore(store.subscribe, store.get)
  const state = useMemo<LayoutState>(() => {
    if (current.key === key) return current.state
    const { navOpen: _, ...saved } = app.storage.get<Partial<LayoutState>>(key) ?? {}
    return { ...defaultLayout, ...saved }
  }, [current, key, app.storage])
  const set = useMemo(
    () => (changes: Partial<LayoutState>) => {
      const next = { ...state, ...changes }
      const { navOpen: _, ...saved } = next
      app.storage.set(key, saved)
      store.set({ key, state: next })
    },
    [state, key, app.storage, store],
  )
  return [state, set]
}

/**
 * 用户可以在几种布局之间切换时（例如侧栏和顶部导航），布局路由用它决定当前用哪一种：
 * 用户选过并且在 allowed 中时用用户的选择，否则用 allowed[0]。setVariant 保存用户的选择。
 *
 *   const [variant, setVariant] = useLayoutVariant(["side", "top"])
 */
export function useLayoutVariant<V extends string>(allowed: readonly [V, ...V[]]): [V, (variant: V) => void] {
  const [state, set] = useLayoutState()
  const variant = allowed.includes(state.variant as V) ? (state.variant as V) : allowed[0]
  return [variant, (v: V) => set({ variant: v })]
}

/** 是否是窄屏（默认宽度小于 768px）：布局据此把主导航收进抽屉或底部标签栏。 */
export function useNarrow(query = "(max-width: 767px)"): boolean {
  return useSyncExternalStore(
    (onChange) => {
      const mql = window.matchMedia?.(query)
      mql?.addEventListener("change", onChange)
      return () => mql?.removeEventListener("change", onChange)
    },
    () => window.matchMedia?.(query).matches ?? false,
    () => false,
  )
}
