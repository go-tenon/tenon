import { describe, expect, it } from "vitest"
import { formatSort, pageCount, parsePageParams, parseSort, toPageParams, toTableState } from "./pagination"

describe("pagination", () => {
  it("表格状态与查询参数互相转换", () => {
    expect(toPageParams({ pageIndex: 2, pageSize: 50 })).toEqual({ page: 3, per_page: 50 })
    expect(toTableState({ page: 3, per_page: 50 })).toEqual({ pageIndex: 2, pageSize: 50 })
    expect(toTableState({})).toEqual({ pageIndex: 0, pageSize: 20 })
    expect(pageCount(41, 20)).toBe(3)
    expect(pageCount(0, 20)).toBe(1)
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
