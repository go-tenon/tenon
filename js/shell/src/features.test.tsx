// @vitest-environment happy-dom
// 通知、后台操作和 AI 对话的模型。
import { createSession, type OperationLike, type Session } from "@go-tenon/client"
import { createMemoryHistory, createRootRouteWithContext, Outlet } from "@tanstack/react-router"
import { act, cleanup, render, screen, waitFor } from "@testing-library/react"
import type { UIMessage } from "ai"
import { type ReactNode, useEffect } from "react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { type App, AppRoot, createApp, type RouterContext, useApp } from "./app"
import { type AssistantChat, useAssistantChat } from "./chat"
import { type NotificationItem, type Notifications, type NotificationsSource, useNotifications } from "./notifications"
import { dismissOperation, type TrackedOperation, trackOperation, useOperations } from "./operations"

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

/** 渲染 children，返回应用和会话。 */
async function mount(children: () => ReactNode) {
  const session: Session = createSession({ refresh: async () => ({ access_token: "a1", expires_at: "" }) })
  await session.signIn({ access_token: "a1", expires_at: "" }, { account: "u1" })
  const app = createApp({
    id: "features",
    routeTree: createRootRouteWithContext<RouterContext>()({
      component: () => (
        <>
          {children()}
          <Outlet />
        </>
      ),
    }),
    session,
    router: { history: createMemoryHistory({ initialEntries: ["/"] }) },
  })
  render(<AppRoot app={app} />)
  return { app, session }
}

/** 把 hook 的结果交给测试读取。 */
function Probe<T>({ use, onValue }: { use: () => T; onValue: (v: T) => void }): null {
  const v = use()
  useEffect(() => {
    onValue(v)
  })
  return null
}

describe("useNotifications", () => {
  const items: NotificationItem[] = [
    { id: "n2", title: "小红评论了你的文章", read: false, created_at: "2026-11-15T08:00:00Z", href: "/articles/1" },
    { id: "n1", title: "文章已发布", read: true, created_at: "2026-11-14T08:00:00Z" },
  ]

  it("未读数来自事件流，断开后重连；未读数变化时重新读取列表；标记已读", async () => {
    let connections = 0
    const source: NotificationsSource = {
      list: vi.fn(async () => items.map((n) => ({ ...n }))),
      markRead: vi.fn(async () => {}),
      markAllRead: vi.fn(async () => {}),
      badge: async function* () {
        connections++
        yield { event: "badge", data: { unread: connections === 1 ? 2 : 1 } }
        if (connections === 1) throw new TypeError("connection lost")
        await new Promise(() => {}) // 保持连接
      },
      pageSize: 50,
    }
    let n: Notifications | undefined
    await mount(() => <Probe use={() => useNotifications(source)} onValue={(v) => (n = v)} />)
    await waitFor(() => expect(n?.items.length).toBe(2))
    await waitFor(() => expect(connections).toBe(2), { timeout: 3000 }) // 断开后重连
    await waitFor(() => expect(n?.unread).toBe(1))
    expect(n?.hasMore).toBe(false)

    await act(() => n?.markRead("n2") ?? Promise.resolve())
    expect(source.markRead).toHaveBeenCalledWith(["n2"])
    expect(n?.items.find((i) => i.id === "n2")?.read).toBe(true)
    expect(n?.unread).toBe(0)
    await act(() => n?.markRead("n1") ?? Promise.resolve()) // 已读的不再调用接口
    expect(source.markRead).toHaveBeenCalledTimes(1)
    await act(() => n?.markAllRead() ?? Promise.resolve())
    expect(source.markAllRead).toHaveBeenCalled()
  })

  it("没有事件流时定期读取未读数", async () => {
    const unread = vi.fn(async () => 5)
    let n: Notifications | undefined
    await mount(() => (
      <Probe
        use={() =>
          useNotifications({ list: async () => [], markRead: async () => {}, markAllRead: async () => {}, unread })
        }
        onValue={(v) => (n = v)}
      />
    ))
    await waitFor(() => expect(n?.unread).toBe(5))
  })
})

