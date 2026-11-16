// @vitest-environment happy-dom
// 资源和设置的模型：用 clientgen 示例 API 生成的代码（resources.ts、settings.ts、api.ts、queries.ts、schemas.ts、enums.ts）。
import { createSession } from "@go-tenon/client"
import { QueryClient } from "@tanstack/react-query"
import { createMemoryHistory, createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { act, cleanup, render, screen, waitFor } from "@testing-library/react"
import { useEffect } from "react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { createClient } from "../../../httpx/rest/clientgen/internal/example/ts/api"
import { enums } from "../../../httpx/rest/clientgen/internal/example/ts/enums"
import { messages } from "../../../httpx/rest/clientgen/internal/example/ts/messages"
import { createQueries } from "../../../httpx/rest/clientgen/internal/example/ts/queries"
import { resources } from "../../../httpx/rest/clientgen/internal/example/ts/resources"
import { components, schemas } from "../../../httpx/rest/clientgen/internal/example/ts/schemas"
import { settings } from "../../../httpx/rest/clientgen/internal/example/ts/settings"
import { AppRoot, createApp, type RouterContext } from "./app"
import { formValidator } from "./form"
import {
  formValues,
  type ResourceDef,
  type ResourceList,
  type ResourceModel,
  resourceModel,
  type Settings,
  settingsModel,
  useResourceList,
  useResourceMutations,
  useSettings,
} from "./resource"

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

type Call = { method: string; url: string; body?: unknown }

function server() {
  const calls: Call[] = []
  let items = [
    { id: "a1", title: "你好", slug: "hello", status: "published", tags: [], author: { name: "小明" }, featured: true },
    { id: "a2", title: "草稿", slug: "draft", status: "draft", tags: [], author: { name: "小明" }, featured: false },
  ]
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://x")
      const method = init?.method ?? "GET"
      const body = init?.body ? JSON.parse(String(init.body)) : undefined
      calls.push({ method, url: url.pathname + url.search, body })
      if (method === "GET" && url.pathname === "/api/articles") {
        return Response.json({ items, page: 1, per_page: 20, total: items.length })
      }
      if (method === "DELETE") {
        items = items.filter((a) => !url.pathname.endsWith(a.id))
        return new Response(null, { status: 204 })
      }
      return Response.json(items[0])
    }),
  )
  return calls
}

function kit(language = "zh-CN") {
  const client = createClient({ baseURL: "/api" })
  const queryClient = new QueryClient()
  return {
    client,
    queryClient,
    kit: { client, queries: createQueries(client, { queryClient }), schemas, components, enums, language },
  }
}

