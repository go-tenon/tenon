// @vitest-environment happy-dom
// 设置的 antd 外观：用 clientgen 示例 API 生成的代码（settings.ts）渲染博客设置的表单。
import { createSession } from "@go-tenon/client"
import { AppRoot, createApp, type RouterContext, settingsModel } from "@go-tenon/shell"
import { QueryClient } from "@tanstack/react-query"
import { createMemoryHistory, createRootRouteWithContext } from "@tanstack/react-router"
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { StrictMode } from "react"
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest"
import { createClient } from "../../../httpx/rest/clientgen/internal/example/ts/api"
import { enums } from "../../../httpx/rest/clientgen/internal/example/ts/enums"
import { messages } from "../../../httpx/rest/clientgen/internal/example/ts/messages"
import { createQueries } from "../../../httpx/rest/clientgen/internal/example/ts/queries"
import { components, schemas } from "../../../httpx/rest/clientgen/internal/example/ts/schemas"
import { settings } from "../../../httpx/rest/clientgen/internal/example/ts/settings"
import { AdminProvider } from "./provider"
import { SettingsForm } from "./settings"

beforeAll(() => {
  window.matchMedia ??= ((query: string) => ({
    matches: false,
    media: query,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
  })) as never
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

type Call = { method: string; body?: unknown }

/** 设置的接口：GET 读取，PUT 保存；fail 为 true 时保存返回字段错误。 */
function server(options: { fail?: boolean } = {}) {
  const calls: Call[] = []
  let saved: Record<string, unknown> = { name: "博客", comments: false, default_status: "draft" }
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? "GET"
      const body = init?.body ? JSON.parse(String(init.body)) : undefined
      calls.push({ method, body })
      if (method === "PUT" && options.fail) {
        return Response.json(
          {
            status: 422,
            title: "Unprocessable Entity",
            errors: [{ location: "body.name", message: "名称已被使用", code: "taken" }],
          },
          { status: 422, headers: { "Content-Type": "application/problem+json" } },
        )
      }
      if (method === "PUT") saved = body
      return Response.json(saved)
    }),
  )
  return calls
}

async function mount(autoSave = false, strict = false) {
  const session = createSession({ load: async () => ({ account: "u1" }) })
  await session.signIn({ account: "u1" })
  const client = createClient({ baseURL: "/api" })
  const queryClient = new QueryClient()
  const model = settingsModel(
    settings.blog,
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
  const app = createApp({
    id: "admin-settings",
    queryClient,
    routeTree: createRootRouteWithContext<RouterContext>()({
      component: () => (
        <AdminProvider>
          <SettingsForm model={model} autoSave={autoSave} />
        </AdminProvider>
      ),
    }),
    session,
    router: { history: createMemoryHistory({ initialEntries: ["/"] }) },
  })
  render(
    strict ? (
      <StrictMode>
        <AppRoot app={app} />
      </StrictMode>
    ) : (
      <AppRoot app={app} />
    ),
  )
  await screen.findByText("博客设置")
}

describe("SettingsForm", () => {
  it("字段和显示文字来自声明，初始值来自读取的接口；点击保存", async () => {
    const calls = server()
    await mount()
    const name = screen.getByLabelText("博客名称") as HTMLInputElement
    expect(name.value).toBe("博客")
    expect(screen.getByLabelText("开放评论")).toBeTruthy()
    expect(screen.getByText("新文章的状态")).toBeTruthy()
    fireEvent.change(name, { target: { value: "新的名称" } })
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /保\s*存/ }))
    })
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true))
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({
      name: "新的名称",
      comments: false,
      default_status: "draft",
    })
  })

  it("校验规则与服务端相同：名称为空时不提交", async () => {
    const calls = server()
    await mount()
    fireEvent.change(screen.getByLabelText("博客名称"), { target: { value: "" } })
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /保\s*存/ }))
    })
    await screen.findByText("不能为空")
    expect(calls.some((c) => c.method === "PUT")).toBe(false)
  })

  it("服务端的字段错误显示在输入框下面", async () => {
    server({ fail: true })
    await mount()
    fireEvent.change(screen.getByLabelText("博客名称"), { target: { value: "重复的名称" } })
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /保\s*存/ }))
    })
    await screen.findByText("名称已被使用")
  })

  it("自动保存：没有保存按钮，修改后自动保存并显示状态", async () => {
    const calls = server()
    await mount(true)
    expect(screen.queryByRole("button", { name: /保\s*存/ })).toBeNull()
    fireEvent.change(screen.getByLabelText("博客名称"), { target: { value: "自动保存的名称" } })
    expect(screen.getByRole("status").textContent).toBe("有未保存的修改")
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toMatchObject({ name: "自动保存的名称" }), {
      timeout: 3000,
    })
    await waitFor(() => expect(screen.getByRole("status").textContent).toBe("已保存"))
  })

  it("自动保存：StrictMode 下照常保存", async () => {
    const calls = server()
    await mount(true, true)
    fireEvent.change(screen.getByLabelText("博客名称"), { target: { value: "严格模式" } })
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toMatchObject({ name: "严格模式" }), {
      timeout: 3000,
    })
  })
})
