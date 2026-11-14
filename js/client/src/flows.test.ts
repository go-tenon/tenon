import { describe, expect, it, vi } from "vitest"
import { chatControls, chatTransport } from "./chat"
import { describeError } from "./errors"
import type { FetchLike, RequestInitLike, ResponseLike } from "./fetch"
import { isFinished, type OperationLike, progressRatio, watchOperation } from "./operation"
import { realtimeAuth } from "./realtime"
import { createSession, type SessionBody } from "./session"
import { UploadError, uploadFile } from "./upload"

const body = (access: string): SessionBody => ({ access_token: access, expires_at: "2026-11-11T08:10:00Z" })

function response(status: number, json: unknown = {}, headers: Record<string, string> = {}): ResponseLike {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: (name) => headers[name] ?? headers[name.toLowerCase()] ?? null },
    json: async () => json,
    text: async () => JSON.stringify(json),
  }
}

/** 记录请求的假 fetch：handler 按请求返回响应。 */
function fakeFetch(handler: (url: string, init: RequestInitLike) => ResponseLike) {
  const calls: { url: string; init: RequestInitLike }[] = []
  const fetch: FetchLike = async (url, init = {}) => {
    calls.push({ url, init })
    return handler(url, init)
  }
  return { fetch, calls }
}

describe("session.fetch", () => {
  it("带上 access token；401 时刷新一次再重试", async () => {
    const refresh = vi.fn().mockResolvedValue(body("a2"))
    const session = createSession({ refresh })
    await session.signIn(body("a1"))
    const { fetch, calls } = fakeFetch((_, init) =>
      response((init.headers as Record<string, string>).Authorization === "Bearer a2" ? 200 : 401),
    )
    const res = await session.fetch(fetch)("/api/x", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
    })
    expect(res.status).toBe(200)
    expect(calls.map((c) => (c.init.headers as Record<string, string>).Authorization)).toEqual([
      "Bearer a1",
      "Bearer a2",
    ])
    expect((calls[1].init.headers as Record<string, string>)["Content-Type"]).toBe("application/json")
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it("刷新失败时返回原来的 401", async () => {
    const session = createSession({ refresh: vi.fn().mockRejectedValue({ status: 401, fields: [] }) })
    const { fetch, calls } = fakeFetch(() => response(401))
    expect((await session.fetch(fetch)("/api/x")).status).toBe(401)
    expect(calls).toHaveLength(1)
  })
})

describe("realtimeAuth", () => {
  it("连接时用当前的 token，过期后通过会话刷新，失败时返回空字符串", async () => {
    const refresh = vi.fn().mockResolvedValueOnce(body("a2")).mockRejectedValueOnce({ status: 401, fields: [] })
    const session = createSession({ refresh })
    await session.signIn(body("a1"))
    const auth = realtimeAuth(session)
    expect(auth.token).toBe("a1")
    expect(await auth.getToken()).toBe("a2")
    expect(await auth.getToken()).toBe("")
  })
})

describe("watchOperation", () => {
  const op = (status: string, processed: number): OperationLike => ({
    id: "op1",
    status,
    total: 4,
    processed,
    failed: 0,
  })
  async function* events(...items: OperationLike[]) {
    for (const data of items) yield { event: "progress", data }
  }

  it("读取进度直到结束；连接断开后重连", async () => {
    const streams = [events(op("running", 1), op("running", 2)), events(op("running", 2), op("completed", 4))]
    const open = vi.fn(() => streams.shift() ?? events())
    const seen: number[] = []
    const done = await watchOperation(open, { onProgress: (o) => seen.push(o.processed), retryDelay: 1 })
    expect(done.status).toBe("completed")
    expect(seen).toEqual([1, 2, 2, 4])
    expect(open).toHaveBeenCalledTimes(2)
  })

  it("4xx 错误不重连，网络错误超过次数后放弃", async () => {
    // 打开时就失败的事件流。
    const failing = (err: unknown) =>
      async function* (): AsyncGenerator<{ event: string; data: OperationLike }> {
        yield* []
        throw err
      }
    await expect(watchOperation(failing({ status: 404, fields: [] }), { retryDelay: 1 })).rejects.toMatchObject({
      status: 404,
    })
    const open = vi.fn(failing(new TypeError("offline")))
    await expect(watchOperation(open, { retryDelay: 1, maxRetries: 2 })).rejects.toBeInstanceOf(TypeError)
    expect(open).toHaveBeenCalledTimes(3)
  })

  it("isFinished 与 progressRatio", () => {
    expect(isFinished(op("failed", 0))).toBe(true)
    expect(isFinished(op("running", 0))).toBe(false)
    expect(progressRatio(op("running", 1))).toBe(0.25)
    expect(progressRatio({ total: 0, processed: 3 })).toBeUndefined()
  })
})

describe("chat", () => {
  it("chatTransport 只发送最后一条消息", () => {
    const t = chatTransport("/api/assistant/chat")
    expect(t.api).toBe("/api/assistant/chat")
    expect(t.prepareSendMessagesRequest({ id: "c1", messages: [{ id: "m1" }, { id: "m2" }] })).toEqual({
      body: { id: "c1", message: { id: "m2" } },
    })
  })

  it("chatControls 按约定的路径请求，失败时抛出可以分类的错误", async () => {
    const { fetch, calls } = fakeFetch((url) =>
      url.endsWith("/steer")
        ? response(409, { status: 409, code: "ai.finished", detail: "回答已经结束" })
        : response(200, [{ id: "m1" }]),
    )
    const c = chatControls("/api/assistant/chat", fetch)
    await c.stop("c 1")
    expect(await c.messages("c1")).toEqual([{ id: "m1" }])
    const err = await c.steer("c1", "再短一点").catch((e) => e)
    expect(describeError(err)).toMatchObject({ kind: "business", code: "ai.finished", message: "回答已经结束" })
    expect(calls.map((x) => `${x.init.method} ${x.url}`)).toEqual([
      "POST /api/assistant/chat/c%201/stop",
      "GET /api/assistant/chat/c1/messages",
      "POST /api/assistant/chat/c1/steer",
    ])
    expect(calls[2].init.body).toBe(JSON.stringify({ text: "再短一点" }))
  })
})

describe("uploadFile", () => {
  /** 大小为 size 的假文件，slice 返回描述范围的字符串。 */
  const file = (size: number) => ({
    size,
    type: "application/pdf",
    name: "a.pdf",
    slice: (s?: number, e?: number) => `${s}-${e}`,
  })
  const signed = (url: string) => ({ url, method: "PUT", headers: { "Content-Type": ["application/pdf"] } })

  type Stored = { id: string; parts?: { number: number; etag: string }[] }

  function steps() {
    return {
      prepare: vi.fn(
        async (_input: { filename: string; content_type?: string; size: number }): Promise<Stored> => ({ id: "f1" }),
      ),
      sign: vi.fn(async () => signed("https://s3/f1")),
      multipart: {
        create: vi.fn(async () => ({ upload_id: "u1", part_size: 10, part_count: 3 })),
        signPart: vi.fn(async (_: string, __: string, n: number) => signed(`https://s3/f1?part=${n}`)),
        complete: vi.fn(
          async (id: string, _: string, parts: { number: number; etag: string }[]): Promise<Stored> => ({ id, parts }),
        ),
        abort: vi.fn(async () => {}),
      },
    }
  }

  it("小文件单次上传", async () => {
    const s = steps()
    const { fetch, calls } = fakeFetch(() => response(200))
    const progress: number[] = []
    const f = await uploadFile(file(5), s, { fetch, onProgress: (p) => progress.push(p.loaded) })
    expect(f.id).toBe("f1")
    expect(s.prepare).toHaveBeenCalledWith({ filename: "a.pdf", content_type: "application/pdf", size: 5 })
    expect(calls).toHaveLength(1)
    expect(calls[0]).toMatchObject({
      url: "https://s3/f1",
      init: { method: "PUT", headers: { "Content-Type": "application/pdf" } },
    })
    expect(progress).toEqual([5])
    expect(s.multipart.create).not.toHaveBeenCalled()
  })

  it("大文件分片上传：每片带 ETag，按顺序合并", async () => {
    const s = steps()
    const { fetch, calls } = fakeFetch((url) => response(200, {}, { ETag: `"e${url.split("=")[1]}"` }))
    const progress: number[] = []
    const f = await uploadFile(file(25), s, {
      fetch,
      multipartThreshold: 8,
      concurrency: 2,
      onProgress: (p) => progress.push(p.loaded),
    })
    expect(f.parts).toEqual([
      { number: 1, etag: '"e1"' },
      { number: 2, etag: '"e2"' },
      { number: 3, etag: '"e3"' },
    ])
    expect(calls.map((c) => c.init.body).sort()).toEqual(["0-10", "10-20", "20-25"])
    expect(progress.at(-1)).toBe(25)
    expect(s.multipart.abort).not.toHaveBeenCalled()
  })

  it("一片失败时重新签名并重传", async () => {
    const s = steps()
    let failures = 1
    const { fetch } = fakeFetch((url) =>
      url.endsWith("part=2") && failures-- > 0 ? response(500) : response(200, {}, { ETag: '"x"' }),
    )
    const f = await uploadFile(file(25), s, { fetch, multipartThreshold: 8, concurrency: 1 })
    expect(f.parts).toHaveLength(3)
    expect(s.multipart.signPart.mock.calls.map((c) => c[2])).toEqual([1, 2, 2, 3])
    expect(s.multipart.abort).not.toHaveBeenCalled()
  })

  it("分片失败时放弃分片会话", async () => {
    const s = steps()
    const { fetch } = fakeFetch((url) => (url.endsWith("part=2") ? response(403) : response(200, {}, { ETag: '"x"' })))
    const err = await uploadFile(file(25), s, { fetch, multipartThreshold: 8, concurrency: 1 }).catch((e) => e)
    expect(err).toBeInstanceOf(UploadError)
    expect(err).toMatchObject({ status: 403, part: 2 })
    expect(s.multipart.abort).toHaveBeenCalledWith("f1", "u1")
    expect(s.multipart.complete).not.toHaveBeenCalled()
  })

  it("对象存储没有暴露 ETag 时报错并放弃", async () => {
    const s = steps()
    const { fetch } = fakeFetch(() => response(200))
    await expect(uploadFile(file(25), s, { fetch, multipartThreshold: 8 })).rejects.toThrow(/ETag/)
    expect(s.multipart.abort).toHaveBeenCalled()
  })
})
