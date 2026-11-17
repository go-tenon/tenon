// @vitest-environment happy-dom
// 资源页面：用 clientgen 示例 API 生成的代码渲染文章的列表、表单和操作。
import { createSession } from "@go-tenon/client"
import { TextInput } from "@mantine/core"
import { QueryClient } from "@tanstack/react-query"
import { createMemoryHistory, createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { createClient } from "../../../httpx/rest/clientgen/internal/example/ts/api"
import { enums } from "../../../httpx/rest/clientgen/internal/example/ts/enums"
import { messages } from "../../../httpx/rest/clientgen/internal/example/ts/messages"
import { createQueries } from "../../../httpx/rest/clientgen/internal/example/ts/queries"
import { resources } from "../../../httpx/rest/clientgen/internal/example/ts/resources"
import { components, schemas } from "../../../httpx/rest/clientgen/internal/example/ts/schemas"
import { ResourcePage, type ResourcePageProps } from "./resource"
import { createApp, type ResourceModel, type RouterContext, resourceModel } from "./shell"
import { renderRoot } from "./testing"

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  localStorage.clear()
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
      const id = url.pathname.split("/").at(-1)
      return Response.json(items.find((a) => a.id === id) ?? items[0])
    }),
  )
  return calls
}

async function mount(path = "/articles", ready = "你好", inputs?: ResourcePageProps["inputs"]) {
  const session = createSession({ load: async () => ({ account: "u1" }) })
  await session.signIn({ account: "u1" })
  const client = createClient({ baseURL: "/api", session })
  const queryClient = new QueryClient()
  const model: ResourceModel = resourceModel(
    resources.article,
    {
      client,
      queries: createQueries(client, { queryClient }),
      schemas,
      components,
      enums,
      messages,
      language: "zh-CN",
    },
    queryClient,
  )
  const root = createRootRouteWithContext<RouterContext>()({ component: Outlet })
  const articles = createRoute({
    getParentRoute: () => root,
    path: "/articles",
    validateSearch: model.listSearch,
    component: () => <ResourcePage model={model} route={articles as never} inputs={inputs} />,
  })
  const app = createApp({
    id: `ui-resource-${Math.random()}`,
    routeTree: root.addChildren([articles]),
    session,
    queryClient,
    router: { history: createMemoryHistory({ initialEntries: [path] }) },
  })
  renderRoot(app)
  await screen.findByText(ready)
  return app
}

const rowOf = (text: string) => screen.getByText(text).closest("tr") as HTMLElement

