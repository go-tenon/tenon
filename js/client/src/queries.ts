// 查询缓存：身份变化时清空 TanStack Query 的缓存，旧身份的数据不会出现在新身份的页面上。

import type { Session } from "./session"

/** TanStack Query 的 QueryClient 中需要的部分。 */
export interface QueryCache {
  clear(): void
}

/**
 * 登录、退出、切换账号或租户时清空 queryClient 的缓存。query key 的作用域另由 session.scope 提供，
 * 交给生成的 createQueries。返回取消的函数。
 */
export function clearOnIdentityChange(session: Pick<Session, "subscribe">, queryClient: QueryCache): () => void {
  return session.subscribe(() => queryClient.clear())
}
