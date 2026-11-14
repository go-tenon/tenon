// 租户切换：当前租户是会话身份中的 tenant。切换时更新身份（TanStack Query 的缓存随之清空，上一个租户的数据不会出现），
// 再按租户的确定方式跳转，见 TenancyOptions.home。

import { useQuery } from "@tanstack/react-query"
import { useMemo, useSyncExternalStore } from "react"
import { type App, type Tenant, useApp } from "./app"

/** 切换到租户 id。 */
export async function switchTenant(app: App, id: string): Promise<void> {
  const { session, router, tenancy } = app
  const identity = session.identity()
  if (identity?.tenant !== id) session.setIdentity({ account: identity?.account ?? "", tenant: id })
  const home = tenancy?.home?.(id)
  if (!home) {
    await router.invalidate()
    return
  }
  if (/^[a-z][a-z\d+.-]*:\/\//i.test(home)) {
    window.location.assign(home)
    return
  }
  await router.navigate({ href: home })
}

/**
 * 租户在路径中时，租户布局路由的 beforeLoad 用它：用户直接打开另一个租户的地址时，会话的身份随之切换。
 *
 *   createFileRoute("/t/$tenant")({ beforeLoad: ({ context, params }) => enterTenant(context.app, params.tenant) })
 */
export function enterTenant(app: App, id: string): void {
  const identity = app.session.identity()
  if (identity?.tenant !== id) app.session.setIdentity({ account: identity?.account ?? "", tenant: id })
}

/** useTenant 的返回值。 */
export interface TenantState {
  /** 当前租户的 id；没有时为空。 */
  current?: string
  /** 当前用户所属的全部租户；读取中为空数组。 */
  tenants: Tenant[]
  /** 当前租户的信息。 */
  tenant?: Tenant
  switchTo(id: string): Promise<void>
}

/** 当前租户和切换租户，例如顶栏的租户菜单。 */
export function useTenant(): TenantState {
  const app = useApp()
  const current = useSyncExternalStore(app.session.subscribe, () => app.session.identity()?.tenant)
  const account = useSyncExternalStore(app.session.subscribe, () => app.session.identity()?.account ?? "")
  const { data = [] } = useQuery({
    queryKey: ["tenon", "tenants", account],
    queryFn: () => app.tenancy?.tenants() ?? Promise.resolve([]),
    enabled: Boolean(app.tenancy) && app.signedIn(),
    staleTime: 60_000,
  })
  return useMemo(
    () => ({
      current: current || undefined,
      tenants: data,
      tenant: data.find((t) => t.id === current),
      switchTo: (id: string) => switchTenant(app, id),
    }),
    [current, data, app],
  )
}
