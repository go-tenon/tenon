// 在 Bun 中运行的 SSR 服务：tenon-start compile 生成的入口用 serve 监听 Go 给的 unix socket。
// 这里不导入 TanStack 的包：服务端入口已经由 Vite 构建好（dist/server/server.js）。

import { unlinkSync } from "node:fs"
import { EnvSocket, HeaderNonce, PrerenderNonce, socketPath } from "./headers"

/** serve 的选项。 */
export interface ServeOptions {
  /** Start 构建出的服务端入口（dist/server/server.js 的默认导出）。 */
  handler: { fetch: (request: Request) => Response | Promise<Response> }
  /**
   * 嵌入可执行文件的静态文件：路径（例如 /assets/index-1a2b.js、/docs/intro/index.html）到 Bun 嵌入文件的映射，
   * 由 tenon-start compile 生成。路径对应的文件、目录下的 index.html（预渲染的页面）直接返回，其余请求交给 handler。
   */
  files: Record<string, string>
  /** 监听的 socket，默认读环境变量 SSR_SOCKET。 */
  socket?: string
}

/** 在 Go 给的 unix socket 上提供页面，收到 SIGTERM 时停止接收新请求、等进行中的请求完成后退出。 */
export function serve(options: ServeOptions): void {
  const name = options.socket ?? process.env[EnvSocket]
  if (!name) throw new Error(`@go-tenon/start: ${EnvSocket} is required`)
  const unix = socketPath(name)
  if (!unix.startsWith("\0")) {
    try {
      unlinkSync(unix) // 上次异常退出留下的 socket 文件
    } catch {}
  }
  const server = Bun.serve({
    unix,
    async fetch(request) {
      const file = await staticFile(options.files, request)
      return file ?? options.handler.fetch(request)
    },
  })
  console.log(`ssr ready on ${name}`)
  process.on("SIGTERM", async () => {
    await server.stop()
    process.exit(0)
  })
}

/**
 * 返回请求路径对应的嵌入文件：assets 下的文件名带内容哈希，永久缓存；预渲染的 HTML 每次协商，
 * 其中的占位 nonce 换成本次请求的 nonce（X-CSP-Nonce）。
 */
export async function staticFile(files: Record<string, string>, request: Request): Promise<Response | undefined> {
  const path = new URL(request.url).pathname
  const dir = path.endsWith("/") ? path : `${path}/`
  for (const candidate of [path, `${dir}index.html`]) {
    const file = files[candidate]
    if (!file) continue
    if (candidate.endsWith(".html")) {
      const html = (await Bun.file(file).text()).replaceAll(PrerenderNonce, request.headers.get(HeaderNonce) ?? "")
      return new Response(html, {
        headers: { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-cache" },
      })
    }
    const immutable = candidate.startsWith("/assets/")
    return new Response(Bun.file(file), {
      headers: { "Cache-Control": immutable ? "public, max-age=31536000, immutable" : "no-cache" },
    })
  }
  return undefined
}
