// @vitest-environment happy-dom

import { createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { AppLayout } from "./app-layout"
import { Page } from "./page"
import { Fill, type RouterContext, requireAuth } from "./shell"
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
    component: () => <AppLayout title="测试后台" user={<span>用户菜单</span>} actions={<span>通知</span>} />,
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
      component: () => (
        <Page title={title}>
          {title === "用户" && (
            <Fill name="actions">
              <button type="button">新建用户</button>
            </Fill>
          )}
          <p>{`${title}页面`}</p>
        </Page>
      ),
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
  it("侧栏按权限过滤菜单，高亮当前页面并展开所在的分组；页面标题上面的面包屑是当前位置", async () => {
    await renderApp(tree(), { path: "/trade/orders", permissions: ["users:manage"] })
    await screen.findByText("订单页面")
    const nav = screen.getByRole("navigation", { name: "主导航" })
    expect(await within(nav).findByText("用户")).toBeTruthy()
    expect(within(nav).queryByText("计费")).toBeNull()
    const current = within(nav).getByText("订单").closest("a") as HTMLAnchorElement
    expect(current.dataset.active).toBe("true")
    expect(within(nav).getByText("退款")).toBeTruthy() // 当前页面所在的分组展开
    expect((within(nav).getByText("交易").closest("[data-in-path]") as HTMLElement | null)?.tagName).toBe("BUTTON") // 分组是按钮，可以用键盘展开
    const crumbs = screen.getByText("交易", { selector: ".tn-crumbs a" })
    expect(crumbs.getAttribute("href")).toBe("/trade")
    expect(screen.getByText("订单", { selector: "[aria-current=page]" })).toBeTruthy()
  })

  it("参数中的用户菜单和操作显示在顶栏；页面用 Fill 放的操作在前面，离开页面时移除", async () => {
    const app = await renderApp(tree(), { path: "/users", permissions: ["users:manage"] })
    await screen.findByText("用户页面")
    const header = document.querySelector(".tn-header") as HTMLElement
    expect(within(header).getByText("用户菜单")).toBeTruthy()
    expect(within(header).getByText("新建用户")).toBeTruthy()
    expect(header.textContent?.indexOf("新建用户")).toBeLessThan(header.textContent?.indexOf("通知") ?? 0)
    await act(() => app.router.navigate({ href: "/" }))
    await screen.findByText("概览页面")
    expect(within(header).queryByText("新建用户")).toBeNull()
    expect(within(header).getByText("通知")).toBeTruthy()
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

  it("顶栏有标志和名称，点击回到首页", async () => {
    await renderApp(tree(), { path: "/users", permissions: ["users:manage"] })
    await screen.findByText("用户页面")
    const header = document.querySelector(".tn-header") as HTMLElement
    const brand = within(header).getByText("测试后台").closest("a") as HTMLAnchorElement
    expect(brand.getAttribute("href")).toBe("/")
    expect(within(brand).getByText("测")).toBeTruthy()
  })

  it("页面搜索列出有权限的页面，说明是所在的分组；选择后打开页面", async () => {
    const app = await renderApp(tree(), { permissions: ["users:manage"] })
    await screen.findByText("概览页面")
    await act(async () => fireEvent.click(screen.getByRole("button", { name: /搜索/ })))
    const input = await screen.findByPlaceholderText("搜索页面…")
    const options = () => [...document.querySelectorAll(".mantine-Spotlight-action")].map((a) => a.textContent)
    expect(options()).toEqual(["概览", "订单交易", "退款交易", "用户"]) // 没有权限的“计费”不在其中
    await act(async () => fireEvent.change(input, { target: { value: "退" } }))
    expect(options()).toEqual(["退款交易"])
    const option = document.querySelector(".mantine-Spotlight-action") as HTMLElement
    await act(async () => fireEvent.click(option))
    await screen.findByText("退款页面")
    expect(app.router.state.location.pathname).toBe("/trade/refunds")
  })

  it("search 为 false 时没有页面搜索", async () => {
    const root = createRootRouteWithContext<RouterContext>()({ component: Outlet })
    const layout = createRoute({
      getParentRoute: () => root,
      id: "_app",
      component: () => <AppLayout title="测试后台" search={false} />,
    })
    const home = createRoute({ getParentRoute: () => layout, path: "/", component: () => <p>首页</p> })
    await renderApp(root.addChildren([layout.addChildren([home])]))
    await screen.findByText("首页")
    expect(screen.queryByRole("button", { name: /搜索/ })).toBeNull()
  })

  it("没有登录时跳到登录页", async () => {
    const app = await renderApp(tree(), { signedIn: false })
    await screen.findByText("登录页")
    expect(app.router.state.location.pathname).toBe("/login")
  })
})
