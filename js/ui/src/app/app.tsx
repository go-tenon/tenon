// 应用装配：把 Router、QueryClient 和会话接到一起，提供登录检查、权限检查、菜单和面包屑。

import {
  clearOnIdentityChange,
  createI18n,
  createSession,
  describeError,
  type ErrorInfo,
  grants,
  type I18n,
  type MessageTable,
  type Session,
} from "@go-tenon/client"
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query"
import {
  type AnyRoute,
  createRouter,
  ErrorComponent,
  type ErrorComponentProps,
  type ErrorRouteComponent,
  type ParsedLocation,
  type Router,
  RouterProvider,
  redirect,
  type StaticDataRouteOption,
  useMatch,
  useMatches,
  useParams,
  useRouterState,
} from "@tanstack/react-router"
import { createContext, Fragment, type ReactNode, useContext, useEffect, useMemo, useSyncExternalStore } from "react"
import "./meta"
import { I18nProvider, type UiMessageId, useLanguage, withUiMessages } from "../i18n/i18n"
import { useBadges } from "./badges"
import { breadcrumbs, buildMenu, type Crumb, firstPage, type MenuItem, routeInfos } from "./menu"
import { safeRedirect } from "./redirect"
import { signedOut } from "./snapshot"
import { type AppStorage, appStorage } from "./storage"

/** 路由上下文：根路由用 createRootRouteWithContext<RouterContext>() 创建，beforeLoad 中通过 context.app 使用。 */
export interface RouterContext {
  app: App
}

/** 进入页面缺少权限时抛出，外观层的 errorComponent 用 isForbidden 判断并显示 403。 */
export class ForbiddenError extends Error {
  readonly permission: string
  constructor(permission: string) {
    super(`forbidden: ${permission}`)
    this.name = "ForbiddenError"
    this.permission = permission
  }
}

/** 报告 err 是否是缺少权限。 */
export function isForbidden(err: unknown): err is ForbiddenError {
  return err instanceof ForbiddenError
}

/** 读取权限的查询选项。 */
export interface PermissionsQuery {
  queryKey: readonly unknown[]
  queryFn: () => Promise<ReadonlySet<string>>
  staleTime: number
}

/** 交给 Router 的选项中，应用可以设置的部分，例如外观层的 defaultErrorComponent、测试用的 history。 */
export type AppRouterOptions = Omit<Parameters<typeof createRouter>[0], "routeTree" | "context">

/** createApp 的选项。 */
export interface AppOptions<TRouteTree extends AnyRoute> {
  /** 应用的标识，例如 "admin"：本地存储的命名空间。同一个域名下的前端各用一个。 */
  id: string
  /** 路由插件生成的 routeTree.gen.ts 中的 routeTree。 */
  routeTree: TRouteTree
  /** @go-tenon/client 的会话，由应用创建并连接各个 API 的客户端。没有登录功能的应用不传，一直是未登录。 */
  session?: Session
  /** 登录页的路径，默认 /login。没有登录时跳到这里，带上 ?redirect=<原来的地址>。 */
  loginPath?: string
  /** 登录后没有回跳地址时的去处；默认是菜单中第一个可以进入的页面，没有时为 /。 */
  homePath?: string
  /** 读取当前身份的权限，通常调用应用的“当前用户”接口，原样返回账号的权限，其中可以有通配符。不提供时没有任何权限。 */
  permissions?: () => Promise<Iterable<string>>
  /** 不传时创建一个：4xx 不重试，其他错误重试 2 次。 */
  queryClient?: QueryClient
  router?: AppRouterOptions
  /** 多租户应用的租户切换，见 useTenant。 */
  tenancy?: TenancyOptions
  /**
   * 界面文字的翻译：@go-tenon/client 的 createI18n，交给它应用生成的 messages.gen.ts。createApp 在其中加入框架组件内置的文字，
   * 组件按它的当前语言显示，<html lang> 跟随它。不传时按 language（默认 zh-CN）显示框架内置文字。
   */
  i18n?: I18n
  /**
   * 单语言应用的界面语言（例如 "zh-CN"、"en"），决定 @go-tenon/ui 内置组件的文字和 <html lang>。
   * 传了 i18n 时忽略它；两者都不传时默认是 "zh-CN"。
   */
  language?: string
  /**
   * 覆盖 @go-tenon/ui 内置组件的文字（例如 { "tenon.table.empty": "还没有内容" }），只在不传 i18n 时生效。
   * 多语言应用直接写在各自的翻译文件中。
   */
  uiMessages?: Partial<Record<UiMessageId, string>>
  /**
   * 页面出错时调用，交给前端观测的 SDK，例如 openobserveLogs.logger.error。路由的错误组件接住的错误
   * （渲染、loader 中抛出的）不会冒泡到 window，SDK 自动捕获不到，所以框架在这里报告。缺少权限、请求被取消、
   * 5xx 以外的接口错误是预期的，不报告，见 shouldReport。
   */
  onError?: (error: unknown, report: ErrorReport) => void
}