describe("resourceModel", () => {
  it("显示文字、输入方式、选项和接口", () => {
    const def: ResourceDef = resources.article // 生成的定义满足 ResourceDef
    const m = resourceModel(def, kit().kit)
    expect(m.title).toBe("文章")
    expect(m.columns.map((c) => [c.name, c.label, c.kind])).toEqual([
      ["title", "标题", "text"],
      ["status", "状态", "select"],
      ["published_at", "发布时间", "datetime"],
      ["featured", "精选", "boolean"],
    ])
    expect(m.columns[1]?.options).toEqual([
      { value: "draft", label: "草稿" },
      { value: "published", label: "已发布" },
    ])
    expect(m.display(m.columns[1] as never, "published")).toBe("已发布")
    expect(m.form.map((f) => [f.name, f.kind, f.required])).toEqual([
      ["title", "text", true],
      ["slug", "text", true],
      ["tags", "tags", false],
    ])
    expect(m.filters.map((f) => f.name)).toEqual(["status"])
    expect(m.search).toBe("q")
    expect(m.actions).toEqual([{ name: "publish", label: "发布", confirm: true, danger: false, permissions: [] }])
    expect(m.has).toEqual({ list: true, get: true, create: true, update: true, delete: true })
    expect(m.rowKey({ id: "a1" })).toBe("a1")
    expect(formValues(m)).toEqual({ tags: [] })
    expect(formValues(m, { id: "a1", title: "你好", slug: "hello", tags: ["x"], status: "draft" })).toEqual({
      title: "你好",
      slug: "hello",
      tags: ["x"],
    })

    // 其他语言；没有的语言先用主语言，再用第一种语言。
    expect(resourceModel(def, kit("en-US").kit).columns[0]?.label).toBe("Title")
    expect(resourceModel(def, kit("fr").kit).title).toBe("Articles")
  })

  it("表单的校验规则与服务端相同；列表的查询条件来自查询参数的规则", () => {
    const m = resourceModel(resources.article, kit().kit)
    const validate = formValidator(m.formSchema("create"), { ...m.ruleOptions })
    expect(Object.keys(validate({ title: "", slug: "Bad Slug" })).sort()).toEqual(["slug", "title"])
    expect(validate({ title: "你好", slug: "hello" })).toEqual({})
    expect(m.listSearch({ page: "2", status: "draft" })).toEqual({ page: 2, status: "draft" })
  })

  it("权限：接口要求的权限都具备时才能使用", () => {
    const def: ResourceDef = {
      ...resources.article,
      permissions: { list: [], create: ["article:write"], update: ["article:write"], delete: ["article:admin"] },
      actions: [{ ...resources.article.actions[0], permissions: ["article:publish"] }],
    }
    const m = resourceModel(def, kit().kit)
    const can = (p: string) => p === "article:write"
    expect(m.allowed(can, "create")).toBe(true)
    expect(m.allowed(can, "delete")).toBe(false)
    expect(m.allowed(can, { action: "publish" })).toBe(false)
    expect(m.allowed(() => true, { action: "nope" })).toBe(false)
    const noDelete = resourceModel({ ...def, operations: { list: def.operations.list } }, kit().kit)
    expect(noDelete.allowed(() => true, "delete")).toBe(false)
  })

  it("调用生成的客户端：创建、修改、删除、操作", async () => {
    const calls = server()
    const { kit: k, queryClient } = kit()
    const m = resourceModel(resources.article, k, queryClient)
    const spy = vi.spyOn(queryClient, "invalidateQueries")
    await m.create({ title: "新的", slug: "new" })
    await m.update("a1", { title: "改", slug: "x" })
    await m.remove("a2")
    await m.run("publish", "a1")
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      "POST /api/articles",
      "PUT /api/articles/a1",
      "DELETE /api/articles/a2",
      "POST /api/articles/a1/publish",
    ])
    expect(calls[1]?.body).toEqual({ title: "改", slug: "x" })
    expect(spy).toHaveBeenCalledWith({ queryKey: ["api", "article"] })
  })
})

describe("useResourceList", () => {
  it("列表的数据、地址中的查询条件、按权限得出的按钮；修改后刷新", async () => {
    const calls = server()
    const session = createSession({ refresh: async () => ({ access_token: "t", expires_at: "" }) })
    await session.signIn({ access_token: "t", expires_at: "" }, { account: "u1" })
    const { kit: k } = kit()
    let model: ResourceModel | undefined
    let view: ResourceList | undefined
    let mutations: ReturnType<typeof useResourceMutations> | undefined
    const root = createRootRouteWithContext<RouterContext>()({ component: Outlet })
    const articles = createRoute({
      getParentRoute: () => root,
      path: "/articles",
      validateSearch: (s: Record<string, unknown>) => model?.listSearch(s) ?? s,
      component: function Articles() {
        const v = useResourceList(model as ResourceModel, articles as never)
        const mu = useResourceMutations(model as ResourceModel)
        useEffect(() => {
          view = v
          mutations = mu
        })
        return <p>{v.rows.map((r) => r.title).join(",")}</p>
      },
    })
    const app = createApp({
      id: "resource",
      routeTree: root.addChildren([articles]),
      session,
      permissions: async () => ["x"],
      router: { history: createMemoryHistory({ initialEntries: ["/articles?status=draft&page=2"] }) },
    })
    model = resourceModel(resources.article, k)
    render(<AppRoot app={app} />)
    await screen.findByText("你好,草稿")
    expect(calls[0]?.url).toBe("/api/articles?page=2&status=draft")
    expect(view?.total).toBe(2)
    expect(view?.list.filters).toEqual({ status: "draft" })
    await waitFor(() => expect(view?.can.create).toBe(true)) // 示例的接口不要求权限
    expect(view?.can.action("publish")).toBe(true)

    await act(async () => {
      await mutations?.remove.mutateAsync("a2")
    })
    await screen.findByText("你好")
    expect(calls.filter((c) => c.method === "GET")).toHaveLength(2) // 删除后重新读取列表
  })
})

