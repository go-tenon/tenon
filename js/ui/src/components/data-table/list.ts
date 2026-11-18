// 列表：查询条件、排序和分页同步到地址。刷新页面、分享链接、浏览器前进后退都回到同一个列表。
// 地址中的参数名与接口相同（page、per_page、sort 和各筛选条件），解析结果可以直接交给生成的客户端。

import { parseSort, type Schema, type Sort, type TableState, toTableState, validate } from "@go-tenon/client"
import { useNavigate } from "@tanstack/react-router"
import { useCallback, useMemo, useState } from "react"

/** listSearch 的选项。 */
export interface ListSearchOptions {
  /** 生成的 schemas.ts 中的 components：查询参数引用的命名类型（例如枚举）。 */
  components?: { readonly [name: string]: Schema }
}

/** listSearch 返回的 validateSearch，带上各参数的默认值。 */
export type ListSearch<T> = ((search: Record<string, unknown>) => Partial<T>) & {
  /** 查询参数规则中的默认值，例如 { page: 1, per_page: 20 }；等于默认值的参数不写进地址。 */
  readonly defaults: Partial<T>
}

type Prop = { [key: string]: unknown }

function resolve(schema: Prop, components: ListSearchOptions["components"]): Prop {
  const ref = schema.$ref
  if (typeof ref === "string") {
    const target = components?.[ref.slice(ref.lastIndexOf("/") + 1)]
    return target ? resolve(target as Prop, components) : schema
  }
  return schema
}

function types(schema: Prop): string[] {
  const t = schema.type
  return Array.isArray(t) ? (t as string[]) : typeof t === "string" ? [t] : []
}

/** 把地址中的值转换为 schema 的类型：地址中都是字符串，或者路由解析出的 JSON 值。 */
function coerce(value: unknown, schema: Prop, components: ListSearchOptions["components"]): unknown {
  const s = resolve(schema, components)
  const t = types(s)
  if (t.includes("array")) {
    const items = (s.items ?? {}) as Prop
    const list = Array.isArray(value)
      ? value
      : typeof value === "string"
        ? value.split(",").filter((v) => v !== "")
        : [value]
    return list.map((v) => coerce(v, items, components))
  }
  if ((t.includes("integer") || t.includes("number")) && typeof value === "string" && value.trim() !== "") {
    const n = Number(value)
    return Number.isNaN(n) ? value : n
  }
  if (t.includes("boolean") && (value === "true" || value === "false")) return value === "true"
  if (t.includes("string") && (typeof value === "number" || typeof value === "boolean")) return String(value)
  return value
}

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)

/** 空值：不写进地址。 */
const empty = (v: unknown) => v === undefined || v === null || v === "" || (Array.isArray(v) && v.length === 0)

/**
 * 路由的 validateSearch：按生成的查询参数规则解析地址。值转换为规则中的类型；不合法的值、不认识的参数、
 * 空值和等于默认值的值都去掉。
 *
 *   validateSearch: listSearch<ArticleListInput>(schemas.article.list.query, { components })
 */
export function listSearch<T = Record<string, unknown>>(
  querySchema: Schema,
  options: ListSearchOptions = {},
): ListSearch<T> {
  const properties = ((querySchema as Prop).properties ?? {}) as Record<string, Prop>
  const defaults: Record<string, unknown> = {}
  for (const [name, prop] of Object.entries(properties)) {
    const d = resolve(prop, options.components).default ?? prop.default
    if (d !== undefined) defaults[name] = d
  }
  const parse = (search: Record<string, unknown>) => {
    const out: Record<string, unknown> = {}
    for (const [name, prop] of Object.entries(properties)) {
      if (empty(search[name])) continue
      const value = coerce(search[name], prop, options.components)
      const one = { type: "object", properties: { [name]: prop } }
      if (validate(one, { [name]: value }, { components: options.components, location: "query" }).length > 0) continue
      if (name in defaults && same(value, defaults[name])) continue
      out[name] = value
    }
    return out as Partial<T>
  }
  return Object.assign(parse, { defaults: defaults as Partial<T> })
}

/** useList 需要的路由：createFileRoute 返回的 Route。 */
export interface ListRoute<T> {
  useSearch(): Partial<T>
  options: { validateSearch?: unknown }
}

