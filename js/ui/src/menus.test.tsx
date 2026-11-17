// @vitest-environment happy-dom

import { createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { AppLayout } from "./app-layout"
import { UserMenu } from "./menus"
import { type RouterContext, requireAuth, useUnsavedChanges } from "./shell"
import { renderApp } from "./testing"

afterEach(() => {
  cleanup()
  localStorage.clear()
  vi.unstubAllGlobals()
})

function tree(logout: () => Promise<unknown>) {
  const root = createRootRouteWithContext<RouterContext>()({ component: Outlet })
  const layout = createRoute({
    getParentRoute: () => root,
    id: "_app",
    beforeLoad: requireAuth,
    component: () => <AppLayout title="测试后台" tabs user={<UserMenu name="小明" logout={logout} />} />,
  })
  const editor = createRoute({
    getParentRoute: () => layout,
    path: "/",
    staticData: { title: "编辑" },
    component: () => {
      useUnsavedChanges(true)
      return <p>编辑页面</p>
    },
  })
  const login = createRoute({ getParentRoute: () => root, path: "/login", component: () => <p>登录页</p> })
  return root.addChildren([layout.addChildren([editor]), login])
}

async function clickSignOut() {
  await act(async () => fireEvent.click(screen.getByRole("button", { name: "用户菜单" })))
  await act(async () => fireEvent.click(await screen.findByText("退出登录")))
}

describe("UserMenu", () => {
  it("标签中有未保存的修改时，退出之前询问；选择留下时不退出", async () => {
    const logout = vi.fn(async () => {})
    const ask = vi.fn((_: string) => false)
    vi.stubGlobal("confirm", ask)
    const app = await renderApp(tree(logout))
    await screen.findByText("编辑页面")

    await clickSignOut()
    expect(ask).toHaveBeenCalledWith("有未保存的修改，确定放弃吗？")
    expect(logout).not.toHaveBeenCalled()
    expect(app.session.identity()).not.toBeNull()

    ask.mockReturnValue(true)
    await clickSignOut()
    await waitFor(() => expect(logout).toHaveBeenCalledTimes(1))
    expect(await screen.findByText("登录页")).toBeTruthy()
  })
})
