// @go-tenon/start/vite：Vite 配置，TanStack Start 加上 Tenon 的约定。
//
//	// vite.config.ts
//	import { tenon } from "@go-tenon/start/vite"
//	export default defineConfig({ plugins: [tenon({ prerender: ["/", "/docs"] })] })
//
// 用 JavaScript 编写：开发时 vite dev 在 Node 中运行，加载 vite.config.ts 时由 Node 直接导入这个文件，
// Node 不处理其中的 TypeScript。类型见 vite.d.ts。
// 不导入 ./headers（TypeScript）：用到的两个常量写在这里，start.test.ts 检查它们与 headers.ts 相同。

import { tanstackStart } from "@tanstack/react-start/plugin/vite"
import viteReact from "@vitejs/plugin-react"
import { versionPlugin } from "./version.js"

/** Go 为页面请求生成的 CSP nonce 的请求头，同 headers.ts 的 HeaderNonce。 */
export const HeaderNonce = "X-CSP-Nonce"
/** 预渲染时写在页面中的占位 nonce，Bun 返回页面时换成本次请求的，同 headers.ts 的 PrerenderNonce。 */
export const PrerenderNonce = "__tenon_csp_nonce__"

/**
 * 返回 TanStack Start 和 React 的 Vite 插件，加上构建版本（version.json，见 version.js）。
 *
 * @param {import("./vite").TenonOptions} [options]
 * @returns {import("vite").PluginOption[]}
 */
export function tenon(options = {}) {
  const pages = options.prerender ?? []
  /** @param {string} path */
  const within = (path) => pages.some((p) => (p === "/" ? path === "/" : path === p || path.startsWith(`${p}/`)))
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
    versionPlugin({ version: options.version, sourcemaps: options.sourcemaps }),
  ]
}
