// 顶栏 + 侧栏：第一级菜单（模块）在顶栏，当前模块下的菜单在左侧的侧栏。适合模块多、每个模块页面也多的后台，
// 对应运营后台 AdminLayout 的 variant="mix"。窄屏时顶栏的模块收起，侧栏变为抽屉，其中是完整的菜单。

import {
  firstPage,
  Slot,
  type TabsOutletProps,
  useLayoutState,
  useNarrow,
  useNavigation,
  useSlotFilled,
} from "@go-tenon/shell"
import type { CSSProperties, ReactNode } from "react"
import { Crumbs, MenuIcon, NavLink } from "@/components/tenon/nav"
import { SidebarItem } from "@/components/tenon/sidebar-layout"
import { LayoutContent } from "@/components/tenon/tab-bar"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarInset,
  SidebarMenu,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { TooltipProvider } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

/** MixedLayout 的选项。 */
export interface MixedLayoutProps {
  title: string
  logo?: ReactNode
  /** 开启多标签：true 或 TabsOutlet 的选项。 */
  tabs?: boolean | TabsOutletProps
}

/**
 * 顶栏 + 侧栏布局，用作受保护布局路由的 component：
 *
 *   createFileRoute("/_app")({ beforeLoad: requireAuth, component: () => <MixedLayout title="控制台" /> })
 *
 * 插槽：brand（顶栏左侧，默认是 logo 和 title）、search、actions、tenant、user（顶栏右侧）、footer（页脚）。
 * 点击顶栏的模块进入它下面第一个可以进入的页面。侧栏的展开、收起保存在布局状态中。
 */
export function MixedLayout({ title, logo, tabs }: MixedLayoutProps): ReactNode {
  const { menu, modules, module, secondary, active } = useNavigation()
  const [state, setState] = useLayoutState()
  const narrow = useNarrow()
  const hasBrand = useSlotFilled("brand")
  // 窄屏时顶栏放不下模块，侧栏（抽屉）中显示完整的菜单。
  const side = narrow ? menu : secondary
  return (
    <TooltipProvider delayDuration={0}>
      <SidebarProvider
        open={!state.collapsed}
        onOpenChange={(open) => setState({ collapsed: !open })}
        className="flex-col"
        style={{ "--header-height": "3.5rem" } as CSSProperties}
      >
        <header className="sticky top-0 z-20 flex h-(--header-height) shrink-0 items-center gap-4 border-b bg-background px-4">
          <SidebarTrigger className="-ml-1" />
          <div className="flex items-center gap-2 font-semibold">
            {hasBrand ? (
              <Slot name="brand" />
            ) : (
              <>
                {logo}
                <span className="truncate">{title}</span>
              </>
            )}
          </div>
          {!narrow && (
            <nav className="flex items-center gap-1">
              {modules.map((item) => (
                <NavLink
                  key={item.id}
                  to={(item.children ? firstPage(item.children) : item.path) ?? item.path}
                  aria-current={item.id === module?.id ? "page" : undefined}
                  className={cn(
                    "inline-flex h-9 items-center gap-1.5 rounded-md px-3 text-sm transition-colors hover:bg-muted",
                    item.id === module?.id ? "bg-muted font-medium text-foreground" : "text-muted-foreground",
                  )}
                >
                  <MenuIcon item={item} className="size-4" />
                  {item.title}
                </NavLink>
              ))}
            </nav>
          )}
          <div className="ml-auto flex items-center gap-2">
            <Slot name="search" />
            <Slot name="actions" />
            <Slot name="tenant" />
            <Slot name="user" />
          </div>
        </header>
        <div className="flex flex-1">
          <Sidebar collapsible="icon" className="top-(--header-height) h-[calc(100svh-var(--header-height))]!">
            <SidebarContent>
              <SidebarGroup>
                <SidebarGroupLabel>{narrow ? "导航" : (module?.title ?? "导航")}</SidebarGroupLabel>
                <SidebarMenu>
                  {side.map((item) => (
                    <SidebarItem key={item.id} item={item} active={active} />
                  ))}
                </SidebarMenu>
              </SidebarGroup>
            </SidebarContent>
            <SidebarFooter />
          </Sidebar>
          <SidebarInset>
            {!tabs && (
              <div className="flex h-12 shrink-0 items-center border-b px-4">
                <Crumbs />
              </div>
            )}
            <main className={tabs ? "flex flex-1 flex-col" : "flex flex-1 flex-col gap-4 p-4"}>
              <LayoutContent tabs={tabs} />
            </main>
            <Slot name="footer" />
          </SidebarInset>
        </div>
      </SidebarProvider>
    </TooltipProvider>
  )
}
