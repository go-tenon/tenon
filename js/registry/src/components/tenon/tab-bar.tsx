// 标签栏：@go-tenon/shell 多标签模型（useTabs）的 shadcn 外观。每个标签的菜单（右键或点击箭头）中是刷新、固定和关闭，
// 拖动排序用浏览器原生的拖放。

import { TabsOutlet, type TabsOutletProps, type TabView, useTabs } from "@go-tenon/shell"
import { Outlet } from "@tanstack/react-router"
import { ChevronDown, Pin, X } from "lucide-react"
import { type ReactNode, useState } from "react"
import { MenuIcon } from "@/components/tenon/nav"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { cn } from "@/lib/utils"

function Tab({ tab, index }: { tab: TabView; index: number }): ReactNode {
  const tabs = useTabs()
  const [menuOpen, setMenuOpen] = useState(false)
  return (
    <div
      role="tab"
      aria-selected={tab.active}
      tabIndex={tab.active ? 0 : -1}
      data-tab-key={tab.key}
      draggable
      onDragStart={(e) => e.dataTransfer.setData("text/tenon-tab", tab.key)}
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        const from = e.dataTransfer.getData("text/tenon-tab")
        if (from && from !== tab.key) tabs.move(from, tab.key)
      }}
      onContextMenu={(e) => {
        e.preventDefault()
        setMenuOpen(true)
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") void tabs.activate(tab.key)
      }}
      className={cn(
        "group flex h-8 shrink-0 cursor-pointer items-center gap-1.5 rounded-md border px-2 text-sm",
        tab.active
          ? "bg-background text-foreground shadow-xs"
          : "bg-muted/50 text-muted-foreground hover:text-foreground",
      )}
      onClick={() => void tabs.activate(tab.key)}
    >
      {tab.pinned && <Pin className="size-3" />}
      <MenuIcon item={tab} className="size-3.5" />
      <span className="max-w-40 truncate">{tab.title}</span>
      {tab.dirty && <span className="size-1.5 rounded-full bg-amber-500" title="有未保存的修改" />}
      <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            aria-label={`${tab.title} 的操作`}
            className="opacity-50 hover:opacity-100"
            onClick={(e) => e.stopPropagation()}
          >
            <ChevronDown className="size-3" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" onClick={(e) => e.stopPropagation()}>
          <DropdownMenuItem onSelect={() => void tabs.refresh(tab.key)}>刷新</DropdownMenuItem>
          <DropdownMenuItem onSelect={() => (tab.pinned ? tabs.unpin(tab.key) : tabs.pin(tab.key))}>
            {tab.pinned ? "取消固定" : "固定"}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem disabled={!tab.closable} onSelect={() => void tabs.close(tab.key)}>
            关闭
          </DropdownMenuItem>
          <DropdownMenuItem disabled={tabs.tabs.length <= 1} onSelect={() => void tabs.closeOthers(tab.key)}>
            关闭其他
          </DropdownMenuItem>
          <DropdownMenuItem disabled={index === tabs.tabs.length - 1} onSelect={() => void tabs.closeRight(tab.key)}>
            关闭右侧
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => void tabs.closeAll()}>关闭全部</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {tab.closable && (
        <Button
          variant="ghost"
          size="icon"
          className="size-4 rounded-sm"
          aria-label={`关闭 ${tab.title}`}
          onClick={(e) => {
            e.stopPropagation()
            void tabs.close(tab.key)
          }}
        >
          <X className="size-3" />
        </Button>
      )}
    </div>
  )
}

/** 多标签的标签栏，放在页面区域的上方（布局的 tabs 开启时自动放置）。 */
export function TabBar({ className }: { className?: string }): ReactNode {
  const tabs = useTabs()
  if (tabs.tabs.length === 0) return null
  return (
    <div role="tablist" className={cn("flex gap-1 overflow-x-auto border-b bg-muted/30 px-3 py-1.5", className)}>
      {tabs.tabs.map((tab, index) => (
        <Tab key={tab.key} tab={tab} index={index} />
      ))}
    </div>
  )
}

/** 页面区域：多标签时是标签栏和 TabsOutlet，否则是 Outlet。 */
export function LayoutContent({ tabs }: { tabs?: boolean | TabsOutletProps }): ReactNode {
  if (!tabs) return <Outlet />
  return (
    <>
      <TabBar />
      <TabsOutlet {...(typeof tabs === "object" ? tabs : {})} />
    </>
  )
}
