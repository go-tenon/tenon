// 保活：隐藏的标签保留状态（输入框、计数），effect 暂停；关闭或刷新后重新开始。
import { expect, test } from "@playwright/test"
import { active, menu, pane, tab, tabs } from "./helpers"

test("切到别的标签再回来，输入框中的草稿还在", async ({ page }) => {
  await page.goto("/edit/a")
  await active(page).getByLabel("草稿").fill("留着")
  await menu(page, "计数器").click()
  await expect(active(page).getByTestId("count")).toBeVisible()
  await tab(page, "编辑 A").click()
  await expect(page).toHaveURL(/\/edit\/a$/)
  await expect(active(page).getByLabel("草稿")).toHaveValue("留着")
})

test("同一个路由的两个实体各是一个标签，状态互不影响", async ({ page }) => {
  await page.goto("/items/a")
  await active(page).getByLabel("备注").fill("alpha")
  await menu(page, "实体 B").click()
  await expect(active(page).getByTestId("item-id")).toHaveText("b")
  await active(page).getByLabel("备注").fill("beta")

  await tab(page, "实体 A").click()
  await expect(page).toHaveURL(/\/items\/a$/)
  await expect(active(page).getByTestId("item-id")).toHaveText("a")
  await expect(active(page).getByLabel("备注")).toHaveValue("alpha")
  await tab(page, "实体 B").click()
  await expect(active(page).getByLabel("备注")).toHaveValue("beta")
  // 隐藏的标签读到的仍是自己的参数。
  await expect(pane(page, "/_app/items/$id?id=a").getByTestId("item-id")).toHaveText("a")
})

test("查询条件和分页改变地址，不打开新的标签；回到标签时地址也回来", async ({ page }) => {
  await page.goto("/list")
  await active(page).getByLabel("关键词").fill("hello")
  await active(page).getByRole("button", { name: "搜索" }).click()
  await active(page).getByRole("button", { name: "下一页" }).click()
  await expect(page).toHaveURL(/keyword=hello/)
  await expect(page).toHaveURL(/page=2/)
  await expect(active(page).getByTestId("page")).toHaveText("page=2")
  await expect(tabs(page)).toHaveCount(1)

  await menu(page, "关于").click()
  await tab(page, "列表").click()
  await expect(page).toHaveURL(/\/list\?.*page=2/)
  await expect(active(page).getByTestId("page")).toHaveText("page=2")
  await expect(active(page).getByLabel("关键词")).toHaveValue("hello")
})

test("浏览器后退时，当前标签随地址切换，原来的页面状态还在", async ({ page }) => {
  await page.goto("/list")
  await active(page).getByLabel("关键词").fill("未提交")
  await active(page).getByRole("link", { name: "打开实体 A" }).click()
  await expect(active(page).getByTestId("item-id")).toHaveText("a")
  await expect(tab(page, "实体 A")).toHaveAttribute("aria-selected", "true")

  await page.goBack()
  await expect(page).toHaveURL(/\/list$/)
  await expect(tab(page, "列表")).toHaveAttribute("aria-selected", "true")
  await expect(active(page).getByLabel("关键词")).toHaveValue("未提交")

  await page.goForward()
  await expect(tab(page, "实体 A")).toHaveAttribute("aria-selected", "true")
  await expect(active(page).getByTestId("item-id")).toHaveText("a")
})

test("关闭标签释放页面，再打开时从头开始", async ({ page }) => {
  await page.goto("/counter")
  await active(page).getByRole("button", { name: "加一" }).click()
  await expect(active(page).getByTestId("count")).toHaveText("1")
  await menu(page, "关于").click()
  await expect(pane(page, "/_app/counter")).toHaveCount(1)

  await page.getByRole("button", { name: "关闭 计数器" }).click()
  await expect(tab(page, "计数器")).toHaveCount(0)
  await expect(pane(page, "/_app/counter")).toHaveCount(0)

  await menu(page, "计数器").click()
  await expect(active(page).getByTestId("count")).toHaveText("0")
})

test("刷新只重新挂载这一个标签", async ({ page }) => {
  await page.goto("/counter")
  await active(page).getByRole("button", { name: "加一" }).click()
  await active(page).getByRole("button", { name: "加一" }).click()
  await menu(page, "实体 A").click()
  await active(page).getByLabel("备注").fill("不受影响")

  await tab(page, "计数器").click()
  await expect(active(page).getByTestId("count")).toHaveText("2")
  await page.getByRole("button", { name: "刷新 计数器" }).click()
  await expect(active(page).getByTestId("count")).toHaveText("0")

  await tab(page, "实体 A").click()
  await expect(active(page).getByLabel("备注")).toHaveValue("不受影响")
})

test("隐藏的标签暂停 effect，回来时继续", async ({ page }) => {
  await page.goto("/ticker")
  const ticks = () => pane(page, "/_app/ticker").getByTestId("ticks")
  await expect(active(page).getByTestId("ticker-active")).toHaveText("true")
  await expect.poll(async () => Number(await ticks().textContent())).toBeGreaterThan(2)

  await menu(page, "关于").click()
  await expect(pane(page, "/_app/ticker")).toBeHidden()
  const hidden = Number(await ticks().textContent())
  await page.waitForTimeout(400)
  expect(Number(await ticks().textContent())).toBe(hidden)

  await tab(page, "计时器").click()
  await expect(active(page).getByTestId("ticker-active")).toHaveText("true")
  await expect.poll(async () => Number(await ticks().textContent())).toBeGreaterThan(hidden)
})
