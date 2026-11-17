// @vitest-environment happy-dom
// 通知、后台操作、操作日志和 AI 对话的模型。
import { createSession, type OperationLike, type Session } from "@go-tenon/client"
import { createMemoryHistory, createRootRouteWithContext, Outlet } from "@tanstack/react-router"
import { act, cleanup, render, screen, waitFor } from "@testing-library/react"
import type { UIMessage } from "ai"
import { type ReactNode, useEffect } from "react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { type Activity, type ActivityItem, useActivity } from "./activity"
import { type App, AppRoot, createApp, type RouterContext, useApp } from "./app"
import {
  type AssistantChat,
  type AssistantComposer,
  AttachmentError,
  type Attachments,
  useAssistantChat,
  useAssistantComposer,
  useAttachments,
} from "./chat"
import { type NotificationItem, type Notifications, type NotificationsSource, useNotifications } from "./notifications"
import { dismissOperation, type TrackedOperation, trackOperation, useOperations } from "./operations"
import { type Export, type Import, useExport, useImport } from "./transfer"

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

/** 渲染 children，返回应用和会话。 */
async function mount(children: () => ReactNode) {
  const session: Session = createSession({ load: async () => ({ account: "u1" }) })
  await session.signIn({ account: "u1" })
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

describe("useActivity", () => {
  const record = (i: number): ActivityItem => ({
    id: `a${i}`,
    text: `第 ${i} 条`,
    actor: "小明",
    created_at: "2026-11-16T08:00:00Z",
  })

  it("从新到旧分页读取：一页满了才有下一页，before 是上一页最后一条的 ID", async () => {
    const list = vi.fn(async (before?: string) =>
      before === undefined ? [record(4), record(3)] : before === "a3" ? [record(2)] : [],
    )
    let a: Activity | undefined
    await mount(() => (
      <Probe use={() => useActivity({ list, key: ["article", "1"], pageSize: 2 })} onValue={(v) => (a = v)} />
    ))
    await waitFor(() => expect(a?.items.map((i) => i.id)).toEqual(["a4", "a3"]))
    expect(a?.hasMore).toBe(true)
    act(() => a?.loadMore())
    await waitFor(() => expect(a?.items.map((i) => i.id)).toEqual(["a4", "a3", "a2"]))
    expect(list).toHaveBeenLastCalledWith("a3")
    expect(a?.hasMore).toBe(false)
  })

  it("refresh 重新读取第一页", async () => {
    let n = 1
    const list = vi.fn(async () => [record(n)])
    let a: Activity | undefined
    await mount(() => <Probe use={() => useActivity({ list })} onValue={(v) => (a = v)} />)
    await waitFor(() => expect(a?.items[0]?.text).toBe("第 1 条"))
    n = 2
    await act(() => a?.refresh() ?? Promise.resolve())
    await waitFor(() => expect(a?.items[0]?.text).toBe("第 2 条"))
  })
})

/** 按顺序推送进度，最后一个事件之后结束。 */
function stream(...ops: Partial<OperationLike>[]) {
  return async function* () {
    for (const op of ops) {
      yield { event: "progress", data: { id: "op", status: "running", total: 0, processed: 0, failed: 0, ...op } }
    }
  }
}

describe("useImport、useExport", () => {
  it("导入：上传、发起、读取进度，结束后可以下载没有导入的行；进度出现在操作中心", async () => {
    const failures = vi.fn(async () => {})
    const upload = vi.fn(async (_: File, o: { onProgress?: (r: number) => void }) => {
      o.onProgress?.(0.5)
      return "file1"
    })
    const start = vi.fn(async () => ({ id: "imp1" }))
    let imp: Import | undefined
    let ops: readonly TrackedOperation[] = []
    await mount(() => (
      <>
        <Probe
          use={() =>
            useImport({
              title: "导入文章",
              upload,
              start,
              progress: () =>
                stream({ total: 10, processed: 5 }, { status: "completed", total: 10, processed: 10, failed: 2 }),
              failures,
            })
          }
          onValue={(v) => (imp = v)}
        />
        <Probe use={useOperations} onValue={(v) => (ops = v)} />
      </>
    ))
    await waitFor(() => expect(imp?.phase).toBe("idle"))
    await act(() => imp?.start(new File(["a"], "articles.xlsx")) ?? Promise.resolve())
    expect(start).toHaveBeenCalledWith("file1")
    await waitFor(() => expect(imp?.phase).toBe("completed"))
    expect(imp?.operation?.operation?.failed).toBe(2)
    expect(ops[0]?.title).toBe("导入文章")
    await act(() => imp?.downloadFailures?.() ?? Promise.resolve())
    expect(failures).toHaveBeenCalledWith("imp1")
    act(() => imp?.reset())
    expect(imp?.phase).toBe("idle")
  })

  it("导入：全部成功时没有下载失败行；上传出错时是 failed 并带着错误", async () => {
    let imp: Import | undefined
    let fail = false
    await mount(() => (
      <Probe
        use={() =>
          useImport({
            upload: async () => {
              if (fail) throw new Error("文件太大")
              return "f"
            },
            start: async () => ({ id: "imp2" }),
            progress: () => stream({ status: "completed", total: 1, processed: 1 }),
            failures: async () => {},
          })
        }
        onValue={(v) => (imp = v)}
      />
    ))
    await waitFor(() => expect(imp).toBeDefined())
    await act(() => imp?.start(new File(["a"], "a.csv")) ?? Promise.resolve())
    await waitFor(() => expect(imp?.phase).toBe("completed"))
    expect(imp?.downloadFailures).toBeUndefined()
    act(() => imp?.reset())
    fail = true
    await act(() => imp?.start(new File(["a"], "a.csv")) ?? Promise.resolve())
    expect(imp?.phase).toBe("failed")
    expect(imp?.error).toEqual(new Error("文件太大"))
  })

  it("导出：完成后取进度事件中的下载地址；失败时是 failed", async () => {
    let exp: Export | undefined
    await mount(() => (
      <Probe
        use={() =>
          useExport({
            start: async () => ({ id: "exp1" }),
            progress: () =>
              stream({ total: 3, processed: 1 }, {
                status: "completed",
                total: 3,
                processed: 3,
                url: "/storage/x.xlsx",
                filename: "articles.xlsx",
              } as Partial<OperationLike>),
          })
        }
        onValue={(v) => (exp = v)}
      />
    ))
    await waitFor(() => expect(exp).toBeDefined())
    await act(() => exp?.start() ?? Promise.resolve())
    await waitFor(() => expect(exp?.phase).toBe("completed"))
    expect(exp?.file).toEqual({ url: "/storage/x.xlsx", filename: "articles.xlsx" })

    let failed: Export | undefined
    cleanup()
    await mount(() => (
      <Probe
        use={() =>
          useExport({
            start: async () => ({ id: "exp2" }),
            progress: () => stream({ status: "failed", error: "export.too_large" }),
          })
        }
        onValue={(v) => (failed = v)}
      />
    ))
    await waitFor(() => expect(failed).toBeDefined())
    await act(() => failed?.start() ?? Promise.resolve())
    await waitFor(() => expect(failed?.phase).toBe("failed"))
    expect(failed?.file).toBeUndefined()
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

function Chat({ onValue, upload }: { onValue: (c: AssistantChat) => void; upload?: (f: File) => Promise<string> }) {
  const chat = useAssistantChat({ api: "/api/chat", id: "c1", messages: history, attachments: { upload } })
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
  type Call = { url: string; method: string; body?: unknown; auth?: string | null; credentials?: RequestCredentials }

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
          credentials: init?.credentials,
        }
        calls.push(call)
        return handler(call)
      }),
    )
    return calls
  }

  it("发送：只发送最后一条消息、同源发送 cookie，不带 token；插话是回答中的 data-steering", async () => {
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
    expect(post).toMatchObject({ url: "/api/chat", auth: null, credentials: "same-origin" })
    expect((post?.body as { message?: UIMessage } | undefined)?.message?.parts).toEqual([
      { type: "text", text: "讲个故事" },
    ])
  })

  it("带文件发送：文件片段在文字前面，默认是 data URL；设置了 upload 时放链接；可以只发文件", async () => {
    const calls = server((c) => {
      if (c.url.endsWith("/stream")) return new Response(null, { status: 204 })
      return uiStream([{ type: "start", messageId: "a1" }, { type: "finish" }])
    })
    const png = new File(["PNG"], "cat.png", { type: "image/png" })
    let chat: AssistantChat | undefined
    await mount(() => <Chat onValue={(c) => (chat = c)} />)
    await waitFor(() => expect(chat).toBeDefined())
    await act(() => chat?.send("这是什么", [png]) ?? Promise.resolve())
    const posts = () => calls.filter((c) => c.method === "POST").map((c) => (c.body as { message: UIMessage }).message)
    await waitFor(() => expect(posts()).toHaveLength(1))
    expect(posts()[0]?.parts).toEqual([
      { type: "file", mediaType: "image/png", filename: "cat.png", url: `data:image/png;base64,${btoa("PNG")}` },
      { type: "text", text: "这是什么" },
    ])
    await waitFor(() => expect(chat?.busy).toBe(false))
    await act(() => chat?.send("", [png]) ?? Promise.resolve())
    await waitFor(() => expect(posts()).toHaveLength(2))
    expect(posts()[1]?.parts.map((p) => p.type)).toEqual(["file"])

    // 不符合限制的文件不发送。
    const exe = new File(["MZ"], "a.exe", { type: "application/x-msdownload" })
    await expect(chat?.send("", [exe])).rejects.toMatchObject({ reason: "type" })
    expect(posts()).toHaveLength(2)
  })

  it("带文件发送：设置了 upload 时先上传，消息中放链接", async () => {
    const calls = server((c) => {
      if (c.url.endsWith("/stream")) return new Response(null, { status: 204 })
      return uiStream([{ type: "start", messageId: "a1" }, { type: "finish" }])
    })
    const upload = vi.fn(async (f: File) => `https://cdn.example.com/${f.name}`)
    let chat: AssistantChat | undefined
    await mount(() => <Chat upload={upload} onValue={(c) => (chat = c)} />)
    await waitFor(() => expect(chat).toBeDefined())
    await act(() => chat?.send("看看", [new File(["%PDF"], "a.pdf", { type: "application/pdf" })]) ?? Promise.resolve())
    const post = calls.find((c) => c.method === "POST")?.body as { message: UIMessage }
    expect(upload).toHaveBeenCalledOnce()
    expect(post.message.parts[0]).toEqual({
      type: "file",
      mediaType: "application/pdf",
      filename: "a.pdf",
      url: "https://cdn.example.com/a.pdf",
    })
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
    // 插话只能是文字：回答进行中带文件时不发送。
    await expect(chat?.send("看图", [new File(["PNG"], "a.png", { type: "image/png" })])).rejects.toBeInstanceOf(
      AttachmentError,
    )
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1)
    await act(() => chat?.send("补充一句") ?? Promise.resolve())
    expect(steer).toBe(1)
    expect(calls.filter((c) => c.method === "POST" && c.url === "/api/chat")).toHaveLength(2) // 改为发送新消息
    await act(() => chat?.stop() ?? Promise.resolve())
    expect(calls.some((c) => c.url === "/api/chat/c1/stop")).toBe(true)
  })
})

