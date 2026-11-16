// @vitest-environment happy-dom
// 资源的 shadcn 外观：用 clientgen 示例 API 生成的代码渲染文章的列表、表单和操作，与 antd 的外观是同一份定义。
import { createSession } from "@go-tenon/client"
import { AppRoot, createApp, type ResourceModel, type RouterContext, resourceModel } from "@go-tenon/shell"
import { QueryClient } from "@tanstack/react-query"
import { createMemoryHistory, createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest"
import { ConfirmProvider } from "@/components/tenon/dialogs"
import { ResourcePage } from "@/components/tenon/resource-page"
import { createClient } from "../../../httpx/rest/clientgen/internal/example/ts/api"
import { enums } from "../../../httpx/rest/clientgen/internal/example/ts/enums"
import { createQueries } from "../../../httpx/rest/clientgen/internal/example/ts/queries"
import { resources } from "../../../httpx/rest/clientgen/internal/example/ts/resources"
import { components, schemas } from "../../../httpx/rest/clientgen/internal/example/ts/schemas"

beforeAll(() => {
  window.matchMedia ??= ((query: string) => ({
    matches: false,
    media: query,
    addEventListener() {},
    removeEventListener() {},
    onchange: null,
    dispatchEvent: () => false,
  })) as never
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as never
  Element.prototype.scrollIntoView ??= () => {}
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.releasePointerCapture ??= () => {}
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const openMenu = (el: HTMLElement) =>
  act(async () => {
    fireEvent.pointerDown(el, { button: 0, ctrlKey: false, pointerType: "mouse" })
  })

type Call = { method: string; url: string; body?: unknown }

function server() {
  const calls: Call[] = []
  let items = [
    {
      id: "a1",
      title: "你好",
      slug: "hello",
      status: "published",
      tags: ["go"],
      author: { name: "小明" },
      featured: true,
      published_at: "2026-11-01T08:00:00Z",
    },
    {
      id: "a2",
      title: "草稿一篇",
      slug: "draft",
      status: "draft",
      tags: [],
      author: { name: "小明" },
      featured: false,
    },
  ]
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://x")
      const method = init?.method ?? "GET"
      const body = init?.body ? JSON.parse(String(init.body)) : undefined
      calls.push({ method, url: url.pathname + url.search, body })
      if (method === "GET" && url.pathname === "/api/articles") {
        const status = url.searchParams.get("status")
        const rows = status ? items.filter((a) => a.status === status) : items
        return Response.json({ items: rows, page: 1, per_page: 20, total: rows.length })
      }
      if (method === "POST" && url.pathname === "/api/articles") {
        if (body.slug === "taken") {
          return Response.json(
            { status: 422, detail: "validation failed", errors: [{ location: "body.slug", message: "已被使用" }] },
            { status: 422, headers: { "Content-Type": "application/problem+json" } },
          )
        }
        const created = { ...body, id: "a3", status: "draft", author: { name: "小明" }, featured: false }
        items = [...items, created]
        return Response.json(created)
      }
      if (method === "DELETE") {
        items = items.filter((a) => !url.pathname.endsWith(`/${a.id}`))
        return new Response(null, { status: 204 })
      }
      if (method === "PUT") {
        items = items.map((a) => (url.pathname.endsWith(`/${a.id}`) ? { ...a, ...body } : a))
        return Response.json(items[0])
      }
      if (url.pathname.endsWith("/publish")) {
        items = items.map((a) => (url.pathname.includes(`/${a.id}/`) ? { ...a, status: "published" } : a))
        return Response.json(items[0])
      }
      return Response.json(items[0])
    }),
  )
  return calls
}

async function mount(path = "/articles", ready = "你好") {
  const session = createSession({ refresh: async () => ({ access_token: "t", expires_at: "" }) })
  await session.signIn({ access_token: "t", expires_at: "" }, { account: "u1" })
  const client = createClient({ baseURL: "/api" })
  const queryClient = new QueryClient()
  const model: ResourceModel = resourceModel(
    resources.article,
    { client, queries: createQueries(client, { queryClient }), schemas, components, enums, language: "zh-CN" },
    queryClient,
  )
  const root = createRootRouteWithContext<RouterContext>()({
    component: () => (
      <ConfirmProvider>
        <Outlet />
      </ConfirmProvider>
    ),
  })
  const articles = createRoute({
    getParentRoute: () => root,
    path: "/articles",
    validateSearch: model.listSearch,
    component: () => <ResourcePage model={model} route={articles as never} />,
  })
  const app = createApp({
    id: "registry-resource",
    routeTree: root.addChildren([articles]),
    session,
    queryClient,
    router: { history: createMemoryHistory({ initialEntries: [path] }) },
  })
  render(<AppRoot app={app} />)
  await screen.findByText(ready)
  return app
}

