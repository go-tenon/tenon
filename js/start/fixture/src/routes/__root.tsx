import { createRootRoute, HeadContent, Outlet, Scripts } from "@tanstack/react-router"
import type { ReactNode } from "react"

/** 根路由：输出 HTML 文档的外壳。 */
export const Route = createRootRoute({
  head: () => ({ meta: [{ charSet: "utf-8" }, { title: "fixture" }] }),
  shellComponent: Document,
  component: Outlet,
})

function Document({ children }: { children: ReactNode }) {
  return (
    <html lang="zh-CN">
      <head>
        <HeadContent />
      </head>
      <body>
        {children}
        <Scripts />
      </body>
    </html>
  )
}
