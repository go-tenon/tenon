// 资源：服务端用 rest.API.Resource 声明、tenon generate 生成在 resources.ts 中的后台页面定义。
// 这里把定义和生成的客户端、校验规则、枚举接在一起，得到与组件库无关的模型：列、筛选条件、表单字段（带显示文字、
// 输入方式和选项）、权限、读取和修改。ResourcePage（resource-page.tsx）按这个模型渲染。
// 设置（rest.API.Settings、settings.ts）是只有一组值的表单，用同样的字段模型，见 settingsModel。

import {
  type FetchAllPagesOptions,
  fetchAllPages,
  type OffsetPage,
  type PageParams,
  pickLanguage,
  type Schema,
} from "@go-tenon/client"
import { keepPreviousData, type QueryClient, useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useCallback, useMemo } from "react"
import { useCan } from "../../app/app"
import { type PreloadOptions, preloadQueries } from "../../app/preload"
import { type ListRoute, type ListSearch, type ListState, listSearch, useList } from "../../components/data-table/list"
import { useSmoothTransition } from "../../components/loading"

/** 客户端方法的位置：[分组, 方法]，没有分组时分组为 null。 */
export type OperationRef = readonly [string | null, string]

/** resources.ts 中的字段。 */
export interface ResourceFieldDef {
  readonly name: string
  readonly type: string
  readonly format?: string
  /** enums.ts 中的命名类型。 */
  readonly enum?: string
  /** 没有命名类型的枚举的取值。 */
  readonly values?: readonly string[]
  /** 数组元素的类型。 */
  readonly items?: string
  /** 引用的资源名（resources.ts 中的键），由 Go 中 rest.Resource.Refs 或 rest.Settings.Refs 声明。 */
  readonly ref?: string
  readonly nullable?: boolean
  readonly required?: boolean
  readonly sortable?: boolean
}

/** resources.ts 中的一个资源。 */
export interface ResourceDef {
  readonly name: string
  readonly key?: string
  readonly operations: {
    readonly list: OperationRef
    readonly get?: OperationRef
    readonly create?: OperationRef
    readonly update?: OperationRef
    readonly delete?: OperationRef
  }
  readonly permissions: { readonly [operation: string]: readonly string[] }
  readonly columns: readonly ResourceFieldDef[]
  readonly filters: readonly ResourceFieldDef[]
  readonly createForm: readonly ResourceFieldDef[]
  readonly updateForm: readonly ResourceFieldDef[]
  readonly actions: readonly {
    readonly name: string
    readonly operation: OperationRef
    readonly permissions: readonly string[]
    readonly confirm: boolean
    readonly danger: boolean
    /** 显示的条件：字段 → 允许的取值，见 rest.Action 的 When。 */
    readonly when?: { readonly [field: string]: readonly string[] }
  }[]
  /** 一行上显示“修改”“删除”的条件，写法与 actions 的 when 相同，见 rest.Resource 的 UpdateWhen、DeleteWhen。 */
  readonly updateWhen?: { readonly [field: string]: readonly string[] }
  readonly deleteWhen?: { readonly [field: string]: readonly string[] }
  readonly labels: {
    readonly [language: string]: {
      readonly title?: string
      readonly fields: { readonly [field: string]: string }
      /** 字段的说明，没有说明的字段不在其中。 */
      readonly descriptions?: { readonly [field: string]: string }
      readonly actions: { readonly [action: string]: string }
    }
  }
}

type Generated = { readonly [key: string]: unknown }

/** 表单的校验选项：交给 formValidator 或 useSchemaForm。 */
export interface RuleOptions {
  components?: { readonly [name: string]: Schema }
  messages?: { readonly [language: string]: { readonly [code: string]: string } }
  language?: string
}