/** useList 的返回值。 */
export interface ListState<T> {
  /** 地址中的查询参数，直接交给生成的客户端，例如 api.article.list(list.params)。默认值由服务端补上。 */
  params: Partial<T>
  page: number
  perPage: number
  /** 排序参数，例如 -created；没有时为 undefined。 */
  sort?: string
  /** 除分页和排序之外的查询条件。 */
  filters: Partial<T>
  /** TanStack Table、ProTable 用的分页和排序状态：pageIndex 从 0 开始。 */
  tableState: TableState & { sorting: { id: string; desc: boolean }[] }
  setPage(page: number): void
  /** 修改每页条数，回到第一页。 */
  setPerPage(perPage: number): void
  /** 修改排序（例如 -created，undefined 表示默认排序），回到第一页。 */
  setSort(sort: string | Sort | undefined): void
  /** 合并查询条件（值为 undefined 时去掉这个条件），回到第一页。 */
  setFilters(filters: Partial<T>): void
  /** 清除全部查询条件和排序，回到第一页。 */
  reset(): void
}

/**
 * 列表的状态，保存在地址中。路由的 validateSearch 用 listSearch：
 *
 *   const list = useList(Route)
 *   const articles = useQuery(queries.article.list(list.params))
 */
export function useList<T>(route: ListRoute<T>): ListState<T> {
  const search = route.useSearch() as Record<string, unknown>
  const navigate = useNavigate()
  const defaults = ((route.options.validateSearch as ListSearch<T> | undefined)?.defaults ?? {}) as Record<
    string,
    unknown
  >

  const update = useCallback(
    (changes: Record<string, unknown>, keepPage = false) => {
      void navigate({
        to: ".",
        search: ((prev: Record<string, unknown>) => {
          const next: Record<string, unknown> = { ...prev, ...changes }
          if (!keepPage) delete next.page
          for (const [k, v] of Object.entries(next)) {
            if (empty(v) || (k in defaults && same(v, defaults[k]))) delete next[k]
          }
          return next
        }) as never,
      })
    },
    [navigate, defaults],
  )

  return useMemo(() => {
    const page = Number(search.page ?? defaults.page ?? 1)
    const perPage = Number(search.per_page ?? defaults.per_page ?? 20)
    const sort = typeof search.sort === "string" ? search.sort : undefined
    const { page: _p, per_page: _pp, sort: _s, ...filters } = search
    const parsed = parseSort(sort)
    return {
      params: search as Partial<T>,
      page,
      perPage,
      sort,
      filters: filters as Partial<T>,
      tableState: {
        ...toTableState({ page, per_page: perPage }),
        sorting: parsed ? [{ id: parsed.field, desc: parsed.desc }] : [],
      },
      setPage: (p: number) => update({ page: p }, true),
      setPerPage: (n: number) => update({ per_page: n }),
      setSort: (s: string | Sort | undefined) =>
        update({ sort: typeof s === "object" ? `${s.desc ? "-" : ""}${s.field}` : s }),
      setFilters: (f: Partial<T>) => update(f as Record<string, unknown>),
      reset: () => update(Object.fromEntries(Object.keys(search).map((k) => [k, undefined]))),
    }
  }, [search, defaults, update])
}

/** useSelection 的返回值。 */
export interface Selection<K> {
  /** 选中的键。 */
  keys: ReadonlySet<K>
  has(key: K): boolean
  toggle(key: K): void
  /** 设置选中的键，例如表格的“全选当前页”。 */
  set(keys: Iterable<K>): void
  clear(): void
}

/**
 * 列表的选中项，用于批量操作。resetOn 变化时（例如 list.params）清空：换了查询条件或页码后，原来选中的行已经不在眼前。
 *
 *   const selection = useSelection<string>(list.params)
 */
export function useSelection<K>(resetOn?: unknown): Selection<K> {
  const [state, setState] = useState<{ keys: ReadonlySet<K>; on: string }>({ keys: new Set(), on: "" })
  const on = JSON.stringify(resetOn ?? null)
  const keys = state.on === on ? state.keys : new Set<K>()
  return useMemo(
    () => ({
      keys,
      has: (k: K) => keys.has(k),
      toggle: (k: K) => {
        const next = new Set(keys)
        if (next.has(k)) next.delete(k)
        else next.add(k)
        setState({ keys: next, on })
      },
      set: (ks: Iterable<K>) => setState({ keys: new Set(ks), on }),
      clear: () => setState({ keys: new Set(), on }),
    }),
    [keys, on],
  )
}
