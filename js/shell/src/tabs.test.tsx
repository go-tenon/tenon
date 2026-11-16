// @vitest-environment happy-dom
// 多标签：每个标签有自己的路由上下文（页面只用 Router 的标准 hooks），以及标签栏的操作。
// 这里的场景也检查 TanStack Router 的 router.stores 是否仍是 TabsOutlet 依赖的结构：升级 Router 后失败时先看这里。
import { createSession, type Session } from "@go-tenon/client"
import {
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  Outlet,
  useLocation,
} from "@tanstack/react-router"
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react"
import { createContext, useContext, useState } from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { AppRoot, createApp, type RouterContext } from "./app"
import { useUnsavedChanges } from "./form"
import { confirmDiscard, TabsOutlet, useTab, useTabs, useTabTitle } from "./tabs"
import { switchTenant } from "./tenant"

const seen: string[] = []
const Tick = createContext(0)
let bump: () => void = () => {}
let confirmImpl: (message: string) => boolean = () => true
let max = 10

function Root() {
  const [tick, setTick] = useState(0)
  bump = () => setTick((t) => t + 1)
  return (
    <Tick.Provider value={tick}>
      <Outlet />
    </Tick.Provider>
  )
}

function Layout() {
  const tabs = useTabs()
  return (
    <div>
      <nav data-testid="tabs">
        {tabs.tabs.map((t) => `${t.active ? ">" : ""}${t.pinned ? "*" : ""}${t.title}${t.dirty ? "!" : ""}`).join("|")}
      </nav>
      {tabs.tabs.map((t) => (
        <span key={t.key}>
          <button type="button" onClick={() => void tabs.activate(t.key)}>{`go ${t.title}`}</button>
          <button type="button" onClick={() => void tabs.close(t.key)}>{`close ${t.title}`}</button>
          <button type="button" onClick={() => void tabs.refresh(t.key)}>{`refresh ${t.title}`}</button>
          <button type="button" onClick={() => tabs.pin(t.key)}>{`pin ${t.title}`}</button>
        </span>
      ))}
      <button type="button" onClick={() => tabs.active && void tabs.closeOthers(tabs.active.key)}>
        close others
      </button>
      <TabsOutlet max={max} confirm={(m) => confirmImpl(m)} />
    </div>
  )
}

function UserPage() {
  const tick = useContext(Tick)
  const { id } = user.useParams()
  const { tab } = user.useSearch()
  const { pathname } = useLocation()
  const [n, setN] = useState(0)
  const [dirty, setDirty] = useState(false)
  useUnsavedChanges(dirty)
  useTabTitle(`用户 ${id}`)
  const current = useTab()
  seen.push(`${current.key}|${id}@${pathname}`)
  return (
    <div>
      <p>{`user ${id} n=${n} tab=${tab ?? ""} path=${pathname} tick=${tick} active=${current.active}`}</p>
      <button type="button" onClick={() => setN(n + 1)}>{`inc ${id}`}</button>
      <button type="button" onClick={() => setDirty(true)}>{`edit ${id}`}</button>
    </div>
  )
}

const root = createRootRouteWithContext<RouterContext>()({ component: Root })
const layout = createRoute({ getParentRoute: () => root, id: "_app", component: Layout })
const user = createRoute({
  getParentRoute: () => layout,
  path: "/users/$id",
  validateSearch: (s: Record<string, unknown>) => ({ tab: typeof s.tab === "string" ? s.tab : undefined }),
  staticData: { title: "用户" },
  component: UserPage,
})
const about = createRoute({
  getParentRoute: () => layout,
  path: "/about",
  staticData: { title: "关于" },
  component: () => <p>about page</p>,
})
const result = createRoute({
  getParentRoute: () => layout,
  path: "/result",
  staticData: { title: "结果", tab: false },
  component: () => <p>result page</p>,
})
const routeTree = root.addChildren([layout.addChildren([user, about, result])])

async function setup(path: string) {
  const session: Session = createSession({ refresh: async () => ({ access_token: "a1", expires_at: "" }) })
  await session.signIn({ access_token: "a1", expires_at: "" }, { account: "u1", tenant: "acme" })
  const app = createApp({
    id: "tabs-test",
    routeTree,
    session,
    tenancy: { tenants: async () => [] },
    router: { history: createMemoryHistory({ initialEntries: [path] }) },
  })
  render(<AppRoot app={app} />)
  const go = (href: string) => act(() => app.router.navigate({ href }))
  const click = (name: string) => act(async () => fireEvent.click(screen.getByText(name)))
  const tabs = () => screen.getByTestId("tabs").textContent
  return { app, session, go, click, tabs }
}

/** 一次渲染读到的是不是它所在标签自己的参数和地址：标签的 key 是 /_app/users/$id?id=<id>。 */
function ownParams(entry: string): boolean {
  const [key, rest] = entry.split("|")
  const [id, pathname] = rest.split("@")
  return key.endsWith(`?id=${id}`) && pathname === `/users/${id}`
}

/** 让上下文变化，隐藏的标签也重新渲染（它们以最低的优先级更新）。 */
async function rerenderAll() {
  await act(async () => {
    bump()
    await new Promise((r) => setTimeout(r, 100))
  })
}

beforeEach(() => {
  seen.length = 0
  confirmImpl = () => true
  max = 10
})
afterEach(() => {
  cleanup()
  localStorage.clear()
})