function Composer({ onValue }: { onValue: (c: AssistantComposer) => void }) {
  const upload = async (): Promise<string> => {
    throw new Error("上传失败")
  }
  const chat = useAssistantChat({ api: "/api/chat", id: "c1", messages: history, attachments: { upload } })
  const composer = useAssistantComposer(chat)
  useEffect(() => {
    onValue(composer)
  })
  return null
}

describe("useAssistantComposer", () => {
  it("发送失败（这里是上传失败）时恢复文字和附件，error 是原因", async () => {
    vi.spyOn(URL, "createObjectURL").mockImplementation(() => "blob:x")
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {})
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(null, { status: 204 })),
    )
    let c: AssistantComposer | undefined
    await mount(() => <Composer onValue={(v) => (c = v)} />)
    await waitFor(() => expect(c).toBeDefined())
    expect(c?.canSend).toBe(false)
    act(() => c?.setText("看图"))
    act(() => c?.attachments.add([new File(["PNG"], "a.png", { type: "image/png" })]))
    expect(c?.canSend).toBe(true)
    await act(() => c?.submit() ?? Promise.resolve())
    await waitFor(() => expect(c?.error).toBe("上传失败"))
    expect(c?.text).toBe("看图")
    expect(c?.attachments.files.map((f) => f.name)).toEqual(["a.png"])
  })
})

