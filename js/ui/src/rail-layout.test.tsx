// @vitest-environment happy-dom

import { type AnyRoute, createRootRouteWithContext, createRoute } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import { RailLayout } from "./rail-layout"
import type { RouterContext } from "./shell"
import { renderApp } from "./testing"

afterEach(cleanup)
beforeEach(() => localStorage.clear())

// 首页（只有一个页面的模块）、伙伴（找人、团队）、设置（个人 → 个人资料、安全）。
function tree() {
  const root = createRootRouteWithContext<RouterContext>()({ component: () => <RailLayout title="Acme" /> })
  const page = (parent: AnyRoute, path: string, title: string, order = 0) =>
    createRoute({
      getParentRoute: () => parent,
      path,
      staticData: { title, order },
      component: () => <p>{title}内容</p>,
    })
  const people = createRoute({ getParentRoute: () => root, id: "people", staticData: { title: "伙伴", order: 1 } })
  const settings = createRoute({
    getParentRoute: () => root,
    path: "/settings",
    staticData: { title: "设置", order: 9 },
  })
  const personal = createRoute({ getParentRoute: () => settings, id: "personal", staticData: { title: "个人" } })
  return root.addChildren([
    page(root, "/", "首页"),
    people.addChildren([page(people, "/members", "找人"), page(people, "/teams", "团队", 1)]),
    settings.addChildren([
      personal.addChildren([page(personal, "/profile", "个人资料"), page(personal, "/security", "安全", 1)]),
    ]),
  ])
}

const rail = () => screen.getByRole("navigation", { name: "模块导航" })
const secondary = () => document.querySelector("aside.tn-rail-secondary") as HTMLElement

describe("RailLayout", () => {
  it("图标栏列出模块（不含设置）；点击模块打开它的第一个页面，二级导航列出模块中的页面", async () => {
    const app = await renderApp(tree() as never)
    await screen.findByText("首页内容")
    expect(
      within(rail())
        .getAllByRole("button")
        .map((b) => b.getAttribute("aria-label")),
    ).toEqual(["首页", "伙伴"])
    expect(within(rail()).getByRole("button", { name: "首页" }).getAttribute("aria-current")).toBe("page")
    // 只有一个页面的模块，二级导航中就是它自己。
    expect(
      within(secondary())
        .getAllByRole("link")
        .map((a) => a.textContent),
    ).toEqual(["首页"])

    await act(async () => fireEvent.click(within(rail()).getByRole("button", { name: "伙伴" })))
    expect(app.router.state.location.pathname).toBe("/members")
    expect(await screen.findByText("找人内容")).toBeTruthy()
    expect(
      within(secondary())
        .getAllByRole("link")
        .map((a) => a.textContent),
    ).toEqual(["找人", "团队"])
  })

  it("设置的页面从地址进入：二级导航是设置，按分组列出，搜索过滤设置项", async () => {
    await renderApp(tree() as never, { path: "/settings/profile" })
    await screen.findByText("个人资料内容")
    expect(within(rail()).queryByRole("button", { name: "设置" })).toBeNull()
    expect(within(secondary()).getByText("个人")).toBeTruthy()
    expect(
      within(secondary())
        .getAllByRole("link")
        .map((a) => a.textContent),
    ).toEqual(["个人资料", "安全"])
    fireEvent.change(within(secondary()).getByRole("textbox", { name: "搜索设置" }), { target: { value: "安全" } })
    expect(
      within(secondary())
        .getAllByRole("link")
        .map((a) => a.textContent),
    ).toEqual(["安全"])
  })

  it("二级导航可以收起，记在浏览器中；收起后点击当前模块展开", async () => {
    await renderApp(tree() as never, { path: "/members" })
    await screen.findByText("找人内容")
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "收起二级导航" })))
    expect(secondary().getAttribute("aria-hidden")).toBe("true")
    expect(localStorage.getItem("tenon:rail-nav")).toBe("false")
    await act(async () => fireEvent.click(within(rail()).getByRole("button", { name: "伙伴" })))
    expect(secondary().getAttribute("aria-hidden")).toBe("false")
  })
})