/** 打开一行的操作菜单，选择 label。 */
async function rowAction(text: string, label: string) {
  const row = screen.getByText(text).closest("tr") as HTMLElement
  await openMenu(within(row).getByRole("button", { name: "操作" }))
  fireEvent.click(await screen.findByRole("menuitem", { name: label }))
}

describe("ResourcePage（shadcn）", () => {
  it("按定义渲染列表：列的显示文字、枚举、布尔、时间、标题和新建按钮", async () => {
    server()
    await mount()
    for (const title of ["标题", "状态", "发布时间", "精选"]) {
      expect(screen.getByRole("columnheader", { name: title })).toBeTruthy()
    }
    expect(screen.getByRole("heading", { name: "文章" })).toBeTruthy()
    expect(screen.getByText("已发布")).toBeTruthy()
    expect(screen.getByText("草稿")).toBeTruthy()
    expect(screen.getByText("是")).toBeTruthy()
    expect(screen.getByText(/2026/)).toBeTruthy()
    expect(screen.getByRole("button", { name: "新建" })).toBeTruthy()
  })

  it("筛选写进地址并交给列表接口", async () => {
    const calls = server()
    await mount("/articles?status=draft", "草稿一篇")
    expect(screen.queryByText("你好")).toBeNull()
    expect(calls.some((c) => c.url === "/api/articles?status=draft")).toBe(true)
  })

  it("新建：校验与服务端相同，服务端的字段错误显示在输入框下面", async () => {
    const calls = server()
    await mount()
    fireEvent.click(screen.getByRole("button", { name: "新建" }))
    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByText("新建文章")).toBeTruthy()
    fireEvent.change(within(dialog).getByLabelText("链接名 *"), { target: { value: "Bad Slug" } })
    fireEvent.click(within(dialog).getByRole("button", { name: "保存" }))
    await waitFor(() =>
      expect(
        within(dialog)
          .getAllByRole("textbox")
          .filter((e) => e.getAttribute("aria-invalid") === "true"),
      ).toHaveLength(2),
    )
    expect(calls.some((c) => c.method === "POST")).toBe(false)

    fireEvent.change(within(dialog).getByLabelText("标题 *"), { target: { value: "新文章" } })
    fireEvent.change(within(dialog).getByLabelText("链接名 *"), { target: { value: "taken" } })
    fireEvent.click(within(dialog).getByRole("button", { name: "保存" }))
    await within(dialog).findByText("已被使用")

    fireEvent.change(within(dialog).getByLabelText("链接名 *"), { target: { value: "new-one" } })
    fireEvent.change(within(dialog).getByLabelText("标签"), { target: { value: "go, react" } })
    fireEvent.click(within(dialog).getByRole("button", { name: "保存" }))
    await screen.findByText("新文章")
    expect(calls.filter((c) => c.method === "POST").at(-1)?.body).toEqual({
      title: "新文章",
      slug: "new-one",
      tags: ["go", "react"],
    })
  })

  it("修改、操作和删除：操作和删除先确认，完成后刷新列表", async () => {
    const calls = server()
    await mount()
    await rowAction("草稿一篇", "修改")
    const dialog = await screen.findByRole("dialog")
    expect((within(dialog).getByLabelText("标题 *") as HTMLInputElement).value).toBe("草稿一篇")
    fireEvent.change(within(dialog).getByLabelText("标题 *"), { target: { value: "改过的草稿" } })
    fireEvent.click(within(dialog).getByRole("button", { name: "保存" }))
    await screen.findByText("改过的草稿")
    expect(calls.find((c) => c.method === "PUT")?.url).toBe("/api/articles/a2")

    await rowAction("改过的草稿", "发布")
    const confirmPublish = await screen.findByRole("alertdialog")
    fireEvent.click(within(confirmPublish).getByRole("button", { name: "发布" }))
    await waitFor(() => expect(calls.some((c) => c.url === "/api/articles/a2/publish")).toBe(true))

    await rowAction("改过的草稿", "删除")
    const confirmDelete = await screen.findByRole("alertdialog")
    fireEvent.click(within(confirmDelete).getByRole("button", { name: "删除" }))
    await waitFor(() => expect(screen.queryByText("改过的草稿")).toBeNull())
    expect(calls.some((c) => c.method === "DELETE" && c.url === "/api/articles/a2")).toBe(true)
  })
})
