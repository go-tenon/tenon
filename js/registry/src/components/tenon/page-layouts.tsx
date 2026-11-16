// 几种简单的布局：居中窄栏、设置、全屏、登录。

import { type MenuItem, Slot, useNavigation } from "@go-tenon/shell"
import { Outlet, useRouter } from "@tanstack/react-router"
import { ArrowLeft } from "lucide-react"
import type { ReactNode } from "react"
import { MenuIcon, NavLink } from "@/components/tenon/nav"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { cn } from "@/lib/utils"

/** 居中窄栏：无侧栏，内容限宽居中。适合简单的用户端、个人中心。children 默认是 Outlet。 */
export function CenteredLayout({ title, children }: { title?: ReactNode; children?: ReactNode }): ReactNode {
  return (
    <div className="flex min-h-svh flex-col">
      <header className="border-b">
        <div className="mx-auto flex h-14 w-full max-w-2xl items-center gap-2 px-4">
          <Slot name="brand" />
          <span className="font-semibold">{title}</span>
          <div className="ml-auto flex items-center gap-2">
            <Slot name="actions" />
            <Slot name="user" />
          </div>
        </div>
      </header>
      <main className="mx-auto flex w-full max-w-2xl flex-1 flex-col gap-4 px-4 py-6">{children ?? <Outlet />}</main>
    </div>
  )
}

/**
 * 设置类页面：页面内左侧是二级导航，右侧是内容。用作 /settings 这类布局路由的 component；
 * items 默认是当前第一级菜单下的菜单。
 */
export function SettingsLayout({ items }: { items?: MenuItem[] }): ReactNode {
  const nav = useNavigation()
  const list = (items ?? nav.secondary).flatMap((item) => item.children ?? [item])
  return (
    <div className="flex flex-col gap-6 md:flex-row">
      <nav className="flex shrink-0 gap-1 md:w-48 md:flex-col">
        {list.map((item) => (
          <NavLink
            key={item.id}
            to={item.path}
            className={cn(
              "flex items-center gap-2 rounded-md px-3 py-2 text-sm hover:bg-muted",
              nav.active.includes(item.id) ? "bg-muted font-medium" : "text-muted-foreground",
            )}
          >
            <MenuIcon item={item} className="size-4" />
            {item.title}
          </NavLink>
        ))}
      </nav>
      <div className="min-w-0 flex-1">
        <Outlet />
      </div>
    </div>
  )
}

/** 全屏：只保留返回和操作入口，适合编辑器、画布、数据大屏。back 是返回的地址，默认是浏览器的上一页。 */
export function FullscreenLayout({ back }: { back?: string }): ReactNode {
  const router = useRouter()
  const { crumbs } = useNavigation()
  return (
    <div className="flex min-h-svh flex-col">
      <header className="flex h-12 items-center gap-2 border-b px-2">
        <Button
          variant="ghost"
          size="icon"
          aria-label="返回"
          onClick={() => (back ? void router.navigate({ href: back }) : router.history.back())}
        >
          <ArrowLeft />
        </Button>
        <span className="text-sm font-medium">{crumbs.at(-1)?.title}</span>
        <div className="ml-auto flex items-center gap-2">
          <Slot name="actions" />
        </div>
      </header>
      <main className="flex flex-1 flex-col">
        <Outlet />
      </main>
    </div>
  )
}

/** AuthLayout 的选项。 */
export interface AuthLayoutProps {
  title: string
  description?: ReactNode
  logo?: ReactNode
  /** 左侧的品牌区（例如插图）：有它时左右分栏，否则卡片居中。 */
  side?: ReactNode
  children?: ReactNode
}

/** 登录、注册、找回密码等页面的布局。children 默认是 Outlet。 */
export function AuthLayout({ title, description, logo, side, children }: AuthLayoutProps): ReactNode {
  return (
    <div className={cn("grid min-h-svh", side && "lg:grid-cols-2")}>
      {side && <div className="hidden bg-primary text-primary-foreground lg:block">{side}</div>}
      <div className="flex items-center justify-center p-6">
        <Card className="w-full max-w-sm">
          <CardHeader>
            <div className="flex items-center gap-2">
              {logo}
              <CardTitle>{title}</CardTitle>
            </div>
            {description && <CardDescription>{description}</CardDescription>}
          </CardHeader>
          <CardContent>{children ?? <Outlet />}</CardContent>
        </Card>
      </div>
    </div>
  )
}
