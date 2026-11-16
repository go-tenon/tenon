// @vitest-environment happy-dom
import { createSession, type Session } from "@go-tenon/client"
import { AppRoot, createApp, Fill, ForbiddenError, type RouterContext, requireAuth } from "@go-tenon/shell"
import { createMemoryHistory, createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { theme } from "antd"
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest"
import { AdminLayout, type AdminVariant, AuthLayout, RailLayout } from "./layouts"
import { NewVersionNotice, TenantSwitcher, UserMenu } from "./menus"
import { LoginPage, NotFoundPage, RouteError } from "./pages"
import { AdminProvider } from "./provider"

beforeAll(() => {
  // antd 和 ProLayout 用到的浏览器 API，happy-dom 中没有。
  window.matchMedia ??= ((query: string) => ({
    matches: false,
    media: query,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    onchange: null,
    dispatchEvent: () => false,
  })) as never
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as never
})

afterEach(() => {
  cleanup()
  localStorage.clear()
})

const tokens = {
  brand: { light: { colorPrimary: "#e11d48", borderRadius: 4 }, dark: { colorPrimary: "#fb7185", borderRadius: 4 } },
  full: { light: { colorPrimary: "#e11d48" }, dark: { colorPrimary: "#fb7185" } },
  density: "compact",
}

function Primary() {
  const { token } = theme.useToken()
  return <p data-testid="primary">{`${token.colorPrimary} ${token.borderRadius}`}</p>
}

let layout: () => React.ReactNode = () => <AdminLayout title="运营后台" />
let logout = vi.fn(async () => {})

function Root() {
  return (
    <AdminProvider tokens={tokens}>
      <Primary />
      <Outlet />
    </AdminProvider>
  )
}

function AppShell() {
  return (
    <>
      <Fill name="user">
        <UserMenu name="小明" logout={logout} />
      </Fill>
      <Fill name="tenant">
        <TenantSwitcher />
      </Fill>
      {layout()}
    </>
  )
}

const root = createRootRouteWithContext<RouterContext>()({ component: Root })
const appRoute = createRoute({ getParentRoute: () => root, id: "_app", beforeLoad: requireAuth, component: AppShell })
const home = createRoute({
  getParentRoute: () => appRoute,
  path: "/",
  staticData: { title: "首页", icon: "home", order: -1 },
  component: () => <p>home page</p>,
})
const articles = createRoute({
  getParentRoute: () => appRoute,
  path: "/articles",
  staticData: { title: "文章", icon: "file-text" },
  component: () => (
    <>
      <Fill name="actions">
        <button type="button">写文章</button>
      </Fill>
      <p>articles page</p>
    </>
  ),
})
const secret = createRoute({
  getParentRoute: () => appRoute,
  path: "/secret",
  staticData: { title: "机密", menu: false },
  beforeLoad: () => {
    throw new ForbiddenError("secret:read")
  },
  component: () => <p>secret page</p>,
})
const login = createRoute({
  getParentRoute: () => root,
  path: "/login",
  validateSearch: (s: Record<string, unknown>): { redirect?: string } => ({
    redirect: typeof s.redirect === "string" ? s.redirect : undefined,
  }),
  component: LoginRoute,
})
function LoginRoute() {
  const { redirect } = login.useSearch()
  return (
    <AuthLayout title="登录">
      <LoginPage login={(values) => loginFn(values)} redirect={redirect} />
    </AuthLayout>
  )
}
let loginFn = vi.fn(async (_: { email: string; password: string }) => ({ access_token: "a2", expires_at: "" }))
const routeTree = root.addChildren([appRoute.addChildren([home, articles, secret]), login])

async function setup(path: string, options: { signedIn?: boolean } = {}) {
  const session: Session = createSession({
    refresh: async () => {
      if (options.signedIn === false) throw { status: 401, fields: [] }
      return { access_token: "a1", expires_at: "" }
    },
  })
  if (options.signedIn !== false)
    await session.signIn({ access_token: "a1", expires_at: "" }, { account: "u1", tenant: "acme" })
  const app = createApp({
    id: "admin-test",
    routeTree,
    session,
    tenancy: {
      tenants: async () => [
        { id: "acme", name: "Acme" },
        { id: "globex", name: "Globex" },
      ],
    },
    router: {
      history: createMemoryHistory({ initialEntries: [path] }),
      defaultErrorComponent: RouteError,
      defaultNotFoundComponent: NotFoundPage,
    },
  })
  render(<AppRoot app={app} />)
  return { app, session }
}

describe("AdminLayout", () => {
  it.each<AdminVariant>(["side", "top", "mix"])("%s：菜单来自路由，插槽中的内容出现在顶栏", async (variant) => {
    layout = () => <AdminLayout title="运营后台" variant={variant} />
    const { app } = await setup("/articles")
    await screen.findByText("articles page")
    expect(screen.getAllByText("首页").length).toBeGreaterThan(0)
    expect(screen.getAllByText("文章").length).toBeGreaterThan(0)
    expect(screen.queryByText("机密")).toBeNull() // menu: false
    expect(screen.getByText("写文章")).toBeTruthy() // actions 插槽
    expect(screen.getByTestId("user-menu").textContent).toContain("小明") // user 插槽
    expect(await screen.findByText("Acme")).toBeTruthy() // tenant 插槽
    // 菜单中的链接由路由跳转
    const link = screen
      .getAllByText("首页")
      .map((el) => el.closest("a"))
      .find(Boolean) as HTMLAnchorElement
    await act(async () => fireEvent.click(link))
    await screen.findByText("home page")
    expect(app.router.state.location.pathname).toBe("/")
  })

  it("设计令牌：品牌色和圆角交给 antd", async () => {
    layout = () => <AdminLayout title="运营后台" />
    await setup("/")
    await screen.findByText("home page")
    expect(screen.getByTestId("primary").textContent).toBe("#e11d48 4")
  })

  it("多标签：标签栏显示打开的页面，点击切换、关闭", async () => {
    layout = () => <AdminLayout title="运营后台" tabs />
    const { app } = await setup("/")
    await screen.findByText("home page")
    await act(() => app.router.navigate({ href: "/articles" }))
    await screen.findByText("articles page")
    const tabs = () => [...document.querySelectorAll(".ant-tabs-tab")].map((t) => t.textContent)
    expect(tabs()).toEqual(["首页", "文章"])
    await act(async () =>
      fireEvent.click(screen.getAllByText("首页").find((el) => el.closest(".ant-tabs-tab")) as HTMLElement),
    )
    await waitFor(() => expect(app.router.state.location.pathname).toBe("/"))
    const remove = document.querySelectorAll(".ant-tabs-tab-remove")[1] as HTMLElement
    await act(async () => fireEvent.click(remove))
    await waitFor(() => expect(tabs()).toEqual(["首页"]))
  })

  it("缺少权限时在布局之中显示 403", async () => {
    layout = () => <AdminLayout title="运营后台" />
    await setup("/secret")
    await screen.findByText("没有权限")
    expect(screen.getAllByText("文章").length).toBeGreaterThan(0) // 菜单仍在
  })

  it("RailLayout：图标栏是第一级菜单", async () => {
    layout = () => <RailLayout />
    await setup("/articles")
    await screen.findByText("articles page")
    expect(screen.getByLabelText("首页")).toBeTruthy()
    expect(screen.getByLabelText("文章")).toBeTruthy()
  })
})

describe("LoginPage", () => {
  it("登录后回到原来的页面", async () => {
    layout = () => <AdminLayout title="运营后台" />
    loginFn = vi.fn(async () => ({ access_token: "a2", expires_at: "" }))
    const { app } = await setup("/articles", { signedIn: false })
    await screen.findByText("登录", { selector: "span" })
    expect(app.router.state.location.pathname).toBe("/login")
    fireEvent.change(screen.getByLabelText("邮箱"), { target: { value: "ming@example.com" } })
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "secret123" } })
    await act(async () => fireEvent.submit(document.querySelector("form") as HTMLFormElement))
    await screen.findByText("articles page")
    expect(loginFn).toHaveBeenCalledWith({ email: "ming@example.com", password: "secret123" })
  })

  it("接口的错误：字段错误在输入框下面，其他错误在表单上方", async () => {
    loginFn = vi.fn(async () => {
      throw { status: 401, code: "auth.invalid_credentials", detail: "邮箱或密码不对", fields: [] }
    })
    await setup("/login", { signedIn: false })
    fireEvent.change(await screen.findByLabelText("邮箱"), { target: { value: "ming@example.com" } })
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "x" } })
    await act(async () => fireEvent.submit(document.querySelector("form") as HTMLFormElement))
    await screen.findByText("邮箱或密码不对")

    loginFn = vi.fn(async () => {
      throw { status: 422, fields: [{ location: "body.email", message: "邮箱格式不对" }] }
    })
    await act(async () => fireEvent.submit(document.querySelector("form") as HTMLFormElement))
    await screen.findByText("邮箱格式不对")
  })
})

