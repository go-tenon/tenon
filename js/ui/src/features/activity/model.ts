// 操作日志：一个对象的历史（例如文章的修改记录）、一个人的操作记录，或整个租户的操作日志。接口由应用定义（见操作日志一章），
// 这里通过 ActivitySource 按页读取。表格外观（ActivityLog）只负责渲染。

import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query"
import { useCallback, useMemo, useState } from "react"
import { useApp } from "../../app/app"

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

/** 一页操作记录，与服务端 page.Offset[T] 的响应结构一致。 */
export interface ActivityPage {
  items: ActivityItem[]
  total: number
}

/** 读取操作记录的接口，通常是生成的客户端的一个方法。 */
export interface ActivitySource {
  /** 按页读取操作记录，从新到旧。 */
  list(params: { page: number; per_page: number }): Promise<ActivityPage>
  /** 区分不同的列表或筛选条件，例如 ["article", id]：同一个页面上的多份历史各自缓存，变化时回到第一页。 */
  key?: readonly unknown[]
  /** 初始每页条数，默认 20（与 page.OffsetParams、activity.DefaultPerPage 相同）。 */
  perPage?: number
}

/** useActivity 的返回值。 */
export interface Activity {
  items: ActivityItem[]
  total?: number
  page: number
  perPage: number
  setPage(page: number): void
  /** 修改每页条数，回到第一页。 */
  setPerPage(perPage: number): void
  loading: boolean
  error?: unknown
  /** 重新读取，例如刚刚做了一次修改。 */
  refresh(): Promise<void>
}

/** 操作日志的模型：固定分页读取，从新到旧。缓存按当前身份区分，切换账号后不会看到上一个人的数据。 */
export function useActivity(source: ActivitySource): Activity {
  const app = useApp()
  const queryClient = useQueryClient()
  const extra = source.key ?? []
  const keySig = JSON.stringify(extra)
  const [paging, setPaging] = useState({ key: keySig, page: 1 })
  const page = paging.key === keySig ? paging.page : 1
  const [perPage, setPerPageState] = useState(source.perPage ?? 20)
  const setPage = useCallback((next: number) => setPaging({ key: keySig, page: next }), [keySig])
  const setPerPage = useCallback(
    (next: number) => {
      setPerPageState(next)
      setPaging({ key: keySig, page: 1 })
    },
    [keySig],
  )
  // biome-ignore lint/correctness/useExhaustiveDependencies: 按 key 的内容比较，调用方每次渲染都可以传入新的数组
  const baseKey = useMemo(() => ["tenon", "activity", ...app.session.scope(), ...extra], [app, keySig])
  const query = useQuery({
    queryKey: [...baseKey, page, perPage],
    queryFn: () => source.list({ page, per_page: perPage }),
    placeholderData: keepPreviousData,
  })
  return {
    items: query.data?.items ?? [],
    total: query.data?.total,
    page,
    perPage,
    setPage,
    setPerPage,
    loading: query.isPending || query.isPlaceholderData,
    error: query.error ?? undefined,
    refresh: () => queryClient.resetQueries({ queryKey: baseKey }),
  }
}
