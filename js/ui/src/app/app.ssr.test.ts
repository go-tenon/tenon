import { createI18n, createSession } from "@go-tenon/client"
import { createMemoryHistory, createRootRouteWithContext, createRoute } from "@tanstack/react-router"
import { describe, expect, it, vi } from "vitest"
import { createApp, type RouterContext } from "./app"

describe("createApp SSR 隔离", () => {
  it("共享 routeTree、session 与 i18n 多次创建应用时不叠加 beforeLoad 且不泄漏订阅", async () => {
    let rootBeforeCount = 0
    let childBeforeCount = 0
    const rootRoute = createRootRouteWithContext<RouterContext>()({
      beforeLoad: () => {
        rootBeforeCount++
      },
    })
    const adminRoute = createRoute({
      getParentRoute: () => rootRoute,
      path: "/admin",
      staticData: { permission: "articles:write" },
      beforeLoad: () => {
        childBeforeCount++
      },
      component: () => null,
    })
    const routeTree = rootRoute.addChildren([adminRoute])
    const i18n = createI18n({ messages: { "zh-CN": { "app.title": "示例" } }, defaultLanguage: "zh-CN" })

    const listeners = new Set<() => void>()
    const baseSession = createSession({ load: async () => ({ account: "u1" }) })
    await baseSession.signIn({ account: "u1" })
    const trackedSession = {
      ...baseSession,
      subscribe(fn: () => void) {
        listeners.add(fn)
        const unsub = baseSession.subscribe(fn)
        return () => {
          listeners.delete(fn)
          unsub()
        }
      },
    }

    const perms = vi.fn(async () => ["articles:write"])
    // 连续创建多次应用（模拟 SSR 多次请求共用模块级 routeTree）
    for (let i = 0; i < 5; i++) {
      const app = createApp({
        id: "ssr-test",
        routeTree,
        session: trackedSession,
        i18n,
        permissions: perms,
        router: { history: createMemoryHistory({ initialEntries: ["/admin"] }) },
      })
      app.dispose()
    }

    rootBeforeCount = 0
    childBeforeCount = 0
    const last = createApp({
      id: "ssr-test",
      routeTree,
      session: trackedSession,
      i18n,
      permissions: perms,
      router: { history: createMemoryHistory({ initialEntries: ["/admin"] }) },
    })
    await last.router.load()
    // 每次导航只执行一次 root 与 child 的 beforeLoad，不随历史创建次数叠加。
    expect(rootBeforeCount).toBe(1)
    expect(childBeforeCount).toBe(1)
    last.dispose()
    expect(listeners.size).toBe(0)
  })
})
