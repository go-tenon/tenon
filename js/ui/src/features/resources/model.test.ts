import { QueryClient } from "@tanstack/react-query"
import { describe, expect, it, vi } from "vitest"
import { type ResourceDef, type ResourceKit, resourceModel } from "./model"

function makeDef(overrides: Partial<ResourceDef> = {}): ResourceDef {
  return {
    name: "provider",
    key: "id",
    operations: {
      list: ["provider", "list"],
      create: ["provider", "create"],
      update: ["provider", "update"],
      delete: ["provider", "delete"],
    },
    permissions: { list: ["provider:read"], create: [], update: [], delete: [] },
    columns: [{ name: "name", type: "string" }],
    filters: [],
    createForm: [],
    updateForm: [],
    actions: [
      {
        name: "enable",
        operation: ["provider", "enable"],
        permissions: [],
        confirm: false,
        danger: false,
      },
    ],
    labels: { "zh-CN": { title: "供应商", fields: { name: "名称" }, actions: { enable: "启用" } } },
    ...overrides,
  }
}

describe("resourceModel.listAllQuery", () => {
  it("翻页拉取全部列表项并透传筛选与分页选项", async () => {
    const rows = Array.from({ length: 5 }, (_, i) => ({ id: `p${i + 1}`, name: `供应商 ${i + 1}`, active: true }))
    const list = vi.fn(async (params: Record<string, unknown>) => {
      const page = Number(params.page ?? 1)
      const perPage = Number(params.per_page ?? 20)
      return {
        items: rows.slice((page - 1) * perPage, page * perPage),
        page,
        per_page: perPage,
        total: rows.length,
      }
    })
    const kit: ResourceKit = {
      client: { provider: { list } },
      queries: {},
      schemas: {},
    }
    const model = resourceModel(makeDef(), kit)
    // 传入的 page 和 per_page 被剥离，不污染筛选条件和 queryKey。
    const query = model.listAllQuery({ active: true, page: 9, per_page: 1 }, { perPage: 2 })
    expect(query.queryKey).toEqual(["provider", "list", "all", { active: true }, { perPage: 2 }])

    const result = await query.queryFn()
    expect(result).toEqual(rows)
    expect(list).toHaveBeenCalledTimes(3)
    expect(list).toHaveBeenNthCalledWith(1, { active: true, page: 1, per_page: 2 })
    expect(list).toHaveBeenNthCalledWith(2, { active: true, page: 2, per_page: 2 })
    expect(list).toHaveBeenNthCalledWith(3, { active: true, page: 3, per_page: 2 })
  })

  it("复用分组的 queryKey 前缀，scope 变化后更新，修改资源后自动失效缓存", async () => {
    let scope = ["u1"]
    let calls = 0
    const client = {
      provider: {
        list: async () => {
          calls++
          return { items: [{ id: `p${calls}`, name: `第 ${calls} 次` }], page: 1, per_page: 100, total: 1 }
        },
        create: async (body: Record<string, unknown>) => body,
        update: async (_id: string, body: Record<string, unknown>) => body,
        delete: async () => undefined,
        enable: async () => undefined,
      },
    }
    const queries = {
      key: () => [...scope, "api"],
      provider: {
        key: () => [...scope, "api", "provider"],
        list: (params: Record<string, unknown>) => ({
          queryKey: [...scope, "api", "provider", "list", params],
          queryFn: () => client.provider.list(),
        }),
      },
    }
    const queryClient = new QueryClient()
    const model = resourceModel(makeDef(), { client, queries, schemas: {} }, queryClient)

    const q1 = model.listAllQuery()
    expect(q1.queryKey).toEqual(["u1", "api", "provider", "list", "all", {}])
    await queryClient.fetchQuery(q1)
    expect(queryClient.getQueryState(q1.queryKey)?.isInvalidated).toBe(false)

    await model.create({ name: "新供应商" })
    expect(queryClient.getQueryState(q1.queryKey)?.isInvalidated).toBe(true)

    await queryClient.fetchQuery(q1)
    expect(queryClient.getQueryState(q1.queryKey)?.isInvalidated).toBe(false)
    await model.update("p1", { name: "更新" })
    expect(queryClient.getQueryState(q1.queryKey)?.isInvalidated).toBe(true)

    await queryClient.fetchQuery(q1)
    await model.remove("p1")
    expect(queryClient.getQueryState(q1.queryKey)?.isInvalidated).toBe(true)

    await queryClient.fetchQuery(q1)
    await model.run("enable", "p1")
    expect(queryClient.getQueryState(q1.queryKey)?.isInvalidated).toBe(true)

    // 切换身份后 queryKey 带上新的 scope。
    scope = ["u2"]
    expect(model.listAllQuery().queryKey).toEqual(["u2", "api", "provider", "list", "all", {}])
  })

  it("同一分组下不同列表接口的 listAllQuery 缓存键互不冲突", () => {
    const queries = {
      admin: {
        key: () => ["api", "admin"],
      },
    }
    const users = resourceModel(makeDef({ name: "user", operations: { list: ["admin", "listUsers"] } }), {
      client: {},
      queries,
      schemas: {},
    })
    const roles = resourceModel(makeDef({ name: "role", operations: { list: ["admin", "listRoles"] } }), {
      client: {},
      queries,
      schemas: {},
    })
    expect(users.listAllQuery().queryKey).toEqual(["api", "admin", "listUsers", "all", {}])
    expect(roles.listAllQuery().queryKey).toEqual(["api", "admin", "listRoles", "all", {}])
  })
})

describe("引用字段（ref）", () => {
  it("从 kit.resources 解析引用资源模型、设置 select/multiselect 并保持引用稳定", () => {
    const roleDef = makeDef({
      name: "role",
      operations: { list: ["role", "list"] },
      permissions: { list: ["roles:read"] },
    })
    const memberDef = makeDef({
      name: "member",
      operations: { list: ["member", "list"], create: ["member", "create"] },
      filters: [{ name: "role_id", type: "string", ref: "role" }],
      createForm: [
        { name: "role_id", type: "string", ref: "role", required: true },
        { name: "extra_role_ids", type: "array", items: "string", ref: "role" },
      ],
    })
    const kit: ResourceKit = {
      client: {},
      queries: {},
      schemas: {},
      resources: { role: roleDef, member: memberDef },
    }
    const members = resourceModel(memberDef, kit)
    expect(members.filters[0].kind).toBe("select")
    expect(members.createForm[0].kind).toBe("select")
    expect(members.createForm[1].kind).toBe("multiselect")
    const refA = members.filters[0].reference
    const refB = members.createForm[0].reference
    const refC = members.createForm[1].reference
    expect(refA?.name).toBe("role")
    expect(refA).toBe(refB)
    expect(refB).toBe(refC)
  })

  it("支持自引用资源且不发生递归溢出，缺少 kit.resources 时安全降级", () => {
    const categoryDef = makeDef({
      name: "category",
      operations: { list: ["category", "list"], create: ["category", "create"] },
      createForm: [{ name: "parent_id", type: "string", ref: "category" }],
    })
    const kit: ResourceKit = {
      client: {},
      queries: {},
      schemas: {},
      resources: { category: categoryDef },
    }
    const categories = resourceModel(categoryDef, kit)
    expect(categories.createForm[0].kind).toBe("select")
    expect(categories.createForm[0].reference).toBe(categories)

    const fallback = resourceModel(categoryDef, { client: {}, queries: {}, schemas: {} })
    expect(fallback.createForm[0].kind).toBe("text")
    expect(fallback.createForm[0].reference).toBeUndefined()
  })
})
