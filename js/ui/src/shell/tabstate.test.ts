import { describe, expect, it } from "vitest"
import {
  closeTab,
  closeTabs,
  emptyTabs,
  evictTabs,
  moveTab,
  openTab,
  pinFirst,
  pinTab,
  type TabsState,
  tabKey,
  titleTab,
} from "./tabstate"

const tab = (key: string) => ({ key, routeId: key, params: {}, href: `/${key}`, title: key })
const open = (...keys: string[]) => keys.reduce<TabsState>((s, k) => openTab(s, tab(k)), emptyTabs)
const keys = (s: TabsState) => s.tabs.map((t) => (t.pinned ? `*${t.key}` : t.key)).join(",")

describe("tabKey", () => {
  it("路由加按名称排序的路径参数，空参数不算", () => {
    expect(tabKey("/_app/users/$id", { id: "1" })).toBe("/_app/users/$id?id=1")
    expect(tabKey("/r", { b: "2", a: "1", c: "" })).toBe("/r?a=1&b=2")
    expect(tabKey("/r")).toBe("/r")
  })
})

describe("openTab", () => {
  it("新标签加在最后；已经打开的更新地址，保留固定和页面设置的标题", () => {
    let s = open("a", "b")
    s = titleTab(pinTab(s, "b", true), "b", "自定义")
    s = openTab(s, { ...tab("b"), href: "/b?page=2", title: "路由标题" })
    expect(keys(s)).toBe("*b,a")
    expect(s.tabs[0]).toMatchObject({ href: "/b?page=2", title: "路由标题", customTitle: "自定义", pinned: true })
    expect(s.active).toBe("b")
    expect(s.used).toEqual(["a", "b"])
  })
})

describe("closeTab", () => {
  it("关闭当前标签时接着显示右边的，没有时显示左边的", () => {
    let r = closeTab({ ...open("a", "b", "c"), active: "b" }, "b")
    expect([keys(r.state), r.next?.key, r.state.active]).toEqual(["a,c", "c", "c"])
    r = closeTab(open("a", "b", "c"), "c")
    expect([keys(r.state), r.next?.key]).toEqual(["a,b", "b"])
    r = closeTab(open("a", "b", "c"), "a") // 不是当前标签：不跳转
    expect([keys(r.state), r.next, r.state.active]).toEqual(["b,c", undefined, "c"])
  })

  it("固定的标签可以单独关闭；唯一的标签不能关闭", () => {
    expect(closeTab(pinTab(open("a", "b"), "a", true), "a").closed).toEqual(["a"])
    expect(closeTab(open("a"), "a").closed).toEqual([])
  })

  it("pinFirst：固定在最前面，已经打开的改为固定，当前标签不变", () => {
    const home = { key: "h", routeId: "h", params: {}, href: "/" }
    expect(keys(pinFirst(open("a", "b"), home))).toBe("*h,a,b")
    const s = pinFirst(open("a", "h", "b"), home)
    expect([keys(s), s.active]).toEqual(["*h,a,b", "b"])
  })
})

describe("closeTabs", () => {
  it("关闭其他、右边、全部：固定的标签总是保留", () => {
    const s = pinTab({ ...open("a", "b", "c", "d"), active: "d" }, "a", true)
    let r = closeTabs(s, (t) => t.key === "c", "c") // 关闭其他
    expect([keys(r.state), r.next?.key]).toEqual(["*a,c", "c"])
    const index = s.tabs.findIndex((t) => t.key === "b")
    r = closeTabs(s, (_, i) => i <= index, "b") // 关闭右边
    expect([keys(r.state), r.closed.join(","), r.next?.key]).toEqual(["*a,b", "c,d", "b"])
    r = closeTabs(s, () => false) // 全部
    expect([keys(r.state), r.next?.key]).toEqual(["*a", "a"])
  })
})

describe("pinTab 与 moveTab", () => {
  it("固定的标签排在最左边；只在同一组中移动", () => {
    let s = pinTab(open("a", "b", "c"), "c", true)
    expect(keys(s)).toBe("*c,a,b")
    s = moveTab(s, "b", "a")
    expect(keys(s)).toBe("*c,b,a")
    expect(moveTab(s, "a", "c")).toBe(s) // 不能移进固定的一组
    expect(keys(pinTab(s, "c", false))).toBe("c,b,a") // 取消固定后留在原处
  })
})

describe("evictTabs", () => {
  it("超过上限时关闭最久没用的；当前的、固定的、受保护的不关；恢复后没打开过的先关", () => {
    let s = open("a", "b", "c", "d")
    s = openTab(s, tab("a")) // a 最近用过
    s = pinTab(s, "b", true)
    const r = evictTabs(s, 2, new Set(["c"]))
    expect(r.closed).toEqual(["d"]) // b 固定，c 受保护，a 是当前的，只能关 d
    expect(keys(r.state)).toBe("*b,a,c")

    const restored: TabsState = {
      tabs: [tab("x"), tab("y")].map((t) => ({ ...t, pinned: false })),
      active: "",
      used: [],
    }
    const r2 = evictTabs(openTab(restored, tab("z")), 2)
    expect(r2.closed).toEqual(["x"])
  })
})
