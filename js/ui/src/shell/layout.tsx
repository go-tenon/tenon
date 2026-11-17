// 布局约定：一个布局读取导航模型和布局状态，把插槽放到自己的位置上；页面只往插槽中放内容，不关心当前是哪一种布局。
// 侧栏、顶部导航等具体的布局由 @go-tenon/ui 按这个约定实现。

import { useMatches } from "@tanstack/react-router"
import { type ReactNode, useCallback, useLayoutEffect, useMemo, useSyncExternalStore } from "react"
import { createPortal } from "react-dom"
import { useApp, useBreadcrumbs, useMenu } from "./app"
import { type Badge, useBadgeOf } from "./badges"
import type { Crumb, MenuItem } from "./menu"
import { noAccount } from "./snapshot"
import { createStore, perOwner, type Store } from "./store"

// ———————————————————————————— 导航模型 ————————————————————————————

/** 布局需要的导航信息。 */
export interface Navigation {
  /** 按权限过滤后的菜单树。 */
  menu: MenuItem[]
  /** 当前位置在菜单中的路径：从第一级到最深一级的菜单项 id，用于高亮当前项、展开所在的分组。 */
  active: string[]
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
  return useMemo(() => ({ menu, active: activePath(menu, matched), crumbs }), [menu, matched, crumbs])
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

/**
 * AppLayout 中的插槽：页面往里面放随页面变化的内容。布局自带的部分（标志、搜索、租户切换、用户菜单）
 * 是 AppLayout 的参数，不是插槽。自己写的布局可以用其他名字。
 */
export type SlotName =
  | "actions" // 顶栏中的操作
  | "aside" // 右侧面板，有内容时才显示
  | "footer" // 内容区底部

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
  const filled = () => (store.get().counts.get(name) ?? 0) > 0
  return useSyncExternalStore(store.subscribe, filled, filled)
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
  const get = () => store.get().elements.get(name)
  const element = useSyncExternalStore(store.subscribe, get, get)
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

/** 布局状态。collapsed、tabs 按账号保存。 */
export interface LayoutState {
  /** 侧栏收起为图标。 */
  collapsed: boolean
  /** 用户打开了多标签（布局开启了多标签时才有意义），默认打开。 */
  tabs: boolean
  /** 窄屏时导航抽屉打开。只在当前页面有效，不保存。 */
  navOpen: boolean
}

const defaultLayout: LayoutState = { collapsed: false, tabs: true, navOpen: false }

const layoutOf = perOwner(() => createStore<{ key: string; state: LayoutState }>({ key: "", state: defaultLayout }))

/** 布局状态和修改它的函数。保存在 app.storage 中，按账号区分：换个账号登录各是各的。 */
export function useLayoutState(): [LayoutState, (changes: Partial<LayoutState>) => void] {
  const app = useApp()
  const store = layoutOf(app)
  const account = useSyncExternalStore(app.session.subscribe, () => app.session.identity()?.account ?? "", noAccount)
  const key = `layout:${account}`
  const current = useSyncExternalStore(store.subscribe, store.get, store.get)
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

/** 是否是窄屏（默认宽度小于 768px）：布局据此把主导航收进抽屉。 */
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
