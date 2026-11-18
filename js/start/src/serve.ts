// 在 Bun 中运行的 SSR 服务：tenon-start compile 生成的可执行文件用 serve 监听 Go 给的 unix socket。
// 这里不导入 TanStack 的包：服务端入口已经由 Vite 构建好（dist/server/server.js）。静态文件和预渲染的页面
// 由 Go 直接返回，不经过这里。

import { unlinkSync } from "node:fs"
import { EnvSocket, socketPath } from "./headers"

/** 服务端入口：Start 构建出的 dist/server/server.js 的默认导出。 */
export interface Handler {
  fetch: (request: Request) => Response | Promise<Response>
}

/** serve 的选项。 */
export interface ServeOptions {
  /** 这个前端的服务端入口。 */
  handler: Handler
  /** 监听的 socket，默认读环境变量 SSR_SOCKET。 */
  socket?: string
}

/** 在 Go 给的 unix socket 上渲染页面，收到 SIGTERM 时停止接收新请求、等进行中的请求完成后退出。 */
export function serve(options: ServeOptions): void {
  const name = options.socket ?? process.env[EnvSocket]
  if (!name) throw new Error(`@go-tenon/start: ${EnvSocket} is required`)
  const unix = socketPath(name)
  if (!unix.startsWith("\0")) {
    try {
      unlinkSync(unix) // 上次异常退出留下的 socket 文件
    } catch {}
  }
  const server = Bun.serve({ unix, fetch: (request) => options.handler.fetch(request) })
  console.log(`ssr ready on ${name}`)
  process.on("SIGTERM", async () => {
    await server.stop()
    process.exit(0)
  })
}
