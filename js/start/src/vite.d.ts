import type { PluginOption } from "vite"
import type { VersionPluginOptions } from "./version"

/** tenon 的选项。version、sourcemaps 是构建版本的选项：页面据此提示新版本，错误追踪据此还原堆栈。 */
export interface TenonOptions extends VersionPluginOptions {
  /**
   * 构建时预渲染的页面，例如官网首页 "/" 和文档 "/docs"：内容在代码仓库中、不随请求变化的页面。
   * 从这些页面出发跟随链接，同一前缀下的页面一起预渲染（"/" 只表示首页本身）。Bun 直接返回预渲染的 HTML，
   * 其余页面在请求时渲染。
   */
  prerender?: string[]
}

/** 返回 TanStack Start 和 React 的 Vite 插件，加上构建版本：构建时写入 version.json 并注入版本号。 */
export function tenon(options?: TenonOptions): PluginOption[]

/** Go 把 CSP nonce 交给 Bun 时使用的请求头。 */
export const HeaderNonce: "X-CSP-Nonce"
/** 预渲染页面中 CSP nonce 的占位符，Bun 返回页面时换成本次请求的 nonce。 */
export const PrerenderNonce: "__tenon_csp_nonce__"
