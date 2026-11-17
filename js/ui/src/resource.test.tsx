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
import { ResourceFormPage, ResourcePage, type ResourcePageProps } from "./resource"
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

async function mount(
  path = "/articles",
  ready = "你好",
  inputs?: ResourcePageProps["inputs"],
  {
    form = "page",
    withPages = true,
    noGet = false,
  }: { form?: "page" | "modal"; withPages?: boolean; noGet?: boolean } = {},
) {
  const session = createSession({ load: async () => ({ account: "u1" }) })
  await session.signIn({ account: "u1" })
  const client = createClient({ baseURL: "/api", session })
  const queryClient = new QueryClient()
  const model: ResourceModel = resourceModel(
    noGet
      ? { ...resources.article, operations: { ...resources.article.operations, get: undefined } }
      : resources.article,
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
    staticData: { title: "文章" },
    validateSearch: model.listSearch,
    component: () => <ResourcePage model={model} route={articles as never} inputs={inputs} form={form} />,
  })
  // 新建和修改的页面：列表路由下的 new 和 $id/edit。
  const pages = [
    createRoute({
      getParentRoute: () => articles,
      path: "/new",
      component: () => <ResourceFormPage model={model} inputs={inputs} />,
    }),
    createRoute({
      getParentRoute: () => articles,
      path: "/$id/edit",
      component: () => <ResourceFormPage model={model} inputs={inputs} />,
    }),
  ]
  const app = createApp({
    id: `ui-resource-${Math.random()}`,
    routeTree: root.addChildren([form === "page" && withPages ? articles.addChildren(pages) : articles]),
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

  it("文字的筛选条件是输入框，提示是字段的显示文字；回车后写进地址交给列表接口，可以清除", async () => {
    const calls = server()
    const app = await mount("/articles", "你好")
    const input = screen.getByPlaceholderText("标题")
    fireEvent.change(input, { target: { value: "你" } })
    await act(async () => fireEvent.keyDown(input, { key: "Enter" }))
    await waitFor(() => expect(app.router.state.location.search).toEqual({ title: "你" }))
    await waitFor(() => expect(calls.some((c) => c.url === "/api/articles?title=%E4%BD%A0")).toBe(true))
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "清除标题" })))
    await waitFor(() => expect(app.router.state.location.search).toEqual({}))
  })

  it("在筛选中删除一行后清除筛选：不会因为原来缓存的列表又显示被删的那一行", async () => {
    server()
    const app = await mount()
    await act(() => app.router.navigate({ to: "/articles", search: { status: "draft" } as never }))
    await screen.findByText("草稿一篇")
    await waitFor(() => expect(screen.queryByText("你好")).toBeNull())
    fireEvent.click(within(rowOf("草稿一篇")).getByRole("button", { name: "更多操作" }))
    fireEvent.click(await screen.findByRole("menuitem", { name: "删除" }))
    await act(async () => fireEvent.click(await screen.findByRole("button", { name: "删除" })))
    await waitFor(() => expect(screen.queryByText("草稿一篇")).toBeNull())
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "清除筛选" })))
    expect(screen.queryByText("草稿一篇")).toBeNull() // 读到之前显示的是当前的（已经没有这一行），不是原来缓存的全部
    await screen.findByText("你好")
    expect(screen.queryByText("草稿一篇")).toBeNull()
  })

  it("新建是单独的页面：面包屑是“文章 / 新建”；校验与服务端相同，服务端的字段错误显示在输入框下面；创建后回到列表", async () => {
    const calls = server()
    const app = await mount()
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "新建" })))
    await screen.findByRole("heading", { name: "新建文章" })
    expect(app.router.state.location.pathname).toBe("/articles/new")
    expect(screen.getByText("文章", { selector: ".tn-crumbs a" })).toBeTruthy()
    expect(screen.getByText("新建", { selector: "[aria-current=page]" })).toBeTruthy()
    expect(screen.queryByRole("dialog")).toBeNull()

    const form = document.querySelector("form") as HTMLElement
    const slug = within(form).getByLabelText(/链接名/)
    fireEvent.change(slug, { target: { value: "Bad Slug" } })
    await act(async () => fireEvent.click(within(form).getByRole("button", { name: "创建" })))
    // 标题为空、链接名不合规：两个字段下面都有提示，没有调用接口。
    await waitFor(() => expect(form.querySelectorAll("[aria-invalid=true]").length).toBe(2))
    expect(calls.some((c) => c.method === "POST")).toBe(false)

    fireEvent.change(within(form).getByLabelText(/标题/), { target: { value: "新文章" } })
    fireEvent.change(slug, { target: { value: "taken" } })
    await act(async () => fireEvent.click(within(form).getByRole("button", { name: "创建" })))
    await within(form).findByText("已被使用")

    fireEvent.change(slug, { target: { value: "new-one" } })
    await act(async () => fireEvent.click(within(form).getByRole("button", { name: "创建" })))
    await screen.findByText("新文章")
    expect(app.router.state.location.pathname).toBe("/articles")
    expect(calls.filter((c) => c.method === "POST").at(-1)?.body).toEqual({
      title: "新文章",
      slug: "new-one",
      tags: [],
    })
  })

  it("覆盖的输入组件收到字段的显示文字作为标签", async () => {
    server()
    await mount("/articles/new", "新建文章", {
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
    expect(screen.getByLabelText(/链接名/).getAttribute("data-testid")).toBe("custom-slug")
  })

  it("有未保存的修改时，取消之前询问；放弃后回到列表", async () => {
    server()
    const app = await mount()
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "新建" })))
    fireEvent.change(await screen.findByLabelText(/标题/), { target: { value: "写了一半" } })
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "取消" })))
    await screen.findByText("放弃未保存的修改？")
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "放弃" })))
    await screen.findByText("你好")
    expect(app.router.state.location.pathname).toBe("/articles")
  })

  it("修改、删除和操作：修改是单独的页面，操作和删除先确认，完成后刷新列表", async () => {
    const calls = server()
    const app = await mount()
    await act(async () => fireEvent.click(within(rowOf("草稿一篇")).getByRole("button", { name: "修改" })))
    await screen.findByRole("heading", { name: "修改文章" })
    expect(app.router.state.location.pathname).toBe("/articles/a2/edit")
    const title = await screen.findByLabelText(/标题/)
    expect((title as HTMLInputElement).value).toBe("草稿一篇")
    fireEvent.change(title, { target: { value: "改过的草稿" } })
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "保存" })))
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

  it("没有读取单条的接口时，修改页面在打开时从列表取出这一行并留住：之后列表的数据被丢掉也照样能编辑", async () => {
    server()
    const app = await mount("/articles", "你好", undefined, { noGet: true })
    await act(async () => fireEvent.click(within(rowOf("草稿一篇")).getByRole("button", { name: "修改" })))
    await screen.findByRole("heading", { name: "修改文章" })
    const title = (await screen.findByLabelText(/标题/)) as HTMLInputElement
    expect(title.value).toBe("草稿一篇")
    // 列表不在眼前时它的数据被丢掉（例如切换标签）。
    app.queryClient.removeQueries()
    fireEvent.change(title, { target: { value: "继续编辑" } })
    expect(screen.queryByText("读取失败")).toBeNull()
    expect((screen.getByLabelText(/标题/) as HTMLInputElement).value).toBe("继续编辑")
  })

  it("没有新建和修改的子路由时不显示这两个按钮", async () => {
    server()
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {})
    await mount("/articles", "你好", undefined, { withPages: false })
    expect(screen.queryByRole("button", { name: "新建" })).toBeNull()
    expect(within(rowOf("你好")).queryByRole("button", { name: "修改" })).toBeNull()
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })

  it("form 为 modal 时在对话框中新建和修改，不需要子路由；关闭有未保存修改的对话框之前询问", async () => {
    const calls = server()
    const app = await mount("/articles", "你好", undefined, { form: "modal", withPages: false })
    fireEvent.click(screen.getByRole("button", { name: "新建" }))
    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByText("新建文章")).toBeTruthy()
    expect(app.router.state.location.pathname).toBe("/articles")
    fireEvent.change(within(dialog).getByLabelText(/标题/), { target: { value: "写了一半" } })
    fireEvent.click(within(dialog).getByRole("button", { name: "取消" }))
    await screen.findByText("放弃未保存的修改？")
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "放弃" })))
    await waitFor(() => expect(screen.queryByText("新建文章")).toBeNull())

    fireEvent.click(within(rowOf("草稿一篇")).getByRole("button", { name: "修改" }))
    const edit = await screen.findByRole("dialog")
    const title = await within(edit).findByLabelText(/标题/)
    fireEvent.change(title, { target: { value: "对话框改的" } })
    await act(async () => fireEvent.click(within(edit).getByRole("button", { name: "保存" })))
    await screen.findByText("对话框改的")
    expect(calls.find((c) => c.method === "PUT")?.url).toBe("/api/articles/a2")
  })
})
