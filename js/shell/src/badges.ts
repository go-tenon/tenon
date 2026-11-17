// 菜单徽标：运行时才知道的数字或小红点，例如未读通知数。页面或功能模块登记，菜单项上带上 badge。

import { useEffect, useSyncExternalStore } from "react"
import { createStore, perOwner, type Store } from "./store"

/** 徽标的值：数字、文字，或者 true 表示小红点。0、false、空字符串不显示。 */
export type Badge = number | string | boolean

type Badges = Store<ReadonlyMap<string, Badge>>

/** 应用的徽标，键是路由的 id。 */
export const badgesOf = perOwner<Badges>(() => createStore<ReadonlyMap<string, Badge>>(new Map()))

/** 读取应用的全部徽标。 */
export function useBadges(owner: object): ReadonlyMap<string, Badge> {
  const store = badgesOf(owner)
  return useSyncExternalStore(store.subscribe, store.get, store.get)
}

/** 登记 routeId 对应菜单项的徽标；组件卸载或 value 为空时去掉。 */
export function useBadgeOf(owner: object, routeId: string, value: Badge | undefined | null): void {
  useEffect(() => {
    const store = badgesOf(owner)
    const show = value !== undefined && value !== null && value !== 0 && value !== false && value !== ""
    const next = new Map(store.get())
    if (show) next.set(routeId, value)
    else next.delete(routeId)
    store.set(next)
    return () => {
      const after = new Map(store.get())
      after.delete(routeId)
      store.set(after)
    }
  }, [owner, routeId, value])
}
