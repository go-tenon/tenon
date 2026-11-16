// 页面的部件：页头、加载与错误状态、空状态、右侧抽屉面板。

import { describeError } from "@go-tenon/client"
import { useNavigation } from "@go-tenon/shell"
import { CircleAlert, Inbox } from "lucide-react"
import type { ReactNode } from "react"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"

/** 页头：标题默认是当前页面的标题（面包屑的最后一级），右侧放操作按钮。 */
export function PageHeader({
  title,
  description,
  children,
}: {
  title?: ReactNode
  description?: ReactNode
  children?: ReactNode
}): ReactNode {
  const { crumbs } = useNavigation()
  return (
    <div className="flex flex-wrap items-start justify-between gap-4">
      <div className="space-y-1">
        <h1 className="text-xl font-semibold tracking-tight">{title ?? crumbs.at(-1)?.title}</h1>
        {description && <p className="text-sm text-muted-foreground">{description}</p>}
      </div>
      {children && <div className="flex items-center gap-2">{children}</div>}
    </div>
  )
}

/** 加载中：rows 行骨架。 */
export function PageLoading({ rows = 5 }: { rows?: number }): ReactNode {
  return (
    <div className="space-y-3" aria-busy="true">
      {Array.from({ length: rows }, (_, i) => i).map((row) => (
        <Skeleton key={row} className="h-10 w-full" />
      ))}
    </div>
  )
}

/** 出错：显示服务端的提示（describeError），断网等没有提示时显示通用的文字；retry 时显示重试按钮。 */
export function PageError({ error, retry }: { error: unknown; retry?: () => void }): ReactNode {
  const info = describeError(error)
  const message =
    info.message ??
    (info.kind === "network"
      ? "网络连接失败，请检查网络后重试。"
      : info.kind === "forbidden"
        ? "没有权限。"
        : "加载失败，请稍后重试。")
  return (
    <div role="alert" className="flex flex-col items-center gap-3 py-12 text-center">
      <CircleAlert className="size-8 text-destructive" />
      <p className="text-sm text-muted-foreground">{message}</p>
      {retry && (
        <Button variant="outline" size="sm" onClick={retry}>
          重试
        </Button>
      )}
    </div>
  )
}

/** 空状态。 */
export function PageEmpty({ children = "还没有内容。" }: { children?: ReactNode }): ReactNode {
  return (
    <div className="flex flex-col items-center gap-3 py-12 text-center text-sm text-muted-foreground">
      <Inbox className="size-8" />
      {children}
    </div>
  )
}

/** 右侧抽屉面板，例如在列表页中查看、编辑一条记录。 */
export function SidePanel({
  open,
  onOpenChange,
  title,
  description,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  description?: ReactNode
  children: ReactNode
}): ReactNode {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full sm:max-w-lg">
        <SheetHeader>
          <SheetTitle>{title}</SheetTitle>
          {description && <SheetDescription>{description}</SheetDescription>}
        </SheetHeader>
        <div className="flex-1 overflow-y-auto px-4 pb-4">{children}</div>
      </SheetContent>
    </Sheet>
  )
}
