// 多标签与保活（按需开启）：布局路由中用 <TabsOutlet /> 代替 <Outlet />，标签栏是 TabBar，由布局放在自己的位置上。
//
// 每个标签有自己的路由上下文：标签是当前显示的页面时读取真实的路由状态，隐藏后读取它最后一次显示时的快照，
// 跳转等操作交给真实的路由。所以页面照常使用 Route.useParams()、useSearch()、useLocation() 等标准 hooks，
// 不需要知道自己是否在标签中。隐藏的标签放在 React 的 <Activity mode="hidden"> 中：状态保留，effect 暂停。
//
// 这依赖 TanStack Router 的 router.stores（RouterStores）的结构；升级 TanStack Router 时由 tabs.test.tsx 检查。

import {
  type AnyRouter,
  Match,
  Outlet,
  RouterContextProvider,
  type StaticDataRouteOption,
  useMatch,
  useRouter,
  useRouterState,
} from "@tanstack/react-router"
import {
  Activity,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useSyncExternalStore,
} from "react"
import "../meta"
import { text, useI18n, useLanguage } from "../../i18n/i18n"
import { type App, useApp, useMenu } from "../app"
import type { MenuItem } from "../menu"
import { noAccount } from "../snapshot"
import { createStore, perOwner } from "../store"
import { type TabPane, TabPaneContext } from "./context"
import {
  type CloseResult,
  closeTab,
  closeTabs,
  emptyTabs,
  evictTabs,
  moveTab,
  openTab,
  pinFirst,
  pinTab,
  type TabRecord,
  type TabsState,
  tabKey,
} from "./state"

declare module "@tanstack/react-router" {
  interface StaticDataRouteOption {
    /** false 时页面不进入标签，照常显示，例如表单提交后的结果页。 */
    tab?: boolean
  }
}

// ———————————————————————————— 状态 ————————————————————————————

interface TabsModel {
  /** 已经读取的是哪个账号的标签；null 表示还没有读取。 */
  account: string | null
  state: TabsState
  dirty: ReadonlySet<string>
  /** 页面用 useTabTitle 设置的标题。与标签分开保存：页面可能在标签打开之前就设置了标题。 */
  titles: Readonly<Record<string, string>>
  /** 每个标签刷新的次数：变化时重新挂载页面。 */
  refresh: Readonly<Record<string, number>>
  /** 正在关闭的当前标签：跳到下一个标签之前，不要因为地址还没变就重新打开它。 */
  closing?: string
  /** 这个账号还没有保存过标签：开启 home 时固定菜单的第一个页面。 */
  fresh?: boolean
}

interface TabsConfig {
  max: number
  confirm: (message: string) => boolean | Promise<boolean>
}

const tabsOf = perOwner(() =>
  createStore<TabsModel>({ account: null, state: emptyTabs, dirty: new Set(), titles: {}, refresh: {} }),
)
const configOf = perOwner<{ current: TabsConfig }>(() => ({
  current: { max: 10, confirm: (message) => window.confirm(message) },
}))

const storageKey = (account: string) => `tabs:${account}`

function update(app: App, change: (model: TabsModel) => TabsModel): void {
  const store = tabsOf(app)
  const before = store.get()
  const next = change(before)
  if (next === before) return
  store.set(next)
  if (next.account !== null && (next.state.tabs !== before.state.tabs || next.titles !== before.titles)) {
    app.storage.set(
      storageKey(next.account),
      next.state.tabs.map(({ key, routeId, params, href, title, customTitle, icon, pinned }) => ({
        key,
        routeId,
        params,
        href,
        title,
        customTitle: next.titles[key] ?? customTitle,
        icon,
        pinned,
      })),
    )
  }
}

/** 应用关闭（或驱逐）的结果：去掉被关闭的标签的未保存标记和刷新次数，关闭了当前标签时跳到下一个。 */
function apply(app: App, result: CloseResult): void {
  if (result.closed.length === 0) return
  const closed = new Set(result.closed)
  update(app, (m) => ({
    ...m,
    state: result.state,
    dirty: new Set([...m.dirty].filter((k) => !closed.has(k))),
    titles: Object.fromEntries(Object.entries(m.titles).filter(([k]) => !closed.has(k))),
    refresh: Object.fromEntries(Object.entries(m.refresh).filter(([k]) => !closed.has(k))),
    closing: result.next ? m.state.active : m.closing,
  }))
  if (result.next) void app.router.navigate({ href: result.next.href })
}