describe("UserMenu 与 TenantSwitcher", () => {
  it("退出：调用登出接口，清除会话，回到登录页", async () => {
    layout = () => <AdminLayout title="运营后台" />
    logout = vi.fn(async () => {})
    const { app } = await setup("/")
    await screen.findByText("home page")
    await act(async () => fireEvent.mouseEnter(screen.getByTestId("user-menu")))
    await act(async () => fireEvent.click(await screen.findByText("退出登录")))
    await waitFor(() => expect(app.router.state.location.pathname).toBe("/login"))
    expect(logout).toHaveBeenCalledTimes(1)
  })

  it("颜色模式保存在本地存储中", async () => {
    layout = () => <AdminLayout title="运营后台" />
    await setup("/")
    await screen.findByText("home page")
    await act(async () => fireEvent.mouseEnter(screen.getByTestId("user-menu")))
    await act(async () => fireEvent.click(await screen.findByText("暗色")))
    expect(localStorage.getItem("tenon:admin-test:color-mode")).toBe('"dark"')
    expect(document.documentElement.classList.contains("dark")).toBe(true)
    // 暗色时用令牌的暗色品牌色（#fb7185）作种子，antd 的暗色算法按暗色背景微调。
    await waitFor(() => expect(screen.getByTestId("primary").textContent).not.toBe("#e11d48 4"))
    expect(screen.getByTestId("primary").textContent).toMatch(/ 4$/)
  })

  it("切换租户", async () => {
    layout = () => <AdminLayout title="运营后台" />
    const { session } = await setup("/")
    await screen.findByText("home page")
    await act(async () => fireEvent.mouseEnter(await screen.findByTestId("tenant-switcher")))
    await act(async () => fireEvent.click(await screen.findByText("Globex")))
    await waitFor(() => expect(session.identity()?.tenant).toBe("globex"))
  })
})

describe("NewVersionNotice", () => {
  it("有新版本时提示刷新", async () => {
    const fetch = vi.fn(async () => ({ ok: true, json: async () => ({ version: "v2" }) }) as Response)
    const app = createApp({
      id: "nv",
      routeTree: createRootRouteWithContext<RouterContext>()({
        component: () => (
          <AdminProvider>
            <NewVersionNotice current="v1" url="/version.json" fetch={fetch} />
          </AdminProvider>
        ),
      }),
      router: { history: createMemoryHistory({ initialEntries: ["/"] }) },
    })
    render(<AppRoot app={app} />)
    await screen.findByText("有新版本")
    expect(fetch).toHaveBeenCalledWith("/version.json", { cache: "no-store" })
  })
})
