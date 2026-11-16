// @vitest-environment happy-dom
// 通知铃铛、任务中心、操作进度和 AI 对话的外观。
import { createSession, type OperationLike } from "@go-tenon/client"
import {
  AppRoot,
  createApp,
  type NotificationsSource,
  type OperationStream,
  type RouterContext,
  trackOperation,
  useApp,
} from "@go-tenon/shell"
import { createMemoryHistory, createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { type ReactNode, useEffect } from "react"
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest"
import { AssistantChat } from "./chat"
import { NotificationBell, OperationCenter, OperationProgress } from "./features"
import { AdminProvider } from "./provider"

beforeAll(() => {
  window.matchMedia ??= ((query: string) => ({
    matches: false,
    media: query,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    onchange: null,
    dispatchEvent: () => false,
  })) as never
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as never
  // Bubble.List 滚动到底部。
  Element.prototype.scrollTo ??= () => {}
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

async function mount(children: () => ReactNode, path = "/") {
  const session = createSession({ refresh: async () => ({ access_token: "a1", expires_at: "" }) })
  await session.signIn({ access_token: "a1", expires_at: "" }, { account: "u1" })
  const root = createRootRouteWithContext<RouterContext>()({
    component: () => (
      <AdminProvider>
        {children()}
        <Outlet />
      </AdminProvider>
    ),
  })
  const routes = [
    createRoute({ getParentRoute: () => root, path: "/", component: () => <p>home page</p> }),
    createRoute({ getParentRoute: () => root, path: "/articles/$id", component: () => <p>article page</p> }),
  ]
  const app = createApp({
    id: "admin-features",
    routeTree: root.addChildren(routes),
    session,
    router: { history: createMemoryHistory({ initialEntries: [path] }) },
  })
  render(<AppRoot app={app} />)
  await screen.findByText("home page")
  return { app, session }
}

describe("NotificationBell", () => {
  it("角标是未读数；点击通知标记已读并打开页面；全部已读", async () => {
    const source: NotificationsSource = {
      list: async () => [
        { id: "n2", title: "小红评论了你的文章", read: false, created_at: "2026-11-15T08:00:00Z", href: "/articles/1" },
        { id: "n1", title: "文章已发布", read: true, created_at: "2026-11-14T08:00:00Z" },
      ],
      markRead: vi.fn(async () => {}),
      markAllRead: vi.fn(async () => {}),
      unread: async () => 1,
    }
    await mount(() => <NotificationBell source={source} />)
    const bell = await screen.findByRole("button", { name: "通知（1 条未读）" })
    fireEvent.click(bell)
    fireEvent.click(await screen.findByText("小红评论了你的文章"))
    expect(source.markRead).toHaveBeenCalledWith(["n2"])
    await screen.findByText("article page")
    await screen.findByRole("button", { name: "通知" })

    fireEvent.click(screen.getByRole("button", { name: "通知" }))
    expect((await screen.findByRole("button", { name: "全部已读" })).hasAttribute("disabled")).toBe(true)
  })
})

/** 登记一个操作，并在页面中显示它的进度。 */
function Start({ open }: { open: OperationStream }) {
  const app = useApp()
  useEffect(() => trackOperation(app, { id: "op1", title: "批量发布 4 篇文章", open }), [app, open])
  return <OperationProgress id="op1" />
}

describe("后台操作", () => {
  it("任务中心列出操作和进度，结束后可以移除；页面中的进度条", async () => {
    let release: () => void = () => {}
    const gate = new Promise<void>((r) => {
      release = r
    })
    const op = (status: string, processed: number, failed = 0): OperationLike => ({
      id: "op1",
      status,
      total: 4,
      processed,
      failed,
    })
    async function* stream() {
      yield { event: "progress", data: op("running", 2) }
      await gate
      yield { event: "progress", data: op("completed", 4, 1) }
    }
    await mount(() => (
      <>
        <OperationCenter />
        <Start open={stream} />
      </>
    ))
    const button = await screen.findByRole("button", { name: "后台任务（1 个进行中）" })
    expect(await screen.findByText("进行中 · 2/4")).toBeTruthy()
    fireEvent.click(button)
    const popover = await screen.findByRole("tooltip")
    expect(within(popover).getByText("批量发布 4 篇文章")).toBeTruthy()

    await act(async () => release())
    await screen.findByRole("button", { name: "后台任务" })
    await waitFor(() => expect(screen.getAllByText("已完成 · 4/4 · 失败 1").length).toBe(2))
    fireEvent.click(within(popover).getByRole("button", { name: "移除 批量发布 4 篇文章" }))
    await waitFor(() => expect(screen.queryByRole("button", { name: /后台任务/ })).toBeNull())
  })
})

/** Vercel AI SDK 的 UI 消息流，每一步包在 start-step 和 finish-step 之间。 */
function uiStream(messageId: string, chunks: object[], finishReason?: string): Response {
  const all = [
    { type: "start", messageId },
    { type: "start-step" },
    ...chunks,
    { type: "finish-step" },
    { type: "finish", ...(finishReason ? { finishReason } : {}) },
  ]
  const body = `${all.map((c) => `data: ${JSON.stringify(c)}\n\n`).join("")}data: [DONE]\n\n`
  return new Response(body, { headers: { "Content-Type": "text/event-stream", "x-vercel-ai-ui-message-stream": "v1" } })
}

describe("AssistantChat", () => {
  it("显示历史（插话是单独的用户消息）；发送；工具调用需要批准", async () => {
    const posts: unknown[] = []
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        if (url.endsWith("/messages")) {
          return Response.json([
            { id: "u0", role: "user", parts: [{ type: "text", text: "查订单 A1" }] },
            {
              id: "a0",
              role: "assistant",
              parts: [
                { type: "step-start" },
                { type: "text", text: "**订单 A1** 已发货", state: "done" },
                { type: "data-steering", id: "s1", data: { text: "顺便查 A2" } },
                { type: "step-start" },
                { type: "text", text: "A2 还在打包", state: "done" },
              ],
            },
          ])
        }
        if (url.endsWith("/stream")) return new Response(null, { status: 204 })
        posts.push(JSON.parse(String(init?.body)))
        if (posts.length === 1) {
          return uiStream(
            "a1",
            [
              { type: "tool-input-available", toolCallId: "r1", toolName: "refund_order", input: { order_id: "A1" } },
              { type: "tool-approval-request", approvalId: "r1", toolCallId: "r1" },
            ],
            "tool-calls",
          )
        }
        return uiStream("a1", [
          { type: "tool-output-available", toolCallId: "r1", output: { ok: true } },
          { type: "text-start", id: "t2" },
          { type: "text-delta", id: "t2", delta: "已退款" },
          { type: "text-end", id: "t2" },
        ])
      }),
    )
    await mount(() => <AssistantChat api="/api/chat" id="c1" toolTitles={{ refund_order: "退款" }} height={600} />)
    // 历史：Markdown 渲染；插话是单独的用户消息，插话前后的回答分开。
    expect((await screen.findByText("订单 A1")).tagName).toBe("STRONG")
    expect(screen.getByText("顺便查 A2")).toBeTruthy()
    expect(screen.getByText("A2 还在打包")).toBeTruthy()

    const input = screen.getByRole("textbox")
    fireEvent.change(input, { target: { value: "给 A1 退款" } })
    fireEvent.keyDown(input, { key: "Enter", code: "Enter", keyCode: 13 })
    const card = (await screen.findByText("需要你确认：退款")).closest(".ant-card") as HTMLElement
    expect(within(card).getByText(/"order_id": "A1"/)).toBeTruthy()
    expect(screen.getByRole("textbox").hasAttribute("disabled")).toBe(true) // 等待审批时不能发送
    fireEvent.click(within(card).getByRole("button", { name: /批准/ }))
    await screen.findByText("已退款")
    expect(screen.queryByText("需要你确认：退款")).toBeNull()
    expect(posts).toHaveLength(2)
    const approval = (posts[1] as { message: { parts: { approval?: { approved: boolean } }[] } }).message.parts
    expect(approval.some((p) => p.approval?.approved === true)).toBe(true)
  })
})
