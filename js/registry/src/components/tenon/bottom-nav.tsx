// 底部标签栏：窄屏时第一级菜单固定在屏幕底部，适合第一级菜单不多（不超过 5 个）的用户端。
// 顶部导航布局用 narrowNav="bottom" 开启它，其他布局也可以直接放置。

import { useBottomNav } from "@go-tenon/shell"
import type { ReactNode } from "react"
import { badgeText, MenuIcon, NavLink } from "@/components/tenon/nav"
import { cn } from "@/lib/utils"

/**
 * 底部标签栏。固定在屏幕底部，使用它的布局要给内容留出底部的空间（pb-16）。
 * 带刘海的手机上，底部的安全区域由 env(safe-area-inset-bottom) 留出。
 */
export function BottomNav({ max = 5 }: { max?: number }): ReactNode {
  const items = useBottomNav(max)
  if (items.length === 0) return null
  return (
    <nav
      aria-label="主导航"
      className="fixed inset-x-0 bottom-0 z-20 flex border-t bg-background/95 pb-[env(safe-area-inset-bottom)] backdrop-blur"
    >
      {items.map((item) => {
        const badge = badgeText(item.badge)
        return (
          <NavLink
            key={item.id}
            to={item.path}
            aria-current={item.active ? "page" : undefined}
            className={cn(
              "relative flex h-16 flex-1 flex-col items-center justify-center gap-1 text-xs",
              item.active ? "font-medium text-primary" : "text-muted-foreground",
            )}
          >
            <MenuIcon item={item} className="size-5" />
            <span>{item.title}</span>
            {badge && (
              <span className="absolute top-2 left-1/2 ml-2 min-w-4 rounded-full bg-destructive px-1 text-center text-[10px] leading-4 text-white">
                {badge}
              </span>
            )}
          </NavLink>
        )
      })}
    </nav>
  )
}
