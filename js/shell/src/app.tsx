// 应用装配：把 Router、QueryClient 和会话接到一起，提供登录检查、权限检查、菜单和面包屑。

import { clearOnIdentityChange, createSession, describeError, type Session } from "@go-tenon/client"
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query"
import {
  type AnyRoute,
  createRouter,
  type ParsedLocation,
  type Router,
  RouterProvider,
  redirect,
  type StaticDataRouteOption,
  useMatches,
} from "@tanstack/react-router"
import { createContext, type ReactNode, useContext, useMemo, useSyncExternalStore } from "react"
import { breadcrumbs, buildMenu, type Crumb, firstPage, type MenuItem, routeInfos } from "./menu"
import "./meta"
import { safeRedirect } from "./redirect"
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
  /** 读取当前身份的权限，通常调用应用的“当前用户”接口。不提供时没有任何权限。 */
  permissions?: () => Promise<Iterable<string>>
  /** 不传时创建一个：4xx 不重试，其他错误重试 2 次。 */
  queryClient?: QueryClient
  router?: AppRouterOptions
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

/** 创建应用。 */
export function createApp<TRouteTree extends AnyRoute>(options: AppOptions<TRouteTree>): App<TRouteTree> {
  const session = options.session ?? signedOutSession()
  const queryClient = options.queryClient ?? defaultQueryClient()
  const loginPath = options.loginPath ?? "/login"
  let restoring: Promise<boolean> | undefined

  const app = {} as App<TRouteTree>
  const router = createRouter({
    ...options.router,
    routeTree: options.routeTree,
    context: { app },
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
    if (session.accessToken() === "") {
      throw redirect({ href: `${loginPath}?${new URLSearchParams({ redirect: location.href })}` })
    }
  }

  Object.assign(app, {
    id: options.id,
    session,
    queryClient,
    router,
    storage: appStorage(options.id),
    loginPath,
    ready: () => {
      if (!restoring) restoring = session.restore().catch(() => false)
      return restoring
    },
    signedIn: () => session.accessToken() !== "",
    permissions: loadPermissions,
    permissionsQuery,
    guard,
    async redirectAfterSignIn(target?: unknown) {
      let home = options.homePath
      if (!home) {
        const held = await loadPermissions()
        home = firstPage(buildMenu(routeInfos(router.routesById), (p) => held.has(p))) ?? "/"
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
        if (!(await loadPermissions()).has(permission)) throw new ForbiddenError(permission)
        return before?.(ctx as never)
      },
    } as never)
  }

  // 首次加载任何页面之前恢复登录状态：公开页面的页头也能知道是否已经登录。
  const root = router.routeTree as AnyRoute
  const rootBefore = root.options.beforeLoad
  root.update({
    beforeLoad: async (ctx: unknown) => {
      await app.ready()
      return rootBefore?.(ctx as never)
    },
  } as never)

  // 登录、退出、切换身份：清空缓存，重新运行守卫（例如退出后离开受保护的页面）。
  clearOnIdentityChange(session, queryClient)
  session.subscribe(() => void router.invalidate())
  return app
}

/**
 * 渲染应用：QueryClientProvider、应用上下文和 RouterProvider。
 *
 *   createRoot(document.getElementById("root")!).render(<AppRoot app={app} />)
 */
export function AppRoot<TRouteTree extends AnyRoute>({ app }: { app: App<TRouteTree> }): ReactNode {
  return (
    <QueryClientProvider client={app.queryClient}>
      <AppContext.Provider value={app as unknown as App}>
        <RouterProvider router={app.router as unknown as Router<AnyRoute>} />
      </AppContext.Provider>
    </QueryClientProvider>
  )
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

/** 没有登录功能的应用使用的会话：刷新总是失败，一直是未登录。 */
function signedOutSession(): Session {
  return createSession({
    refresh: async () => {
      throw { status: 401, fields: [] }
    },
  })
}

/** 当前的应用，在 AppRoot 之内使用。 */
export function useApp(): App {
  const app = useContext(AppContext)
  if (!app) throw new Error("useApp: not inside AppRoot")
  return app
}

/** 当前身份的权限：返回 can(permission)。权限读取完成之前都返回 false。 */
export function useCan(): (permission: string) => boolean {
  const app = useApp()
  const { data } = useQuery({ ...app.permissionsQuery(), enabled: app.signedIn() })
  return useMemo(() => (permission: string) => data?.has(permission) ?? false, [data])
}

/** 是否已经登录：登录、退出时更新，例如页头在“登录”和“退出”之间切换。 */
export function useSignedIn(): boolean {
  const { session } = useApp()
  return useSyncExternalStore(session.subscribe, () => session.accessToken() !== "")
}

/** 按当前身份的权限过滤后的菜单。 */
export function useMenu(): MenuItem[] {
  const app = useApp()
  const can = useCan()
  return useMemo(() => buildMenu(routeInfos(app.router.routesById), can), [app, can])
}

/** 当前位置的面包屑。 */
export function useBreadcrumbs(): Crumb[] {
  const matches = useMatches()
  return useMemo(() => breadcrumbs(matches), [matches])
}
