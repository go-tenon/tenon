// 浏览器标签的标题：布局把当前页面的标题和应用名合成“页面 - 应用”，写进 document.title。

import { useEffect } from "react"
import { useBreadcrumbs } from "./app"
import { useTabs } from "./tabs/tabs"

/**
 * 浏览器标签的标题：有页面标题时是“页面 - 应用”，例如“文章 - 博客后台”；页面标题与应用名相同、
 * 或者没有页面标题时只有应用名；没有应用名时只有页面标题。两者都没有时返回 undefined。
 */
export function formatDocumentTitle(page: string | undefined, app: string | undefined): string | undefined {
  if (!page || page === app) return app || page || undefined
  return app ? `${page} - ${app}` : page
}

/** 把 title 写进 document.title，title 变化时更新；undefined 时保持原来的标题。 */
export function useDocumentTitle(title: string | undefined): void {
  useEffect(() => {
    if (title !== undefined) document.title = title
  }, [title])
}

/** 把 href 写进页面的 <link rel="icon">，href 变化时更新；清空或卸载时恢复原有图标。 */
export function useFavicon(href: string | undefined): void {
  useEffect(() => {
    if (typeof document === "undefined") return
    const clean = href?.trim()
    if (!clean) return
    let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    const created = !link
    const prevHref = link?.getAttribute("href") ?? null
    const prevType = link?.getAttribute("type") ?? null
    if (!link) {
      link = document.createElement("link")
      link.rel = "icon"
      document.head.appendChild(link)
    }
    link.href = clean
    link.removeAttribute("type")
    return () => {
      if (!link) return
      if (created) {
        link.remove()
      } else {
        if (prevHref !== null) link.setAttribute("href", prevHref)
        else link.removeAttribute("href")
        if (prevType !== null) link.setAttribute("type", prevType)
      }
    }
  }, [href])
}

/**
 * 当前页面的标题。tabs 为 true（布局开启了多标签）时是当前标签的标题，页面用 useTabTitle 设置的标题优先，
 * 例如“文章：你好”；否则是路由 staticData.title 中最里面的一个，与面包屑的最后一项相同。
 */
export function usePageTitle(tabs: boolean): string | undefined {
  const crumbs = useBreadcrumbs()
  const active = useTabs().active
  // 没有标题的标签显示地址，这时用面包屑。
  if (tabs && active && active.title !== active.href) return active.title
  return crumbs[crumbs.length - 1]?.title
}
