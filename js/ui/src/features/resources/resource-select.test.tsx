import { MantineProvider } from "@mantine/core"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import type { ReactNode } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { type ResourceDef, type ResourceKit, resourceModel } from "./model"
import { buildResourceOptions, ResourceMultiSelect, ResourceSelect } from "./resource-select"

let granted = new Set<string>()

vi.mock("../../app/app", () => ({
  useCan: () => (permission: string) => granted.has(permission) || granted.has("*"),
}))

function makeModel(list: (params: Record<string, unknown>) => Promise<unknown>, overrides: Partial<ResourceDef> = {}) {
  const def: ResourceDef = {
    name: "provider",
    key: "id",
    operations: { list: ["provider", "list"] },
    permissions: { list: ["provider:read"] },
    columns: [
      { name: "name", type: "string" },
      { name: "status", type: "string", enum: "Status" },
    ],
    filters: [],
    createForm: [],
    updateForm: [],
    actions: [],
    labels: { "zh-CN": { title: "供应商", fields: { name: "名称", status: "状态" }, actions: {} } },
    ...overrides,
  }
  const kit: ResourceKit = {
    client: { provider: { list } },
    queries: {},
    schemas: {},
    enums: {
      Status: {
        values: ["active", "disabled"],
        labels: { "zh-CN": { active: "启用", disabled: "停用" } },
      },
    },
    language: "zh-CN",
  }
  return resourceModel(def, kit)
}

function renderWithProviders(node: ReactNode, queryClient: QueryClient): string {
  return renderToStaticMarkup(
    <QueryClientProvider client={queryClient}>
      <MantineProvider>{node}</MantineProvider>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  granted = new Set(["provider:read"])
})

describe("buildResourceOptions", () => {
  it("默认取第一列作为显示文字，缺省时回退到 name、title、label 与行标识", () => {
    const model = makeModel(async () => ({ items: [], total: 0 }))
    const { items, rowsByValue } = buildResourceOptions(model, [
      { id: "p1", name: "OpenAI" },
      { id: "p2", title: "Anthropic" },
      { id: "p3" },
    ])
    expect(items).toEqual([
      { value: "p1", label: "OpenAI" },
      { value: "p2", label: "Anthropic" },
      { value: "p3", label: "p3" },
    ])
    expect(rowsByValue.get("p1")).toEqual({ id: "p1", name: "OpenAI" })
  })

  it("支持 labelField 并对枚举字段调用 model.display", () => {
    const model = makeModel(async () => ({ items: [], total: 0 }))
    const { items } = buildResourceOptions(
      model,
      [
        { id: "p1", name: "OpenAI", status: "active" },
        { id: "p2", name: "Legacy", status: "disabled" },
        { id: "p3", name: "Unknown" },
      ],
      { labelField: "status" },
    )
    expect(items).toEqual([
      { value: "p1", label: "启用" },
      { value: "p2", label: "停用" },
      { value: "p3", label: "p3" },
    ])
  })

  it("支持 filterRow、getOption 自定义映射与按 value 去重", () => {
    const model = makeModel(async () => ({ items: [], total: 0 }))
    const { items } = buildResourceOptions(
      model,
      [
        { id: "p1", name: "OpenAI", vendor: "openai", active: true },
        { id: "p1", name: "OpenAI 重复", vendor: "openai", active: true },
        { id: "p2", name: "Disabled", vendor: "legacy", active: false },
        { id: "p3", name: "SkipMe", vendor: "skip", active: true },
        { id: "p4", name: "Anthropic", vendor: "anthropic", active: true, locked: true },
      ],
      {
        filterRow: (row) => Boolean(row.active),
        getOption: (row) => {
          if (row.vendor === "skip") return null
          return {
            value: String(row.id),
            label: `${String(row.name)} (${String(row.vendor)})`,
            disabled: Boolean(row.locked),
          }
        },
      },
    )
    expect(items).toEqual([
      { value: "p1", label: "OpenAI (openai)", disabled: false },
      { value: "p4", label: "Anthropic (anthropic)", disabled: true },
    ])
  })
})

