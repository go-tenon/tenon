// 布局共用的部件：应用内的链接、面包屑、图标。由 tenon 的 shadcn registry 提供，加入项目后可以随意修改。

import { type MenuItem, pageUnder, useNavigation } from "@go-tenon/shell"
import { useRouter } from "@tanstack/react-router"
import {
  AppWindow,
  Bell,
  ChartColumn,
  FileText,
  Folder,
  House,
  LayoutDashboard,
  type LucideIcon,
  MessageSquare,
  Settings,
  Shield,
  ShoppingCart,
  Store,
  User,
  Users,
} from "lucide-react"
import { Fragment, type MouseEvent, type ReactNode } from "react"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"

/**
 * 路由元数据中 icon 的名字到 lucide 图标的映射。名字与运营后台（@go-tenon/admin 的 defaultIcons）相同，
 * 同一份路由元数据在两套外观中都能显示图标。需要别的图标时加在这里。
 */
export const icons: Record<string, LucideIcon> = {
  appstore: AppWindow,
  "bar-chart": ChartColumn,
  bell: Bell,
  dashboard: LayoutDashboard,
  "file-text": FileText,
  folder: Folder,
  home: House,
  message: MessageSquare,
  safety: Shield,
  setting: Settings,
  shop: Store,
  "shopping-cart": ShoppingCart,
  team: Users,
  user: User,
}

/** 菜单项的图标；没有登记的名字不显示。 */
export function MenuIcon({ item, className }: { item: Pick<MenuItem, "icon">; className?: string }): ReactNode {
  const Icon = item.icon ? icons[item.icon] : undefined
  return Icon ? <Icon className={className} /> : null
}

/** 应用内的链接：保留浏览器的默认行为（新窗口打开等），普通点击由路由跳转。 */
export function NavLink({
  to,
  children,
  className,
  ...rest
}: { to: string; children: ReactNode; className?: string } & Omit<React.ComponentProps<"a">, "href">): ReactNode {
  const router = useRouter()
  const onClick = (e: MouseEvent<HTMLAnchorElement>) => {
    rest.onClick?.(e)
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
    e.preventDefault()
    void router.navigate({ href: to })
  }
  return (
    <a {...rest} href={router.buildLocation({ href: to } as never).href} className={className} onClick={onClick}>
      {children}
    </a>
  )
}

/** 面包屑：最后一级是当前页面；分组跳到它下面第一个可以进入的页面。 */
export function Crumbs(): ReactNode {
  const { crumbs, menu } = useNavigation()
  if (crumbs.length === 0) return null
  return (
    <Breadcrumb>
      <BreadcrumbList>
        {crumbs.map((c, i) => {
          const last = i === crumbs.length - 1
          const target = last ? undefined : pageUnder(menu, c.path)
          return (
            <Fragment key={c.path}>
              <BreadcrumbItem>
                {last ? (
                  <BreadcrumbPage>{c.title}</BreadcrumbPage>
                ) : target ? (
                  <BreadcrumbLink asChild>
                    <NavLink to={target}>{c.title}</NavLink>
                  </BreadcrumbLink>
                ) : (
                  c.title
                )}
              </BreadcrumbItem>
              {!last && <BreadcrumbSeparator />}
            </Fragment>
          )
        })}
      </BreadcrumbList>
    </Breadcrumb>
  )
}

/** 菜单项的徽标文字：数字、文字，true 是小红点。 */
export function badgeText(badge: MenuItem["badge"]): string | undefined {
  if (badge === undefined || badge === false) return undefined
  return badge === true ? "•" : String(badge)
}
