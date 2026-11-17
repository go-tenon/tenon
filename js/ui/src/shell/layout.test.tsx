// @vitest-environment happy-dom
import { createSession, type Session } from "@go-tenon/client"
import { useQuery } from "@tanstack/react-query"
import { createMemoryHistory, createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { AppRoot, createApp, type RouterContext, type TenancyOptions } from "./app"
import { Fill, Slot, useLayoutState, useMenuBadge, useNarrow, useNavigation, useSlotFilled } from "./layout"
import { enterTenant, useTenant } from "./tenant"

// ———— 一个最小的布局：显示导航模型、布局状态和插槽 ————

function Layout() {
  const nav = useNavigation()
  const [state, setState] = useLayoutState()
  const aside = useSlotFilled("aside")
  const tenant = useTenant()
  const narrow = useNarrow()
  const flat = (items: typeof nav.menu): string[] =>
    items.flatMap((i) => [`${i.title}${i.badge ? `(${i.badge})` : ""}`, ...flat(i.children ?? [])])
  return (
    <div>
      <p data-testid="menu">{flat(nav.menu).join(",")}</p>
      <p data-testid="active">{nav.active.join(" > ")}</p>
      <p data-testid="crumbs">{nav.crumbs.map((c) => c.title).join(" / ")}</p>
      <p data-testid="collapsed">{String(state.collapsed)}</p>
      <p data-testid="narrow">{String(narrow)}</p>
      <p data-testid="tenant">{`${tenant.current ?? ""}:${tenant.tenant?.name ?? ""}:${tenant.tenants.length}`}</p>
      <header data-testid="actions">
        <Slot name="actions" />
      </header>
      {aside && (
        <aside data-testid="aside">
          <Slot name="aside" />
        </aside>
      )}
      <button type="button" onClick={() => setState({ collapsed: !state.collapsed, navOpen: true })}>
        toggle
      </button>
      <button type="button" onClick={() => void tenant.switchTo("globex")}>
        switch
      </button>
      <Fill name="actions">
        <span>帮助</span>
      </Fill>
      <Outlet />
    </div>
  )
}

function ArticlePage() {
  return (
    <>
      <Fill name="actions">
        <button type="button">写文章</button>
      </Fill>
      <Fill name="aside">
        <p>文章说明</p>
      </Fill>
      <p>articles page</p>
    </>
  )
}

function NotificationsPage() {
  useMenuBadge("/_app/notifications", 3)
  return <p>notifications page</p>
}

function Data() {
  const app = useApp2()
  const data = useQuery({
    queryKey: ["data", ...app.session.scope()],
    queryFn: async () => `data of ${app.session.identity()?.tenant}`,
  })
  return <p data-testid="data">{data.data}</p>
}

// useApp 在 AppRoot 之内可用；这里取同一个应用。
let current: ReturnType<typeof setup>["app"]
const useApp2 = () => current

function setup(options: { path?: string; tenancy?: TenancyOptions; account?: string } = {}) {
  const root = createRootRouteWithContext<RouterContext>()({ component: Outlet })
  const layout = createRoute({ getParentRoute: () => root, id: "_app", component: Layout })
  const home = createRoute({ getParentRoute: () => layout, path: "/", component: Data })
  const articles = createRoute({
    getParentRoute: () => layout,
    path: "/articles",
    staticData: { title: "文章", order: 1 },
  })
  const articleList = createRoute({
    getParentRoute: () => articles,
    path: "/",
    staticData: { title: "全部文章" },
    component: ArticlePage,
  })
  const article = createRoute({
    getParentRoute: () => articles,
    path: "/$id",
    staticData: { title: "文章详情" },
    component: () => <p>article page</p>,
  })
  const notifications = createRoute({
    getParentRoute: () => layout,
    path: "/notifications",
    staticData: { title: "通知", order: 2 },
    component: NotificationsPage,
  })
  const globex = createRoute({ getParentRoute: () => root, path: "/t/$tenant/", component: () => <p>tenant home</p> })
  const routeTree = root.addChildren([
    layout.addChildren([home, articles.addChildren([articleList, article]), notifications]),
    globex,
  ])
  const session: Session = createSession({ load: async () => ({ account: "u1" }) })
  const app = createApp({
    id: "layout-test",
    routeTree,
    session,
    tenancy: options.tenancy,
    router: { history: createMemoryHistory({ initialEntries: [options.path ?? "/"] }) },
  })
  current = app
  return { app, session }
}

async function signIn(session: Session, account = "u1", tenant = "acme") {
  await act(() => session.signIn({ account, tenant }))
}

afterEach(() => {
  cleanup()
  localStorage.clear()
})

const text = (id: string) => screen.getByTestId(id).textContent

describe("导航模型", () => {
  it("当前位置和面包屑；详情页高亮所在的分组", async () => {
    const { app } = setup({ path: "/articles" })
    render(<AppRoot app={app} />)
    await screen.findByText("articles page")
    expect(text("menu")).toBe("文章,全部文章,通知")
    expect(text("active")).toBe("/_app/articles > /_app/articles/")
    expect(text("crumbs")).toBe("文章 / 全部文章")

    await act(() => app.router.navigate({ href: "/articles/42" }))
    await screen.findByText("article page")
    expect(text("active")).toBe("/_app/articles")
    expect(text("crumbs")).toBe("文章 / 文章详情")
  })

  it("页面登记的徽标出现在菜单项上，离开页面后去掉", async () => {
    const { app } = setup({ path: "/notifications" })
    render(<AppRoot app={app} />)
    await screen.findByText("notifications page")
    expect(text("menu")).toBe("文章,全部文章,通知(3)")
    await act(() => app.router.navigate({ href: "/articles" }))
    await screen.findByText("articles page")
    expect(text("menu")).toBe("文章,全部文章,通知")
  })
})

describe("插槽", () => {
  it("页面往插槽放内容，按放入的顺序排列，离开页面时移除", async () => {
    const { app } = setup({ path: "/articles" })
    render(<AppRoot app={app} />)
    await screen.findByText("articles page")
    expect(text("actions")).toBe("帮助写文章")
    expect(text("aside")).toBe("文章说明")
    await act(() => app.router.navigate({ href: "/notifications" }))
    await screen.findByText("notifications page")
    expect(text("actions")).toBe("帮助")
    expect(screen.queryByTestId("aside")).toBeNull()
  })
})

describe("布局状态", () => {
  it("按账号保存；navOpen 不保存", async () => {
    const { app, session } = setup({ path: "/articles" })
    render(<AppRoot app={app} />)
    await signIn(session, "u1")
    await screen.findByText("articles page")
    expect(text("collapsed")).toBe("false")
    await act(async () => fireEvent.click(screen.getByText("toggle")))
    expect(text("collapsed")).toBe("true")
    expect(JSON.parse(localStorage.getItem("tenon:layout-test:layout:u1") ?? "{}")).toEqual({
      collapsed: true,
    })

    await signIn(session, "u2") // 换个账号：各是各的
    expect(text("collapsed")).toBe("false")
    await signIn(session, "u1")
    expect(text("collapsed")).toBe("true")
  })

  it("useNarrow 按媒体查询判断窄屏", async () => {
    const spy = vi
      .spyOn(window, "matchMedia")
      .mockImplementation(
        (q) => ({ matches: q === "(max-width: 767px)", addEventListener() {}, removeEventListener() {} }) as never,
      )
    const { app } = setup()
    render(<AppRoot app={app} />)
    await screen.findByTestId("narrow")
    expect(text("narrow")).toBe("true")
    spy.mockRestore()
  })
})

describe("租户", () => {
  const tenants = async () => [
    { id: "acme", name: "Acme" },
    { id: "globex", name: "Globex" },
  ]

  it("请求头方式：切换后留在当前页面，按新租户重新读取数据", async () => {
    const { app, session } = setup({ tenancy: { tenants } })
    render(<AppRoot app={app} />)
    await signIn(session)
    expect(await screen.findByText("data of acme")).toBeTruthy()
    await vi.waitFor(() => expect(text("tenant")).toBe("acme:Acme:2"))
    await act(async () => fireEvent.click(screen.getByText("switch")))
    expect(await screen.findByText("data of globex")).toBeTruthy()
    expect(app.router.state.location.pathname).toBe("/")
    await vi.waitFor(() => expect(text("tenant")).toBe("globex:Globex:2"))
  })

  it("路径方式：切换后去新租户的首页", async () => {
    const { app, session } = setup({ tenancy: { tenants, home: (id) => `/t/${id}/` } })
    render(<AppRoot app={app} />)
    await signIn(session)
    await screen.findByTestId("tenant")
    await act(async () => fireEvent.click(screen.getByText("switch")))
    expect(await screen.findByText("tenant home")).toBeTruthy()
    expect(app.router.state.location.pathname).toBe("/t/globex")
    expect(session.identity()?.tenant).toBe("globex")
  })

  it("子域名方式：整页跳到新租户的地址", async () => {
    const assign = vi.spyOn(window.location, "assign").mockImplementation(() => {})
    const { app, session } = setup({ tenancy: { tenants, home: (id) => `https://${id}.example.com/` } })
    render(<AppRoot app={app} />)
    await signIn(session)
    await screen.findByTestId("tenant")
    await act(async () => fireEvent.click(screen.getByText("switch")))
    expect(assign).toHaveBeenCalledWith("https://globex.example.com/")
    assign.mockRestore()
  })

  it("enterTenant：直接打开另一个租户的地址时切换身份，身份不变时不切换", async () => {
    const { app, session } = setup()
    await signIn(session)
    const listener = vi.fn()
    session.subscribe(listener)
    enterTenant(app, "acme")
    expect(listener).not.toHaveBeenCalled()
    enterTenant(app, "globex")
    expect(session.identity()).toEqual({ account: "u1", tenant: "globex" })
    expect(listener).toHaveBeenCalledTimes(1)
  })
})
