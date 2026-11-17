// 服务端渲染（没有 window）：shell 的 hook 都要有服务端快照，否则 React 报 Missing getServerSnapshot。
import { createSession } from "@go-tenon/client"
import { createMemoryHistory, createRootRouteWithContext, createRoute, Outlet, RouterProvider } from "@tanstack/react-router"
import { renderToString } from "react-dom/server"
import { describe, expect, it, vi } from "vitest"
import { createApp, type RouterContext, useSignedIn } from "./app"
import { Fill, Slot, useSlotFilled } from "./layout"
import { useTenant } from "./tenant"

function Root() {
  const tenant = useTenant()
  return (
    <>
      <header>
        signed-in={String(useSignedIn())} tenant={String(tenant.current)} actions={String(useSlotFilled("actions"))}
        <Slot name="actions" />
      </header>
      <Outlet />
    </>
  )
}

describe("server rendering", () => {
  it("按未登录渲染，不读取会话", async () => {
    const load = vi.fn(async () => ({ account: "u1" }))
    const root = createRootRouteWithContext<RouterContext>()({ component: Root })
    const home = createRoute({
      getParentRoute: () => root,
      path: "/",
      component: () => <Fill name="actions">新建</Fill>,
    })
    const app = createApp({
      id: "ssr-test",
      routeTree: root.addChildren([home]),
      session: createSession({ load }),
      router: { history: createMemoryHistory({ initialEntries: ["/"] }), isServer: true },
    })
    await app.router.load()
    const html = renderToString(<RouterProvider router={app.router} />)
    expect(html).toContain("signed-in=<!-- -->false")
    expect(html).toContain("tenant=<!-- -->undefined")
    expect(load).not.toHaveBeenCalled()
  })
})
