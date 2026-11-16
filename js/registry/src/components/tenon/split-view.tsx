// 列表加详情的分栏：左侧是列表，右侧是选中项的详情，中间的分隔条可以拖动调整左侧的宽度。
// 宽度按账号保存（@go-tenon/shell 的 useResizable）；窄屏时只显示一侧，由页面决定显示哪一侧。

import { useNarrow, useResizable } from "@go-tenon/shell"
import type { ReactNode } from "react"
import { cn } from "@/lib/utils"

/** SplitView 的选项。 */
export interface SplitViewProps {
  /** 区分不同的分栏，左侧的宽度按它保存，例如 "articles"。 */
  id: string
  /** 左侧：通常是列表。 */
  pane: ReactNode
  /** 右侧：通常是详情，没有选中时放一个空状态。 */
  children: ReactNode
  /** 左侧的默认、最小、最大宽度（像素），默认 320、240、560。 */
  defaultWidth?: number
  minWidth?: number
  maxWidth?: number
  /**
   * 窄屏时显示哪一侧，默认 pane。列表加详情的页面通常在选中了一项时显示 main：
   *
   *   <SplitView id="articles" pane={<List />} narrow={id ? "main" : "pane"}>…</SplitView>
   */
  narrow?: "pane" | "main"
  className?: string
}

/**
 * 列表加详情的分栏，放在页面中，例如文章列表的路由用它，详情是子路由的 Outlet：
 *
 *   <SplitView id="articles" pane={<ArticleList />} narrow={params.id ? "main" : "pane"}>
 *     <Outlet />
 *   </SplitView>
 *
 * 分隔条可以拖动，也可以聚焦后用方向键调整，双击恢复默认宽度。
 */
export function SplitView({
  id,
  pane,
  children,
  defaultWidth = 320,
  minWidth = 240,
  maxWidth = 560,
  narrow = "pane",
  className,
}: SplitViewProps): ReactNode {
  const isNarrow = useNarrow()
  const split = useResizable({ id, defaultSize: defaultWidth, min: minWidth, max: maxWidth })
  if (isNarrow) {
    return (
      <div data-slot="split-view" className={cn("flex min-h-0 flex-1 flex-col", className)}>
        {narrow === "pane" ? pane : children}
      </div>
    )
  }
  return (
    <div
      data-slot="split-view"
      className={cn(
        "flex min-h-0 flex-1 overflow-hidden rounded-lg border",
        split.dragging && "select-none",
        className,
      )}
    >
      <aside
        data-slot="split-view-pane"
        className="flex min-h-0 shrink-0 flex-col overflow-auto"
        style={{ width: split.size }}
      >
        {pane}
      </aside>
      {/* biome-ignore lint/a11y/useSemanticElements: 可以聚焦、拖动的分隔条是 WAI-ARIA 的 Window Splitter 模式，<hr> 不能聚焦，也不能带 aria-valuenow */}
      <div
        {...split.separatorProps}
        role="separator"
        aria-label="调整宽度"
        className={cn(
          "relative w-px shrink-0 cursor-col-resize bg-border outline-none transition-colors",
          "after:absolute after:inset-y-0 after:-left-1.5 after:w-3", // 加宽可以拖动的范围
          "hover:bg-primary focus-visible:bg-primary",
          split.dragging && "bg-primary",
        )}
      />
      <div data-slot="split-view-main" className="flex min-h-0 min-w-0 flex-1 flex-col overflow-auto">
        {children}
      </div>
    </div>
  )
}
