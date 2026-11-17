// @vitest-environment happy-dom
import type { RouterContext } from "@go-tenon/shell"
import { createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { cleanup, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { DocsLayout, headingId } from "./docs"
import { renderApp } from "./testing"

afterEach(cleanup)

const nav = [
  {
    title: "开始",
    items: [
      { title: "快速开始", to: "/docs/start" },
      { title: "安装", to: "/docs/install" },
    ],
  },
  { title: "指南", items: [{ title: "部署", to: "/docs/deploy" }] },
]

function tree() {
  const root = createRootRouteWithContext<RouterContext>()({ component: Outlet })
  const doc = createRoute({
    getParentRoute: () => root,
    path: "/docs/$slug",
    component: () => (
      <DocsLayout nav={nav}>
        <h1>安装</h1>
        <h2>准备环境</h2>
        <h3>Go 版本</h3>
        <h2 id="custom">下载</h2>
        <h2>准备环境</h2>
      </DocsLayout>
    ),
  })
  return root.addChildren([doc])
}

describe("DocsLayout", () => {
  it("本页目录按 h2、h3 生成，没有 id 的标题按文字生成，重复的加序号", async () => {
    await renderApp(tree(), { path: "/docs/install" })
    const toc = await screen.findByRole("navigation", { name: "本页目录" })
    await waitFor(() => expect(within(toc).getAllByRole("link")).toHaveLength(4))
    const links = within(toc).getAllByRole("link")
    expect(links.map((a) => a.getAttribute("href"))).toEqual(["#准备环境", "#go-版本", "#custom", "#准备环境-2"])
    expect(links[1].dataset.depth).toBe("3")
    expect(document.getElementById("准备环境-2")?.textContent).toBe("准备环境")
  })

  it("高亮当前页面，底部是上一篇和下一篇", async () => {
    await renderApp(tree(), { path: "/docs/install" })
    const sidebar = await screen.findByRole("complementary", { name: "文档目录" })
    expect((within(sidebar).getByText("安装").closest("a") as HTMLAnchorElement).dataset.active).toBe("true")
    expect(screen.getByText("快速开始", { selector: "p" })).toBeTruthy()
    expect(screen.getByText("部署", { selector: "p" })).toBeTruthy()
  })
})

describe("headingId", () => {
  it("保留中文和字母数字，空白换成 -", () => {
    expect(headingId("  Hello World! ")).toBe("hello-world")
    expect(headingId("安装 Go 1.27")).toBe("安装-go-127")
  })
})
