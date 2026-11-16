// 顶栏上的通知铃铛、后台操作的任务中心，以及页面中的操作进度。逻辑在 @go-tenon/shell，这里只负责外观。

import {
  dismissOperation,
  type NotificationItem,
  type NotificationsSource,
  type TrackedOperation,
  useApp,
  useNotifications,
  useOperation,
  useOperations,
} from "@go-tenon/shell"
import { useRouter } from "@tanstack/react-router"
import { Bell, CircleCheck, CircleX, ListChecks, LoaderCircle, X } from "lucide-react"
import { type ReactNode, useState } from "react"
import { Button } from "@/components/ui/button"
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
import { Popover, PopoverContent, PopoverHeader, PopoverTitle, PopoverTrigger } from "@/components/ui/popover"
import { Progress } from "@/components/ui/progress"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"

const timeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "short" })
const when = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : timeFormat.format(d)
}

/** 角标：未读数或进行中的数量，超过 99 显示 99+。 */
function Count({ n }: { n: number }): ReactNode {
  if (n <= 0) return null
  return (
    <span className="absolute -top-0.5 -right-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-destructive px-1 text-[10px] leading-none font-medium text-white">
      {n > 99 ? "99+" : n}
    </span>
  )
}

/** NotificationBell 的选项。 */
export interface NotificationBellProps {
  /** 通知的接口，见 useNotifications。 */
  source: NotificationsSource
  /** 点击一条通知：默认标记已读，有 href 时打开它。 */
  onOpen?: (n: NotificationItem) => void
}

/**
 * 通知铃铛：角标是未读数，点开是通知列表。点击一条通知标记已读并打开它的页面。放在 actions 插槽：
 *
 *	<Fill name="actions"><NotificationBell source={notifications} /></Fill>
 */
export function NotificationBell({ source, onOpen }: NotificationBellProps): ReactNode {
  const n = useNotifications(source)
  const router = useRouter()
  const [open, setOpen] = useState(false)
  const openItem = (item: NotificationItem) => {
    void n.markRead(item.id)
    if (onOpen) onOpen(item)
    else if (item.href) void router.navigate({ href: item.href })
    setOpen(false)
  }
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="relative"
          aria-label={n.unread ? `通知（${n.unread} 条未读）` : "通知"}
        >
          <Bell />
          <Count n={n.unread} />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 gap-0 p-0">
        <PopoverHeader className="flex-row items-center justify-between border-b px-3 py-2">
          <PopoverTitle>通知</PopoverTitle>
          <Button variant="link" size="sm" disabled={n.unread === 0} onClick={() => void n.markAllRead()}>
            全部已读
          </Button>
        </PopoverHeader>
        {n.loading ? (
          <div className="flex flex-col gap-2 p-3">
            <Skeleton className="h-10" />
            <Skeleton className="h-10" />
          </div>
        ) : n.items.length === 0 ? (
          <Empty className="py-8">
            <EmptyHeader>
              <EmptyTitle className="text-sm">没有通知</EmptyTitle>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="max-h-96 overflow-y-auto">
            {n.items.map((item) => (
              <button
                key={item.id}
                type="button"
                onClick={() => openItem(item)}
                className={cn(
                  "flex w-full flex-col items-start gap-0.5 border-b px-3 py-2 text-left text-sm last:border-b-0 hover:bg-muted",
                  !item.read && "bg-primary/5",
                )}
              >
                <span className={cn("flex w-full items-center gap-2", !item.read && "font-medium")}>
                  {!item.read && <span className="size-1.5 shrink-0 rounded-full bg-primary" />}
                  <span className="truncate">{item.title}</span>
                </span>
                {item.body && <span className="text-muted-foreground">{item.body}</span>}
                <span className="text-xs text-muted-foreground">{when(item.created_at)}</span>
              </button>
            ))}
            {n.hasMore && (
              <Button variant="ghost" className="w-full rounded-none" onClick={n.loadMore}>
                加载更多
              </Button>
            )}
          </div>
        )}
      </PopoverContent>
    </Popover>
  )
}

const statusText: Record<string, string> = { pending: "等待中", running: "进行中", completed: "已完成", failed: "失败" }

/** 一个操作的状态说明，例如“进行中 · 2/4 · 失败 1”。 */
export function describeOperation(op: TrackedOperation): string {
  const o = op.operation
  if (op.error) return "无法读取进度"
  if (!o) return "等待中"
  const parts = [statusText[o.status] ?? o.status]
  if (o.total) parts.push(`${o.processed}/${o.total}`)
  if (o.failed) parts.push(`失败 ${o.failed}`)
  if (o.error) parts.push(o.error)
  return parts.join(" · ")
}

function StatusIcon({ op }: { op: TrackedOperation }): ReactNode {
  if (op.error || op.operation?.status === "failed") return <CircleX className="size-4 text-destructive" />
  if (op.finished) return <CircleCheck className="size-4 text-primary" />
  return <LoaderCircle className="size-4 animate-spin text-muted-foreground" />
}

/** 一个操作的进度：名称、状态和进度条；不知道总数时进度条显示动画。 */
export function OperationRow({ op, onDismiss }: { op: TrackedOperation; onDismiss?: () => void }): ReactNode {
  const failed = !!op.error || op.operation?.status === "failed"
  const value = op.ratio === undefined ? (op.finished ? 100 : undefined) : Math.round(op.ratio * 100)
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2 text-sm">
        <StatusIcon op={op} />
        <span className="flex-1 truncate font-medium">{op.title}</span>
        {onDismiss && (
          <Button variant="ghost" size="icon-xs" aria-label={`移除 ${op.title}`} onClick={onDismiss}>
            <X />
          </Button>
        )}
      </div>
      <Progress
        value={value ?? 30}
        className={cn(
          value === undefined && "animate-pulse",
          failed && "[&>[data-slot=progress-indicator]]:bg-destructive",
        )}
      />
      <span className="text-xs text-muted-foreground">{describeOperation(op)}</span>
    </div>
  )
}

/** 页面中的操作进度：传入 trackOperation 登记过的 ID。 */
export function OperationProgress({ id }: { id: string | undefined }): ReactNode {
  const op = useOperation(id)
  return op ? <OperationRow op={op} /> : null
}

/**
 * 任务中心：顶栏上的图标，角标是进行中的操作数，点开是 trackOperation 登记的操作和它们的进度。
 * 没有登记过操作时不显示。放在 actions 插槽：<Fill name="actions"><OperationCenter /></Fill>
 */
export function OperationCenter(): ReactNode {
  const app = useApp()
  const ops = useOperations()
  if (ops.length === 0) return null
  const running = ops.filter((o) => !o.finished).length
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="relative"
          aria-label={running ? `后台任务（${running} 个进行中）` : "后台任务"}
        >
          {running ? <LoaderCircle className="animate-spin" /> : <ListChecks />}
          <Count n={running} />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80">
        <PopoverHeader>
          <PopoverTitle>后台任务</PopoverTitle>
        </PopoverHeader>
        <div className="flex max-h-96 flex-col gap-4 overflow-y-auto">
          {ops.map((op) => (
            <OperationRow
              key={op.id}
              op={op}
              onDismiss={op.finished ? () => dismissOperation(app, op.id) : undefined}
            />
          ))}
        </div>
      </PopoverContent>
    </Popover>
  )
}
