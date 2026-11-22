import { describe, expect, it, vi } from "vitest"
import {
  fetchAllPages,
  formatSort,
  type OffsetPage,
  type PageParams,
  parsePageParams,
  parseSort,
  toPageParams,
  toTableState,
} from "./pagination"

describe("pagination", () => {
  it("表格状态与查询参数互相转换", () => {
    expect(toPageParams({ pageIndex: 2, pageSize: 50 })).toEqual({ page: 3, per_page: 50 })
    expect(toTableState({ page: 3, per_page: 50 })).toEqual({ pageIndex: 2, pageSize: 50 })
    expect(toTableState({})).toEqual({ pageIndex: 0, pageSize: 20 })
  })

  it("从地址读取分页，不合法的值取默认值，per_page 不超过 100", () => {
    const search = new Map([
      ["page", "2"],
      ["per_page", "50"],
    ])
    expect(parsePageParams({ get: (k: string) => search.get(k) ?? null })).toEqual({ page: 2, per_page: 50 })
    expect(parsePageParams({ page: "x", per_page: 500 })).toEqual({ page: 1, per_page: 100 })
    expect(parsePageParams({}, { per_page: 10 })).toEqual({ page: 1, per_page: 10 })
    expect(parsePageParams({ page: -1, per_page: 0 })).toEqual({ page: 1, per_page: 20 })
  })

  it("排序参数", () => {
    expect(parseSort("-created")).toEqual({ field: "created", desc: true })
    expect(parseSort("title")).toEqual({ field: "title", desc: false })
    expect(parseSort("")).toBeUndefined()
    expect(formatSort({ field: "created", desc: true })).toBe("-created")
    expect(formatSort(undefined)).toBeUndefined()
  })
})

describe("fetchAllPages", () => {
  it("空结果只请求第 1 页并返回空数组", async () => {
    const fetchPage = vi.fn(
      async (p: PageParams): Promise<OffsetPage<number>> => ({
        items: [],
        page: p.page,
        per_page: p.per_page,
        total: 0,
      }),
    )
    await expect(fetchAllPages(fetchPage)).resolves.toEqual([])
    expect(fetchPage).toHaveBeenCalledTimes(1)
    expect(fetchPage).toHaveBeenCalledWith({ page: 1, per_page: 100 })
  })

  it("单页数据只请求一次", async () => {
    const fetchPage = vi.fn(
      async (p: PageParams): Promise<OffsetPage<number>> => ({
        items: [1, 2, 3],
        page: p.page,
        per_page: p.per_page,
        total: 3,
      }),
    )
    await expect(fetchAllPages(fetchPage)).resolves.toEqual([1, 2, 3])
    expect(fetchPage).toHaveBeenCalledTimes(1)
  })

  it("按 total 和 per_page 推导总页数并合并多页数据", async () => {
    const all = Array.from({ length: 235 }, (_, i) => i + 1)
    const calls: PageParams[] = []
    const fetchPage = async (p: PageParams): Promise<OffsetPage<number>> => {
      calls.push(p)
      const start = (p.page - 1) * p.per_page
      return {
        items: all.slice(start, start + p.per_page),
        page: p.page,
        per_page: p.per_page,
        total: all.length,
      }
    }
    const items = await fetchAllPages(fetchPage)
    expect(items).toEqual(all)
    expect(calls).toEqual([
      { page: 1, per_page: 100 },
      { page: 2, per_page: 100 },
      { page: 3, per_page: 100 },
    ])
  })

  it("优先使用响应中的 total_pages，并遵守服务端返回的较小 per_page", async () => {
    const calls: PageParams[] = []
    const fetchPage = async (p: PageParams): Promise<OffsetPage<string>> => {
      calls.push(p)
      return {
        items: [`p${p.page}-a`, `p${p.page}-b`],
        page: p.page,
        per_page: 2,
        total_pages: 3,
      }
    }
    const items = await fetchAllPages(fetchPage, { perPage: 500 })
    expect(items).toEqual(["p1-a", "p1-b", "p2-a", "p2-b", "p3-a", "p3-b"])
    expect(calls).toEqual([
      { page: 1, per_page: 100 },
      { page: 2, per_page: 2 },
      { page: 3, per_page: 2 },
    ])
  })

  it("并发拉取乱序返回时仍按页序合并，且不超过并发上限", async () => {
    let active = 0
    let maxActive = 0
    const pending = new Map<number, () => void>()
    const fetchPage = async (p: PageParams): Promise<OffsetPage<string>> => {
      if (p.page === 1) {
        return { items: ["page-1"], page: 1, per_page: 1, total: 5 }
      }
      active++
      maxActive = Math.max(maxActive, active)
      await new Promise<void>((resolve) => {
        pending.set(p.page, () => {
          active--
          resolve()
        })
      })
      return { items: [`page-${p.page}`], page: p.page, per_page: 1, total: 5 }
    }

    const promise = fetchAllPages(fetchPage, { perPage: 1, concurrency: 2 })
    // 最多只有 2 个并发请求在途（第 2、3 页）。
    await vi.waitFor(() => expect(pending.size).toBe(2))
    expect(maxActive).toBe(2)

    // 先完成第 3 页，再完成第 4 页、第 2 页、第 5 页（乱序返回）。
    pending.get(3)?.()
    pending.delete(3)
    await vi.waitFor(() => expect(pending.has(4)).toBe(true))
    expect(maxActive).toBe(2)

    pending.get(4)?.()
    pending.delete(4)
    await vi.waitFor(() => expect(pending.has(5)).toBe(true))

    pending.get(2)?.()
    pending.delete(2)
    pending.get(5)?.()
    pending.delete(5)

    await expect(promise).resolves.toEqual(["page-1", "page-2", "page-3", "page-4", "page-5"])
    expect(maxActive).toBe(2)
  })

  it("超过 maxPages 上限时停止拉取后续页", async () => {
    const fetched: number[] = []
    const fetchPage = async (p: PageParams): Promise<OffsetPage<number>> => {
      fetched.push(p.page)
      return {
        items: [p.page],
        page: p.page,
        per_page: 1,
        total: 100,
      }
    }
    const items = await fetchAllPages(fetchPage, { perPage: 1, maxPages: 3 })
    expect(items).toEqual([1, 2, 3])
    expect(fetched.sort((a, b) => a - b)).toEqual([1, 2, 3])
  })

  it("缺少 total 和 total_pages 时直接返回第 1 页", async () => {
    const fetchPage = vi.fn(async (): Promise<OffsetPage<string>> => ({ items: ["a", "b"] }))
    await expect(fetchAllPages(fetchPage)).resolves.toEqual(["a", "b"])
    expect(fetchPage).toHaveBeenCalledTimes(1)
  })

  it("任意一页失败时向上抛出错误", async () => {
    const fetchPage = async (p: PageParams): Promise<OffsetPage<number>> => {
      if (p.page === 2) throw new Error("page 2 failed")
      return { items: [p.page], page: p.page, per_page: 1, total: 3 }
    }
    await expect(fetchAllPages(fetchPage, { perPage: 1 })).rejects.toThrow("page 2 failed")
  })
})
