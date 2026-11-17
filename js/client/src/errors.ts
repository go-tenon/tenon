// 错误：按客户端协议分类生成客户端抛出的错误，把字段错误的位置转为表单的字段路径。

/** 字段错误，与服务端响应中 errors 的每一项相同。 */
export interface FieldError {
  /** 出错的位置，例如 body.items[0].name、query.per_page。 */
  location?: string
  /** 按语言翻译的提示。 */
  message: string
  /** 违反的规则，例如 validation.min_length；无法识别的规则没有它。 */
  code?: string
  /** 收到的值。 */
  value?: unknown
}

/** 生成的客户端抛出的 ApiError 的形状。 */
export interface ApiErrorLike {
  status: number
  code?: string
  detail?: string
  fields: FieldError[]
  /** 业务错误的公开参数。 */
  params?: Record<string, unknown>
  requestId?: string
  retryAfter?: number
}

/** 报告 err 是否具有 ApiError 的形状。各个 API 的客户端各有一个 ApiError 类，这里按形状判断。 */
export function isApiErrorLike(err: unknown): err is ApiErrorLike {
  if (typeof err !== "object" || err === null) return false
  const e = err as Record<string, unknown>
  return typeof e.status === "number" && Array.isArray(e.fields)
}

/** 错误的种类，见客户端协议的错误一节。 */
export type ErrorKind =
  | "unauthenticated" // 401：刷新后仍然失败，需要登录
  | "forbidden" // 403 auth.forbidden 等：没有权限
  | "confirm_required" // 403 auth.confirm_required：再次验证身份后重试
  | "two_factor_required" // 403 auth.two_factor_required：完成二次验证后重试
  | "method_required" // 403 auth.method_required：换一种方式重新登录
  | "upgrade_required" // 426：App 版本过低
  | "rate_limited" // 429：等待 retryAfter 秒后重试
  | "invalid" // 400、422 带 errors：显示在字段旁边
  | "business" // 带 code 的业务错误
  | "not_found" // 404
  | "server" // 5xx
  | "network" // 请求没有得到响应：断网、超时、跨域被拒
  | "aborted" // 请求被取消
  | "unknown"

/** 一个错误的说明：种类和可以展示的信息。 */
export interface ErrorInfo {
  kind: ErrorKind
  status?: number
  /** 业务错误码。 */
  code?: string
  /** 展示给用户的文案，已经按请求的语言翻译；网络错误等没有服务端文案时为空。 */
  message?: string
  fields: FieldError[]
  /** 业务错误的公开参数，例如 method_required 时的 methods；没有时是空对象。 */
  params: Record<string, unknown>
  retryAfter?: number
  requestId?: string
}

const forbiddenCodes: Record<string, ErrorKind> = {
  "auth.confirm_required": "confirm_required",
  "auth.two_factor_required": "two_factor_required",
  "auth.method_required": "method_required",
}

/**
 * fetch 没有得到响应时各环境的 TypeError 信息：Chrome 的 Failed to fetch（包括按需加载代码块失败的
 * Failed to fetch dynamically imported module）、Firefox 的 NetworkError、Safari 的 Load failed、
 * Node 的 fetch failed、React Native 的 Network request failed。其他 TypeError 是代码中的错误。
 */
const networkFailure = /failed to fetch|networkerror|load failed|fetch failed|network request failed/i

/** 按客户端协议分类 err。 */
export function describeError(err: unknown): ErrorInfo {
  if (!isApiErrorLike(err)) {
    const name = typeof err === "object" && err !== null ? (err as { name?: unknown }).name : undefined
    if (name === "AbortError") return { kind: "aborted", fields: [], params: {} }
    if (err instanceof TypeError && networkFailure.test(err.message)) return { kind: "network", fields: [], params: {} }
    return { kind: "unknown", fields: [], params: {} }
  }
  const info: ErrorInfo = {
    kind: "unknown",
    status: err.status,
    code: err.code,
    message: err.detail,
    fields: err.fields,
    params: err.params ?? {},
    retryAfter: err.retryAfter,
    requestId: err.requestId,
  }
  const s = err.status
  if (s === 401) info.kind = "unauthenticated"
  else if (s === 403) info.kind = (err.code && forbiddenCodes[err.code]) || "forbidden"
  else if (s === 426) info.kind = "upgrade_required"
  else if (s === 429) info.kind = "rate_limited"
  else if (err.code) info.kind = "business"
  else if ((s === 400 || s === 422) && err.fields.length > 0) info.kind = "invalid"
  else if (s === 404) info.kind = "not_found"
  else if (s >= 500) info.kind = "server"
  return info
}

/** 字段的位置拆开后的结果。 */
export interface FieldPath {
  /** 位置的来源：body、query、path 或 header。 */
  source: string
  /** 字段路径的各段，数组下标是数字，例如 ["items", 0, "name"]。 */
  segments: (string | number)[]
  /** 用点连接的字段路径，例如 items.0.name，@mantine/form 等表单库的写法。 */
  path: string
}

/** 拆开服务端的位置，例如 body.items[0].name 得到 { source: "body", segments: ["items", 0, "name"], path: "items.0.name" }。 */
export function parseLocation(location: string): FieldPath {
  const dot = location.indexOf(".")
  const source = dot < 0 ? location : location.slice(0, dot)
  const rest = dot < 0 ? "" : location.slice(dot + 1)
  const segments: (string | number)[] = []
  for (const m of rest.matchAll(/([^.[\]]+)|\[(\d+)\]/g)) {
    segments.push(m[2] !== undefined ? Number(m[2]) : m[1])
  }
  return { source, segments, path: segments.join(".") }
}

/**
 * 把字段错误按字段路径分组，每个字段取第一条提示。err 可以是错误，也可以是字段错误的数组（例如 validate 的结果）。
 * 默认只取请求体（body）的错误；source 指定其他来源，例如 query。
 */
export function fieldErrors(err: unknown, source = "body"): Record<string, string> {
  const fields = Array.isArray(err) ? (err as FieldError[]) : isApiErrorLike(err) ? err.fields : []
  const out: Record<string, string> = {}
  for (const f of fields) {
    if (!f.location) continue
    const p = parseLocation(f.location)
    if (p.source === source && !(p.path in out)) out[p.path] = f.message
  }
  return out
}
