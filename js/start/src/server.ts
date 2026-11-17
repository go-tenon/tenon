// 应用的服务端入口（src/server.ts）用 streamHandler 渲染页面；Bun 中的运行时见 @go-tenon/start/serve。
//
//	// src/server.ts
//	import { createServerEntry, createStartHandler } from "@tanstack/react-start/server"
//	import { streamHandler } from "@go-tenon/start/server"
//	export default createServerEntry({ fetch: createStartHandler(streamHandler) })

import type { AnyRouter } from "@tanstack/react-router"
import { defaultStreamHandler } from "@tanstack/react-start/server"
import { HeaderRoute } from "./headers"

type HandlerContext = Parameters<typeof defaultStreamHandler>[0]

/** 匹配到的最后一个路由的模板，例如 /articles/$id；没有匹配时为空字符串。 */
export function routeTemplate(router: AnyRouter): string {
  // 应用注册的路由树不同，matches 的类型随之变化；这里只读两个字段。
  const matches = router.state.matches as readonly { routeId: string; fullPath?: string }[]
  const last = matches[matches.length - 1]
  if (!last || last.routeId === "__root__") return ""
  return last.fullPath ?? last.routeId
}

/** 流式渲染页面，并在响应头 X-Tenon-Route 中报告匹配到的路由模板，Go 用它命名链路的 span。 */
export function streamHandler(ctx: HandlerContext) {
  const route = routeTemplate(ctx.router)
  if (route) ctx.responseHeaders.set(HeaderRoute, route)
  return defaultStreamHandler(ctx)
}
