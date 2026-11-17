// @vitest-environment happy-dom
// 设置的 shadcn 外观：用 clientgen 示例 API 生成的代码（settings.ts）渲染博客设置的表单。
import { createSession } from "@go-tenon/client"
import { AppRoot, createApp, type RouterContext, settingsModel } from "@go-tenon/shell"
import { QueryClient } from "@tanstack/react-query"
import { createMemoryHistory, createRootRouteWithContext } from "@tanstack/react-router"
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest"
import { SettingsForm } from "@/components/tenon/settings-form"
import { createClient } from "../../../httpx/rest/clientgen/internal/example/ts/api"
import { enums } from "../../../httpx/rest/clientgen/internal/example/ts/enums"
import { messages } from "../../../httpx/rest/clientgen/internal/example/ts/messages"
import { createQueries } from "../../../httpx/rest/clientgen/internal/example/ts/queries"
import { components, schemas } from "../../../httpx/rest/clientgen/internal/example/ts/schemas"
import { settings } from "../../../httpx/rest/clientgen/internal/example/ts/settings"

beforeAll(() => {
  window.matchMedia ??= ((query: string) => ({
    matches: false,
    media: query,
    addEventListener() {},
    removeEventListener() {},
  })) as never
  Element.prototype.scrollIntoView ??= () => {}
  Element.prototype.hasPointerCapture ??= () => false
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

type Call = { method: string; body?: unknown }

function server() {
  const calls: Call[] = []
  let saved: Record<string, unknown> = { name: "博客", comments: false, default_status: "draft" }
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? "GET"
      const body = init?.body ? JSON.parse(String(init.body)) : undefined
      calls.push({ method, body })
      if (method === "PUT") saved = body
      return Response.json(saved)
    }),
  )
  return calls
}

async function mount(autoSave = false) {
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
    id: "registry-settings",
    queryClient,
    routeTree: createRootRouteWithContext<RouterContext>()({
      component: () => <SettingsForm model={model} autoSave={autoSave} />,
    }),
    session,
    router: { history: createMemoryHistory({ initialEntries: ["/"] }) },
  })
  render(<AppRoot app={app} />)
  await screen.findByText("博客设置")
}

describe("SettingsForm", () => {
  it("字段和显示文字来自声明，初始值来自读取的接口；名称为空时提示；点击保存", async () => {
    const calls = server()
    await mount()
    const name = (await screen.findByLabelText("博客名称 *")) as HTMLInputElement
    expect(name.value).toBe("博客")
    fireEvent.change(name, { target: { value: "" } })
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "保存" }))
    })
    await screen.findByText("不能为空")
    expect(calls.some((c) => c.method === "PUT")).toBe(false)

    fireEvent.change(name, { target: { value: "新的名称" } })
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "保存" }))
    })
    await waitFor(() =>
      expect(calls.find((c) => c.method === "PUT")?.body).toEqual({
        name: "新的名称",
        comments: false,
        default_status: "draft",
      }),
    )
  })

  it("自动保存：没有保存按钮，修改后自动保存并显示状态", async () => {
    const calls = server()
    await mount(true)
    expect(screen.queryByRole("button", { name: "保存" })).toBeNull()
    fireEvent.change(await screen.findByLabelText("博客名称 *"), { target: { value: "自动保存的名称" } })
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toMatchObject({ name: "自动保存的名称" }))
    await waitFor(() => expect(screen.getByRole("status").textContent).toBe("已保存"))
  })
})
