// 生成的 queries.ts 与 TanStack Query 一起工作：用 clientgen 的示例客户端（clientgen/internal/example/ts）和真实的 QueryClient 测试。
// 放在 start 中：这里的测试可以用 TanStack Query，类型检查带 DOM。

import { MutationObserver, QueryClient, QueryObserver } from "@tanstack/react-query"
import { describe, expect, it, vi } from "vitest"
import type { Client } from "../../../clientgen/internal/example/ts/api"
import { createQueries } from "../../../clientgen/internal/example/ts/queries"

describe("生成的 queries", () => {
  it("修改成功后用执行修改的 QueryClient 让同一分组的查询失效，createQueries 不用传入 QueryClient", async () => {
    let lists = 0
    const client = {
      article: {
        list: async () => {
          lists++
          return { items: [], page: 1, per_page: 20, total: 0 }
        },
        create: async () => ({ id: "1" }),
      },
    } as unknown as Client
    // 与 api.ts 中在模块里创建 kit 一样：没有 queryClient。
    const queries = createQueries(client)
    const queryClient = new QueryClient()
    const observer = new QueryObserver(queryClient, queries.article.list())
    const unsubscribe = observer.subscribe(() => {})
    await vi.waitFor(() => expect(lists).toBe(1))

    await new MutationObserver(queryClient, queries.article.create()).mutate({} as never)
    await vi.waitFor(() => expect(lists).toBe(2))
    unsubscribe()
  })

  it("设置了 queryClient 时用它失效", async () => {
    const invalidated: unknown[] = []
    const client = { article: { create: async () => ({ id: "1" }) } } as unknown as Client
    const queries = createQueries(client, {
      queryClient: { invalidateQueries: (filters) => invalidated.push(filters.queryKey) },
    })
    await new MutationObserver(new QueryClient(), queries.article.create()).mutate({} as never)
    expect(invalidated).toEqual([["api", "article"]])
  })
})
