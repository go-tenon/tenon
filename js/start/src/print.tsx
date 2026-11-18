// 打印模板：Go 用 frontend.Host.Render 把数据交给 SSR 进程，这里用 React 渲染成完整的 HTML，交给 pdf 包生成 PDF。
// 模板只用传入的数据渲染，不调用接口，所以队列任务中没有用户的 cookie 也能使用。
//
//	// src/print.tsx
//	export const templates = { invoice: definePrint<Invoice>(({ data }) => <PrintDocument title={data.no}>…</PrintDocument>) }
//
//	// src/server.ts
//	const print = printHandler(templates)
//	const start = createStartHandler(streamHandler)
//	export default createServerEntry({ fetch: (request) => print(request) ?? start(request) })

import type { ReactElement, ReactNode } from "react"
import { renderToString } from "react-dom/server"

/** 地址前缀，与 Go 的 frontend.PrintPath 相同。 */
export const PrintPath = "/__tenon/print/"

/** 打印模板：用 data 渲染一个完整的页面，通常以 PrintDocument 为根。 */
export type PrintTemplate<T = unknown> = (props: { data: T }) => ReactElement

/** 声明打印模板，标注数据的类型。 */
export function definePrint<T>(template: PrintTemplate<T>): PrintTemplate<T> {
  return template
}

/**
 * 返回处理打印请求的函数：POST /__tenon/print/{name}，请求体是数据（JSON），响应是 HTML；
 * 不是打印请求时返回 undefined，交给 Start 处理。没有这个模板时返回 404。
 */
// biome-ignore lint/suspicious/noExplicitAny: 各模板的数据类型不同
export function printHandler(templates: Record<string, PrintTemplate<any>>) {
  return (request: Request): Promise<Response> | undefined => {
    const path = new URL(request.url).pathname
    if (request.method !== "POST" || !path.startsWith(PrintPath)) return undefined
    return render(templates[decodeURIComponent(path.slice(PrintPath.length))], request)
  }
}

async function render(template: PrintTemplate | undefined, request: Request): Promise<Response> {
  if (!template) return new Response("unknown print template", { status: 404 })
  const data: unknown = await request.json()
  const Template = template
  const html = `<!doctype html>${renderToString(<Template data={data} />)}`
  return new Response(html, { headers: { "Content-Type": "text/html; charset=utf-8" } })
}

/** PrintDocument 的选项。 */
export interface PrintDocumentProps {
  /** PDF 的标题。 */
  title: string
  /** 内联的样式表，例如 import css from "./invoice.css?inline"；Chromium 读不到外部文件，样式都要内联。 */
  styles?: string[]
  /** 页面的语言，默认 zh-CN。 */
  lang?: string
  children: ReactNode
}

/**
 * 打印页面的骨架：默认 A4、中文字体（Gotenberg 的镜像带有 Noto CJK）、打印背景色，以及跨页时重复表头、
 * 表格行不在页中间断开。纸张和边距用 @page 覆盖，见 styles。
 */
export function PrintDocument({ title, styles = [], lang = "zh-CN", children }: PrintDocumentProps): ReactElement {
  return (
    <html lang={lang}>
      <head>
        <meta charSet="utf-8" />
        <title>{title}</title>
        <style>{baseStyle}</style>
        {styles.map((css, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: 样式列表在一次渲染中固定，不会重排
          <style key={i}>{css}</style>
        ))}
      </head>
      <body>{children}</body>
    </html>
  )
}

const baseStyle = `@page { size: A4; margin: 18mm 15mm; }
html { -webkit-print-color-adjust: exact; print-color-adjust: exact; }
body { margin: 0; font-family: "Noto Sans CJK SC", "Noto Sans SC", "PingFang SC", sans-serif; font-size: 11pt; line-height: 1.5; color: #111; }
thead { display: table-header-group; }
tr, img { break-inside: avoid; }`
