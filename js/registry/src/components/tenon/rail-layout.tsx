// 图标栏 + 二级面板：最左侧的窄图标栏切换第一级菜单（模块），旁边是模块内的导航。
// 适合客服、IM、项目管理这类工作台。

import { pageUnder, Slot, type TabsOutletProps, useLayoutState, useNavigation } from "@go-tenon/shell"
import { useRouter } from "@tanstack/react-router"
import type { ReactNode } from "react"
import { badgeText, Crumbs, MenuIcon, NavLink } from "@/components/tenon/nav"
import { LayoutContent } from "@/components/tenon/tab-bar"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

/** RailLayout 的选项。 */
export interface RailLayoutProps {
  logo?: ReactNode
  tabs?: boolean | TabsOutletProps
}

/** 图标栏 + 二级面板。二级面板的宽度保存在布局状态中（secondaryWidth）。插槽与侧栏布局相同。 */
export function RailLayout({ logo, tabs }: RailLayoutProps): ReactNode {
  const nav = useNavigation()
  const router = useRouter()
  const [state] = useLayoutState()
  return (
    <TooltipProvider>
      <div className="flex min-h-svh">
        <aside className="flex w-14 shrink-0 flex-col items-center gap-2 border-r bg-sidebar py-3">
          {logo}
          {nav.modules.map((m) => (
            <Tooltip key={m.id}>
              <TooltipTrigger asChild>
                <Button
                  variant={nav.module?.id === m.id ? "default" : "ghost"}
                  size="icon"
                  aria-label={m.title}
                  onClick={() => void router.navigate({ href: pageUnder(nav.menu, m.path) ?? m.path })}
                >
                  <MenuIcon item={m} />
                  {!m.icon && <span>{m.title.slice(0, 1)}</span>}
                </Button>
              </TooltipTrigger>
              <TooltipContent side="right">{m.title}</TooltipContent>
            </Tooltip>
          ))}
          <div className="mt-auto">
            <Slot name="user" />
          </div>
        </aside>
        {nav.secondary.length > 0 && (
          <nav
            className="flex shrink-0 flex-col gap-1 border-r bg-sidebar/50 p-2"
            style={{ width: state.secondaryWidth ?? 220 }}
          >
            <div className="px-2 py-1.5 text-xs font-medium text-muted-foreground">{nav.module?.title}</div>
            {nav.secondary.flatMap((item) =>
              (item.children ?? [item]).map((leaf) => (
                <NavLink
                  key={leaf.id}
                  to={leaf.path}
                  className={cn(
                    "flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted",
                    nav.active.includes(leaf.id) && "bg-muted font-medium",
                  )}
                >
                  <MenuIcon item={leaf} className="size-4" />
                  <span className="truncate">{leaf.title}</span>
                  {badgeText(leaf.badge) && <span className="ml-auto text-xs">{badgeText(leaf.badge)}</span>}
                </NavLink>
              )),
            )}
          </nav>
        )}
        <div className="flex min-w-0 flex-1 flex-col">
          <header className="flex h-14 items-center gap-2 border-b px-4">
            <Crumbs />
            <div className="ml-auto flex items-center gap-2">
              <Slot name="search" />
              <Slot name="actions" />
              <Slot name="tenant" />
            </div>
          </header>
          <main className={tabs ? "flex flex-1 flex-col" : "flex flex-1 flex-col gap-4 p-4"}>
            <LayoutContent tabs={tabs} />
          </main>
        </div>
      </div>
    </TooltipProvider>
  )
}