/** onError 收到的信息。 */
export interface ErrorReport {
  /** 出错的路由，例如 /_app/articles/$id；按它聚合同一个页面的错误。 */
  routeId: string
  pathname: string
  /** 错误的分类；接口的错误带有 requestId，可以在服务端的日志和链路中查到这个请求。 */
  error: ErrorInfo
}

/**
 * 页面中的错误是否需要报告：没有 HTTP 状态的错误（代码中的错误、代码块加载失败）和 5xx 报告；
 * 缺少权限、请求被取消和其他接口错误（未登录、校验失败、不存在等）是预期的，不报告。
 */
export function shouldReport(error: unknown): boolean {
  if (isForbidden(error)) return false
  const info = describeError(error)
  if (info.kind === "aborted") return false
  return info.status === undefined || info.status >= 500
}

/** 一个租户。 */
export interface Tenant {
  id: string
  name: string
}

/** 租户切换的选项。 */
export interface TenancyOptions {
  /** 当前用户所属的全部租户，通常调用 roles.Tenants 对应的接口。 */
  tenants: () => Promise<Tenant[]>
  /**
   * 切换到租户 id 之后去哪里：
   *   - 租户在路径中（/t/{tenant}/…）：返回新租户的首页，例如 `/t/${id}/`；
   *   - 租户在子域名中：返回完整的地址，例如 `https://${id}.example.com/`，整页跳转；
   *   - 租户在请求头中（X-Tenant）：不设置，留在当前页面，重新读取数据。
   */
  home?: (id: string) => string
}

/** createApp 返回的应用。 */
export interface App<TRouteTree extends AnyRoute = AnyRoute> {
  readonly id: string
  readonly session: Session
  readonly queryClient: QueryClient
  readonly router: Router<TRouteTree>
  readonly storage: AppStorage
  readonly loginPath: string
  /** 打开页面后恢复一次登录状态（session.restore），之后返回同一个结果。 */
  ready(): Promise<boolean>
  /** 是否已经登录。 */
  signedIn(): boolean
  /** 当前身份的权限，缓存在 queryClient 中，身份变化时重新读取。 */
  permissions(): Promise<ReadonlySet<string>>
  /** 读取权限的查询选项，useQuery 和 fetchQuery 共用；key 随身份的代次变化。 */
  permissionsQuery(): PermissionsQuery
  /** 登录检查，用在受保护布局路由的 beforeLoad 中；见 requireAuth。 */
  guard(location: Pick<ParsedLocation, "href">): Promise<void>
  /** 登录成功后调用：回到 redirect（只接受同源的相对路径），没有时去 homePath。 */
  redirectAfterSignIn(redirect?: unknown): Promise<void>
  /** 租户切换的选项；单租户应用为空。 */
  readonly tenancy?: TenancyOptions
  /** 界面文字的翻译：AppOptions.i18n，加入了框架组件内置的文字。 */
  readonly i18n: I18n
  /** 报告一个错误（交给 AppOptions.onError）：同一个错误只报告一次，不需要报告的错误被忽略。 */
  reportError(error: unknown, where: { routeId: string; pathname: string }): void
}

function defaultQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: (count, err) => {
          const status = describeError(err).status
          return !(status !== undefined && status >= 400 && status < 500) && count < 2
        },
      },
    },
  })
}

const AppContext = createContext<App | null>(null)

/**
 * 在错误组件中报告错误，交给 AppOptions.onError。默认的错误组件已经调用了它；路由自己写了 errorComponent 时，
 * 在其中调用：useReportError(error)。
 */
export function useReportError(error: unknown): void {
  const app = useContext(AppContext)
  // shell 不知道应用的路由树，match 的类型中没有具体的 routeId，这里只读取它。
  const match = useMatch({ strict: false }) as { routeId?: string } | undefined
  const routeId = match?.routeId
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  useEffect(() => {
    app?.reportError(error, { routeId: routeId ?? "", pathname })
  }, [app, error, routeId, pathname])
}

/** 包装错误组件：先报告错误，再显示原来的组件。 */
function reporting(Component: ErrorRouteComponent): ErrorRouteComponent {
  return function ReportingError(props: ErrorComponentProps) {
    useReportError(props.error)
    return <Component {...props} />
  }
}

/** 创建应用。 */
export function createApp<TRouteTree extends AnyRoute>(options: AppOptions<TRouteTree>): App<TRouteTree> {
  const session = options.session ?? signedOutSession()
  const queryClient = options.queryClient ?? defaultQueryClient()
  const loginPath = options.loginPath ?? "/login"
  let restoring: Promise<boolean> | undefined

  const app = {} as App<TRouteTree>
  // 前端挂在子路径（例如 /admin/）时，路由的 basepath 默认是 Vite 的 base：页面中的地址、跳转和登录回跳都不含它。
  const basepath = options.router?.basepath ?? (import.meta.env?.BASE_URL || "/")
  // 同一个错误对象只报告一次：错误组件可能多次渲染（包括 React 的严格模式）。
  const reported = new WeakSet<object>()
  const reportError: App["reportError"] = (error, where) => {
    if (!options.onError || !shouldReport(error)) return
    if (typeof error === "object" && error !== null) {
      if (reported.has(error)) return
      reported.add(error)
    }
    try {
      options.onError(error, { ...where, error: describeError(error) })
    } catch (err) {
      console.error("onError failed", err)
    }
  }
  const Outer = options.router?.Wrap
  const router = createRouter({
    // 默认悬停预加载目标路由模块，应用可通过 options.router.defaultPreload 覆盖。
    defaultPreload: "intent",
    ...options.router,
    // 默认的错误组件先报告错误，再显示外观层的组件（没有时是 TanStack Router 的默认组件）。
    defaultErrorComponent: reporting(options.router?.defaultErrorComponent ?? ErrorComponent),
    basepath,
    routeTree: options.routeTree,
    context: { app },
    // QueryClient 和应用上下文由 Wrap 提供：TanStack Start 自己渲染 RouterProvider，AppRoot 中同样适用。
    // biome-ignore lint/correctness/noComponentHookFactories: 每个应用只创建一次，组件的身份在应用的生命周期内不变
    Wrap: ({ children }: { children: ReactNode }) => {
      const inner = <AppProviders app={app as unknown as App}>{children}</AppProviders>
      return Outer ? <Outer>{inner}</Outer> : inner
    },
  }) as unknown as Router<TRouteTree>

  const permissionsQuery = (): PermissionsQuery => ({
    queryKey: ["tenon", "permissions", session.generation(), ...session.scope()],
    queryFn: async () => new Set(options.permissions ? await options.permissions() : []),
    staleTime: Number.POSITIVE_INFINITY,
  })
  const loadPermissions = () => queryClient.fetchQuery(permissionsQuery())

  const guard = async (location: Pick<ParsedLocation, "href">) => {
    if (!restoring) restoring = session.restore().catch(() => false)
    await restoring
    if (!session.signedIn()) {
      throw redirect({ href: `${loginPath}?${new URLSearchParams({ redirect: location.href })}` })
    }
  }

  const defaultLanguage = options.language ?? "zh-CN"
  const defaultI18n = createI18n({
    messages: options.uiMessages ? { [defaultLanguage]: options.uiMessages as MessageTable } : {},
    defaultLanguage,
    preferred: () => undefined,
  })

  Object.assign(app, {
    id: options.id,
    session,
    queryClient,
    router,
    storage: appStorage(options.id),
    loginPath,
    tenancy: options.tenancy,
    // 没有应用的 i18n 时使用单语言配置（默认简体中文），叠加框架内置文字和可选的 uiMessages 覆盖
    i18n: withUiMessages(options.i18n ?? defaultI18n),
    reportError,
    ready: () => {
      if (!restoring) restoring = session.restore().catch(() => false)
      return restoring
    },
    signedIn: () => session.signedIn(),
    permissions: loadPermissions,
    permissionsQuery,
    guard,
    async redirectAfterSignIn(target?: unknown) {
      let home = options.homePath
      if (!home) {
        const held = await loadPermissions()
        home = firstPage(buildMenu(routeInfos(router.routesById), (p) => grants(held, p))) ?? "/"
      }
      const to = safeRedirect(target, home)
      await router.navigate({ href: to.startsWith(loginPath) ? home : to })
    },
  } satisfies App<TRouteTree>)

  // 带 permission 的路由在自己的 beforeLoad 之前检查登录和权限：缺少权限时抛出 ForbiddenError，
  // 由这个路由的 errorComponent（或 defaultErrorComponent）在上级布局之中显示，页面自己不用写检查。
  for (const route of Object.values(router.routesById) as AnyRoute[]) {
    const permission = (route.options.staticData as StaticDataRouteOption | undefined)?.permission
    if (!permission) continue
    const before = route.options.beforeLoad
    route.update({
      beforeLoad: async (ctx: { location: ParsedLocation }) => {
        await guard(ctx.location)
        if (!grants(await loadPermissions(), permission)) throw new ForbiddenError(permission)
        return before?.(ctx as never)
      },
    } as never)
  }

  // 首次加载任何页面之前恢复登录状态：公开页面的页头也能知道是否已经登录。
  // 只在浏览器中进行：服务端渲染时不读取身份，页面按未登录渲染，浏览器接管后再更新。
  const root = router.routeTree as AnyRoute
  const rootBefore = root.options.beforeLoad
  root.update({
    beforeLoad: async (ctx: unknown) => {
      if (typeof window !== "undefined") await app.ready()
      return rootBefore?.(ctx as never)
    },
  } as never)

  // 登录、退出、切换身份：清空缓存，重新运行守卫（例如退出后离开受保护的页面）。
  clearOnIdentityChange(session, queryClient)
  session.subscribe(() => void router.invalidate())
  return app
}