/** 生成的代码：一个 API 的客户端和元数据，交给 resourceModel。生成的 kit.ts 中 createKit 的返回值满足它。 */
export interface ResourceKit {
  /** createClient 创建的客户端。 */
  client: object
  /** createQueries 创建的查询选项：列表和详情的 query key 与应用中的其他查询相同。 */
  queries: object
  /** schemas.ts 中的 schemas 和 components。 */
  schemas: Generated
  components?: { readonly [name: string]: Schema }
  /** enums.ts 中的 enums。 */
  enums?: { readonly [name: string]: { readonly values: readonly string[]; readonly labels: Generated } }
  /** resources.ts 中的全部资源定义，供引用字段按名称解析关联的资源模型。 */
  resources?: { readonly [name: string]: ResourceDef }
  /** messages.ts 中的 messages：验证提示的模板。没有时表单中的提示是错误码，例如 validation.filled。 */
  messages?: { readonly [language: string]: { readonly [code: string]: string } }
  /**
   * 显示文字的语言，例如 zh-CN，或者返回当前语言的函数（应用的 i18n.language）：模型的标题、字段名、枚举的文字在读取时
   * 按它取值，切换语言后组件重新渲染即显示新的语言。没有这种语言时依次用主语言和第一种语言。默认是浏览器的语言。
   */
  language?: string | (() => string | undefined)
}

/** 字段的输入和显示方式，由类型、格式和枚举得出。secret 是格式为 password 的字符串，例如密钥：显示时遮住中间部分。 */
export type FieldKind =
  | "text"
  | "multiline"
  | "secret"
  | "number"
  | "boolean"
  | "select"
  | "multiselect"
  | "tags"
  | "date"
  | "datetime"
  | "json"

/** 模型中的字段：显示文字、输入方式、选项。 */
export interface ResourceField {
  name: string
  label: string
  /** 说明：翻译文件中 <名称>.descriptions.<字段> 的消息，显示在标签和输入框之间；没有时为空。 */
  description?: string
  kind: FieldKind
  required: boolean
  sortable: boolean
  /** 枚举的选项。 */
  options?: { value: string; label: string }[]
  /** 引用的资源模型：字段声明了 ref 且 kit.resources 中存在目标资源时给出。 */
  reference?: ResourceModel
  def: ResourceFieldDef
}

/** 模型中的操作。 */
export interface ResourceAction {
  name: string
  label: string
  confirm: boolean
  danger: boolean
  permissions: readonly string[]
  /** 这个操作是否显示在 row 上：声明了 When 时，row 的每个字段都取允许的值之一。 */
  appliesTo(row: ResourceRow): boolean
}

/** 资源的操作：读取、创建、修改、删除和其他操作。 */
export type ResourceOperation = "list" | "get" | "create" | "update" | "delete"

/** 一行数据。 */
export type ResourceRow = { readonly [field: string]: unknown }

/**
 * 写入接口的请求体：新建、修改和保存设置时交给模型的值。约束与 @mantine/form 的 useForm、useSchemaForm 相同，
 * 生成的客户端中的请求体类型（interface）可以直接传入，例如 save.mutateAsync({ values: changes as ArticleChanges })。
 */
// biome-ignore lint/suspicious/noExplicitAny: 与 @mantine/form 的 useForm 相同的约束，interface 才能满足它
export type RequestValues = Record<string, any>

