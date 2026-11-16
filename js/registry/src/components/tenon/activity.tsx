// 操作日志的时间线：一个对象的历史，或者一个人的操作记录。逻辑在 @go-tenon/shell（useActivity），这里只负责外观。

import { type ActivityItem, type ActivitySource, useActivity } from "@go-tenon/shell"
import { useRouter } from "@tanstack/react-router"
import { CircleAlert } from "lucide-react"
import type { ReactNode } from "react"
import { Button } from "@/components/ui/button"
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"

const timeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "short" })
const when = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : timeFormat.format(d)
}

/** ActivityTimeline 的选项。 */
export interface ActivityTimelineProps {
  /** 操作记录的接口，见 @go-tenon/shell 的 useActivity。 */
  source: ActivitySource
  /** 点击一条记录：默认有 href 时打开它。 */
  onOpen?: (item: ActivityItem) => void
  /** 没有记录时的说明，默认“还没有操作记录”。 */
  empty?: ReactNode
}

/**
 * 操作日志的时间线：从新到旧，每条是发生了什么、谁、什么时候；代为登录时注明实际操作的人。放在详情页或侧边面板中：
 *
 *   <ActivityTimeline source={{ key: ["article", id], list: (before) => api.article.history(id, { before }) }} />
 */
export function ActivityTimeline({ source, onOpen, empty = "还没有操作记录" }: ActivityTimelineProps): ReactNode {
  const a = useActivity(source)
  const router = useRouter()
  if (a.loading) {
    return (
      <div className="flex flex-col gap-3">
        <Skeleton className="h-10" />
        <Skeleton className="h-10" />
      </div>
    )
  }
  if (a.error && a.items.length === 0) {
    return (
      <div role="alert" className="flex items-center gap-2 text-sm text-destructive">
        <CircleAlert className="size-4" />
        操作记录读取失败
        <Button variant="outline" size="sm" className="ml-auto" onClick={() => void a.refresh()}>
          重试
        </Button>
      </div>
    )
  }
  if (a.items.length === 0) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>{empty}</EmptyTitle>
        </EmptyHeader>
      </Empty>
    )
  }
  const open = (item: ActivityItem) => {
    if (onOpen) onOpen(item)
    else if (item.href) void router.navigate({ href: item.href })
  }
  return (
    <div className="flex flex-col gap-3">
      <ol className="relative flex flex-col gap-4 border-l pl-4">
        {a.items.map((item) => (
          <li key={item.id} className="relative">
            <span className="absolute top-1.5 -left-[21px] size-2.5 rounded-full border-2 border-background bg-primary" />
            {item.href || onOpen ? (
              <button type="button" className="text-left text-sm hover:underline" onClick={() => open(item)}>
                {item.text}
              </button>
            ) : (
              <p className="text-sm">{item.text}</p>
            )}
            <p className="text-xs text-muted-foreground">
              {[item.actor ?? "系统", item.impersonator && `（${item.impersonator} 代为操作）`, when(item.created_at)]
                .filter(Boolean)
                .join(" · ")}
            </p>
          </li>
        ))}
      </ol>
      {a.hasMore && (
        <Button variant="outline" size="sm" onClick={a.loadMore}>
          加载更多
        </Button>
      )}
    </div>
  )
}