describe("后台操作", () => {
  const op = (status: string, processed: number): OperationLike => ({
    id: "op1",
    status,
    total: 4,
    processed,
    failed: 0,
  })

  it("登记后读取进度直到结束；移除；退出登录时清空", async () => {
    let release: () => void = () => {}
    const gate = new Promise<void>((r) => {
      release = r
    })
    async function* stream() {
      yield { event: "progress", data: op("running", 1) }
      await gate
      yield { event: "progress", data: op("completed", 4) }
    }
    let ops: readonly TrackedOperation[] = []
    let app: App | undefined
    const mounted = await mount(() => (
      <Probe
        use={() => [useOperations(), useApp()] as const}
        onValue={([v, a]) => {
          ops = v
          app = a
        }}
      />
    ))
    await waitFor(() => expect(app).toBeDefined())
    act(() => trackOperation(app as App, { id: "op1", title: "批量发布", open: stream }))
    act(() => trackOperation(app as App, { id: "op1", title: "重复登记", open: stream }))
    await waitFor(() => expect(ops[0]?.ratio).toBe(0.25))
    expect(ops).toHaveLength(1)
    expect(ops[0]).toMatchObject({ title: "批量发布", finished: false })
    await act(async () => release())
    await waitFor(() => expect(ops[0]).toMatchObject({ finished: true, ratio: 1 }))

    act(() => dismissOperation(app as App, "op1"))
    expect(ops).toHaveLength(0)
    act(() => trackOperation(app as App, { id: "op2", title: "导出", open: stream }))
    await waitFor(() => expect(ops).toHaveLength(1))
    await act(() => mounted.session.signOut())
    await waitFor(() => expect(ops).toHaveLength(0))
  })
})

/** Vercel AI SDK 的 UI 消息流。 */
function uiStream(chunks: object[]): Response {
  // 与服务端相同：每一步包在 start-step 和 finish-step 之间。
  const [start, ...rest] = chunks
  const finish = rest.pop()
  chunks = [start, { type: "start-step" }, ...rest, { type: "finish-step" }, finish] as object[]
  const body = `${chunks.map((c) => `data: ${JSON.stringify(c)}\n\n`).join("")}data: [DONE]\n\n`
  return new Response(body, {
    headers: { "Content-Type": "text/event-stream", "x-vercel-ai-ui-message-stream": "v1" },
  })
}

function Chat({ onValue }: { onValue: (c: AssistantChat) => void }) {
  const chat = useAssistantChat({ api: "/api/chat", id: "c1", messages: history })
  useEffect(() => {
    onValue(chat)
  })
  return <p data-testid="text">{chat.messages.map((m) => `${m.role}:${textOf(m)}`).join("|")}</p>
}
const history: UIMessage[] = [{ id: "u0", role: "user", parts: [{ type: "text", text: "你好" }] }]
const textOf = (m: UIMessage) =>
  m.parts
    .filter((p) => p.type === "text")
    .map((p) => (p as { text: string }).text)
    .join("")

