// 通知：未读数、列表、标记已读，实时更新。接口由应用定义（见通知一章），这里通过 NotificationsSource 调用它们。
// 外观（通知铃铛）只负责渲染。

import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query"
import { useCallback, useEffect, useMemo, useState } from "react"
import { useApp } from "../../app/app"

/** 一条通知：title 是服务端按语言生成的文案。 */
export interface NotificationItem {
  id: string
  title: string
  body?: string
  read: boolean
  created_at: string
  /** 点击后打开的页面，应用内的地址。 */
  href?: string
}

/** 读取和修改通知的接口，通常是生成的客户端的几个方法。 */
export interface NotificationsSource {
  /** 一页通知，从新到旧；before 是上一页最后一条的 ID。 */
  list(before?: string): Promise<NotificationItem[]>
  markRead(ids: string[]): Promise<unknown>
  markAllRead(): Promise<unknown>
  /**
   * 未读数的事件流：连接时推送一次，之后每次变化推送（事件 badge，数据 { unread }）。断开时自动重连。
   * 通常是生成的客户端的事件流方法，例如 (o) => api.notification.inbox(o)。
   */
  badge?: (options: { signal?: AbortSignal }) => AsyncIterable<{ event: string; data: { unread: number } }>
  /** 没有事件流时，定期读取未读数。 */
  unread?: () => Promise<number>
  /** 一页的条数，用来判断是否还有下一页，默认 50（与服务端的默认值相同）。 */
  pageSize?: number
  /** 定期读取未读数的间隔，默认 60 秒。 */
  pollInterval?: number
}

/** useNotifications 的返回值。 */
export interface Notifications {
  unread: number
  items: NotificationItem[]
  loading: boolean
  hasMore: boolean
  loadMore(): void
  /** 标记一条通知已读（先在界面上标记，再调用接口）。 */
  markRead(id: string): Promise<void>
  markAllRead(): Promise<void>
}

const sleep = (ms: number, signal: AbortSignal) =>
  new Promise<void>((resolve) => {
    const t = setTimeout(resolve, ms)
    signal.addEventListener("abort", () => {
      clearTimeout(t)
      resolve()
    })
  })

/** 通知的模型：未读数随事件流（或定期读取）更新，未读数变化时重新读取列表。 */
export function useNotifications(source: NotificationsSource): Notifications {
  const app = useApp()
  const queryClient = useQueryClient()
  const key = useMemo(() => ["tenon", "notifications", ...app.session.scope()], [app])
  const pageSize = source.pageSize ?? 50
  const [unread, setUnread] = useState(0)

  const list = useInfiniteQuery({
    queryKey: key,
    queryFn: ({ pageParam }) => source.list(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => (last.length >= pageSize ? last[last.length - 1]?.id : undefined),
    enabled: app.signedIn(),
  })

  // 未读数：事件流（断开后等一会儿重连），或者定期读取。
  const { badge, unread: readUnread, pollInterval = 60_000 } = source
  useEffect(() => {
    if (!app.signedIn()) return
    const ctl = new AbortController()
    const run = async () => {
      let delay = 1000
      while (!ctl.signal.aborted) {
        try {
          if (badge) {
            for await (const e of badge({ signal: ctl.signal })) {
              if (e.event === "badge") {
                setUnread(e.data.unread)
                void queryClient.invalidateQueries({ queryKey: key })
              }
              delay = 1000
            }
          } else if (readUnread) {
            setUnread(await readUnread())
            await sleep(pollInterval, ctl.signal)
            continue
          } else {
            return
          }
        } catch {
          // 断线：稍后重连。
        }
        await sleep(delay, ctl.signal)
        delay = Math.min(delay * 2, 30_000)
      }
    }
    void run()
    return () => ctl.abort()
  }, [app, badge, readUnread, pollInterval, queryClient, key])

  const items = useMemo(() => list.data?.pages.flat() ?? [], [list.data])
  const setRead = useCallback(
    (match: (n: NotificationItem) => boolean) =>
      queryClient.setQueryData<{ pages: NotificationItem[][]; pageParams: unknown[] }>(key, (data) =>
        data ? { ...data, pages: data.pages.map((p) => p.map((n) => (match(n) ? { ...n, read: true } : n))) } : data,
      ),
    [queryClient, key],
  )
  return {
    unread,
    items,
    loading: list.isPending,
    hasMore: list.hasNextPage,
    loadMore: () => void list.fetchNextPage(),
    async markRead(id) {
      const n = items.find((i) => i.id === id)
      if (!n || n.read) return
      setRead((x) => x.id === id)
      setUnread((u) => Math.max(0, u - 1))
      await source.markRead([id])
    },
    async markAllRead() {
      setRead(() => true)
      setUnread(0)
      await source.markAllRead()
    },
  }
}
