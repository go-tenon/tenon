// @vitest-environment happy-dom
import { createSession, type SessionBody } from "@go-tenon/client"
import {
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  type ErrorComponentProps,
  Outlet,
} from "@tanstack/react-router"
import { act, cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import {
  AppRoot,
  createApp,
  isForbidden,
  type RouterContext,
  requireAuth,
  useBreadcrumbs,
  useCan,
  useMenu,
} from "./app"

const body = (access: string): SessionBody => ({ access_token: access, expires_at: "2026-11-11T08:10:00Z" })

function Layout() {
  const menu = useMenu()
  const crumbs = useBreadcrumbs()
  const can = useCan()
  return (
    <div>
      <nav data-testid="menu">{menu.map((m) => m.title).join(",")}</nav>
      <p data-testid="crumbs">{crumbs.map((c) => c.title).join(" / ")}</p>
      <p data-testid="can">{String(can("users:manage"))}</p>
      <Outlet />
    </div>
  )
}

function Problem({ error }: ErrorComponentProps) {
  return <p>{isForbidden(error) ? `403 ${error.permission}` : "error"}</p>
}

/** 根 → 登录页、受保护布局 _app → 首页、用户管理（需要权限）。 */
function setup(options: { signedIn: boolean; permissions?: string[] }) {
  const root = createRootRouteWithContext<RouterContext>()({ component: Outlet })
  const login = createRoute({ getParentRoute: () => root, path: "/login", component: () => <p>login page</p> })
  const layout = createRoute({ getParentRoute: () => root, id: "_app", beforeLoad: requireAuth, component: Layout })
  const home = createRoute({
    getParentRoute: () => layout,
    path: "/",
    staticData: { title: "首页" },
    component: () => <p>home page</p>,
  })
  const users = createRoute({
    getParentRoute: () => layout,
    path: "/users",
    staticData: { title: "用户", permission: "users:manage" },
    component: () => <p>users page</p>,
  })
  const routeTree = root.addChildren([login, layout.addChildren([home, users])])

  const refresh = vi.fn(async () => {
    if (!options.signedIn) throw { status: 401, fields: [] }
    return body("a1")
  })
  const session = createSession({ refresh })
  const permissions = vi.fn(async () => options.permissions ?? [])
  const app = createApp({
    id: "test",
    routeTree,
    session,
    permissions,
    router: { history: createMemoryHistory({ initialEntries: ["/"] }), defaultErrorComponent: Problem },
  })
  return { app, session, permissions, refresh }
}

async function go(app: ReturnType<typeof setup>["app"], href: string) {
  await act(() => app.router.navigate({ href }))
}

afterEach(cleanup)

describe("createApp", () => {
  it("没有登录时跳到登录页并带上回跳地址", async () => {
    const { app } = setup({ signedIn: false })
    render(<AppRoot app={app} />)
    await go(app, "/users?tab=2")
    expect(await screen.findByText("login page")).toBeTruthy()
    expect(app.router.state.location.pathname).toBe("/login")
    expect(app.router.state.location.search).toEqual({ redirect: "/users?tab=2" })
  })

  it("登录后按权限显示菜单；缺少权限的页面在布局之中显示 403", async () => {
    const { app, refresh } = setup({ signedIn: true })
    render(<AppRoot app={app} />)
    expect(await screen.findByText("home page")).toBeTruthy()
    expect(screen.getByTestId("menu").textContent).toBe("首页")
    expect(screen.getByTestId("crumbs").textContent).toBe("首页")
    expect(screen.getByTestId("can").textContent).toBe("false")

    await go(app, "/users")
    expect(await screen.findByText("403 users:manage")).toBeTruthy()
    expect(screen.getByTestId("menu").textContent).toBe("首页") // 布局仍在
    expect(refresh).toHaveBeenCalledTimes(1) // 恢复登录只做一次
  })

  it("有权限时可以进入，菜单和 useCan 随之变化", async () => {
    const { app, permissions } = setup({ signedIn: true, permissions: ["users:manage"] })
    render(<AppRoot app={app} />)
    await go(app, "/users")
    expect(await screen.findByText("users page")).toBeTruthy()
    expect(screen.getByTestId("menu").textContent).toBe("首页,用户")
    expect(screen.getByTestId("crumbs").textContent).toBe("用户")
    expect(screen.getByTestId("can").textContent).toBe("true")
    expect(permissions).toHaveBeenCalledTimes(1) // 缓存在 queryClient 中
  })

  it("退出后重新运行守卫，离开受保护的页面", async () => {
    const { app, session } = setup({ signedIn: true })
    render(<AppRoot app={app} />)
    expect(await screen.findByText("home page")).toBeTruthy()
    await act(() => session.signOut())
    expect(await screen.findByText("login page")).toBeTruthy()
  })

  it("登录后回到原来的地址；不安全或没有回跳地址时去第一个可以进入的页面", async () => {
    const { app, session } = setup({ signedIn: false, permissions: ["users:manage"] })
    render(<AppRoot app={app} />)
    await go(app, "/users")
    expect(await screen.findByText("login page")).toBeTruthy()

    await act(() => session.signIn(body("a2"), { account: "u1" }))
    await act(() => app.redirectAfterSignIn((app.router.state.location.search as { redirect?: string }).redirect))
    expect(await screen.findByText("users page")).toBeTruthy()

    await act(() => app.redirectAfterSignIn("//evil.com"))
    expect(await screen.findByText("home page")).toBeTruthy()
    await act(() => app.redirectAfterSignIn("/login?redirect=/users"))
    expect(app.router.state.location.pathname).toBe("/")
  })
})