async function confirmClose(app: App, keys: readonly string[]): Promise<boolean> {
  const { dirty } = tabsOf(app).get()
  const n = keys.filter((k) => dirty.has(k)).length
  if (n === 0) return true
  return configOf(app).current.confirm(
    n === 1 ? text("tenon.tabs.close_dirty_one") : text("tenon.tabs.close_dirty_many", { count: n }),
  )
}

/**
 * 有标签存在未保存的修改时询问是否放弃，返回 true 时继续。退出登录、切换租户之前调用；switchTenant 已经调用它。
 *
 *   if (await confirmDiscard(app)) { await api.auth.logout(); await session.signOut() }
 */
export async function confirmDiscard(app: App, message = text("tenon.unsaved.discard")): Promise<boolean> {
  if (tabsOf(app).get().dirty.size === 0) return true
  return configOf(app).current.confirm(message)
}

// ———————————————————————————— 标签自己的路由 ————————————————————————————

type Readable = { get(): unknown; subscribe(listener: () => void): { unsubscribe(): void } }

const storeNames = ["status", "location", "resolvedLocation", "ids", "matches", "__store"] as const

type Snapshot = { values: Map<string, unknown>; matches: Map<string, unknown> }

const leafKeyOf = (matches: readonly { routeId: string; params: Record<string, unknown> }[]) => {
  const leaf = matches[matches.length - 1]
  return leaf ? tabKey(leaf.routeId, leaf.params) : ""
}

/** 路由空闲、地址已经解析时，记下这一刻的状态。 */
function takeSnapshot(router: AnyRouter): Snapshot | undefined {
  const s = router.stores
  if (s.status.get() !== "idle" || s.location.get().href !== s.resolvedLocation.get()?.href) return undefined
  const values = new Map<string, unknown>(storeNames.map((name) => [name, s[name].get()]))
  return { values, matches: new Map(s.matches.get().map((m) => [m.routeId as string, m as unknown])) }
}

/** 标签自己的路由：标签是当前显示的页面时读取真实的状态，否则读取快照；其他方法交给真实的路由。 */
function paneRouter(router: AnyRouter, key: string, snapshot: () => Snapshot | undefined): AnyRouter {
  const s = router.stores
  const live = () => leafKeyOf(s.matches.get() as never) === key
  const gate = (real: Readable, frozen: () => unknown): Readable => ({
    get: () => (live() ? real.get() : frozen()),
    subscribe: (listener) => real.subscribe(listener),
  })
  const stores: Record<string, unknown> = { ...s }
  for (const name of storeNames) stores[name] = gate(s[name] as Readable, () => snapshot()?.values.get(name))
  const byId = new Map<string, Readable>()
  stores.getMatchStore = (id: string) => {
    let store = byId.get(id)
    if (!store) {
      store = gate(s.getMatchStore(id) as Readable, () => snapshot()?.matches.get(id))
      byId.set(id, store)
    }
    return store
  }
  return Object.assign(Object.create(router), { stores }) as AnyRouter
}

interface Pane {
  key: string
  childRouteId: string
  router: AnyRouter
  snapshot?: Snapshot
  contexts: Map<boolean, TabPane>
}

// ———————————————————————————— TabsOutlet ————————————————————————————

/** TabsOutlet 的选项。 */
export interface TabsOutletProps {
  /** 最多同时打开的标签，默认 10；超出时关闭最久没用的（固定的、有未保存修改的除外）。 */
  max?: number
  /** 询问是否放弃未保存的修改，返回 true 时继续。默认用 window.confirm，可以换成组件库的对话框。 */
  confirm?: (message: string) => boolean | Promise<boolean>
  /** 第一次使用（这个账号没有保存过标签）时，把菜单的第一个页面固定在最前面。FrameLayout 中默认开启。 */
  home?: boolean
}

/**
 * 开启多标签：布局路由中代替 <Outlet />。布局之下的每个页面（路由 + 路径参数）是一个标签，隐藏的标签保留状态。
 * staticData.tab 为 false 的页面不进入标签，照常显示。
 */
