// 侧栏布局：左侧导航可收起为图标，顶栏在内容上方。variant 为 inset 时是“内嵌式侧栏”（侧栏与背景一体，
// 内容区是浮起的卡片），floating 时侧栏浮起。按 @go-tenon/shell 的布局约定实现：读取导航模型和布局状态，放置插槽。

import {
  type MenuItem,
  Slot,
  type TabsOutletProps,
  useLayoutState,
  useNavigation,
  useSlotFilled,
} from "@go-tenon/shell"
import type { ReactNode } from "react"
import { badgeText, Crumbs, MenuIcon, NavLink } from "@/components/tenon/nav"
import { LayoutContent } from "@/components/tenon/tab-bar"
import { Separator } from "@/components/ui/separator"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { TooltipProvider } from "@/components/ui/tooltip"

/** SidebarLayout 的选项。 */
export interface SidebarLayoutProps {
  title: string
  logo?: ReactNode
  /** sidebar（默认）、inset（内嵌式）、floating（浮起）。 */
  variant?: "sidebar" | "inset" | "floating"
  /** 开启多标签：true 或 TabsOutlet 的选项。 */
  tabs?: boolean | TabsOutletProps
}

/** 侧栏中的一个菜单项：分组展开为二级菜单。“顶栏 + 侧栏”布局也用它。 */
export function SidebarItem({ item, active }: { item: MenuItem; active: readonly string[] }): ReactNode {
  const badge = badgeText(item.badge)
  if (!item.children) {
    return (
      <SidebarMenuItem>
        <SidebarMenuButton asChild isActive={active.includes(item.id)} tooltip={item.title}>
          <NavLink to={item.path}>
            <MenuIcon item={item} />
            <span>{item.title}</span>
          </NavLink>
        </SidebarMenuButton>
        {badge && <SidebarMenuBadge>{badge}</SidebarMenuBadge>}
      </SidebarMenuItem>
    )
  }
  return (
    <SidebarMenuItem>
      <SidebarMenuButton isActive={active.includes(item.id)} tooltip={item.title}>
        <MenuIcon item={item} />
        <span>{item.title}</span>
      </SidebarMenuButton>
      <SidebarMenuSub>
        {item.children.map((child) => (
          <SidebarMenuSubItem key={child.id}>
            <SidebarMenuSubButton asChild isActive={active.includes(child.id)}>
              <NavLink to={child.path}>
                <span>{child.title}</span>
                {badgeText(child.badge) && <span className="ml-auto text-xs">{badgeText(child.badge)}</span>}
              </NavLink>
            </SidebarMenuSubButton>
          </SidebarMenuSubItem>
        ))}
      </SidebarMenuSub>
    </SidebarMenuItem>
  )
}

/**
 * 侧栏布局，用作受保护布局路由的 component：
 *
 *   createFileRoute("/_app")({ beforeLoad: requireAuth, component: () => <SidebarLayout title="控制台" tabs /> })
 *
 * 插槽：brand（侧栏顶部，默认是 logo 和 title）、search、actions、tenant（顶栏右侧）、user（侧栏底部）、footer（页脚）。
 * 侧栏的展开、收起保存在布局状态中。
 */
export function SidebarLayout({ title, logo, variant = "sidebar", tabs }: SidebarLayoutProps): ReactNode {
  const { menu, active } = useNavigation()
  const [state, setState] = useLayoutState()
  const hasBrand = useSlotFilled("brand")
  return (
    // 收起为图标时，菜单项用提示显示名称。
    <TooltipProvider delayDuration={0}>
      <SidebarProvider open={!state.collapsed} onOpenChange={(open) => setState({ collapsed: !open })}>
        <Sidebar variant={variant} collapsible="icon">
          <SidebarHeader>
            {hasBrand ? (
              <Slot name="brand" />
            ) : (
              <div className="flex items-center gap-2 px-2 py-1.5 font-semibold">
                {logo}
                <span className="truncate group-data-[collapsible=icon]:hidden">{title}</span>
              </div>
            )}
          </SidebarHeader>
          <SidebarContent>
            <SidebarGroup>
              <SidebarGroupLabel>导航</SidebarGroupLabel>
              <SidebarMenu>
                {menu.map((item) => (
                  <SidebarItem key={item.id} item={item} active={active} />
                ))}
              </SidebarMenu>
            </SidebarGroup>
          </SidebarContent>
          <SidebarFooter>
            <Slot name="user" />
          </SidebarFooter>
        </Sidebar>
        <SidebarInset>
          <header className="flex h-14 shrink-0 items-center gap-2 border-b px-4">
            <SidebarTrigger className="-ml-1" />
            <Separator orientation="vertical" className="mr-2 data-[orientation=vertical]:h-4" />
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
          <Slot name="footer" />
        </SidebarInset>
      </SidebarProvider>
    </TooltipProvider>
  )
}