/**
 * 应用的 Provider：QueryClientProvider 和应用上下文。createApp 把它装在路由的 Wrap 中。
 *
 * 这里包住的是整个路由，包括根路由的 HTML 文档（<html>、<head>）：身份变化时不能在这里重新挂载，
 * 否则整个文档在浏览器中重建（每次打开已登录的页面都会发生一次，因为读到账号也是一次身份变化）。
 * 页面的重新挂载由布局中的 SessionBoundary 负责。
 */
function AppProviders({ app, children }: { app: App; children: ReactNode }): ReactNode {
  return (
    <QueryClientProvider client={app.queryClient}>
      <AppContext.Provider value={app}>
        <I18nProvider i18n={app.i18n}>{children}</I18nProvider>
      </AppContext.Provider>
    </QueryClientProvider>
  )
}

/**
 * 登录、退出、切换账号或租户时（会话的代次变化）重新挂载其中的内容：页面中按身份计算的查询（例如 queryKey 中的
 * session.scope()）重新读取，上一个身份的页面状态不会留下。FrameLayout 用它包住页面；自己写的布局把页面
 * （<Outlet /> 或 <TabsOutlet />）放在其中，不要包住布局本身或 HTML 文档。
 *
 *   <SessionBoundary><Outlet /></SessionBoundary>
 */
export function SessionBoundary({ children }: { children: ReactNode }): ReactNode {
  const app = useApp()
  const generation = useSyncExternalStore(app.session.subscribe, app.session.generation, app.session.generation)
  return <Fragment key={generation}>{children}</Fragment>
}