/** 与组件库无关的资源模型。 */
export interface ResourceModel {
  name: string
  title: string
  /** 标识一行的字段，例如 id；没有详情、修改、删除和操作时为空。 */
  key?: string
  columns: ResourceField[]
  filters: ResourceField[]
  /** 新建表单的字段：新建接口请求体的字段。 */
  createForm: ResourceField[]
  /** 修改表单的字段：修改接口请求体的字段，常常与新建不同。 */
  updateForm: ResourceField[]
  actions: ResourceAction[]
  /** 有哪些接口。 */
  has: Record<ResourceOperation, boolean>
  /** row 上是否显示“修改”：声明了 UpdateWhen 时，row 的每个字段都取允许的值之一。不检查权限，权限见 allowed。 */
  updatable(row: ResourceRow): boolean
  /** row 上是否显示“删除”：声明了 DeleteWhen 时，row 的每个字段都取允许的值之一。不检查权限。 */
  deletable(row: ResourceRow): boolean
  /** 列表路由的 validateSearch：查询条件、排序和分页保存在地址中。 */
  listSearch: ListSearch<Record<string, unknown>>
  /** 当前身份能否使用一个接口或操作：接口要求的权限都具备。 */
  allowed(can: (permission: string) => boolean, operation: ResourceOperation | { action: string }): boolean
  /** 列表的查询选项，交给 useQuery。 */
  listQuery(params: Record<string, unknown>): { queryKey: readonly unknown[]; queryFn: () => Promise<unknown> }
  /** 翻页拉取全部列表项的查询选项，交给 useQuery；与列表共用查询前缀，修改资源后自动失效。 */
  listAllQuery(
    params?: Record<string, unknown>,
    options?: FetchAllPagesOptions,
  ): { queryKey: readonly unknown[]; queryFn: () => Promise<ResourceRow[]> }
  /** 详情的查询选项。 */
  getQuery(key: string): { queryKey: readonly unknown[]; queryFn: () => Promise<unknown> }
  /** 创建、修改、删除和其他操作；完成后让这个资源所在分组的查询失效。 */
  create(body: RequestValues): Promise<unknown>
  update(key: string, body: RequestValues): Promise<unknown>
  remove(key: string): Promise<unknown>
  run(action: string, key: string): Promise<unknown>
  /** 表单的校验规则（请求体的 schema），交给 formValidator 或 useSchemaForm。 */
  formSchema(mode: "create" | "update"): Schema
  /** components、messages 和 language，与 formSchema 一起交给 formValidator 或 useSchemaForm。 */
  ruleOptions: RuleOptions
  /** 一行的标识。 */
  rowKey(row: ResourceRow): string
  /**
   * 字段的值的显示文字：枚举显示它的文字，其他原样返回。field 可以是字段名，依次在列、筛选条件和表单字段中查找，
   * 例如在自定义的列中显示枚举：model.display("status", row.status)。
   */
  display(field: ResourceField | string, value: unknown): unknown
  /**
   * 资源所在分组全部查询的 key 前缀：完成修改后让它们失效。每次读取时计算，createQueries 带 scope 时
   * 包含当前的身份（模型通常在登录之前创建）。
   */
  readonly queryKey: readonly unknown[]
}

/** row 是否满足显示条件 when：每个字段都取允许的值之一（按字符串比较）；没有条件时总是满足。 */
function matchesWhen(when: { readonly [field: string]: readonly string[] } | undefined, row: ResourceRow): boolean {
  return Object.entries(when ?? {}).every(([field, values]) => values.includes(String(row[field] ?? "")))
}

function browserLanguage(): string | undefined {
  return typeof navigator === "undefined" ? undefined : navigator.language
}

/** 返回读取当前显示语言的函数：kit.language 是函数时每次调用它，是文字时用它，没有时用浏览器的语言。 */
function languageOf(kit: Pick<ResourceKit, "language">): () => string | undefined {
  const l = kit.language
  return typeof l === "function" ? () => l() ?? browserLanguage() : () => l ?? browserLanguage()
}

function kindOf(def: ResourceFieldDef, hasOptions: boolean): FieldKind {
  if (def.type === "array") {
    if (hasOptions) return "multiselect"
    return def.items === "string" ? "tags" : "json"
  }
  if (hasOptions) return "select"
  switch (def.type) {
    case "boolean":
      return "boolean"
    case "integer":
    case "number":
      return "number"
    case "object":
      return "json"
  }
  if (def.format === "password") return "secret"
  if (def.format === "multiline") return "multiline"
  if (def.format === "date-time") return "datetime"
  if (def.format === "date") return "date"
  return "text"
}

function method(target: object, ref: OperationRef): ((...args: unknown[]) => unknown) | undefined {
  const [group, name] = ref
  const scope = (group === null ? target : (target as Generated)[group]) as Generated | undefined
  const fn = scope?.[name]
  return typeof fn === "function" ? (fn as (...args: unknown[]) => unknown) : undefined
}

function call(target: object, ref: OperationRef | undefined, args: unknown[]): unknown {
  if (!ref) throw new Error("resource: operation not declared")
  const fn = method(target, ref)
  if (!fn) throw new Error(`resource: no method ${ref.filter(Boolean).join(".")}`)
  return fn(...args)
}

