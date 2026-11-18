// @vitest-environment happy-dom

import { createRootRouteWithContext, createRoute } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { ConsoleSideLayout, ConsoleTopLayout } from "./console-layout"
import type { RouterContext } from "./shell"
import { renderApp } from "./testing"

afterEach(cleanup)

function tree(Layout: typeof ConsoleSideLayout) {
  const root = createRootRouteWithContext<RouterContext>()({ component: () => <Layout title="Acme" /> })
  return root.addChildren([
    createRoute({
      getParentRoute: () => root,
      path: "/",
      staticData: { title: "首页", order: 0 },
      component: () => <p>首页内容</p>,
    }),
    createRoute({
      getParentRoute: () => root,
      path: "/settings",
      staticData: { title: "设置", order: 1 },
      component: () => <p>设置内容</p>,
    }),
  ])
}

describe("商户侧的布局（占位）", () => {
  for (const [name, Layout] of [
    ["侧栏导航", ConsoleSideLayout],
    ["顶部导航", ConsoleTopLayout],
  ] as const) {
    it(`${name}：顶栏有名称，导航列出页面并高亮当前页，点击切换页面`, async () => {
      const app = await renderApp(tree(Layout) as never)
      expect(await screen.findByText("首页内容")).toBeTruthy()
      expect(screen.getByText("Acme")).toBeTruthy()
      expect(screen.getByRole("link", { name: "首页" }).hasAttribute("data-active")).toBe(true)
      await act(async () => fireEvent.click(screen.getByRole("link", { name: "设置" })))
      expect(app.router.state.location.pathname).toBe("/settings")
      expect(await screen.findByText("设置内容")).toBeTruthy()
    })
  }
})