describe("useAssistantChat", () => {
  type Call = { url: string; method: string; body?: unknown; auth?: string | null }

  function server(handler: (c: Call) => Response) {
    const calls: Call[] = []
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const call: Call = {
          url: String(input),
          method: init?.method ?? "GET",
          body: init?.body ? JSON.parse(String(init.body)) : undefined,
          auth: new Headers(init?.headers).get("Authorization"),
        }
        calls.push(call)
        return handler(call)
      }),
    )
    return calls
  }

  it("发送：只发送最后一条消息、带上 access token；插话是回答中的 data-steering", async () => {
    const calls = server((c) => {
      if (c.url.endsWith("/stream")) return new Response(null, { status: 204 })
      return uiStream([
        { type: "start", messageId: "a1" },
        { type: "text-start", id: "t1" },
        { type: "text-delta", id: "t1", delta: "我来" },
        { type: "data-steering", id: "s1", data: { text: "再短一点" } },
        { type: "text-delta", id: "t1", delta: "回答" },
        { type: "text-end", id: "t1" },
        { type: "finish" },
      ])
    })
    let chat: AssistantChat | undefined
    await mount(() => <Chat onValue={(c) => (chat = c)} />)
    await waitFor(() => expect(chat).toBeDefined())
    await act(() => chat?.send("讲个故事") ?? Promise.resolve())
    await waitFor(() =>
      expect(screen.getByTestId("text").textContent).toBe("user:你好|user:讲个故事|assistant:我来回答"),
    )
    expect(chat?.error).toBeUndefined()
    expect(chat?.messages[2]?.parts).toContainEqual({ type: "data-steering", id: "s1", data: { text: "再短一点" } })
    const post = calls.find((c) => c.method === "POST")
    expect(post).toMatchObject({ url: "/api/chat", auth: "Bearer a1" })
    expect((post?.body as { message?: UIMessage } | undefined)?.message?.parts).toEqual([
      { type: "text", text: "讲个故事" },
    ])
  })

  it("工具调用等待审批：审批后自动发送审批结果，回答继续", async () => {
    let posts = 0
    server((c) => {
      if (c.url.endsWith("/stream")) return new Response(null, { status: 204 })
      posts++
      if (posts === 1) {
        return uiStream([
          { type: "start", messageId: "a1" },
          { type: "tool-input-available", toolCallId: "r1", toolName: "refund_order", input: { order_id: "A1" } },
          { type: "tool-approval-request", approvalId: "r1", toolCallId: "r1" },
          { type: "finish", finishReason: "tool-calls" },
        ])
      }
      return uiStream([
        { type: "start", messageId: "a1" },
        { type: "text-start", id: "t2" },
        { type: "text-delta", id: "t2", delta: "已退款" },
        { type: "text-end", id: "t2" },
        { type: "finish" },
      ])
    })
    let chat: AssistantChat | undefined
    await mount(() => <Chat onValue={(c) => (chat = c)} />)
    await waitFor(() => expect(chat).toBeDefined())
    await act(() => chat?.send("给 A1 退款") ?? Promise.resolve())
    await waitFor(() =>
      expect(chat?.pending).toEqual([{ id: "r1", toolName: "refund_order", input: { order_id: "A1" } }]),
    )
    await act(async () => chat?.respond("r1", true))
    await waitFor(() => expect(screen.getByTestId("text").textContent).toContain("已退款"))
    expect(posts).toBe(2)
  })

  it("回答进行中发送时插话；回答恰好已经结束（409 ai.finished）时改为发送新消息", async () => {
    let steer = 0
    let posts = 0
    const calls = server((c) => {
      if (c.url.endsWith("/stream")) return new Response(null, { status: 204 })
      if (c.url.endsWith("/steer")) {
        steer++
        return new Response(JSON.stringify({ status: 409, code: "ai.finished", detail: "回答已经结束" }), {
          status: 409,
          headers: { "Content-Type": "application/problem+json" },
        })
      }
      if (c.url.endsWith("/stop")) return new Response(null, { status: 204 })
      if (++posts > 1) {
        return uiStream([
          { type: "start", messageId: "a2" },
          { type: "text-start", id: "t3" },
          { type: "text-delta", id: "t3", delta: "好的" },
          { type: "text-end", id: "t3" },
          { type: "finish" },
        ])
      }
      return new Response(
        new ReadableStream({
          start(ctl) {
            ctl.enqueue(new TextEncoder().encode(`data: ${JSON.stringify({ type: "start", messageId: "a1" })}\n\n`))
          },
        }),
        { headers: { "Content-Type": "text/event-stream", "x-vercel-ai-ui-message-stream": "v1" } },
      )
    })
    let chat: AssistantChat | undefined
    await mount(() => <Chat onValue={(c) => (chat = c)} />)
    await waitFor(() => expect(chat).toBeDefined())
    // 回答一直进行（消息流不结束）：在 act 之外等待状态变为进行中。
    act(() => {
      void chat?.send("第一问")
    })
    await waitFor(() => expect(chat?.busy).toBe(true))
    await act(() => chat?.send("补充一句") ?? Promise.resolve())
    expect(steer).toBe(1)
    expect(calls.filter((c) => c.method === "POST" && c.url === "/api/chat")).toHaveLength(2) // 改为发送新消息
    await act(() => chat?.stop() ?? Promise.resolve())
    expect(calls.some((c) => c.url === "/api/chat/c1/stop")).toBe(true)
  })
})