function schemaAt(schemas: Generated, ref: OperationRef, part: "body" | "query"): Schema {
  const [group, name] = ref
  const scope = (group === null ? schemas : schemas[group]) as Generated | undefined
  const op = scope?.[name] as { body?: Schema; query?: Schema } | undefined
  return op?.[part] ?? {}
}

// 按 kit 缓存引用字段解析出的关联资源模型：保持引用稳定（避免 useMemo 重算），并防止自引用或互相引用无限递归。
const refModels = new WeakMap<ResourceKit, Map<string, ResourceModel>>()

function resolveReference(kit: ResourceKit, name: string): ResourceModel | undefined {
  const target = kit.resources?.[name]
  if (!target) return undefined
  let cache = refModels.get(kit)
  if (!cache) {
    cache = new Map()
    refModels.set(kit, cache)
  }
  let model = cache.get(name)
  if (!model) {
    model = resourceModel(target, kit)
    cache.set(name, model)
  }
  return model
}

/**
 * 由字段的定义得到模型中的字段：显示文字、说明、输入方式、枚举选项和引用资源。显示文字、说明和选项的文字在读取时
 * 按 language() 取值，labels 返回当前语言的字段名和说明。
 */
function fieldBuilder(
  kit: ResourceKit,
  language: () => string | undefined,
  labels: () => { fields?: { readonly [field: string]: string }; descriptions?: { readonly [field: string]: string } },
): (f: ResourceFieldDef) => ResourceField {
  const options = (f: ResourceFieldDef): ResourceField["options"] => {
    if (f.enum && kit.enums?.[f.enum]) {
      const e = kit.enums[f.enum]
      const text = pickLanguage(e.labels as { [l: string]: { [v: string]: string } }, language()) ?? {}
      return e.values.map((value) => ({ value, label: text[value] ?? value }))
    }
    if (f.values) return f.values.map((value) => ({ value, label: value }))
    return undefined
  }
  return (f) => {
    const hasOptions = !!(f.enum && kit.enums?.[f.enum]) || !!f.values
    const hasRef = !!(f.ref && kit.resources?.[f.ref])
    return {
      name: f.name,
      get label() {
        return labels().fields?.[f.name] ?? f.name
      },
      get description() {
        return labels().descriptions?.[f.name]
      },
      kind: kindOf(f, hasOptions || hasRef),
      required: !!f.required,
      sortable: !!f.sortable,
      get options() {
        return options(f)
      },
      get reference() {
        return f.ref ? resolveReference(kit, f.ref) : undefined
      },
      def: f,
    }
  }
}

/** 枚举字段显示它的文字，其他原样返回。 */
function displayValue(f: ResourceField, value: unknown): unknown {
  if (!f.options) return value
  const label = (v: unknown) => f.options?.find((o) => o.value === v)?.label ?? v
  return Array.isArray(value) ? value.map(label) : label(value)
}

/**
 * 由 resources.ts 中的定义得到资源模型：
 *
 *	export const kit = createKit(api, { scope: session.scope, language: "zh-CN" }) // 生成的 kit.ts
 *	export const articles = resourceModel(kit.resources.article, kit)
 *
 * queryClient 用来在修改后让查询失效。
 */
