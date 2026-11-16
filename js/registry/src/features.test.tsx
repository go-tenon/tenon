// @vitest-environment happy-dom
// 通知铃铛、任务中心、操作进度、Markdown 和 AI 对话：用真实的 Radix、Router、@go-tenon/shell 和会话渲染。
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
import { ActivityTimeline } from "@/components/tenon/activity"
import { AssistantChat } from "@/components/tenon/assistant-chat"
import { Markdown } from "@/components/tenon/markdown"
import { NotificationBell, OperationCenter, OperationProgress } from "@/components/tenon/notifications"

beforeAll(() => {
  window.matchMedia ??= ((query: string) => ({
    matches: false,
    media: query,
    addEventListener() {},
    removeEventListener() {},
    onchange: null,
    dispatchEvent: () => false,
  })) as never
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as never
  Element.prototype.scrollIntoView ??= () => {}
  Element.prototype.scrollTo ??= () => {}
  Element.prototype.hasPointerCapture ??= () => false
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

async function mount(children: () => ReactNode) {
  const session = createSession({ refresh: async () => ({ access_token: "a1", expires_at: "" }) })
  await session.signIn({ access_token: "a1", expires_at: "" }, { account: "u1" })
  const root = createRootRouteWithContext<RouterContext>()({
    component: () => (
      <>
        {children()}
        <Outlet />
      </>
    ),
  })
  const routes = [
    createRoute({ getParentRoute: () => root, path: "/", component: () => <p>home page</p> }),
    createRoute({ getParentRoute: () => root, path: "/articles/$id", component: () => <p>article page</p> }),
  ]
  const app = createApp({
    id: "registry-features",
    routeTree: root.addChildren(routes),
    session,
    router: { history: createMemoryHistory({ initialEntries: ["/"] }) },
  })
  render(<AppRoot app={app} />)
  await screen.findByText("home page")
  return { app, session }
}

describe("NotificationBell", () => {
  it("角标是未读数；点击通知标记已读并打开页面", async () => {
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
    fireEvent.click(await screen.findByRole("button", { name: "通知（1 条未读）" }))
    fireEvent.click(await screen.findByText("小红评论了你的文章"))
    expect(source.markRead).toHaveBeenCalledWith(["n2"])
    await screen.findByText("article page")
    fireEvent.click(await screen.findByRole("button", { name: "通知" }))
    expect((await screen.findByRole("button", { name: "全部已读" })).hasAttribute("disabled")).toBe(true)
  })
})

/** 登记一个操作，并在页面中显示它的进度。 */
function Start({ open }: { open: OperationStream }) {
  const app = useApp()
  useEffect(() => trackOperation(app, { id: "op1", title: "批量发布 4 篇文章", open }), [app, open])
  return <OperationProgress id="op1" />
}

describe("ActivityTimeline", () => {
  it("从新到旧显示发生了什么、谁、代为操作的人；加载更多；点击打开页面", async () => {
    const list = vi.fn(async (before?: string) =>
      before
        ? [{ id: "a1", text: "创建了文章《你好》", created_at: "2026-11-14T08:00:00Z" }]
        : [
            {
              id: "a2",
              text: "发布了文章《你好》",
              actor: "小明",
              impersonator: "客服小李",
              created_at: "2026-11-15T08:00:00Z",
              href: "/articles/1",
            },
          ],
    )
    await mount(() => <ActivityTimeline source={{ list, pageSize: 1 }} />)
    await screen.findByText("发布了文章《你好》")
    expect(screen.getByText(/小明 · （客服小李 代为操作）/)).toBeTruthy()
    fireEvent.click(screen.getByRole("button", { name: "加载更多" }))
    await screen.findByText("创建了文章《你好》")
    expect(screen.getByText(/^系统 · /)).toBeTruthy() // 没有操作人的是系统的操作
    fireEvent.click(screen.getByRole("button", { name: "发布了文章《你好》" }))
    await screen.findByText("article page")
  })

  it("读取失败时显示，点击重试；没有记录", async () => {
    let fail = true
    const list = vi.fn(async () => {
      if (fail) throw { status: 403, fields: [] }
      return []
    })
    await mount(() => <ActivityTimeline source={{ list }} />)
    const retry = await screen.findByRole("button", { name: "重试" })
    fail = false
    fireEvent.click(retry)
    await screen.findByText("还没有操作记录")
  })
})

describe("后台操作", () => {
  it("任务中心列出操作和进度，结束后可以移除；页面中的进度", async () => {
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
    const popover = await screen.findByRole("dialog")
    expect(within(popover).getByText("批量发布 4 篇文章")).toBeTruthy()

    await act(async () => release())
    await waitFor(() => expect(screen.getAllByText("已完成 · 4/4 · 失败 1").length).toBe(2))
    fireEvent.click(within(popover).getByRole("button", { name: "移除 批量发布 4 篇文章" }))
    await waitFor(() => expect(screen.queryByRole("button", { name: /后台任务/ })).toBeNull())
  })
})

describe("Markdown", () => {
  it("行内代码保持行内，代码块用 CodeBlock，带复制按钮", async () => {
    render(<Markdown>{"用 `useDebounce`：\n\n```ts\nconst x = 1\n```\n\n- 一\n- 二"}</Markdown>)
    expect((await screen.findByText("useDebounce")).tagName).toBe("CODE")
    const block = document.querySelector("[data-slot=code-block]") as HTMLElement
    expect(block).toBeTruthy()
    expect(within(block).getByText("ts")).toBeTruthy()
    await waitFor(() => expect(block.textContent).toContain("const x = 1"))
    expect(screen.getAllByRole("listitem")).toHaveLength(2)
  })
})

/** Vercel AI SDK 的 UI 消息流，每一步包在 start-step 和 finish-step 之间。 */
function uiStream(chunks: object[], finishReason?: string): Response {
  const all = [
    { type: "start", messageId: "a1" },
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
            [
              { type: "tool-input-available", toolCallId: "r1", toolName: "refund_order", input: { order_id: "A1" } },
              { type: "tool-approval-request", approvalId: "r1", toolCallId: "r1" },
            ],
            "tool-calls",
          )
        }
        return uiStream([
          { type: "tool-output-available", toolCallId: "r1", output: { ok: true } },
          { type: "text-start", id: "t2" },
          { type: "text-delta", id: "t2", delta: "已退款" },
          { type: "text-end", id: "t2" },
        ])
      }),
    )
    await mount(() => <AssistantChat api="/api/chat" id="c1" toolTitles={{ refund_order: "退款" }} />)
    expect((await screen.findByText("订单 A1")).getAttribute("data-streamdown")).toBe("strong")
    expect(screen.getByText("顺便查 A2")).toBeTruthy()
    expect(screen.getByText("A2 还在打包")).toBeTruthy()

    const input = screen.getByRole("textbox", { name: "消息" })
    fireEvent.change(input, { target: { value: "给 A1 退款" } })
    fireEvent.keyDown(input, { key: "Enter" })
    const title = await screen.findByText("需要你确认：退款")
    const item = title.closest("[data-slot=item]") as HTMLElement
    expect(within(item).getByText(/"order_id": "A1"/)).toBeTruthy()
    expect(screen.getByRole("textbox", { name: "消息" }).hasAttribute("disabled")).toBe(true)
    fireEvent.click(within(item).getByRole("button", { name: "批准" }))
    await screen.findByText("已退款")
    expect(screen.queryByText("需要你确认：退款")).toBeNull()
    expect(screen.getByText("退款")).toBeTruthy() // 工具调用的一行
    const approval = (posts[1] as { message: { parts: { approval?: { approved: boolean } }[] } }).message.parts
    expect(approval.some((p) => p.approval?.approved === true)).toBe(true)
  })

  it("附件：选择、预览、移除，发送后显示在用户消息中；不支持的文件给出原因；历史中的附件照常显示", async () => {
    const posts: { message: { parts: { type: string; url?: string; filename?: string }[] } }[] = []
    vi.spyOn(URL, "createObjectURL").mockImplementation(() => "blob:preview")
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {})
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        if (url.endsWith("/messages")) {
          return Response.json([
            {
              id: "u0",
              role: "user",
              parts: [{ type: "file", mediaType: "application/pdf", filename: "合同.pdf", url: "https://x/合同.pdf" }],
            },
          ])
        }
        if (url.endsWith("/stream")) return new Response(null, { status: 204 })
        posts.push(JSON.parse(String(init?.body)))
        return uiStream([
          { type: "text-start", id: "t1" },
          { type: "text-delta", id: "t1", delta: "是一只猫" },
          { type: "text-end", id: "t1" },
        ])
      }),
    )
    await mount(() => <AssistantChat api="/api/chat" id="c1" />)
    const old = await screen.findByText("合同.pdf")
    expect(old.closest("a")?.getAttribute("href")).toBe("https://x/合同.pdf")

    const picker = screen.getByLabelText("选择文件") as HTMLInputElement
    expect(picker.accept).toBe("image/*,application/pdf")
    const cat = new File(["PNG"], "cat.png", { type: "image/png" })
    const notes = new File(["hi"], "notes.txt", { type: "text/plain" })
    fireEvent.change(picker, { target: { files: [cat, notes] } })
    expect((await screen.findByAltText("cat.png")).getAttribute("src")).toBe("blob:preview")
    expect(screen.getByRole("alert").textContent).toBe("不支持这种文件：notes.txt")

    // 移除后再选，只有文件也可以发送。
    fireEvent.click(screen.getByRole("button", { name: "移除 cat.png" }))
    expect(screen.queryByAltText("cat.png")).toBeNull()
    expect(screen.getByRole("button", { name: "发送" }).hasAttribute("disabled")).toBe(true)
    fireEvent.change(picker, { target: { files: [cat] } })
    await screen.findByAltText("cat.png")
    fireEvent.click(screen.getByRole("button", { name: "发送" }))
    await screen.findByText("是一只猫")
    expect(posts[0]?.message.parts).toEqual([
      { type: "file", mediaType: "image/png", filename: "cat.png", url: `data:image/png;base64,${btoa("PNG")}` },
    ])
    // 发送后待发送的附件清空，用户消息中显示图片。
    expect(screen.queryByRole("button", { name: "移除 cat.png" })).toBeNull()
    expect(screen.getByAltText("cat.png").getAttribute("src")).toBe(`data:image/png;base64,${btoa("PNG")}`)
  })

  it("attachments={false} 时没有附件按钮", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) =>
        String(input).endsWith("/messages") ? Response.json([]) : new Response(null, { status: 204 }),
      ),
    )
    await mount(() => <AssistantChat api="/api/chat" id="c1" attachments={false} />)
    await screen.findByRole("textbox", { name: "消息" })
    expect(screen.queryByRole("button", { name: "添加附件" })).toBeNull()
  })

  it("服务端没有收下消息时，输入框恢复原文，显示原因，对话中不留下这条消息", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith("/messages")) return Response.json([])
        if (url.endsWith("/stream")) return new Response(null, { status: 204 })
        return new Response(
          JSON.stringify({ status: 409, code: "ai.awaiting_approval", detail: "有工具调用等待审批" }),
          {
            status: 409,
            headers: { "Content-Type": "application/problem+json" },
          },
        )
      }),
    )
    await mount(() => <AssistantChat api="/api/chat" id="c1" />)
    const input = await screen.findByRole("textbox", { name: "消息" })
    fireEvent.change(input, { target: { value: "你好" } })
    fireEvent.keyDown(input, { key: "Enter" })
    expect((await screen.findByRole("alert")).textContent).toBe("有工具调用等待审批")
    await waitFor(() =>
      expect((screen.getByRole("textbox", { name: "消息" }) as HTMLTextAreaElement).value).toBe("你好"),
    )
    expect(screen.queryByText("你好", { selector: "[data-slot=bubble-content]" })).toBeNull()
  })
})
