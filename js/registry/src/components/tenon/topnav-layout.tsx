// 顶部导航：导航在顶栏，内容居中或全宽，适合页面少的用户端。窄屏时导航收进抽屉。

import { Slot, type TabsOutletProps, useNarrow, useNavigation } from "@go-tenon/shell"
import { ChevronDown, Menu } from "lucide-react"
import type { ReactNode } from "react"
import { MenuIcon, NavLink } from "@/components/tenon/nav"
import { LayoutContent } from "@/components/tenon/tab-bar"
import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { cn } from "@/lib/utils"

/** TopNavLayout 的选项。 */
export interface TopNavLayoutProps {
  title: string
  logo?: ReactNode
  /** 内容限宽居中（默认）还是全宽。 */
  width?: "contained" | "full"
  tabs?: boolean | TabsOutletProps
}

/** 顶部导航布局。插槽：brand、search、actions、tenant、user 在顶栏，footer 在页脚。 */
export function TopNavLayout({ title, logo, width = "contained", tabs }: TopNavLayoutProps): ReactNode {
  const { menu, active } = useNavigation()
  const narrow = useNarrow()
  const link = (active_: boolean) =>
    cn(
      "inline-flex h-9 items-center gap-1.5 rounded-md px-3 text-sm transition-colors hover:bg-muted",
      active_ ? "font-medium text-foreground" : "text-muted-foreground",
    )
  const nav = menu.map((item) =>
    item.children ? (
      <DropdownMenu key={item.id}>
        <DropdownMenuTrigger className={link(active.includes(item.id))}>
          <MenuIcon item={item} className="size-4" />
          {item.title}
          <ChevronDown className="size-3" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start">
          {item.children.map((child) => (
            <DropdownMenuItem key={child.id} asChild>
              <NavLink to={child.path}>{child.title}</NavLink>
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    ) : (
      <NavLink key={item.id} to={item.path} className={link(active.includes(item.id))}>
        <MenuIcon item={item} className="size-4" />
        {item.title}
      </NavLink>
    ),
  )
  const container = width === "contained" ? "mx-auto w-full max-w-6xl px-4" : "w-full px-4"
  return (
    <div className="flex min-h-svh flex-col">
      <header className="sticky top-0 z-10 border-b bg-background/95 backdrop-blur">
        <div className={cn(container, "flex h-14 items-center gap-4")}>
          {narrow && (
            <Sheet>
              <SheetTrigger asChild>
                <Button variant="ghost" size="icon" aria-label="打开导航">
                  <Menu />
                </Button>
              </SheetTrigger>
              <SheetContent side="left">
                <SheetHeader>
                  <SheetTitle>{title}</SheetTitle>
                </SheetHeader>
                <nav className="flex flex-col gap-1 px-4">
                  {menu.flatMap((item) =>
                    (item.children ?? [item]).map((leaf) => (
                      <NavLink key={leaf.id} to={leaf.path} className={link(active.includes(leaf.id))}>
                        {leaf.title}
                      </NavLink>
                    )),
                  )}
                </nav>
              </SheetContent>
            </Sheet>
          )}
          <div className="flex items-center gap-2 font-semibold">
            <Slot name="brand" />
            {logo}
            {title}
          </div>
          {!narrow && <nav className="flex items-center gap-1">{nav}</nav>}
          <div className="ml-auto flex items-center gap-2">
            <Slot name="search" />
            <Slot name="actions" />
            <Slot name="tenant" />
            <Slot name="user" />
          </div>
        </div>
      </header>
      <main className={cn(tabs ? "flex flex-1 flex-col" : cn(container, "flex flex-1 flex-col gap-4 py-6"))}>
        <LayoutContent tabs={tabs} />
      </main>
      <Slot name="footer" />
    </div>
  )
}