export function TabsOutlet({ max = 10, confirm, home = false }: TabsOutletProps): ReactNode {
  const app = useApp()
  const router = useRouter()
  configOf(app).current = { max, confirm: confirm ?? ((message) => window.confirm(message)) }
  const store = tabsOf(app)
  const model = useSyncExternalStore(store.subscribe, store.get, store.get)
  const account = useSyncExternalStore(app.session.subscribe, () => app.session.identity()?.account ?? "", noAccount)
  const parentId = useMatch({ strict: false, select: (m) => String(m.routeId) })
  const matches = useRouterState({ select: (s) => s.matches })
  // 跳转进行中时地址已经变了、匹配结果还是原来的页面：只在路由空闲时记下标签的地址，否则会记到原来的标签上。
  const href = useRouterState({ select: (s) => (s.status === "idle" ? s.location.href : undefined) })

  const at = matches.findIndex((m) => m.routeId === parentId)
  const child = at >= 0 ? matches[at + 1] : undefined
  const leaf = matches[matches.length - 1]
  const data = leaf?.staticData as StaticDataRouteOption | undefined
  const key = child && data?.tab !== false ? leafKeyOf(matches as never) : ""

  // 读取这个账号保存的标签：刷新浏览器后恢复标签栏，页面在打开时才加载。
  useLayoutEffect(() => {
    if (store.get().account === account) return
    const saved = app.storage.get<TabRecord[]>(storageKey(account))
    const tabs = (saved ?? []).filter((t) => t && typeof t.key === "string")
    const titles = Object.fromEntries(tabs.filter((t) => t.customTitle).map((t) => [t.key, t.customTitle as string]))
    store.set({ account, state: { tabs, active: "", used: [] }, dirty: new Set(), titles, refresh: {}, fresh: !saved })
  }, [app, store, account])

  // 第一次使用：菜单读到之后，把第一个页面固定在最前面。
  const menu = useMenu()
  const first = home ? firstItem(menu) : undefined
  useLayoutEffect(() => {
    if (!first || !store.get().fresh || store.get().account !== account) return
    update(app, (m) => ({
      ...m,
      fresh: false,
      state: pinFirst(m.state, {
        key: tabKey(first.id, first.params),
        routeId: first.id,
        params: first.params ?? {},
        href: first.path,
        title: first.title,
        icon: first.icon,
      }),
    }))
  }, [app, store, account, first])

  // 打开（或回到）当前页面的标签，然后按数量上限关闭最久没用的。
  const loaded = model.account === account
  useLayoutEffect(() => {
    if (!loaded || !href) return
    if (!key || !leaf) {
      // 当前页面不进入标签：标签栏中没有当前标签。
      update(app, (m) => (m.state.active ? { ...m, state: { ...m.state, active: "" } } : m))
      return
    }
    if (store.get().closing === key) return
    update(app, (m) => ({
      ...m,
      closing: undefined,
      state: openTab(m.state, {
        key,
        routeId: leaf.routeId,
        params: Object.fromEntries(
          Object.entries(leaf.params as Record<string, unknown>).map(([k, v]) => [k, String(v)]),
        ),
        href,
        title: data?.title,
        icon: data?.icon,
      }),
    }))
    const m = store.get()
    apply(app, evictTabs(m.state, configOf(app).current.max, m.dirty))
  }, [app, store, loaded, key, href, leaf, data])

  // 每次跳转完成时，给当前显示的标签记下快照：标签隐藏后读取它。
  const panes = useRef(new Map<string, Pane>())
  useEffect(
    () =>
      router.subscribe("onResolved", () => {
        const pane = panes.current.get(leafKeyOf(router.stores.matches.get() as never))
        if (pane) pane.snapshot = takeSnapshot(router) ?? pane.snapshot
      }),
    [router],
  )

  // 有未保存的修改时，刷新或关闭浏览器由浏览器提示。
  const anyDirty = model.dirty.size > 0
  useEffect(() => {
    if (!anyDirty) return
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      e.returnValue = ""
    }
    window.addEventListener("beforeunload", onBeforeUnload)
    return () => window.removeEventListener("beforeunload", onBeforeUnload)
  }, [anyDirty])

  if (key && child && !panes.current.has(key)) {
    const pane: Pane = { key, childRouteId: child.routeId as string, router, contexts: new Map() }
    pane.router = paneRouter(router, key, () => pane.snapshot)
    panes.current.set(key, pane)
  }
  const current = panes.current.get(key)
  if (current) current.snapshot = takeSnapshot(router) ?? current.snapshot
  const open = new Set(model.state.tabs.map((t) => t.key))
  for (const k of panes.current.keys()) if (k !== key && !open.has(k)) panes.current.delete(k)

  const context = (pane: Pane, active: boolean): TabPane => {
    let c = pane.contexts.get(active)
    if (!c) {
      c = {
        key: pane.key,
        active,
        setDirty: (dirty) =>
          update(app, (m) => {
            if (m.dirty.has(pane.key) === dirty) return m
            const next = new Set(m.dirty)
            if (dirty) next.add(pane.key)
            else next.delete(pane.key)
            return { ...m, dirty: next }
          }),
      }
      pane.contexts.set(active, c)
    }
    return c
  }

  return (
    <>
      {[...panes.current.values()].map((pane) => {
        const active = pane.key === key
        return (
          <div key={pane.key} data-tab={pane.key} hidden={!active} style={{ display: active ? "contents" : "none" }}>
            <Activity mode={active ? "visible" : "hidden"}>
              <TabPaneContext.Provider value={context(pane, active)}>
                <RouterContextProvider router={pane.router}>
                  <Match key={model.refresh[pane.key] ?? 0} routeId={pane.childRouteId} />
                </RouterContextProvider>
              </TabPaneContext.Provider>
            </Activity>
          </div>
        )
      })}
      {!key && <Outlet />}
    </>
  )
}