/**
 * 在浏览器中直接渲染应用，用于测试和不经过 TanStack Start 的页面。TanStack Start 的应用在 getRouter 中返回
 * app.router，Provider 由路由的 Wrap 提供。
 *
 *   createRoot(document.getElementById("root")!).render(<AppRoot app={app} />)
 */
export function AppRoot<TRouteTree extends AnyRoute>({ app }: { app: App<TRouteTree> }): ReactNode {
  return <RouterProvider router={app.router as unknown as Router<AnyRoute>} />
}

/**
 * 受保护布局路由的 beforeLoad：没有登录时跳到登录页，并带上回跳地址。
 *
 *   export const Route = createFileRoute("/_app")({ beforeLoad: requireAuth, component: Layout })
 */
export function requireAuth({
  context,
  location,
}: {
  context: RouterContext
  location: ParsedLocation
}): Promise<void> {
  return context.app.guard(location)
}

/** 没有登录功能的应用使用的会话：一直是未登录。 */
function signedOutSession(): Session {
  return createSession({ load: async () => null })
}

/** 当前的应用，在 AppRoot 之内使用。 */
export function useApp(): App {
  const app = useContext(AppContext)
  if (!app) throw new Error("useApp: not inside AppRoot")
  return app
}

/** 读取当前的应用上下文；不在 AppRoot 内时返回 undefined。 */
export function useOptionalApp(): App | undefined {
  return useContext(AppContext) ?? undefined
}

/** 当前身份的权限：返回 can(permission)，按 grants 匹配，权限中的 *、articles:* 这类通配符有效。权限读取完成之前都返回 false。 */
export function useCan(): (permission: string) => boolean {
  const app = useApp()
  const { data } = useQuery({ ...app.permissionsQuery(), enabled: app.signedIn() })
  return useMemo(() => (permission: string) => (data ? grants(data, permission) : false), [data])
}

/** 是否已经登录：登录、退出时更新，例如页头在“登录”和“退出”之间切换。 */
export function useSignedIn(): boolean {
  const { session } = useApp()
  return useSyncExternalStore(session.subscribe, () => session.signedIn(), signedOut)
}

/**
 * 按当前身份的权限过滤后的菜单。上级路由的路径参数（例如租户）取当前地址中的值，见 buildMenu。
 * 路由的标题可以是界面文字的消息 ID，按当前语言翻译；没有这条消息时原样显示。
 */
export function useMenu(): MenuItem[] {
  const app = useApp()
  const can = useCan()
  const language = useLanguage()
  const badges = useBadges(app)
  const current = useParams({ strict: false }) as Record<string, unknown>
  const key = JSON.stringify(current)
  // biome-ignore lint/correctness/useExhaustiveDependencies: 按参数的内容而不是对象的标识重新计算
  const params = useMemo(
    () => Object.fromEntries(Object.entries(current).map(([k, v]) => [k, String(v)])) as Record<string, string>,
    [key],
  )
  // biome-ignore lint/correctness/useExhaustiveDependencies: 切换语言时重新翻译标题
  return useMemo(
    () => translateMenu(buildMenu(routeInfos(app.router.routesById), can, badges, params), app.i18n.t),
    [app, can, badges, params, language],
  )
}

/** 把菜单中每一项的标题按 t 翻译。 */
function translateMenu(items: MenuItem[], t: (id: string) => string): MenuItem[] {
  return items.map((item) => ({
    ...item,
    title: t(item.title),
    ...(item.children ? { children: translateMenu(item.children, t) } : {}),
  }))
}

/** 当前位置的面包屑。 */
export function useBreadcrumbs(): Crumb[] {
  const app = useApp()
  const matches = useMatches()
  const language = useLanguage()
  // biome-ignore lint/correctness/useExhaustiveDependencies: 切换语言时重新翻译标题
  return useMemo(
    () => breadcrumbs(matches).map((c) => ({ ...c, title: app.i18n.t(c.title) })),
    [app, matches, language],
  )
}