export function resourceModel(def: ResourceDef, kit: ResourceKit, queryClient?: QueryClient): ResourceModel {
  const language = languageOf(kit)
  const labels = () => pickLanguage(def.labels, language())
  const field = fieldBuilder(kit, language, () => labels() ?? {})

  const ops = def.operations
  const group = ops.list[0]
  const queries = kit.queries as Generated
  const groupQueries = (group === null ? queries : queries[group]) as { key?: () => readonly unknown[] } | undefined
  // 每次调用时计算：createQueries 的 scope（账号、租户）在登录、切换租户后变化。
  const queryKey = (): readonly unknown[] =>
    groupQueries?.key?.() ?? (queries.key as (() => readonly unknown[]) | undefined)?.() ?? [def.name]
  const invalidate = async <T>(p: Promise<T>): Promise<T> => {
    const result = await p
    await queryClient?.invalidateQueries({ queryKey: [...queryKey()] })
    return result
  }

  const columns = def.columns.map(field)
  const filters = def.filters.map(field)
  const createForm = def.createForm.map(field)
  const updateForm = def.updateForm.map(field)
  const model: ResourceModel = {
    name: def.name,
    get title() {
      return labels()?.title ?? def.name
    },
    key: def.key,
    columns,
    filters,
    createForm,
    updateForm,
    actions: def.actions.map((a) => ({
      name: a.name,
      get label() {
        return labels()?.actions[a.name] ?? a.name
      },
      confirm: a.confirm,
      danger: a.danger,
      permissions: a.permissions,
      appliesTo: (row) => matchesWhen(a.when, row),
    })),
    has: { list: true, get: !!ops.get, create: !!ops.create, update: !!ops.update, delete: !!ops.delete },
    updatable: (row) => matchesWhen(def.updateWhen, row),
    deletable: (row) => matchesWhen(def.deleteWhen, row),
    listSearch: listSearch(schemaAt(kit.schemas, ops.list, "query"), { components: kit.components }),
    allowed(can, operation) {
      if (typeof operation === "object") {
        const a = def.actions.find((x) => x.name === operation.action)
        return !!a && a.permissions.every(can)
      }
      if (!ops[operation]) return false
      return (def.permissions[operation] ?? []).every(can)
    },
    listQuery(params) {
      const q = method(queries, ops.list)
      return q
        ? (q(params) as ReturnType<ResourceModel["listQuery"]>)
        : {
            queryKey: [...queryKey(), "list", params],
            queryFn: () => call(kit.client, ops.list, [params]) as Promise<unknown>,
          }
    },
    listAllQuery(params = {}, options) {
      const { page: _page, per_page: _perPage, ...filters } = params
      return {
        queryKey: options
          ? [...queryKey(), ops.list[1], "all", filters, options]
          : [...queryKey(), ops.list[1], "all", filters],
        queryFn: () =>
          fetchAllPages((p: PageParams) => {
            const merged = { ...filters, ...p }
            const q = method(queries, ops.list)
            return q
              ? (q(merged) as { queryFn: () => Promise<OffsetPage<ResourceRow>> }).queryFn()
              : (call(kit.client, ops.list, [merged]) as Promise<OffsetPage<ResourceRow>>)
          }, options),
      }
    },
    getQuery(key) {
      const q = ops.get && method(queries, ops.get)
      return q
        ? (q(key) as ReturnType<ResourceModel["getQuery"]>)
        : { queryKey: [...queryKey(), "get", key], queryFn: () => call(kit.client, ops.get, [key]) as Promise<unknown> }
    },
    create: (body) => invalidate(Promise.resolve(call(kit.client, ops.create, [body]))),
    update: (key, body) => invalidate(Promise.resolve(call(kit.client, ops.update, [key, body]))),
    remove: (key) => invalidate(Promise.resolve(call(kit.client, ops.delete, [key]))),
    run(action, key) {
      const a = def.actions.find((x) => x.name === action)
      return invalidate(Promise.resolve(call(kit.client, a?.operation, [key])))
    },
    formSchema(mode) {
      const ref = mode === "create" ? (ops.create ?? ops.update) : (ops.update ?? ops.create)
      return ref ? schemaAt(kit.schemas, ref, "body") : {}
    },
    get ruleOptions() {
      return { components: kit.components, messages: kit.messages, language: language() }
    },
    rowKey(row) {
      return String(row[def.key ?? "id"] ?? "")
    },
    display(f, value) {
      const found =
        typeof f === "string" ? [...columns, ...filters, ...createForm, ...updateForm].find((c) => c.name === f) : f
      return found ? displayValue(found, value) : value
    },
    get queryKey() {
      return queryKey()
    },
  }
  if (kit.resources?.[def.name] === def) {
    let cache = refModels.get(kit)
    if (!cache) {
      cache = new Map()
      refModels.set(kit, cache)
    }
    if (!cache.has(def.name)) cache.set(def.name, model)
  }
  return model
}

/** 列表的数据：page.Offset（items、total）或 page.Cursor（items、next_cursor）。 */
interface ListResponse {
  items: ResourceRow[]
  total?: number
  next_cursor?: string
}

