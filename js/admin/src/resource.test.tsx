// @vitest-environment happy-dom
// 资源的 antd 外观：用 clientgen 示例 API 生成的代码渲染文章的列表、表单和操作。
import { createSession } from "@go-tenon/client"
import { AppRoot, createApp, type ResourceModel, type RouterContext, resourceModel } from "@go-tenon/shell"
import { QueryClient } from "@tanstack/react-query"
import { createMemoryHistory, createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest"
import { createClient } from "../../../httpx/rest/clientgen/internal/example/ts/api"
import { enums } from "../../../httpx/rest/clientgen/internal/example/ts/enums"
import { createQueries } from "../../../httpx/rest/clientgen/internal/example/ts/queries"
import { resources } from "../../../httpx/rest/clientgen/internal/example/ts/resources"
import { components, schemas } from "../../../httpx/rest/clientgen/internal/example/ts/schemas"
import { AdminProvider } from "./provider"
import { ResourcePage } from "./resource"

beforeAll(() => {
  window.matchMedia ??= ((query: string) => ({
    matches: false,
    media: query,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    onchange: null,
    dispatchEvent: () => false,
  })) as never
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as never
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
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
      <AdminProvider>
        <Outlet />
      </AdminProvider>
    ),
  })
  const articles = createRoute({
    getParentRoute: () => root,
    path: "/articles",
    validateSearch: model.listSearch,
    component: () => <ResourcePage model={model} route={articles as never} />,
  })
  const app = createApp({
    id: "admin-resource",
    routeTree: root.addChildren([articles]),
    session,
    queryClient,
    router: { history: createMemoryHistory({ initialEntries: [path] }) },
  })
  render(<AppRoot app={app} />)
  await screen.findByText(ready)
  return app
}

describe("ResourcePage", () => {
  it("按定义渲染列表：列的显示文字、枚举、布尔、时间、标题和新建按钮", async () => {
    server()
    await mount()
    for (const title of ["标题", "状态", "发布时间", "精选", "操作"]) {
      expect(screen.getAllByText(title).length).toBeGreaterThan(0)
    }
    expect(screen.getByText("文章")).toBeTruthy()
    expect(screen.getByText("已发布")).toBeTruthy()
    expect(screen.getByText("草稿")).toBeTruthy()
    expect(screen.getByText("是")).toBeTruthy()
    expect(screen.getByText(/2026-11-01/)).toBeTruthy()
    expect(screen.getByRole("button", { name: /新\s*建/ })).toBeTruthy()
  })

  it("筛选写进地址并交给列表接口", async () => {
    const calls = server()
    await mount("/articles?status=draft", "草稿一篇")
    expect(screen.queryByText("你好")).toBeNull()
    expect(calls.some((c) => c.url === "/api/articles?status=draft")).toBe(true)
  })

  it("新建：表单字段来自请求体，校验与服务端相同，服务端的字段错误显示在输入框下面", async () => {
    const calls = server()
    await mount()
    fireEvent.click(screen.getByRole("button", { name: /新\s*建/ }))
    const drawer = await screen.findByRole("dialog")
    expect(within(drawer).getByText("新建文章")).toBeTruthy()
    const title = within(drawer).getByLabelText("标题")
    const slug = within(drawer).getByLabelText("链接名")
    fireEvent.change(slug, { target: { value: "Bad Slug" } })
    fireEvent.click(within(drawer).getByRole("button", { name: /确\s*定/ }))
    // 标题为空、链接名不合规：两个字段下面都有提示，没有调用接口。
    await waitFor(() => expect(drawer.querySelectorAll(".ant-form-item-explain-error")).toHaveLength(2))
    expect(calls.some((c) => c.method === "POST")).toBe(false)

    fireEvent.change(title, { target: { value: "新文章" } })
    fireEvent.change(slug, { target: { value: "taken" } })
    fireEvent.click(within(drawer).getByRole("button", { name: /确\s*定/ }))
    await within(drawer).findByText("已被使用")

    fireEvent.change(slug, { target: { value: "new-one" } })
    fireEvent.click(within(drawer).getByRole("button", { name: /确\s*定/ }))
    await screen.findByText("新文章")
    expect(calls.filter((c) => c.method === "POST").at(-1)?.body).toEqual({
      title: "新文章",
      slug: "new-one",
      tags: [],
    })
  })

  it("修改、删除和操作：操作和删除先确认，完成后刷新列表", async () => {
    const calls = server()
    await mount()
    const row = (await screen.findByText("草稿一篇")).closest("tr") as HTMLElement
    fireEvent.click(within(row).getByRole("button", { name: /修\s*改/ }))
    const drawer = await screen.findByRole("dialog")
    expect((within(drawer).getByLabelText("标题") as HTMLInputElement).value).toBe("草稿一篇")
    fireEvent.change(within(drawer).getByLabelText("标题"), { target: { value: "改过的草稿" } })
    fireEvent.click(within(drawer).getByRole("button", { name: /确\s*定/ }))
    await screen.findByText("改过的草稿")
    expect(calls.find((c) => c.method === "PUT")?.url).toBe("/api/articles/a2")

    const row2 = screen.getByText("改过的草稿").closest("tr") as HTMLElement
    fireEvent.click(within(row2).getByRole("button", { name: "更多操作" }))
    fireEvent.click(await screen.findByText("发布"))
    await screen.findAllByText("发布这一项？")
    fireEvent.click(screen.getByRole("button", { name: /确\s*定|OK/ }))
    await waitFor(() => expect(calls.some((c) => c.url === "/api/articles/a2/publish")).toBe(true))

    fireEvent.click(
      within(screen.getByText("改过的草稿").closest("tr") as HTMLElement).getByRole("button", { name: "更多操作" }),
    )
    fireEvent.click(await screen.findByText("删除"))
    await screen.findAllByText("删除这一项？")
    await act(async () => {
      fireEvent.click(screen.getAllByRole("button", { name: /确\s*定|OK/ }).at(-1) as HTMLElement)
    })
    await waitFor(() => expect(screen.queryByText("改过的草稿")).toBeNull())
    expect(calls.some((c) => c.method === "DELETE" && c.url === "/api/articles/a2")).toBe(true)
  })
})
