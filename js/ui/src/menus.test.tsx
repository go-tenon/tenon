// @vitest-environment happy-dom

import { createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react"
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
  it("只显示头像；菜单中有菜单项、配色（亮色、暗色、跟随系统）和退出登录", async () => {
    await renderApp(tree(async () => {}))
    await screen.findByText("编辑页面")
    const trigger = screen.getByRole("button", { name: "用户菜单" })
    expect(trigger.textContent).toBe("小")
    await act(async () => fireEvent.click(trigger))
    const schemes = await screen.findByRole("radiogroup", { name: "配色" })
    const dark = within(schemes).getByRole("radio", { name: "暗色" })
    expect(dark.getAttribute("aria-checked")).toBe("false")
    await act(async () => fireEvent.click(dark))
    await waitFor(() => expect(document.documentElement.dataset.mantineColorScheme).toBe("dark"))
    expect(within(schemes).getByRole("radio", { name: "暗色" }).getAttribute("aria-checked")).toBe("true")
    expect(screen.getByText("退出登录")).toBeTruthy() // 选择配色不关闭菜单
  })

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