/** useResourceList 的返回值。 */
export interface ResourceList {
  model: ResourceModel
  /** 地址中的查询条件、排序和分页，见 useList。 */
  list: ListState<Record<string, unknown>>
  rows: ResourceRow[]
  total?: number
  loading: boolean
  error: unknown
  /** 当前身份能否使用：没有声明的接口也是 false。 */
  can: {
    create: boolean
    update: boolean
    delete: boolean
    get: boolean
    action(name: string): boolean
  }
  refetch(): void
}

/**
 * 资源的列表：查询条件保存在地址中，数据来自列表接口，按权限得出能用的按钮。列表路由的 validateSearch 用 model.listSearch：
 *
 *	export const Route = createFileRoute("/_app/articles")({ validateSearch: articles.listSearch, component: Articles })
 *	const view = useResourceList(articles, Route)
 */
export function useResourceList(model: ResourceModel, route: ListRoute<Record<string, unknown>>): ResourceList {
  const list = useList(route)
  const can = useCan()
  // 进入页面和换筛选、翻页、排序时总是向服务器读最新的：缓存中已有的（路由 loader 预取的、上次看过的）先显示，
  // 后台刷新后静默替换；换了条件还在读取时暂留旧行，慢请求超过 delayMs 才叠加加载态。
  const query = useQuery({ ...model.listQuery(list.params), placeholderData: keepPreviousData })
  const transition = useSmoothTransition(
    query.data as ListResponse | undefined,
    JSON.stringify(list.params),
    query.isFetching,
  )
  const page = transition.data
  return {
    model,
    list,
    rows: page?.items ?? [],
    total: page?.total,
    loading: (query.isPending && !page) || transition.busy,
    error: query.error,
    can: {
      create: model.allowed(can, "create"),
      update: model.allowed(can, "update"),
      delete: model.allowed(can, "delete"),
      get: model.allowed(can, "get"),
      action: (name) => model.allowed(can, { action: name }),
    },
    refetch: () => void query.refetch(),
  }
}

/**
 * preloadResourceList 在列表路由的 loader 中预取当前地址条件下的列表，进入页面时表格直接显示数据；
 * 缓存中已有时在后台刷新、立即放行。params 是路由的 search（loaderDeps 返回它）：
 *
 *	loaderDeps: ({ search }) => search,
 *	loader: ({ context, deps }) => preloadResourceList(context.app.queryClient, articles, deps),
 */
export function preloadResourceList(
  queryClient: QueryClient,
  model: ResourceModel,
  params: Record<string, unknown>,
  options?: PreloadOptions,
): Promise<void> {
  return preloadQueries(queryClient, [model.listQuery(params)], options)
}

/** 一行的详情：编辑表单的初始值。key 为空时不读取（例如新建）。 */
export function useResourceItem(model: ResourceModel, key: string | undefined) {
  return useQuery({ ...model.getQuery(key ?? ""), enabled: !!key && model.has.get })
}

/** 资源的修改：创建、修改、删除和其他操作，带进行中的状态；完成后列表随之刷新。invalidate 在自己调用接口修改了数据之后刷新列表和详情。 */
export function useResourceMutations(model: ResourceModel) {
  const queryClient = useQueryClient()
  // invalidate 让这个资源所在分组的查询失效：列表和详情重新读取。自己调用接口修改了数据时（不经过 save、remove、run）
  // 调用它，例如 await api.tenant.changeOwner(id, body); await invalidate()。
  const done = useCallback(() => queryClient.invalidateQueries({ queryKey: [...model.queryKey] }), [queryClient, model])
  const save = useMutation({
    mutationFn: ({ key, values }: { key?: string; values: RequestValues }) =>
      key ? model.update(key, values) : model.create(values),
    onSuccess: done,
  })
  const remove = useMutation({ mutationFn: (key: string) => model.remove(key), onSuccess: done })
  const run = useMutation({
    mutationFn: ({ action, key }: { action: string; key: string }) => model.run(action, key),
    onSuccess: done,
  })
  return useMemo(() => ({ save, remove, run, invalidate: done }), [save, remove, run, done])
}

