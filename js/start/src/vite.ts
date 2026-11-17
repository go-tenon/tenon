// Vite 配置：TanStack Start 加上 Tenon 的约定。
//
//	// vite.config.ts
//	import { tenon } from "@go-tenon/start/vite"
//	export default defineConfig({ plugins: [tenon({ prerender: ["/", "/docs"] })] })

import { tanstackStart } from "@tanstack/react-start/plugin/vite"
import viteReact from "@vitejs/plugin-react"
import type { PluginOption } from "vite"
import { HeaderNonce, PrerenderNonce } from "./headers"

/** tenon 的选项。 */
export interface TenonOptions {
  /**
   * 构建时预渲染的页面，例如官网首页 "/" 和文档 "/docs"：内容在代码仓库中、不随请求变化的页面。
   * 从这些页面出发跟随链接，同一前缀下的页面一起预渲染（"/" 只表示首页本身）。Bun 直接返回预渲染的 HTML，
   * 其余页面在请求时渲染。
   */
  prerender?: string[]
}

/** 返回 TanStack Start 和 React 的 Vite 插件。 */
export function tenon(options: TenonOptions = {}): PluginOption[] {
  const pages = options.prerender ?? []
  const within = (path: string) =>
    pages.some((p) => (p === "/" ? path === "/" : path === p || path.startsWith(`${p}/`)))
  return [
    tanstackStart(
      pages.length === 0
        ? {}
        : {
            pages: pages.map((path) => ({ path })),
            prerender: {
              enabled: true,
              crawlLinks: true,
              failOnError: true,
              filter: (page) => within(page.path),
              // 构建时没有请求，用占位 nonce；Bun 返回页面时换成本次请求的。
              headers: { [HeaderNonce]: PrerenderNonce },
            },
          },
    ),
    viteReact(),
  ]
}
