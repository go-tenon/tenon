// @vitest-environment happy-dom
import { createSession } from "@go-tenon/client"
import { createApp, type RouterContext } from "@go-tenon/shell"
import { createMemoryHistory, createRootRouteWithContext } from "@tanstack/react-router"
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { ActivityTimeline } from "./activity"
import { renderRoot } from "./testing"
import { NewVersionNotice } from "./version"

afterEach(cleanup)

async function mount(children: () => ReactNode) {
  const session = createSession({ load: async () => ({ account: "u1" }) })
  await session.signIn({ account: "u1" })
  const app = createApp({
    id: `ui-activity-${Math.random()}`,
    routeTree: createRootRouteWithContext<RouterContext>()({ component: children }),
    session,
    router: { history: createMemoryHistory({ initialEntries: ["/"] }) },
  })
  renderRoot(app)
  return app
}

const item = (id: string, extra: object = {}) => ({
  id,
  text: `事件 ${id}`,
  created_at: "2026-11-17T08:00:00Z",
  ...extra,
})

describe("ActivityTimeline", () => {
  it("显示操作人和代为操作的人，分页加载更多", async () => {
    const list = vi.fn(async (before?: string) =>
      before ? [item("3")] : [item("1", { actor: "小明" }), item("2", { actor: "小红", impersonator: "管理员" })],
    )
    await mount(() => <ActivityTimeline source={{ key: ["a"], list, pageSize: 2 }} />)
    await screen.findByText("事件 1")
    expect(screen.getByText(/小红 · （管理员 代为操作）/)).toBeTruthy()
    fireEvent.click(screen.getByText("加载更多"))
    await screen.findByText("事件 3")
    expect(list).toHaveBeenLastCalledWith("2")
    expect(screen.getByText(/^系统 · /)).toBeTruthy()
    expect(screen.queryByText("加载更多")).toBeNull()
  })

  it("点击有 href 的记录打开它", async () => {
    const app = await mount(() => (
      <ActivityTimeline source={{ key: ["b"], list: async () => [item("1", { href: "/articles/1" })] }} />
    ))
    fireEvent.click(await screen.findByText("事件 1"))
    await waitFor(() => expect(app.router.state.location.pathname).toBe("/articles/1"))
  })

  it("没有记录、读取失败", { timeout: 15000 }, async () => {
    await mount(() => (
      <>
        <ActivityTimeline source={{ key: ["c"], list: async () => [] }} />
        <ActivityTimeline source={{ key: ["d"], list: async () => Promise.reject(new Error("boom")) }} />
      </>
    ))
    await screen.findByText("还没有操作记录")
    // 非 4xx 的错误按 QueryClient 的默认设置重试两次之后才显示。
    await screen.findByText("操作记录读取失败", undefined, { timeout: 8000 })
  })
})

describe("NewVersionNotice", () => {
  it("version.json 中的版本号变了时提示刷新，“稍后”关闭", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ version: "v2" })))
    await mount(() => (
      <NewVersionNotice current="v1" url="/version.json" fetch={fetch as unknown as typeof globalThis.fetch} />
    ))
    await screen.findByText("有新版本可用，刷新后使用。")
    fireEvent.click(screen.getByText("稍后"))
    await waitFor(() => expect(screen.queryByText("有新版本可用，刷新后使用。")).toBeNull())
  })
})
