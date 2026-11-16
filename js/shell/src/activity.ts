// 操作日志：一个对象的历史（例如文章的修改记录），或者一个人的操作记录。接口由应用定义（见操作日志一章），
// 这里通过 ActivitySource 读取。外观（时间线）只负责渲染。

import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query"
import { useMemo } from "react"
import { useApp } from "./app"

/** 一条操作记录。文字由服务端按语言翻译好，例如“发布了文章《你好》”。 */
export interface ActivityItem {
  id: string
  /** 发生了什么：事件的译文。 */
  text: string
  /** 操作人的名字；系统的操作（队列、定时任务）为空。 */
  actor?: string
  /** 代为登录时实际操作的人的名字。 */
  impersonator?: string
  created_at: string
  /** 点击后打开的页面，应用内的地址。 */
  href?: string
}

/** 读取操作记录的接口，通常是生成的客户端的一个方法。 */
export interface ActivitySource {
  /** 一页记录，从新到旧；before 是上一页最后一条的 ID。 */
  list(before?: string): Promise<ActivityItem[]>
  /** 区分不同的列表，例如 ["article", id]：同一个页面上的两份历史各自缓存。 */
  key?: readonly unknown[]
  /** 一页的条数，用来判断是否还有下一页，默认 50（与 activity.List 的默认值相同）。 */
  pageSize?: number
}

/** useActivity 的返回值。 */
export interface Activity {
  items: ActivityItem[]
  loading: boolean
  error?: unknown
  hasMore: boolean
  loadMore(): void
  /** 重新读取，例如刚刚做了一次修改。 */
  refresh(): Promise<void>
}

/** 操作日志的模型：分页读取，从新到旧。缓存按当前身份区分，切换账号后不会看到上一个人的数据。 */
export function useActivity(source: ActivitySource): Activity {
  const app = useApp()
  const queryClient = useQueryClient()
  const extra = source.key ?? []
  // biome-ignore lint/correctness/useExhaustiveDependencies: 按 key 的内容比较，调用方每次渲染都可以传入新的数组
  const key = useMemo(() => ["tenon", "activity", ...app.session.scope(), ...extra], [app, JSON.stringify(extra)])
  const pageSize = source.pageSize ?? 50
  const list = useInfiniteQuery({
    queryKey: key,
    queryFn: ({ pageParam }) => source.list(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => (last.length >= pageSize ? last[last.length - 1]?.id : undefined),
  })
  const items = useMemo(() => list.data?.pages.flat() ?? [], [list.data])
  return {
    items,
    loading: list.isPending,
    error: list.error ?? undefined,
    hasMore: list.hasNextPage,
    loadMore: () => void list.fetchNextPage(),
    refresh: () => queryClient.resetQueries({ queryKey: key }),
  }
}