describe("ResourcePage", () => {
  it("按声明渲染列表：列的显示文字、枚举、布尔、时间、标题和新建按钮", async () => {
    server()
    await mount()
    for (const title of ["标题", "状态", "发布时间", "精选"])
      expect(screen.getAllByText(title).length).toBeGreaterThan(0)
    expect(screen.getByRole("heading", { name: "文章" })).toBeTruthy()
    expect(screen.getAllByText("已发布").length).toBeGreaterThan(0)
    expect(screen.getAllByText("草稿").length).toBeGreaterThan(0)
    expect(within(rowOf("你好")).getByText("是")).toBeTruthy()
    expect(screen.getByText(/2026-11-01/)).toBeTruthy()
    expect(screen.getByText("1–2，共 2 条")).toBeTruthy()
    expect(screen.getByRole("button", { name: "新建" })).toBeTruthy()
  })

  it("筛选写进地址并交给列表接口；可以清除", async () => {
    const calls = server()
    const app = await mount("/articles?status=draft", "草稿一篇")
    expect(screen.queryByText("你好")).toBeNull()
    expect(calls.some((c) => c.url === "/api/articles?status=draft")).toBe(true)
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "清除筛选" })))
    await screen.findByText("你好")
    expect(app.router.state.location.search).toEqual({})
  })

  it("新建：表单字段来自请求体，校验与服务端相同，服务端的字段错误显示在输入框下面", async () => {
    const calls = server()
    await mount()
    fireEvent.click(screen.getByRole("button", { name: "新建" }))
    const drawer = await screen.findByRole("dialog")
    expect(within(drawer).getByText("新建文章")).toBeTruthy()
    const slug = within(drawer).getByLabelText(/链接名/)
    fireEvent.change(slug, { target: { value: "Bad Slug" } })
    await act(async () => fireEvent.click(within(drawer).getByRole("button", { name: "创建" })))
    // 标题为空、链接名不合规：两个字段下面都有提示，没有调用接口。
    await waitFor(() => expect(drawer.querySelectorAll("[aria-invalid=true]").length).toBe(2))
    expect(calls.some((c) => c.method === "POST")).toBe(false)

    fireEvent.change(within(drawer).getByLabelText(/标题/), { target: { value: "新文章" } })
    fireEvent.change(slug, { target: { value: "taken" } })
    await act(async () => fireEvent.click(within(drawer).getByRole("button", { name: "创建" })))
    await within(drawer).findByText("已被使用")

    fireEvent.change(slug, { target: { value: "new-one" } })
    await act(async () => fireEvent.click(within(drawer).getByRole("button", { name: "创建" })))
    await screen.findByText("新文章")
    expect(calls.filter((c) => c.method === "POST").at(-1)?.body).toEqual({
      title: "新文章",
      slug: "new-one",
      tags: [],
    })
  })

  it("覆盖的输入组件收到字段的显示文字作为标签", async () => {
    server()
    await mount("/articles", "你好", {
      slug: ({ label, value, onChange, error }) => (
        <TextInput
          data-testid="custom-slug"
          label={label}
          error={error}
          value={(value as string) ?? ""}
          onChange={(e) => onChange?.(e.currentTarget.value)}
        />
      ),
    })
    fireEvent.click(screen.getByRole("button", { name: "新建" }))
    const drawer = await screen.findByRole("dialog")
    expect(
      within(drawer)
        .getByLabelText(/链接名/)
        .getAttribute("data-testid"),
    ).toBe("custom-slug")
  })

  it("关闭有未保存修改的表单之前询问", async () => {
    server()
    await mount()
    fireEvent.click(screen.getByRole("button", { name: "新建" }))
    const drawer = await screen.findByRole("dialog")
    fireEvent.change(within(drawer).getByLabelText(/标题/), { target: { value: "写了一半" } })
    fireEvent.click(within(drawer).getByRole("button", { name: "取消" }))
    await screen.findByText("放弃未保存的修改？")
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "放弃" })))
    await waitFor(() => expect(screen.queryByText("新建文章")).toBeNull())
  })

  it("修改、删除和操作：操作和删除先确认，完成后刷新列表", async () => {
    const calls = server()
    await mount()
    fireEvent.click(within(rowOf("草稿一篇")).getByRole("button", { name: "修改" }))
    const drawer = await screen.findByRole("dialog")
    const title = await within(drawer).findByLabelText(/标题/)
    expect((title as HTMLInputElement).value).toBe("草稿一篇")
    fireEvent.change(title, { target: { value: "改过的草稿" } })
    await act(async () => fireEvent.click(within(drawer).getByRole("button", { name: "保存" })))
    await screen.findByText("改过的草稿")
    expect(calls.find((c) => c.method === "PUT")?.url).toBe("/api/articles/a2")

    fireEvent.click(within(rowOf("改过的草稿")).getByRole("button", { name: "更多操作" }))
    fireEvent.click(await screen.findByRole("menuitem", { name: "发布" }))
    await screen.findByText("发布这一项？")
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "发布" })))
    await waitFor(() => expect(calls.some((c) => c.url === "/api/articles/a2/publish")).toBe(true))
    await waitFor(() => expect(screen.queryByRole("menuitem")).toBeNull())

    fireEvent.click(within(rowOf("改过的草稿")).getByRole("button", { name: "更多操作" }))
    fireEvent.click(await screen.findByRole("menuitem", { name: "删除" }))
    await screen.findByText("删除这一项？")
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "删除" })))
    await waitFor(() => expect(screen.queryByText("改过的草稿")).toBeNull())
    expect(calls.some((c) => c.method === "DELETE" && c.url === "/api/articles/a2")).toBe(true)
  })
})
