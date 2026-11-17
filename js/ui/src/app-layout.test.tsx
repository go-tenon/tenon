// @vitest-environment happy-dom
import { Fill, type RouterContext, requireAuth } from "@go-tenon/shell"
import { createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { AppLayout } from "./app-layout"
import { renderApp } from "./testing"

afterEach(() => {
  cleanup()
  localStorage.clear()
})

function tree() {
  const root = createRootRouteWithContext<RouterContext>()({ component: Outlet })
  const layout = createRoute({
    getParentRoute: () => root,
    id: "_app",
    beforeLoad: requireAuth,
    component: () => (
      <>
        <Fill name="user">
          <span>用户菜单</span>
        </Fill>
        <AppLayout title="测试后台" />
      </>
    ),
  })
  const page = (
    parent: typeof layout | typeof trade,
    path: string,
    title: string,
    order: number,
    permission?: string,
  ) =>
    createRoute({
      getParentRoute: () => parent,
      path,
      staticData: { title, order, permission, icon: "home" },
      component: () => <p>{`${title}页面`}</p>,
    })
  const trade = createRoute({ getParentRoute: () => layout, path: "/trade", staticData: { title: "交易", order: 1 } })
  const login = createRoute({ getParentRoute: () => root, path: "/login", component: () => <p>登录页</p> })
  return root.addChildren([
    layout.addChildren([
      page(layout, "/", "概览", 0),
      trade.addChildren([page(trade, "/orders", "订单", 0), page(trade, "/refunds", "退款", 1)]),
      page(layout, "/users", "用户", 2, "users:manage"),
      page(layout, "/billing", "计费", 3, "billing:manage"),
    ]),
    login,
  ])
}

describe("AppLayout", () => {
  it("侧栏按权限过滤菜单，高亮当前页面并展开所在的分组；面包屑是当前位置", async () => {
    await renderApp(tree(), { path: "/trade/orders", permissions: ["users:manage"] })
    await screen.findByText("订单页面")
    const nav = screen.getByRole("navigation", { name: "主导航" })
    expect(await within(nav).findByText("用户")).toBeTruthy()
    expect(within(nav).queryByText("计费")).toBeNull()
    const current = within(nav).getByText("订单").closest("a") as HTMLAnchorElement
    expect(current.dataset.active).toBe("true")
    expect(within(nav).getByText("退款")).toBeTruthy() // 当前页面所在的分组展开
    const crumbs = screen.getByText("交易", { selector: ".tn-crumbs a" })
    expect(crumbs.getAttribute("href")).toBe("/trade")
    expect(screen.getByText("订单", { selector: "[aria-current=page]" })).toBeTruthy()
  })

  it("插槽中的内容显示在顶栏", async () => {
    await renderApp(tree())
    expect(await screen.findByText("用户菜单")).toBeTruthy()
  })

  it("收起侧栏后只显示图标，名称在 aria-label 中；状态保存在本地", async () => {
    await renderApp(tree(), { permissions: ["users:manage"] })
    await screen.findByText("概览页面")
    await within(screen.getByRole("navigation", { name: "主导航" })).findByText("用户")
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "收起侧栏" })))
    const nav = screen.getByRole("navigation", { name: "主导航" })
    await waitFor(() => expect(within(nav).queryByText("用户")).toBeNull())
    expect(within(nav).getByLabelText("用户")).toBeTruthy()
    expect(within(nav).getByLabelText("交易")).toBeTruthy()
    expect(JSON.stringify(localStorage)).toContain("collapsed")
  })

  it("没有登录时跳到登录页", async () => {
    const app = await renderApp(tree(), { signedIn: false })
    await screen.findByText("登录页")
    expect(app.router.state.location.pathname).toBe("/login")
  })
})
