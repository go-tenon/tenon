// 多标签的状态与规则：纯函数，不依赖 React 和路由。

/** 一个标签。 */
export interface TabRecord {
  /** 标签的标识：页面路由 + 路径参数，例如 /_app/users/$id?id=1。 */
  key: string
  routeId: string
  params: Record<string, string>
  /** 标签最后的地址，包括查询参数：切回这个标签时回到这里。 */
  href: string
  /** 页面路由的标题；页面用 useTabTitle 设置的标题优先。 */
  title?: string
  /** useTabTitle 设置的标题。 */
  customTitle?: string
  icon?: string
  pinned: boolean
}

/** 标签栏的状态。 */
export interface TabsState {
  tabs: TabRecord[]
  /** 当前标签的 key。 */
  active: string
  /** 最近使用的顺序：最久没用的在前。 */
  used: string[]
}

/** 没有任何标签的状态。 */
export const emptyTabs: TabsState = { tabs: [], active: "", used: [] }

/** 标签的标识：路由 + 按名称排序的路径参数。查询参数不算在内。 */
export function tabKey(routeId: string, params: Record<string, unknown> = {}): string {
  const entries = Object.entries(params)
    .filter(([, v]) => v !== undefined && v !== "")
    .sort(([a], [b]) => a.localeCompare(b))
  if (entries.length === 0) return routeId
  return `${routeId}?${entries.map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`).join("&")}`
}

const touch = (used: string[], key: string) => [...used.filter((k) => k !== key), key]

/** 固定的标签在前，其余保持原来的顺序。 */
const pinnedFirst = (tabs: TabRecord[]) => [...tabs.filter((t) => t.pinned), ...tabs.filter((t) => !t.pinned)]

function keep(state: TabsState, tabs: TabRecord[], active: string): TabsState {
  const keys = new Set(tabs.map((t) => t.key))
  return { tabs, active, used: state.used.filter((k) => keys.has(k)) }
}

/** 打开（或回到）一个标签：已经打开时更新它的地址和路由的标题，否则加在最后。 */
export function openTab(state: TabsState, tab: Omit<TabRecord, "pinned"> & { pinned?: boolean }): TabsState {
  const existing = state.tabs.find((t) => t.key === tab.key)
  const next: TabRecord = {
    ...existing,
    ...tab,
    customTitle: existing?.customTitle,
    pinned: existing?.pinned ?? tab.pinned ?? false,
  }
  return {
    tabs: existing ? state.tabs.map((t) => (t.key === tab.key ? next : t)) : [...state.tabs, next],
    active: tab.key,
    used: touch(state.used, tab.key),
  }
}

/** 关闭的结果：新的状态，关闭了哪些标签，关闭了当前标签时接着显示哪一个。 */
export interface CloseResult {
  state: TabsState
  closed: string[]
  next?: TabRecord
}

/**
 * 关闭一个标签（固定的也可以单独关闭）。唯一的标签不能关闭。关闭当前标签时接着显示右边的，没有时显示左边的。
 */
export function closeTab(state: TabsState, key: string): CloseResult {
  const index = state.tabs.findIndex((t) => t.key === key)
  if (index < 0 || state.tabs.length <= 1) return { state, closed: [] }
  const tabs = state.tabs.filter((t) => t.key !== key)
  const next = state.active === key ? (tabs[index] ?? tabs[index - 1]) : undefined
  return { state: keep(state, tabs, next?.key ?? state.active), closed: [key], next }
}

/**
 * 按条件关闭多个标签：keep 返回 false 的标签被关闭，固定的标签总是保留。
 * 当前标签被关闭时接着显示 prefer（保留时），否则显示剩下的第一个；全部关闭时 next 为空。
 */
export function closeTabs(
  state: TabsState,
  shouldKeep: (tab: TabRecord, index: number) => boolean,
  prefer?: string,
): CloseResult {
  const tabs = state.tabs.filter((t, i) => t.pinned || shouldKeep(t, i))
  const keys = new Set(tabs.map((t) => t.key))
  const closed = state.tabs.filter((t) => !keys.has(t.key)).map((t) => t.key)
  if (closed.length === 0) return { state, closed }
  let next: TabRecord | undefined
  let active = state.active
  if (!keys.has(state.active)) {
    next = tabs.find((t) => t.key === prefer) ?? tabs[0]
    active = next?.key ?? ""
  }
  return { state: keep(state, tabs, active), closed, next }
}

/** 固定或取消固定：固定的标签排在最左边。 */
export function pinTab(state: TabsState, key: string, pinned: boolean): TabsState {
  if (!state.tabs.some((t) => t.key === key)) return state
  return { ...state, tabs: pinnedFirst(state.tabs.map((t) => (t.key === key ? { ...t, pinned } : t))) }
}

/** 把 from 移到 to 的位置：只在同一组（固定的、不固定的）中移动。 */
export function moveTab(state: TabsState, from: string, to: string): TabsState {
  const i = state.tabs.findIndex((t) => t.key === from)
  const j = state.tabs.findIndex((t) => t.key === to)
  if (i < 0 || j < 0 || i === j || state.tabs[i].pinned !== state.tabs[j].pinned) return state
  const tabs = [...state.tabs]
  const [tab] = tabs.splice(i, 1)
  tabs.splice(j, 0, tab)
  return { ...state, tabs }
}

/** 把一个页面作为固定的标签放在最前面（已经打开时改为固定），不改变当前标签。 */
export function pinFirst(state: TabsState, tab: Omit<TabRecord, "pinned">): TabsState {
  const rest = state.tabs.filter((t) => t.key !== tab.key)
  const existing = state.tabs.find((t) => t.key === tab.key)
  return { ...state, tabs: [{ ...tab, ...existing, pinned: true }, ...rest] }
}

/** 超过 max 个标签时，关闭最久没用的标签；固定的、当前的和 protect 中的（例如有未保存的修改）不关闭。 */
export function evictTabs(state: TabsState, max: number, protect: ReadonlySet<string> = new Set()): CloseResult {
  const overflow = state.tabs.length - max
  if (max <= 0 || overflow <= 0) return { state, closed: [] }
  const pinned = new Set(state.tabs.filter((t) => t.pinned).map((t) => t.key))
  // 恢复后还没有打开过的标签算作最久没用的。
  const order = [...state.tabs.map((t) => t.key).filter((k) => !state.used.includes(k)), ...state.used]
  const candidates = order.filter((k) => k !== state.active && !pinned.has(k) && !protect.has(k))
  const remove = new Set(candidates.slice(0, overflow))
  if (remove.size === 0) return { state, closed: [] }
  return {
    state: keep(
      state,
      state.tabs.filter((t) => !remove.has(t.key)),
      state.active,
    ),
    closed: [...remove],
  }
}
