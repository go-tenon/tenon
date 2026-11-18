// Topbar 布局：console（SaaS 控制台）的另一套模板，顶部导航，现在是占位：只有顶栏（名称、导航、用户菜单）和页面。
// 导航来自路由的 staticData，与 AppLayout 相同；身份变化时只重新挂载页面（SessionBoundary）。

import { AppShell, Group, NavLink, Text } from "@mantine/core"
import { Link, Outlet } from "@tanstack/react-router"
import type { ReactNode } from "react"
import { type MenuItem, SessionBoundary, useNavigation } from "./shell"

/** TopbarLayout 的选项。 */
export interface TopbarLayoutProps {
  title: string // 顶栏左侧的名称
  user?: ReactNode // 顶栏右侧，通常是 UserMenu
  children?: ReactNode // 页面内容；默认是子路由
}

/** pages 返回导航中的页面：分组展开成其中的页面。 */
function pages(menu: MenuItem[]): MenuItem[] {
  return menu.flatMap((item) => (item.children ? pages(item.children) : [item]))
}

/** TopbarLayout 是 Topbar 布局：顶部导航（占位）。 */
export function TopbarLayout({ title, user, children }: TopbarLayoutProps): ReactNode {
  const { menu, active } = useNavigation()
  return (
    <AppShell header={{ height: 56 }} padding="lg">
      <AppShell.Header>
        <Group h="100%" px="lg" gap="xl" wrap="nowrap">
          <Text fw={700}>{title}</Text>
          <Group gap={4} component="nav" aria-label="页面导航" style={{ flex: 1 }}>
            {pages(menu).map((item) => (
              <NavLink
                key={item.id}
                component={Link}
                to={item.path}
                label={item.title}
                active={active.includes(item.id)}
                w="auto"
              />
            ))}
          </Group>
          {user}
        </Group>
      </AppShell.Header>
      <AppShell.Main>
        <SessionBoundary>{children ?? <Outlet />}</SessionBoundary>
      </AppShell.Main>
    </AppShell>
  )
}
