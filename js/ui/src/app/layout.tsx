// 布局约定：一个布局读取导航模型和布局状态，把插槽放到自己的位置上；页面只往插槽中放内容，不关心当前是哪一种布局。
// FrameLayout、RailLayout 等具体的布局（blocks/）按这个约定实现。

import { useMatches } from "@tanstack/react-router"
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useLayoutEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from "react"
import { createPortal } from "react-dom"
import { useApp, useBreadcrumbs, useMenu, useOptionalApp } from "./app"
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
 * 内置的插槽。布局放置 actions、aside、footer，页面往里面放随页面变化的内容；UserMenu 放置 user-menu，
 * 布局往里面放自己的开关，例如 FrameLayout 的“多标签”。自己写的布局可以用其他名字。
 */
export type SlotName =
  | "actions" // 顶栏中的操作
  | "aside" // 右侧面板，有内容时才显示
  | "footer" // 内容区底部
  | "user-menu" // 用户菜单中，配色的下面
  | "nav" // 二级导航：模块自己的面板，例如带数量的收件箱列表；有内容时 RailLayout 用它替换按路由生成的链接

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

// ———————————————————————————— 版面 ————————————————————————————

/** 当前页面是否铺满内容区：匹配到的路由中有一个的 staticData.fullBleed 是 true。 */
export function useFullBleed(): boolean {
  return useMatches({ select: (matches) => matches.some((m) => m.staticData?.fullBleed === true) })
}

// 要求收起二级导航的组件数：大于 0 时布局临时收起二级导航，保存的收起状态保持原样。
const collapseRequestsOf = perOwner(() => createStore(0))

/**
 * when 为 true 时临时收起布局的二级导航，变回 false 或组件卸载时恢复；保存的收起状态保持不变。
 * 例如窗口不够宽时打开详情面板，先收起二级导航给内容让出位置：
 *
 *   useCollapseNav(detailOpen && !wide)
 */
export function useCollapseNav(when: boolean): void {
  const store = collapseRequestsOf(useApp())
  useLayoutEffect(() => {
    if (!when) return
    store.set(store.get() + 1)
    return () => store.set(store.get() - 1)
  }, [store, when])
}

/** 是否有组件用 useCollapseNav 要求临时收起二级导航：布局据此收起。 */
export function useNavCollapseRequested(): boolean {
  const store = collapseRequestsOf(useApp())
  const requested = () => store.get() > 0
  return useSyncExternalStore(store.subscribe, requested, requested)
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
const sectionsOf = perOwner(() => createStore<Record<string, boolean>>({}))
const emptySectionStore = createStore<Record<string, boolean>>({})
const noopSubscribe = () => () => {}

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

/**
 * 二级导航分组的展开/收起状态。传入 storeKey 时按账号保存在 app.storage 中，换账号或跨页面各自独立；
 * 不传 storeKey 或不在 AppRoot 内时使用组件内临时状态。
 */
export function useRailSectionState(
  storeKey: string | undefined,
  defaultOpened: boolean,
): [boolean, (opened: boolean) => void] {
  const app = useOptionalApp()
  const [local, setLocal] = useState(defaultOpened)
  const account = useSyncExternalStore(
    app ? app.session.subscribe : noopSubscribe,
    () => app?.session.identity()?.account ?? "",
    noAccount,
  )
  const store = app ? sectionsOf(app) : emptySectionStore
  const map = useSyncExternalStore(store.subscribe, store.get, store.get)
  const fullKey = app && storeKey ? `rail-section:${account}:${storeKey}` : undefined

  const opened = useMemo(() => {
    if (!fullKey || !app) return local
    if (fullKey in map) return map[fullKey]
    const saved = app.storage.get<boolean>(fullKey)
    return typeof saved === "boolean" ? saved : defaultOpened
  }, [fullKey, app, local, map, defaultOpened])

  const setOpened = useCallback(
    (next: boolean) => {
      if (!fullKey || !app) {
        setLocal(next)
        return
      }
      app.storage.set(fullKey, next)
      store.set({ ...store.get(), [fullKey]: next })
    },
    [fullKey, app, store],
  )
  return [opened, setOpened]
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

/** 导航栏的展开状态与切换操作，由 RailLayout 等布局提供给页面使用。 */
export interface NavControl {
  /** 导航栏当前是否展开（窄屏时表示抽屉是否打开）。 */
  open: boolean
  /** 当前是否处于窄屏抽屉模式。 */
  compact: boolean
  /** 展开导航栏（窄屏时打开抽屉）。 */
  openNav(): void
  /** 收起导航栏（窄屏时关闭抽屉）。 */
  closeNav(): void
  /** 切换导航栏展开或收起。 */
  toggleNav(): void
}

const NavControlContext = createContext<NavControl | undefined>(undefined)

/** 向子页面提供当前布局的导航栏控制能力。 */
export function NavControlProvider({ value, children }: { value: NavControl; children: ReactNode }): ReactNode {
  return <NavControlContext.Provider value={value}>{children}</NavControlContext.Provider>
}

/** 读取当前布局的导航栏展开状态与切换操作；不在支持的布局内时返回 undefined。 */
export function useNavControl(): NavControl | undefined {
  return useContext(NavControlContext)
}
