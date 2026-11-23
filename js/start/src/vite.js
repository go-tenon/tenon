// @go-tenon/start/vite：Vite 配置，TanStack Start 加上 Tenon 的约定。
//
//	// package.json
//	"tenon": { "base": "/admin/", "ssr": false }
//
//	// vite.config.ts
//	import { tenon } from "@go-tenon/start/vite"
//	export default defineConfig({ plugins: [tenon({ prerender: ["/docs"] })] })
//
// 用 JavaScript 编写：开发时 vite dev 在 Node 中运行，加载 vite.config.ts 时由 Node 直接导入这个文件，
// Node 不处理其中的 TypeScript。类型见 vite.d.ts。
// 不导入 ./headers（TypeScript）：用到的两个常量写在这里，start.test.ts 检查它们与 headers.ts 相同。

import { readFileSync } from "node:fs"
import { join } from "node:path"
import { tanstackStart } from "@tanstack/react-start/plugin/vite"
import viteReact from "@vitejs/plugin-react"
import { versionPlugin } from "./version.js"

/** Go 为页面请求生成的 CSP nonce 的请求头，同 headers.ts 的 HeaderNonce。 */
export const HeaderNonce = "X-CSP-Nonce"
/** 构建时写在页面中的占位 nonce，Go 返回页面时换成本次请求的，同 headers.ts 的 PrerenderNonce。 */
export const PrerenderNonce = "__tenon_csp_nonce__"

/**
 * 读取前端目录（dir，默认当前目录，即 vite 运行的目录）中 package.json 的 tenon 字段：挂载路径 base 和
 * 是否在服务端渲染 ssr。go tool tenon 读取同一个字段，决定 Go 怎样提供这个前端、程序中是否带 Bun。
 * 环境变量 TENON_RENDER 为 csr 时不论 tenon.ssr 都只在浏览器中渲染：go tool tenon build --client 为桌面端等宿主
 * 构建前端时设置它，宿主从本地加载页面外壳。
 *
 * @param {string} [dir]
 * @returns {import("./vite").FrontendConfig}
 */
export function frontendConfig(dir = process.cwd()) {
  const file = join(dir, "package.json")
  const { tenon } = JSON.parse(readFileSync(file, "utf8"))
  const base = tenon?.base
  if (typeof base !== "string" || !base.startsWith("/") || !base.endsWith("/")) {
    throw new Error(`@go-tenon/start: ${file}: tenon.base must start and end with /, for example "/admin/"`)
  }
  return { base, ssr: process.env.TENON_RENDER === "csr" ? false : tenon.ssr === true }
}

/**
 * 返回 TanStack Start 的选项：ssr 为 false 时打开 SPA 模式，构建时生成页面外壳 _shell.html；
 * pages 是构建时渲染成 HTML 的页面。页面中的内联脚本带占位 nonce，Go 返回页面时换成本次请求的。
 * SPA 模式下同时预渲染首页 / 时，页面外壳改由 /_shell 渲染，避免与首页 / 冲突。
 *
 * @param {boolean} ssr
 * @param {string[]} pages
 * @returns {NonNullable<Parameters<typeof tanstackStart>[0]>}
 */
export function startOptions(ssr, pages) {
  const headers = { [HeaderNonce]: PrerenderNonce }
  // SPA 外壳默认用 / 渲染；当同时预渲染首页 / 时，给外壳请求加上独立查询参数，
  // 既命中存在的 / 路由返回 200，又避免与首页 / 在预渲染队列中去重冲突。
  const shellPath = !ssr && pages.includes("/") ? "/?__tenon_shell=1" : "/"
  /** @param {string} path */
  const within = (path) => pages.some((p) => (p === "/" ? path === "/" : path === p || path.startsWith(`${p}/`)))
  return {
    ...(ssr
      ? {}
      : {
          spa: {
            enabled: true,
            ...(shellPath === "/" ? {} : { maskPath: shellPath }),
            prerender: { headers },
          },
        }),
    ...(pages.length === 0
      ? {}
      : {
          pages: pages.map((path) => ({ path })),
          prerender: {
            enabled: true,
            crawlLinks: true,
            failOnError: true,
            // SPA 模式下页面外壳的地址是 shellPath，它同样要经过这个过滤。
            filter: (/** @type {{ path: string }} */ page) => within(page.path) || (!ssr && page.path === shellPath),
            headers,
          },
        }),
  }
}

/**
 * 返回 TanStack Start 和 React 的 Vite 插件，加上挂载路径（package.json 的 tenon.base）和构建版本
 * （version.json，见 version.js）。是否在服务端渲染由 package.json 的 tenon.ssr 决定，见 frontendConfig。
 *
 * @param {import("./vite").TenonOptions} [options]
 * @returns {import("vite").PluginOption[]}
 */
export function tenon(options = {}) {
  const { base, ssr } = frontendConfig()
  return [
    {
      name: "tenon:base",
      // TanStack Start 的插件在 pre 阶段读取 base，这个插件要排在它之前。
      enforce: "pre",
      config(config) {
        if (config.base !== undefined && config.base !== base) {
          throw new Error(
            `@go-tenon/start: base ${JSON.stringify(config.base)} in vite.config differs from tenon.base ${JSON.stringify(base)} in package.json; remove it from vite.config`,
          )
        }
        return { base }
      },
    },
    // 为宿主构建时（TENON_RENDER=csr）不预渲染页面：宿主中的页面都由前端路由渲染。
    tanstackStart(startOptions(ssr, process.env.TENON_RENDER === "csr" ? [] : (options.prerender ?? []))),
    viteReact(),
    versionPlugin({ version: options.version, sourcemaps: options.sourcemaps }),
  ]
}
