// 与 Go 的约定：请求头、环境变量和 socket 名称。这里只有纯函数，浏览器和服务端都能导入。

/** Go 为每个页面请求生成的 CSP nonce，Bun 把它写到内联脚本上。 */
export const HeaderNonce = "X-CSP-Nonce"
/**
 * 构建时写在页面外壳和预渲染页面中的占位 nonce：构建时还没有请求，Go 返回这些页面时把它换成本次请求的 nonce，
 * 内联脚本才能通过 Go 设置的 CSP。
 */
export const PrerenderNonce = "__tenon_csp_nonce__"
/** Bun 在响应中报告匹配到的路由模板（例如 /articles/$id），Go 用它命名链路的 span，随后从响应中删掉。 */
export const HeaderRoute = "X-Tenon-Route"
/** SSR 进程渲染的前端的名称：可执行文件中有全部 SSR 前端，按它加载其中一个。 */
export const EnvFrontend = "TENON_FRONTEND"
/** Bun 监听的页面 socket。 */
export const EnvSocket = "SSR_SOCKET"
/** Bun 回调 Go 接口的内部 socket。 */
export const EnvAPISocket = "API_SOCKET"
/** 开发时没有内部 socket，Vite 的 SSR 经 TCP 调用 Go 对外的地址。 */
export const EnvAPIOrigin = "API_ORIGIN"

/** SSR 调用 Go 接口时原样带回的页面请求头：登录凭据、请求 ID、链路、语言与代理来源。 */
const forwarded = [
  "cookie",
  "x-request-id",
  "traceparent",
  "tracestate",
  "accept-language",
  "x-forwarded-host",
  "x-forwarded-proto",
]

/** 从页面请求中取出要转交给 Go 的请求头。页面请求带 Host 且未带 X-Forwarded-Host 时补上 X-Forwarded-Host，保留子域名多租户上下文。 */
export function forwardHeaders(incoming: Headers): Record<string, string> {
  const out: Record<string, string> = {}
  for (const name of forwarded) {
    const v = incoming.get(name)
    if (v) out[name] = v
  }
  if (!out["x-forwarded-host"]) {
    const host = incoming.get("host")
    if (host) out["x-forwarded-host"] = host
  }
  return out
}

/**
 * 把 Go 传来的 socket 名称换成 Bun 使用的路径：Linux 抽象 socket 在环境变量中以 @ 开头
 * （环境变量不能含 NUL 字节），Bun 的 unix 选项要求以 \0 开头。
 */
export function socketPath(name: string): string {
  return name.startsWith("@") ? `\0${name.slice(1)}` : name
}