/** 表单的初始值：fields 在 row（详情、列表中的这一行或设置）中的值；新建时 row 为空，布尔是 false，数组是空数组。 */
export function formValues(fields: readonly ResourceField[], row?: ResourceRow): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const f of fields) {
    const v = row?.[f.name]
    if (v !== undefined) out[f.name] = v
    else if (f.kind === "boolean") out[f.name] = false
    else if (f.kind === "tags" || f.kind === "multiselect") out[f.name] = []
  }
  return out
}

// ———————————————————————————— 设置 ————————————————————————————

/** settings.ts 中的一组设置。 */
export interface SettingsDef {
  readonly name: string
  readonly operations: { readonly get: OperationRef; readonly update: OperationRef }
  readonly permissions: { readonly get: readonly string[]; readonly update: readonly string[] }
  readonly form: readonly ResourceFieldDef[]
  readonly labels: {
    readonly [language: string]: {
      readonly title?: string
      readonly fields: { readonly [field: string]: string }
      /** 字段的说明，没有说明的字段不在其中。 */
      readonly descriptions?: { readonly [field: string]: string }
    }
  }
}

/** 与组件库无关的设置模型。 */
export interface SettingsModel {
  name: string
  title: string
  form: ResourceField[]
  /** 当前身份能否读取、保存：接口要求的权限都具备。 */
  allowed(can: (permission: string) => boolean, operation: "get" | "update"): boolean
  /** 读取设置的查询选项，交给 useQuery。 */
  query(): { queryKey: readonly unknown[]; queryFn: () => Promise<unknown> }
  /** 保存：调用保存接口，完成后让读取的查询失效。 */
  save(values: RequestValues): Promise<unknown>
  /** 表单的校验规则（保存接口的请求体），交给 formValidator 或 useSchemaForm。 */
  formSchema(): Schema
  ruleOptions: RuleOptions
  display(field: ResourceField, value: unknown): unknown
}

/**
 * 由 settings.ts 中的定义得到设置模型：
 *
 *	export const site = settingsModel(kit.settings.site, kit)
 *
 * queryClient 用来在保存后让读取的查询失效。
 */
export function settingsModel(def: SettingsDef, kit: ResourceKit, queryClient?: QueryClient): SettingsModel {
  const language = languageOf(kit)
  const labels = () => pickLanguage(def.labels, language())
  const field = fieldBuilder(kit, language, () => labels() ?? {})
  const ops = def.operations
  const query = (): ReturnType<SettingsModel["query"]> => {
    const q = method(kit.queries, ops.get)
    return q
      ? (q() as ReturnType<SettingsModel["query"]>)
      : { queryKey: ["settings", def.name], queryFn: () => call(kit.client, ops.get, []) as Promise<unknown> }
  }
  return {
    name: def.name,
    get title() {
      return labels()?.title ?? def.name
    },
    form: def.form.map(field),
    allowed: (can, operation) => def.permissions[operation].every(can),
    query,
    async save(values) {
      const result = await call(kit.client, ops.update, [values])
      await queryClient?.invalidateQueries({ queryKey: [...query().queryKey] })
      return result
    },
    formSchema: () => schemaAt(kit.schemas, ops.update, "body"),
    get ruleOptions() {
      return { components: kit.components, messages: kit.messages, language: language() }
    },
    display: displayValue,
  }
}

/** useSettings 的返回值。 */
export interface Settings {
  model: SettingsModel
  /** 表单的初始值：读取到的设置中表单字段的值；还没读到时为空。 */
  values?: Record<string, unknown>
  loading: boolean
  error: unknown
  /** 当前身份能否保存。 */
  canSave: boolean
  save(values: RequestValues): Promise<unknown>
  refetch(): void
}

/** 读取设置，给出表单的初始值和保存。 */
export function useSettings(model: SettingsModel): Settings {
  const can = useCan()
  const query = useQuery(model.query())
  const queryClient = useQueryClient()
  const values = useMemo(
    () => (query.data ? formValues(model.form, query.data as ResourceRow) : undefined),
    [model, query.data],
  )
  return {
    model,
    values,
    loading: query.isPending,
    error: query.error,
    canSave: model.allowed(can, "update"),
    async save(v) {
      const result = await model.save(v)
      await queryClient.invalidateQueries({ queryKey: [...model.query().queryKey] })
      return result
    },
    refetch: () => void query.refetch(),
  }
}