function ComposerChat({ onValue }: { onValue: (v: { chat: AssistantChat; composer: AssistantComposer }) => void }) {
  const chat = useAssistantChat({ api: "/api/chat", id: "c1", messages: history })
  const composer = useAssistantComposer(chat)
  useEffect(() => {
    onValue({ chat, composer })
  })
  return null
}

describe("发送失败", () => {
  it("服务端没有收下消息：撤回这条消息，恢复文字和附件，error 是原因", async () => {
    vi.spyOn(URL, "createObjectURL").mockImplementation(() => "blob:x")
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {})
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input).endsWith("/stream")) return new Response(null, { status: 204 })
        return new Response(
          JSON.stringify({ status: 409, code: "ai.awaiting_approval", detail: "有工具调用等待审批" }),
          {
            status: 409,
            headers: { "Content-Type": "application/problem+json" },
          },
        )
      }),
    )
    let v: { chat: AssistantChat; composer: AssistantComposer } | undefined
    await mount(() => <ComposerChat onValue={(x) => (v = x)} />)
    await waitFor(() => expect(v).toBeDefined())
    act(() => v?.composer.setText("看图"))
    act(() => v?.composer.attachments.add([new File(["PNG"], "a.png", { type: "image/png" })]))
    await act(() => v?.composer.submit() ?? Promise.resolve())
    await waitFor(() => expect(v?.composer.error).toBeDefined())
    expect(v?.composer.text).toBe("看图")
    expect(v?.composer.attachments.files.map((f) => f.name)).toEqual(["a.png"])
    expect(v?.chat.messages.map((m) => m.id)).toEqual(["u0"]) // 没发出去的消息撤回了
    expect(v?.composer.error).toBe("有工具调用等待审批")
  })

  it("回答开始后才出错：消息留在对话中，输入框不恢复", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input).endsWith("/stream")) return new Response(null, { status: 204 })
        const body = new ReadableStream({
          start(ctl) {
            ctl.enqueue(new TextEncoder().encode(`data: ${JSON.stringify({ type: "start", messageId: "a1" })}\n\n`))
            ctl.error(new TypeError("network error"))
          },
        })
        return new Response(body, {
          headers: { "Content-Type": "text/event-stream", "x-vercel-ai-ui-message-stream": "v1" },
        })
      }),
    )
    let v: { chat: AssistantChat; composer: AssistantComposer } | undefined
    await mount(() => <ComposerChat onValue={(x) => (v = x)} />)
    await waitFor(() => expect(v).toBeDefined())
    act(() => v?.composer.setText("讲个故事"))
    await act(() => v?.composer.submit() ?? Promise.resolve())
    await waitFor(() => expect(v?.chat.error).toBeDefined())
    expect(v?.composer.text).toBe("")
    expect(v?.chat.messages.map((m) => m.role)).toEqual(["user", "user"]) // 服务端收下了，消息不撤回
  })
})

