import type { PluginOption } from "vite"

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
export function tenon(options?: TenonOptions): PluginOption[]

export const HeaderNonce: "X-CSP-Nonce"
export const PrerenderNonce: "__tenon_csp_nonce__"
