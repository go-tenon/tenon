/// <reference types="node" />
// @go-tenon/start 把 TanStack Start 接到 Tenon：前端由 Go 托管（见 Go 的 frontend 包），服务端渲染的页面由 Go 托管的 Bun 渲染，
// SSR 与浏览器调用同一组 Go 接口。在 src/start.ts 中安装 requestMiddleware，在 src/router.tsx 中使用 cspNonce，
// 把 apiFetch 交给生成的客户端：
//
//	export const startInstance = createStart(() => ({ requestMiddleware: [requestMiddleware] }))
//	createRouter({ routeTree, ssr: { nonce: cspNonce() } })
//	export const api = createClient({ baseURL: "/api", fetch: apiFetch, session })

import { createIsomorphicFn, createMiddleware } from "@tanstack/react-start"
import { getRequestHeader } from "@tanstack/react-start/server"
import { EnvAPIOrigin, EnvAPISocket, forwardHeaders, HeaderNonce, socketPath } from "./headers"

export * from "./headers"

/**
 * 记住页面请求的请求头，供 apiFetch 转交给 Go。安装在 createStart 的 requestMiddleware 中。
 */
export const requestMiddleware = createMiddleware().server(async ({ request, next }) => {
  const { withIncoming } = await import("./context")
  return withIncoming(request.headers, () => next())
})

/**
 * 调用 Go 接口的 fetch，交给生成的客户端（createClient({ fetch: apiFetch })）：
 *
 *   - 浏览器：同源请求，cookie 由浏览器带上；
 *   - SSR：经 Go 提供的内部 unix socket（API_SOCKET）调用同一组接口，开发时经 API_ORIGIN；
 *     原样带上页面请求的 cookie、X-Request-ID、traceparent 和 Accept-Language。
 *     Bun 不解析 cookie，也不持有任何密钥。
 */
export const apiFetch: typeof fetch = createIsomorphicFn()
  .client((input: RequestInfo | URL, init?: RequestInit) => fetch(input, init))
  .server(async (input: RequestInfo | URL, init?: RequestInit) => {
    const { incoming } = await import("./context")
    const headers = new Headers(init?.headers)
    const page = incoming()
    if (page) for (const [k, v] of Object.entries(forwardHeaders(page))) if (!headers.has(k)) headers.set(k, v)
    const url = new URL(String(input), "http://go")
    const socket = process.env[EnvAPISocket]
    if (socket) {
      return fetch(`http://go${url.pathname}${url.search}`, {
        ...init,
        headers,
        unix: socketPath(socket),
      } as RequestInit)
    }
    const origin = process.env[EnvAPIOrigin]
    if (!origin) throw new Error(`@go-tenon/start: neither ${EnvAPISocket} nor ${EnvAPIOrigin} is set`)
    return fetch(new URL(url.pathname + url.search, origin), { ...init, headers })
  }) as typeof fetch

/** 本次页面请求的 CSP nonce，交给 createRouter({ ssr: { nonce } })；浏览器中为 undefined。 */
export const cspNonce = createIsomorphicFn()
  .client(() => undefined)
  .server(() => getRequestHeader(HeaderNonce) ?? undefined)