/** 菜单中第一个可以进入的页面。 */
function firstItem(items: readonly MenuItem[]): MenuItem | undefined {
  for (const item of items) {
    const found = item.children?.length ? firstItem(item.children) : item
    if (found) return found
  }
  return undefined
}

// ———————————————————————————— 标签栏 ————————————————————————————

/** 标签栏中的一个标签。 */
export interface TabView {
  key: string
  /** 页面设置的标题、页面路由的标题，或者地址。 */
  title: string
  icon?: string
  href: string
  pinned: boolean
  /** 有未保存的修改。 */
  dirty: boolean
  active: boolean
  /** 唯一的标签不能关闭。 */
  closable: boolean
}

/** useTabs 的返回值：标签和操作。外观据此渲染标签栏（右键菜单、拖动排序、关闭按钮）。 */
export interface Tabs {
  tabs: TabView[]
  active?: TabView
  activate(key: string): Promise<void>
  /** 关闭标签；有未保存的修改时先询问。返回是否关闭了。 */
  close(key: string): Promise<boolean>
  /** 关闭其他标签（固定的除外）。 */
  closeOthers(key: string): Promise<boolean>
  /** 关闭右边的标签（固定的除外）。 */
  closeRight(key: string): Promise<boolean>
  /** 关闭全部标签：保留固定的标签；没有固定的标签时保留当前标签。 */
  closeAll(): Promise<boolean>
  /** 刷新：重新挂载这个标签的页面；有未保存的修改时先询问。 */
  refresh(key: string): Promise<boolean>
  pin(key: string): void
  unpin(key: string): void
  /** 把 from 移到 to 的位置（同一组之内），用于拖动排序。 */
  move(from: string, to: string): void
}

function tabsActions(app: App): Omit<Tabs, "tabs" | "active"> {
  const state = () => tabsOf(app).get().state
  const closeWith = async (result: CloseResult) => {
    if (result.closed.length === 0 || !(await confirmClose(app, result.closed))) return false
    apply(app, result)
    return true
  }
  return {
    async activate(key) {
      const tab = state().tabs.find((t) => t.key === key)
      if (tab) await app.router.navigate({ href: tab.href })
    },
    close: (key) => closeWith(closeTab(state(), key)),
    closeOthers: (key) => closeWith(closeTabs(state(), (t) => t.key === key, key)),
    closeRight(key) {
      const index = state().tabs.findIndex((t) => t.key === key)
      return closeWith(closeTabs(state(), (_, i) => i <= index, key))
    },
    closeAll() {
      const s = state()
      const anyPinned = s.tabs.some((t) => t.pinned)
      return closeWith(closeTabs(s, (t) => !anyPinned && t.key === s.active))
    },
    async refresh(key) {
      if (!(await confirmClose(app, [key]))) return false
      update(app, (m) => ({
        ...m,
        dirty: new Set([...m.dirty].filter((k) => k !== key)),
        refresh: { ...m.refresh, [key]: (m.refresh[key] ?? 0) + 1 },
      }))
      return true
    },
    pin: (key) => update(app, (m) => ({ ...m, state: pinTab(m.state, key, true) })),
    unpin: (key) => update(app, (m) => ({ ...m, state: pinTab(m.state, key, false) })),
    move: (from, to) => update(app, (m) => ({ ...m, state: moveTab(m.state, from, to) })),
  }
}