describe("验证提示", () => {
  it("kit 带上 messages 时，资源和设置表单的提示是翻译好的文字，否则是错误码", () => {
    const k = kit().kit
    const article = resourceModel(resources.article, { ...k, messages })
    expect(formValidator(article.formSchema("create"), article.ruleOptions)({ title: "" }).title).toBe("不能为空")
    const blog = settingsModel(settings.blog, { ...k, messages })
    expect(formValidator(blog.formSchema(), blog.ruleOptions)({ name: "" }).name).toBe("不能为空")
    const bare = settingsModel(settings.blog, k)
    expect(formValidator(bare.formSchema(), bare.ruleOptions)({ name: "" }).name).toBe("validation.filled")
  })
})

describe("settingsModel", () => {
  it("表单字段带显示文字和枚举的选项；校验规则是保存接口的请求体", () => {
    const m = settingsModel(settings.blog, kit().kit)
    expect(m.title).toBe("博客设置")
    expect(m.form.map((f) => [f.name, f.label, f.kind])).toEqual([
      ["name", "博客名称", "text"],
      ["comments", "开放评论", "boolean"],
      ["default_status", "新文章的状态", "select"],
    ])
    expect(m.form[2]?.options).toEqual([
      { value: "draft", label: "草稿" },
      { value: "published", label: "已发布" },
    ])
    expect(
      formValidator(m.formSchema(), m.ruleOptions)({ name: "", comments: true, default_status: "draft" }),
    ).toHaveProperty("name")
    expect(settingsModel(settings.blog, kit("en").kit).form[0]?.label).toBe("Blog name")
    expect(m.allowed(() => false, "update")).toBe(true) // 示例的接口不要求权限
  })

  it("useSettings 读取设置作为表单的初始值，保存后重新读取", async () => {
    let saved = { name: "博客", comments: false, default_status: "draft", extra: "不在表单中" }
    const calls: Call[] = []
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input), "http://x")
        const method = init?.method ?? "GET"
        const body = init?.body ? JSON.parse(String(init.body)) : undefined
        calls.push({ method, url: url.pathname, body })
        if (method === "PUT") saved = { ...saved, ...body }
        return Response.json(saved)
      }),
    )
    const session = createSession({ refresh: async () => ({ access_token: "t", expires_at: "" }) })
    await session.signIn({ access_token: "t", expires_at: "" }, { account: "u1" })
    const model = settingsModel(settings.blog, kit().kit)
    let view: Settings | undefined
    const root = createRootRouteWithContext<RouterContext>()({
      component: function Page() {
        const v = useSettings(model)
        useEffect(() => {
          view = v
        })
        return <p>{String(v.values?.name ?? "")}</p>
      },
    })
    const app = createApp({
      id: "settings",
      routeTree: root,
      session,
      router: { history: createMemoryHistory({ initialEntries: ["/"] }) },
    })
    render(<AppRoot app={app} />)
    await screen.findByText("博客")
    expect(view?.values).toEqual({ name: "博客", comments: false, default_status: "draft" })
    expect(view?.canSave).toBe(true)
    await act(async () => {
      await view?.save({ name: "新的名称", comments: true, default_status: "published" })
    })
    await screen.findByText("新的名称")
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      "GET /api/settings",
      "PUT /api/settings",
      "GET /api/settings",
    ])
  })
})
