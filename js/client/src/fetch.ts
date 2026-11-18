// fetch 的最小形状。@go-tenon/client 不依赖 DOM 的类型，浏览器、React Native、Node 中的 fetch、Response、File
// 都符合这些形状，可以直接传入。

/** 请求的选项：fetch 的 RequestInit 中用到的部分。 */
export interface RequestInitLike {
  method?: string
  headers?: unknown
  body?: unknown
  signal?: unknown
  credentials?: unknown
}

/** 响应：fetch 的 Response 中用到的部分。 */
export interface ResponseLike {
  ok: boolean
  status: number
  headers: { get(name: string): string | null }
  json(): Promise<unknown>
  text(): Promise<string>
}

/**
 * fetch 函数。用方法的写法声明，参数按双向兼容检查：浏览器的 fetch（参数是 RequestInfo、RequestInit）可以直接赋给它。
 */
export type FetchLike = {
  fetch(url: string, init?: RequestInitLike): Promise<ResponseLike>
}["fetch"]

/** 文件或 Blob：上传用到的部分。 */
export interface BlobLike {
  readonly size: number
  readonly type: string
  readonly name?: string
  slice(start?: number, end?: number): unknown
}

/** 返回全局的 fetch；没有时抛出错误，提示传入一个。 */
export function globalFetch(): FetchLike {
  const f = (globalThis as { fetch?: FetchLike }).fetch
  if (!f) throw new Error("@go-tenon/client: no global fetch; pass one in the options")
  return f.bind(globalThis)
}

/** 不经过生成客户端的请求失败时抛出的错误，形状与生成客户端的 ApiError 相同，describeError 照常分类。 */
export class HttpError extends Error {
  readonly status: number
  readonly code?: string
  readonly detail?: string
  readonly fields: { location?: string; message: string; code?: string; value?: unknown }[]
  readonly params: Record<string, unknown>
  readonly requestId?: string
  readonly retryAfter?: number

  constructor(status: number, body: Record<string, unknown> | undefined, headers?: ResponseLike["headers"]) {
    const detail = typeof body?.detail === "string" ? body.detail : undefined
    super(detail ?? `HTTP ${status}`)
    this.name = "HttpError"
    this.status = status
    this.code = typeof body?.code === "string" ? body.code : undefined
    this.detail = detail
    this.fields = Array.isArray(body?.errors) ? (body.errors as HttpError["fields"]) : []
    const params = body?.params
    this.params =
      typeof params === "object" && params !== null && !Array.isArray(params) ? (params as Record<string, unknown>) : {}
    this.requestId = headers?.get("X-Request-ID") ?? undefined
    const retryAfter = Number(headers?.get("Retry-After") ?? "")
    this.retryAfter = retryAfter > 0 ? retryAfter : undefined
  }
}

/** 响应不是 2xx 时，读出 Problem Details 并抛出 HttpError。 */
export async function ensureOK(res: ResponseLike): Promise<ResponseLike> {
  if (res.ok) return res
  let body: Record<string, unknown> | undefined
  try {
    body = (await res.json()) as Record<string, unknown>
  } catch {
    body = undefined
  }
  throw new HttpError(res.status, body, res.headers)
}

/** 等待 ms 毫秒；signal 取消时提前结束。 */
export function sleep(ms: number, signal?: AbortSignalLike): Promise<void> {
  const timers = globalThis as unknown as {
    setTimeout(cb: () => void, ms: number): unknown
    clearTimeout(id: unknown): void
  }
  return new Promise((resolve) => {
    const id = timers.setTimeout(resolve, ms)
    signal?.addEventListener?.("abort", () => {
      timers.clearTimeout(id)
      resolve()
    })
  })
}

/** AbortSignal 中用到的部分。 */
export interface AbortSignalLike {
  readonly aborted: boolean
  addEventListener?(type: "abort", listener: () => void): void
}

/** 取消时抛出的错误，name 是 AbortError，与 fetch 相同。 */
export function abortError(): Error {
  return Object.assign(new Error("aborted"), { name: "AbortError" })
}
