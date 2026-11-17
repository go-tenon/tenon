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
import { ActivityTimeline, NotificationBell, OperationCenter, OperationProgress } from "./features"
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
  vi.restoreAllMocks()
})

async function mount(children: () => ReactNode, path = "/") {
  const session = createSession({ load: async () => ({ account: "u1" }) })
  await session.signIn({ account: "u1" })
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

describe("ActivityTimeline", () => {
  it("从新到旧显示发生了什么、谁、代为操作的人；加载更多；点击打开页面", async () => {
    const list = vi.fn(async (before?: string) =>
      before
        ? [{ id: "a1", text: "创建了文章《你好》", actor: "小明", created_at: "2026-11-14T08:00:00Z" }]
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
    expect(list).toHaveBeenLastCalledWith("a2")
    fireEvent.click(screen.getByText("发布了文章《你好》"))
    await screen.findByText("article page")
  })

  it("没有记录；读取失败时显示，点击重试", async () => {
    let fail = true
    const list = vi.fn(async () => {
      if (fail) throw { status: 403, fields: [] } // 例如没有权限查看这段历史：4xx 不自动重试
      return []
    })
    await mount(() => <ActivityTimeline source={{ list }} />)
    const retry = await screen.findByRole("button", { name: /重\s*试/ }) // antd 在两个汉字的按钮中间加空格
    fail = false
    fireEvent.click(retry)
    await screen.findByText("还没有操作记录")
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

  it("附件：选择、预览、移除，只有附件也可以发送，发送后显示在用户消息中", async () => {
    const posts: { message: { parts: { type: string; url?: string }[] } }[] = []
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
        return uiStream("a1", [
          { type: "text-start", id: "t1" },
          { type: "text-delta", id: "t1", delta: "是一只猫" },
          { type: "text-end", id: "t1" },
        ])
      }),
    )
    await mount(() => <AssistantChat api="/api/chat" id="c1" height={600} />)
    expect(await screen.findByText("合同")).toBeTruthy() // FileCard 把扩展名单独显示

    const picker = screen.getByLabelText("选择文件") as HTMLInputElement
    expect(picker.accept).toBe("image/*,application/pdf")
    const cat = new File(["PNG"], "cat.png", { type: "image/png" })
    fireEvent.change(picker, { target: { files: [cat, new File(["hi"], "notes.txt", { type: "text/plain" })] } })
    expect((await screen.findByRole("alert")).textContent).toContain("不支持这种文件：notes.txt")
    // 图片的预览：FileCard 显示缩略图。
    await waitFor(() => expect(document.querySelector('.ant-sender-header img[src="blob:preview"]')).not.toBeNull())

    const send = () => document.querySelector(".ant-sender-actions-btn") as HTMLButtonElement
    expect(send().disabled).toBe(false) // 只有附件也可以发送
    fireEvent.click(send())
    await screen.findByText("是一只猫")
    expect(posts[0]?.message.parts).toEqual([
      { type: "file", mediaType: "image/png", filename: "cat.png", url: `data:image/png;base64,${btoa("PNG")}` },
    ])
    // 发送后待发送的附件清空，用户消息中显示图片。
    await waitFor(() => expect(document.querySelector('.ant-sender-header img[src="blob:preview"]')).toBeNull())
    expect(document.querySelector(`.ant-bubble img[src="data:image/png;base64,${btoa("PNG")}"]`)).not.toBeNull()
  })

  it("attachments={false} 时没有附件按钮", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) =>
        String(input).endsWith("/messages") ? Response.json([]) : new Response(null, { status: 204 }),
      ),
    )
    await mount(() => <AssistantChat api="/api/chat" id="c1" height={600} attachments={false} />)
    await screen.findByRole("textbox")
    expect(screen.queryByRole("button", { name: "添加附件" })).toBeNull()
    expect(screen.queryByLabelText("选择文件")).toBeNull()
  })
})
