import type { Page } from "@playwright/test"

/** 当前显示的标签中的页面。隐藏的标签留在 DOM 中（带 hidden），用 pane 读取。 */
export const active = (page: Page) => page.locator("[data-tab]:not([hidden])")

/** 某个标签中的页面，不论是否显示。key 是路由 id 加上路径参数，例如 /_app/counter、/_app/items/$id?id=a。 */
export const pane = (page: Page, key: string) => page.locator(`[data-tab="${key}"]`)

/** 标签栏中标题为 title 的标签。 */
export const tab = (page: Page, title: string) =>
  page.getByRole("tablist", { name: "标签" }).getByRole("tab", { name: title, exact: true })

/** 标签栏中的全部标签。 */
export const tabs = (page: Page) => page.getByRole("tablist", { name: "标签" }).getByRole("tab")

/** 左边菜单中的链接。 */
export const menu = (page: Page, name: string) =>
  page.getByRole("navigation", { name: "菜单" }).getByRole("link", { name, exact: true })

/** 下一个 window.confirm 的回答，返回问题的内容。 */
export function answerConfirm(page: Page, accept: boolean): Promise<string> {
  return new Promise((resolve) => {
    page.once("dialog", (dialog) => {
      resolve(dialog.message())
      void (accept ? dialog.accept() : dialog.dismiss())
    })
  })
}
