// 路由切换时预取页面的数据：路由的 loader 调用 preloadQueries，数据很快到达时新页面一次完整出现，旧页面一直留到那时；
// 慢的时候照常切换，页面先显示自己的骨架或加载状态。列表行悬停这类提前预取用 prefetchQueries。

import type { FetchInfiniteQueryOptions, FetchQueryOptions, QueryClient, QueryKey } from "@tanstack/react-query"

/** PreloadQuery 是可以预取的查询：普通查询的选项，或带 initialPageParam 的游标分页（infinite）查询的选项。 */
export type PreloadQuery =
  // biome-ignore lint/suspicious/noExplicitAny: 生成的查询选项各有自己的数据和分页参数类型，一组中混合传入
  | FetchQueryOptions<any, any, any, any>
  // biome-ignore lint/suspicious/noExplicitAny: 同上
  | FetchInfiniteQueryOptions<any, any, any, any, any>

/** PreloadOptions 是预取的选项。 */
export interface PreloadOptions {
  /** 缓存中没有数据时，loader 最多等待的毫秒数，默认 250。 */
  waitMs?: number
  /** 预取时认为数据还新的时长（毫秒），默认 30000：这段时间内重复预取同一个查询不再请求。 */
  staleTime?: number
}

/** prefetchQueries 预取一组查询，全部结束时完成；请求失败时同样正常完成，由页面自己的查询重试并显示错误。 */
export function prefetchQueries(
  queryClient: QueryClient,
  queries: readonly PreloadQuery[],
  options: PreloadOptions = {},
): Promise<void> {
  const staleTime = options.staleTime ?? 30_000
  return Promise.all(
    queries.map((q) =>
      "initialPageParam" in q
        ? queryClient.prefetchInfiniteQuery({
            ...(q as FetchInfiniteQueryOptions<unknown, unknown, unknown, QueryKey, unknown>),
            staleTime,
          })
        : queryClient.prefetchQuery({ ...(q as FetchQueryOptions<unknown, unknown, unknown, QueryKey>), staleTime }),
    ),
  ).then(() => undefined)
}

/**
 * preloadQueries 是路由 loader 用的预取：缓存中都已有数据时在后台刷新并立即完成，页面立即切换；
 * 有缺的时候预取并最多等 waitMs，数据到了新页面一次完整出现，更慢时先切换。
 *
 *	loader: ({ context, params }) => preloadQueries(context.app.queryClient, [kit.queries.article.get(params.id)])
 */
export function preloadQueries(
  queryClient: QueryClient,
  queries: readonly PreloadQuery[],
  options: PreloadOptions = {},
): Promise<void> {
  const cached = queries.every((q) => queryClient.getQueryData(q.queryKey) !== undefined)
  const work = prefetchQueries(queryClient, queries, options)
  if (cached) return Promise.resolve()
  const waitMs = options.waitMs ?? 250
  return Promise.race([work, new Promise<void>((resolve) => setTimeout(resolve, waitMs))])
}