describe("useAttachments", () => {
  it("加入符合限制的文件，拒绝其余的并给出原因；图片有预览，移除和清空时释放", async () => {
    const created: string[] = []
    const revoked: string[] = []
    vi.spyOn(URL, "createObjectURL").mockImplementation(() => {
      created.push(`blob:${created.length}`)
      return created[created.length - 1] as string
    })
    vi.spyOn(URL, "revokeObjectURL").mockImplementation((u) => void revoked.push(u))
    let a: Attachments | undefined
    render(
      <Probe use={() => useAttachments({ maxFiles: 3, maxFileSize: 10, maxTotalSize: 20 })} onValue={(v) => (a = v)} />,
    )
    const png = new File(["PNG"], "a.png", { type: "image/png" })
    const pdf = new File(["%PDF"], "b.pdf", { type: "application/pdf" })
    const big = new File(["x".repeat(11)], "big.png", { type: "image/png" })
    const exe = new File(["MZ"], "c.exe", { type: "application/x-msdownload" })

    act(() => a?.add([png, exe, pdf]))
    expect(a?.files).toEqual([png, pdf])
    expect(a?.items[0]?.preview).toBe("blob:0")
    expect(a?.items[1]?.preview).toBeUndefined()
    expect(a?.error).toBe("不支持这种文件：c.exe")

    act(() => a?.add([big]))
    expect(a?.error).toContain("big.png 超过了")
    act(() => a?.add([pdf, pdf]))
    expect(a?.files).toHaveLength(3)
    expect(a?.error).toBe("一次最多发送 3 个文件")

    act(() => a?.remove(a.items[0]?.id ?? ""))
    expect(revoked).toEqual(["blob:0"])
    expect(a?.files).toEqual([pdf, pdf])
    expect(a?.error).toBeUndefined()
    act(() => a?.clear())
    expect(a?.files).toEqual([])
  })

  it("accept 支持扩展名、大类和完整的 MIME 类型；没有类型的文件不加入", () => {
    vi.spyOn(URL, "createObjectURL").mockImplementation(() => "blob:x")
    let a: Attachments | undefined
    render(
      <Probe
        use={() => useAttachments({ accept: "image/*,.pdf,audio/mpeg", maxFiles: 10 })}
        onValue={(v) => (a = v)}
      />,
    )
    const file = (name: string, type: string) => new File(["x"], name, { type })
    act(() =>
      a?.add([
        file("a.PNG", "image/png"),
        file("A.PDF", "application/pdf"),
        file("a.mp3", "audio/mpeg"),
        file("a.txt", "text/plain"),
        file("b.pdf", ""),
      ]),
    )
    expect(a?.files.map((f) => f.name)).toEqual(["a.PNG", "A.PDF", "a.mp3"])
    expect(a?.error).toBe("不支持这种文件：a.txt") // 第一个不符合的原因
  })

  it("data URL 发送时检查总大小；设置了 upload 时不检查", () => {
    vi.spyOn(URL, "createObjectURL").mockImplementation(() => "blob:x")
    const f = (n: number) => new File(["x".repeat(n)], `${n}.png`, { type: "image/png" })
    let a: Attachments | undefined
    let b: Attachments | undefined
    render(<Probe use={() => useAttachments({ maxTotalSize: 10 })} onValue={(v) => (a = v)} />)
    render(
      <Probe
        use={() => useAttachments({ maxTotalSize: 10, upload: async () => "https://x" })}
        onValue={(v) => (b = v)}
      />,
    )
    act(() => a?.add([f(8), f(8)]))
    act(() => b?.add([f(8), f(8)]))
    expect(a?.files).toHaveLength(1)
    expect(a?.error).toBe("文件总共不能超过 0 MiB")
    expect(b?.files).toHaveLength(2)
  })
})
