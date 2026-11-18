// @vitest-environment happy-dom

import { createRootRouteWithContext, createRoute } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import type { RouterContext } from "./shell"
import { SiteLayout } from "./site"
import { renderApp } from "./testing"

afterEach(cleanup)

function tree() {
  const root = createRootRouteWithContext<RouterContext>()({
    component: () => <SiteLayout brand="Acme" nav={[{ label: "文档", to: "/docs" }]} />,
  })
  return root.addChildren([
    createRoute({ getParentRoute: () => root, path: "/", component: () => <p>首页内容</p> }),
    createRoute({ getParentRoute: () => root, path: "/docs", component: () => <p>文档内容</p> }),
  ])
}

describe("SiteLayout", () => {
  it("点页头的标志回到首页", async () => {
    const app = await renderApp(tree() as never, { path: "/docs" })
    await screen.findByText("文档内容")
    await act(async () => fireEvent.click(screen.getByRole("link", { name: "Acme" })))
    expect(app.router.state.location.pathname).toBe("/")
    expect(await screen.findByText("首页内容")).toBeTruthy()
  })
})
