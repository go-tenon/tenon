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

/** 固定分页的响应：与服务端的 page.Offset 一致，可选包含 total_pages。 */
export interface OffsetPage<T> {
  items: readonly T[]
  total?: number
  page?: number
  per_page?: number
  total_pages?: number
}

/** fetchAllPages 的选项。 */
export interface FetchAllPagesOptions {
  /** 每页拉取的条数，默认 pageDefaults.maxPerPage（100），超过上限时按上限取值。 */
  perPage?: number
  /** 最多拉取的页数上限，防止数据量过大时发起过多请求；默认 50。 */
  maxPages?: number
  /** 剩余页的并发请求数上限，默认 4。 */
  concurrency?: number
}

/**
 * 翻页拉取固定分页接口的全部数据：先以 perPage（默认 100）拉取第 1 页，再在 maxPages（默认 50）上限内
 * 按 concurrency（默认 4）并发拉取剩余页，按页序合并所有 items 返回。总页数优先取响应中的 total_pages，
 * 缺省时由 total 和每页条数计算。
 */
export async function fetchAllPages<T>(
  fetchPage: (params: PageParams) => Promise<OffsetPage<T>>,
  options: FetchAllPagesOptions = {},
): Promise<T[]> {
  const rawPerPage = options.perPage ?? pageDefaults.maxPerPage
  const perPage =
    Number.isFinite(rawPerPage) && rawPerPage >= 1
      ? Math.min(Math.floor(rawPerPage), pageDefaults.maxPerPage)
      : pageDefaults.maxPerPage
  const maxPages = Math.max(1, Math.floor(options.maxPages ?? 50))
  const concurrency = Math.max(1, Math.floor(options.concurrency ?? 4))

  const first = await fetchPage({ page: 1, per_page: perPage })
  const firstItems = [...first.items]
  if (firstItems.length === 0) return []

  const pageSize = typeof first.per_page === "number" && first.per_page > 0 ? first.per_page : perPage
  let totalPages = 1
  if (typeof first.total_pages === "number" && Number.isFinite(first.total_pages)) {
    totalPages = Math.max(1, Math.floor(first.total_pages))
  } else if (typeof first.total === "number" && Number.isFinite(first.total)) {
    totalPages = Math.max(1, Math.ceil(first.total / pageSize))
  }

  const lastPage = Math.min(totalPages, maxPages)
  if (lastPage <= 1) return firstItems

  const pages: (readonly T[])[] = new Array(lastPage)
  pages[0] = firstItems
  let next = 2
  const workers = Math.min(concurrency, lastPage - 1)
  await Promise.all(
    Array.from({ length: workers }, async () => {
      while (next <= lastPage) {
        const p = next++
        const res = await fetchPage({ page: p, per_page: pageSize })
        pages[p - 1] = res.items
      }
    }),
  )

  const result: T[] = []
  for (const items of pages) {
    if (items) result.push(...items)
  }
  return result
}

/** 表格状态转为查询参数。 */
export function toPageParams(state: TableState): PageParams {
  return { page: state.pageIndex + 1, per_page: state.pageSize }
}

/** 查询参数转为表格状态；缺少的参数取默认值。 */
export function toTableState(params: Partial<PageParams>): TableState {
  return { pageIndex: Math.max((params.page ?? 1) - 1, 0), pageSize: params.per_page ?? pageDefaults.perPage }
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
