import type { tanstackStart } from "@tanstack/react-start/plugin/vite"
import type { PluginOption } from "vite"
import type { VersionPluginOptions } from "./version"

/** tenon 的选项。version、sourcemaps 是构建版本的选项：页面据此提示新版本，错误追踪据此还原堆栈。 */
export interface TenonOptions extends VersionPluginOptions {
  /**
   * 构建时预渲染的页面，例如官网首页 "/" 和文档 "/docs"：内容在代码仓库中、不随请求变化的页面。
   * 从这些页面出发跟随链接，同一前缀下的页面一起预渲染（"/" 只表示首页本身）。Go 直接返回预渲染的 HTML，
   * 其余页面在请求时渲染。只在浏览器中渲染的前端（tenon.ssr 为 false）不能预渲染 "/"：页面外壳由它渲染而来。
   */
  prerender?: string[]
}

/** 前端 package.json 的 tenon 字段。 */
export interface FrontendConfig {
  /** 挂载的路径，以 / 开头和结尾，例如 /admin/。它也是 Vite 的 base。 */
  base: string
  /** 是否在服务端渲染，默认 false：页面只在浏览器中渲染，程序中不带 Bun。 */
  ssr: boolean
}

/** 读取 dir（默认当前目录）中 package.json 的 tenon 字段。 */
export function frontendConfig(dir?: string): FrontendConfig

/** 返回 TanStack Start 的选项：ssr 为 false 时打开 SPA 模式；pages 是预渲染的页面。 */
export function startOptions(ssr: boolean, pages: string[]): NonNullable<Parameters<typeof tanstackStart>[0]>

/** 返回 TanStack Start 和 React 的 Vite 插件，加上挂载路径和构建版本：构建时写入 version.json 并注入版本号。 */
export function tenon(options?: TenonOptions): PluginOption[]

/** Go 把 CSP nonce 交给 Bun 时使用的请求头。 */
export const HeaderNonce: "X-CSP-Nonce"
/** 页面中 CSP nonce 的占位符，Go 返回页面时换成本次请求的 nonce。 */
export const PrerenderNonce: "__tenon_csp_nonce__"