describe("ResourceSelect 与 ResourceMultiSelect", () => {
  it("具备 list 权限时从 listAllQuery 缓存渲染已选项，并自动开启本地搜索", () => {
    const list = vi.fn(async () => ({ items: [], total: 0 }))
    const model = makeModel(list)
    const queryClient = new QueryClient()
    queryClient.setQueryData(model.listAllQuery().queryKey, [
      { id: "p1", name: "OpenAI" },
      { id: "p2", name: "Anthropic" },
    ])

    const singleHtml = renderWithProviders(
      <ResourceSelect model={model} label="供应商" value="p1" onChange={() => {}} />,
      queryClient,
    )
    expect(singleHtml).toContain("供应商")
    expect(singleHtml).toContain('value="OpenAI"')
    // searchable 默认开启，输入框不是只读状态。
    expect(singleHtml).not.toContain("readonly")

    const multiHtml = renderWithProviders(
      <ResourceMultiSelect model={model} label="允许的供应商" value={["p1", "p2"]} onChange={() => {}} />,
      queryClient,
    )
    expect(multiHtml).toContain("允许的供应商")
    expect(multiHtml).toContain("OpenAI")
    expect(multiHtml).toContain("Anthropic")
  })

  it("缺少 list 权限或 enabled 为 false 时不触发加载态且不请求接口", () => {
    const list = vi.fn(async () => ({ items: [], total: 0 }))
    const model = makeModel(list)
    const queryClient = new QueryClient()

    granted = new Set()
    const forbiddenHtml = renderWithProviders(<ResourceSelect model={model} label="供应商" />, queryClient)
    expect(forbiddenHtml).not.toContain("mantine-Loader-root")
    expect(list).not.toHaveBeenCalled()

    granted = new Set(["provider:read"])
    const disabledQueryHtml = renderWithProviders(
      <ResourceSelect model={model} label="供应商" enabled={false} />,
      queryClient,
    )
    expect(disabledQueryHtml).not.toContain("mantine-Loader-root")
    expect(list).not.toHaveBeenCalled()
  })

  it("加载中显示 Loader，自定义 renderRow 与空态/错误文案正常渲染", () => {
    const list = vi.fn(async () => ({ items: [], total: 0 }))
    const model = makeModel(list)
    const queryClient = new QueryClient()

    const loadingHtml = renderWithProviders(
      <ResourceSelect model={model} label="供应商" dropdownOpened comboboxProps={{ withinPortal: false }} />,
      queryClient,
    )
    expect(loadingHtml).toContain("mantine-Loader-root")
    expect(loadingHtml).toContain("加载中…")

    // 空列表时显示默认空态文案。
    queryClient.setQueryData(model.listAllQuery({ active: false }).queryKey, [])
    const emptyHtml = renderWithProviders(
      <ResourceSelect
        model={model}
        params={{ active: false }}
        label="供应商"
        dropdownOpened
        comboboxProps={{ withinPortal: false }}
      />,
      queryClient,
    )
    expect(emptyHtml).toContain("没有可选项")

    // 有数据时通过 renderRow 拿到原始行数据并渲染。
    queryClient.setQueryData(model.listAllQuery({ active: true }).queryKey, [
      { id: "p1", name: "OpenAI", vendor: "openai" },
    ])
    const renderRow = vi.fn((row) => `${String(row.name)} · ${String(row.vendor)}`)
    const loadedHtml = renderWithProviders(
      <ResourceSelect
        model={model}
        params={{ active: true }}
        label="供应商"
        value="p1"
        dropdownOpened
        comboboxProps={{ withinPortal: false }}
        renderRow={renderRow}
      />,
      queryClient,
    )
    expect(loadedHtml).toContain('value="OpenAI"')
    expect(loadedHtml).toContain("OpenAI · openai")
    expect(renderRow).toHaveBeenCalledWith(
      { id: "p1", name: "OpenAI", vendor: "openai" },
      expect.objectContaining({ option: { value: "p1", label: "OpenAI" } }),
    )
  })
})
