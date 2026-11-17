// @vitest-environment happy-dom
import { createSession } from "@go-tenon/client"
import { createApp, type RouterContext, settingsModel } from "@go-tenon/shell"
import { QueryClient } from "@tanstack/react-query"
import { createMemoryHistory, createRootRouteWithContext } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { createClient } from "../../../httpx/rest/clientgen/internal/example/ts/api"
import { enums } from "../../../httpx/rest/clientgen/internal/example/ts/enums"
import { messages } from "../../../httpx/rest/clientgen/internal/example/ts/messages"
import { createQueries } from "../../../httpx/rest/clientgen/internal/example/ts/queries"
import { components, schemas } from "../../../httpx/rest/clientgen/internal/example/ts/schemas"
import { settings } from "../../../httpx/rest/clientgen/internal/example/ts/settings"
import { SettingsForm } from "./settings"
import { renderRoot } from "./testing"

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

async function mount(autoSave = false) {
  const session = createSession({ load: async () => ({ account: "u1" }) })
  await session.signIn({ account: "u1" })
  const client = createClient({ baseURL: "/api", session })
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
    id: `ui-settings-${Math.random()}`,
    queryClient,
    routeTree: createRootRouteWithContext<RouterContext>()({
      component: () => <SettingsForm model={model} autoSave={autoSave} />,
    }),
    session,
    router: { history: createMemoryHistory({ initialEntries: ["/"] }) },
  })
  renderRoot(app)
  await screen.findByText("博客设置")
  await screen.findByDisplayValue("博客")
}

const name = () => screen.getByLabelText(/博客名称/) as HTMLInputElement

describe("SettingsForm", () => {
  it("字段和显示文字来自声明，初始值来自读取的接口；修改后才能保存", async () => {
    const calls = server()
    await mount()
    expect(name().value).toBe("博客")
    expect(screen.getByLabelText("开放评论")).toBeTruthy()
    expect(screen.getAllByText("新文章的状态").length).toBeGreaterThan(0)
    expect((screen.getByRole("button", { name: "保存" }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(name(), { target: { value: "新的名称" } })
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "保存" })))
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
    fireEvent.change(name(), { target: { value: "" } })
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "保存" })))
    await screen.findByText("不能为空")
    expect(calls.some((c) => c.method === "PUT")).toBe(false)
  })

  it("服务端的字段错误显示在输入框下面", async () => {
    server({ fail: true })
    await mount()
    fireEvent.change(name(), { target: { value: "重复的名称" } })
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "保存" })))
    await screen.findByText("名称已被使用")
  })

  it("自动保存：没有保存按钮，修改后自动保存并显示状态", async () => {
    const calls = server()
    await mount(true)
    expect(screen.queryByRole("button", { name: "保存" })).toBeNull()
    fireEvent.change(name(), { target: { value: "自动保存的名称" } })
    expect(screen.getByRole("status").textContent).toBe("有未保存的修改")
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toMatchObject({ name: "自动保存的名称" }), {
      timeout: 3000,
    })
    await waitFor(() => expect(screen.getByRole("status").textContent).toBe("已保存"))
  })
})