describe("每个标签有自己的路由上下文", () => {
  it("同一个路由开两个标签、切到另一个路由再回来：状态保留，标准 hooks 读到自己的参数", async () => {
    const { go, click, tabs } = await setup("/users/1?tab=info")
    await screen.findByText("user 1 n=0 tab=info path=/users/1 tick=0 active=true")
    await click("inc 1")
    await click("inc 1")

    await go("/users/2")
    await screen.findByText("user 2 n=0 tab= path=/users/2 tick=0 active=true")
    expect(tabs()).toBe("用户 1|>用户 2")

    seen.length = 0
    await rerenderAll()
    // 隐藏的标签重新渲染时读到的也是自己的参数和地址；直接用真实的路由会读成另一个标签的。
    expect(seen.length).toBeGreaterThan(1)
    expect(seen.filter((s) => !ownParams(s))).toEqual([])

    // 当前是另一个路由：隐藏的标签不报错（直接用真实的路由会报 Could not find a nearest match）。
    await go("/about")
    await screen.findByText("about page")
    seen.length = 0
    await rerenderAll()
    expect(seen.filter((s) => !ownParams(s))).toEqual([])

    await click("go 用户 1")
    await screen.findByText("user 1 n=2 tab=info path=/users/1 tick=2 active=true")
    expect(tabs()).toBe(">用户 1|用户 2|关于")
    await go("/users/1?tab=more") // 当前标签照常响应跳转
    await screen.findByText("user 1 n=2 tab=more path=/users/1 tick=2 active=true")
  })

  it("staticData.tab 为 false 的页面不进入标签，照常显示", async () => {
    const { go, tabs } = await setup("/users/1")
    await screen.findByText(/user 1 n=0/)
    await go("/result")
    await screen.findByText("result page")
    expect(tabs()).toBe("用户 1")
  })
})

describe("标签栏", () => {
  it("关闭当前标签时显示右边的；有未保存的修改时先询问，切换标签不询问", async () => {
    const { go, click, tabs } = await setup("/users/1")
    await screen.findByText(/user 1 n=0/)
    await go("/users/2")
    await screen.findByText(/user 2 n=0/)
    await go("/about")
    await screen.findByText("about page")
    await click("go 用户 2")
    await screen.findByText(/user 2 n=0 .* active=true/)
    await click("edit 2")
    expect(tabs()).toBe("用户 1|>用户 2!|关于")

    const confirm = vi.fn(() => false)
    confirmImpl = confirm
    await click("go 用户 1") // 切换标签：页面状态保留，不询问
    await screen.findByText(/user 1 n=0/)
    expect(confirm).not.toHaveBeenCalled()

    await click("close 用户 2") // 关闭有未保存修改的标签：询问，取消时保留
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(tabs()).toBe(">用户 1|用户 2!|关于")
    confirmImpl = () => true
    await click("close 用户 2")
    expect(tabs()).toBe(">用户 1|关于")

    await click("close 用户 1") // 关闭当前标签：显示右边的
    await screen.findByText("about page")
    expect(tabs()).toBe(">关于")
    expect(screen.queryByText("close 关于")).toBeTruthy()
    await click("close 关于") // 唯一的标签不能关闭
    expect(tabs()).toBe(">关于")
  })

  it("刷新重新挂载页面；关闭其他保留固定的标签", async () => {
    const { go, click, tabs } = await setup("/users/1")
    await screen.findByText(/user 1 n=0/)
    await click("inc 1")
    await screen.findByText(/user 1 n=1/)
    await click("refresh 用户 1")
    await screen.findByText(/user 1 n=0/)

    await go("/users/2")
    await screen.findByText(/user 2 n=0/)
    await go("/about")
    await screen.findByText("about page")
    await click("pin 用户 2")
    expect(tabs()).toBe("*用户 2|用户 1|>关于")
    await click("close others")
    expect(tabs()).toBe("*用户 2|>关于")
  })

  it("超过上限时关闭最久没用的标签，有未保存修改的除外", async () => {
    max = 2
    const { go, click, tabs } = await setup("/users/1")
    await screen.findByText(/user 1 n=0/)
    await click("edit 1")
    await go("/users/2")
    await screen.findByText(/user 2 n=0/)
    await go("/about")
    await screen.findByText("about page")
    expect(tabs()).toBe("用户 1!|>关于")
  })

  it("标签按账号保存：重新打开页面后恢复标签栏，页面在打开时才加载", async () => {
    const first = await setup("/users/1")
    await screen.findByText(/user 1 n=0/)
    await first.go("/about")
    await screen.findByText("about page")
    cleanup()

    seen.length = 0
    const again = await setup("/about")
    await screen.findByText("about page")
    expect(again.tabs()).toBe("用户 1|>关于")
    expect(seen).toEqual([]) // 恢复的标签还没有加载
    await again.click("go 用户 1")
    await screen.findByText(/user 1 n=0/)
  })
})

describe("confirmDiscard", () => {
  it("有未保存的修改时，切换租户先询问，取消时不切换", async () => {
    const { app, click } = await setup("/users/1")
    await screen.findByText(/user 1 n=0/)
    await click("edit 1")
    confirmImpl = () => false
    expect(await act(() => switchTenant(app, "globex"))).toBe(false)
    expect(app.session.identity()?.tenant).toBe("acme")
    confirmImpl = () => true
    expect(await confirmDiscard(app)).toBe(true)
  })
})
