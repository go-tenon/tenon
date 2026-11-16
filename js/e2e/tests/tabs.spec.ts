// 标签栏与未保存的修改：关闭前询问、固定、快捷键、刷新浏览器后恢复标签栏。
import { expect, test } from "@playwright/test"
import { active, answerConfirm, menu, pane, tab, tabs } from "./helpers"

test("页面打开为标签，标题来自路由或页面；staticData.tab 为 false 的页面不进入标签", async ({ page }) => {
  await page.goto("/")
  await menu(page, "编辑 A").click()
  await expect(tabs(page)).toHaveText(["首页", "编辑 A"])
  await menu(page, "结果").click()
  await expect(page.getByText("结果内容")).toBeVisible()
  await expect(tabs(page)).toHaveText(["首页", "编辑 A"])
  await expect(page.getByRole("tab", { selected: true })).toHaveCount(0)
  // 回到标签中的页面，状态还在。
  await tab(page, "编辑 A").click()
  await expect(active(page).getByLabel("草稿")).toBeVisible()
})

test("有未保存的修改时关闭标签先询问：取消时保留，确定时关闭", async ({ page }) => {
  await page.goto("/edit/a")
  await active(page).getByLabel("草稿").fill("留着")
  await menu(page, "关于").click()
  await expect(page.getByTestId("tab").filter({ hasText: "编辑 A" })).toHaveAttribute("data-dirty", "true")

  let question = answerConfirm(page, false)
  await page.getByRole("button", { name: "关闭 编辑 A" }).click()
  expect(await question).toBe("这个标签有未保存的修改，确定关闭吗？")
  await expect(tab(page, "编辑 A")).toBeVisible()
  await expect(pane(page, "/_app/edit/$id?id=a").getByLabel("草稿")).toHaveValue("留着")

  question = answerConfirm(page, true)
  await page.getByRole("button", { name: "关闭 编辑 A" }).click()
  await question
  await expect(tab(page, "编辑 A")).toHaveCount(0)
  await expect(pane(page, "/_app/edit/$id?id=a")).toHaveCount(0)
})

test("浏览器前进后退时，隐藏标签中未保存的修改还在", async ({ page }) => {
  await page.goto("/about")
  await menu(page, "编辑 A").click()
  await active(page).getByLabel("草稿").fill("未保存")
  await page.goBack()
  await expect(active(page).getByText("关于内容")).toBeVisible()
  await page.goForward()
  await expect(active(page).getByLabel("草稿")).toHaveValue("未保存")
})

test("关闭全部：先询问；固定的标签保留", async ({ page }) => {
  await page.goto("/about")
  await page.getByRole("button", { name: "固定 关于" }).click()
  await expect(page.getByTestId("tab").filter({ hasText: "关于" })).toHaveAttribute("data-pinned", "true")
  await expect(page.getByRole("button", { name: "关闭 关于" })).toHaveCount(0)
  await menu(page, "编辑 A").click()
  await active(page).getByLabel("草稿").fill("会被放弃")
  await menu(page, "计数器").click()

  let question = answerConfirm(page, false)
  await page.getByRole("button", { name: "关闭全部" }).click()
  await question
  await expect(tabs(page)).toHaveText(["关于", "编辑 A", "计数器"])

  question = answerConfirm(page, true)
  await page.getByRole("button", { name: "关闭全部" }).click()
  await question
  await expect(tabs(page)).toHaveText(["关于"])
  await expect(page).toHaveURL(/\/about$/)
  await menu(page, "编辑 A").click()
  await expect(active(page).getByLabel("草稿")).toHaveValue("")
})

test("快捷键：Alt+← / Alt+→ 切换，Alt+W 关闭当前标签", async ({ page }) => {
  await page.goto("/")
  await menu(page, "关于").click()
  await menu(page, "计数器").click()
  await page.keyboard.press("Alt+ArrowLeft")
  await expect(page).toHaveURL(/\/about$/)
  await page.keyboard.press("Alt+ArrowRight")
  await expect(page).toHaveURL(/\/counter$/)
  await page.keyboard.press("Alt+w")
  await expect(tabs(page)).toHaveText(["首页", "关于"])
  await expect(page).toHaveURL(/\/about$/)
})

test("刷新浏览器：有未保存的修改时由浏览器询问；之后恢复标签栏，页面重新开始", async ({ page }) => {
  await page.goto("/counter")
  await page.getByRole("button", { name: "固定 计数器" }).click()
  await menu(page, "实体 B").click()
  await menu(page, "编辑 A").click()
  await active(page).getByLabel("草稿").fill("未保存")

  const asked = new Promise<string>((resolve) =>
    page.once("dialog", (dialog) => {
      resolve(dialog.type())
      void dialog.accept()
    }),
  )
  await page.reload()
  expect(await asked).toBe("beforeunload")

  await expect(tabs(page)).toHaveText(["计数器", "实体 B", "编辑 A"])
  await expect(page.getByTestId("tab").filter({ hasText: "计数器" })).toHaveAttribute("data-pinned", "true")
  await expect(active(page).getByLabel("草稿")).toHaveValue("")
  // 其他标签在打开时才加载。
  await expect(pane(page, "/_app/items/$id?id=b")).toHaveCount(0)
  await tab(page, "实体 B").click()
  await expect(active(page).getByTestId("item-id")).toHaveText("b")
})