/** 标签栏：全部标签和操作。 */
export function useTabs(): Tabs {
  const app = useApp()
  const store = tabsOf(app)
  const model = useSyncExternalStore(store.subscribe, store.get, store.get)
  const actions = useMemo(() => tabsActions(app), [app])
  const i18n = useI18n()
  const language = useLanguage()
  // biome-ignore lint/correctness/useExhaustiveDependencies: 切换语言时重新翻译路由的标题
  return useMemo(() => {
    const { tabs, active } = model.state
    const views = tabs.map<TabView>((t) => ({
      key: t.key,
      // 路由的标题可以是消息 ID，按当前语言翻译；页面用 useTabTitle 设置的标题已经是给人看的文字
      title: model.titles[t.key] ?? t.customTitle ?? (t.title ? i18n.t(t.title) : t.href),
      icon: t.icon,
      href: t.href,
      pinned: t.pinned,
      dirty: model.dirty.has(t.key),
      active: t.key === active,
      closable: tabs.length > 1,
    }))
    return { tabs: views, active: views.find((t) => t.active), ...actions }
  }, [model, actions, i18n, language])
}

// ———————————————————————————— 页面中 ————————————————————————————

/** 页面所在的标签；不在标签中时 key 为空。 */
export interface CurrentTab {
  key: string
  /** 是否是当前显示的标签；不在标签中时总是 true。 */
  active: boolean
  refresh(): Promise<boolean>
  close(): Promise<boolean>
}

/** 页面所在的标签。 */
export function useTab(): CurrentTab {
  const pane = useContext(TabPaneContext)
  const app = useApp()
  const actions = useMemo(() => tabsActions(app), [app])
  const key = pane?.key ?? ""
  const refresh = useCallback(() => (key ? actions.refresh(key) : Promise.resolve(false)), [actions, key])
  const close = useCallback(() => (key ? actions.close(key) : Promise.resolve(false)), [actions, key])
  return { key, active: pane?.active ?? true, refresh, close }
}

/** 页面是否是当前显示的标签，例如隐藏时暂停轮询。不在标签中时总是 true。 */
export function useTabActive(): boolean {
  return useContext(TabPaneContext)?.active ?? true
}

/** 设置页面所在标签的标题，例如“文章：你好”；不在标签中时不起作用。 */
export function useTabTitle(title: string | undefined): void {
  const pane = useContext(TabPaneContext)
  const app = useApp()
  const key = pane?.key
  useEffect(() => {
    if (!key) return
    update(app, (m) => {
      if (m.titles[key] === title) return m
      const titles = { ...m.titles }
      if (title === undefined) delete titles[key]
      else titles[key] = title
      return { ...m, titles }
    })
  }, [app, key, title])
}

/** 标签的快捷键，默认 Alt+W 关闭当前标签，Alt+← / Alt+→ 切换到左边、右边的标签。 */
export interface TabShortcuts {
  close?: string
  previous?: string
  next?: string
}

const matchesKey = (e: KeyboardEvent, combo: string) => {
  const parts = combo.toLowerCase().split("+")
  const key = parts[parts.length - 1]
  const want = (mod: string) => parts.includes(mod)
  return (
    e.key.toLowerCase() === key &&
    e.altKey === want("alt") &&
    e.ctrlKey === want("ctrl") &&
    e.metaKey === want("meta") &&
    e.shiftKey === want("shift")
  )
}

/** 开启标签的快捷键，通常在布局中调用一次。 */
export function useTabShortcuts(shortcuts: TabShortcuts = {}): void {
  const tabs = useTabs()
  const ref = useRef(tabs)
  ref.current = tabs
  const { close = "Alt+w", previous = "Alt+ArrowLeft", next = "Alt+ArrowRight" } = shortcuts
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const { tabs: list, active } = ref.current
      if (!active) return
      const index = list.findIndex((t) => t.key === active.key)
      let handled = true
      if (matchesKey(e, close)) void ref.current.close(active.key)
      else if (matchesKey(e, previous) && index > 0) void ref.current.activate(list[index - 1].key)
      else if (matchesKey(e, next) && index < list.length - 1) void ref.current.activate(list[index + 1].key)
      else handled = false
      if (handled) e.preventDefault()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [close, previous, next])
}
