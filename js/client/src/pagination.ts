// 分页：固定分页的 page、per_page、total 与表格状态、地址中查询参数之间的转换。

/** 固定分页的查询参数：page 从 1 开始。 */
export interface PageParams {
  page: number
  per_page: number
}

/** 表格的分页状态，与 TanStack Table 相同：pageIndex 从 0 开始。 */
export interface TableState {
  pageIndex: number
  pageSize: number
}

/** 分页的默认值和范围，与服务端的 page.OffsetParams 相同。 */
export const pageDefaults = { perPage: 20, maxPerPage: 100 } as const

/** 表格状态转为查询参数。 */
export function toPageParams(state: TableState): PageParams {
  return { page: state.pageIndex + 1, per_page: state.pageSize }
}

/** 查询参数转为表格状态；缺少的参数取默认值。 */
export function toTableState(params: Partial<PageParams>): TableState {
  return { pageIndex: Math.max((params.page ?? 1) - 1, 0), pageSize: params.per_page ?? pageDefaults.perPage }
}

/** 总页数：total 条记录、每页 perPage 条，至少 1 页。 */
export function pageCount(total: number, perPage: number): number {
  return Math.max(Math.ceil(total / Math.max(perPage, 1)), 1)
}

/**
 * 从地址的查询参数中读取分页：不是正整数的值取默认值，per_page 限制在 1 到 100 之间，与服务端接受的范围相同。
 * search 可以是 URLSearchParams 这类带 get 方法的对象，也可以是路由解析出的对象。
 */
export function parsePageParams(
  search: { get(name: string): string | null } | Record<string, unknown>,
  defaults: Partial<PageParams> = {},
): PageParams {
  const get = (k: string) =>
    typeof search.get === "function"
      ? (search as { get(name: string): string | null }).get(k)
      : (search as Record<string, unknown>)[k]
  const int = (v: unknown) => {
    const n = typeof v === "number" ? v : Number(v)
    return Number.isInteger(n) && n > 0 ? n : undefined
  }
  const perPage = int(get("per_page")) ?? defaults.per_page ?? pageDefaults.perPage
  return { page: int(get("page")) ?? defaults.page ?? 1, per_page: Math.min(perPage, pageDefaults.maxPerPage) }
}

/** 排序：字段和方向。 */
export interface Sort {
  field: string
  desc: boolean
}

/** 解析排序参数，前面的 - 表示降序，例如 -created。空值返回 undefined。 */
export function parseSort(value: string | null | undefined): Sort | undefined {
  if (!value) return undefined
  return value.startsWith("-") ? { field: value.slice(1), desc: true } : { field: value, desc: false }
}

/** 写出排序参数，与 parseSort 相反。 */
export function formatSort(sort: Sort | undefined): string | undefined {
  return sort ? (sort.desc ? "-" : "") + sort.field : undefined
}
