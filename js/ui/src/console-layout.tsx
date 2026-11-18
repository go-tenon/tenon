// 商户侧（SaaS 控制台）的两种布局方案，现在都是占位，以后逐步设计：
//   - ConsoleSideLayout：侧栏导航；
//   - ConsoleTopLayout：顶部导航。
// 两者都只有顶栏（名称、用户菜单）、按权限过滤的导航和页面内容。导航来自路由的 staticData（title、order），
// 与 AppLayout 相同；身份变化时只重新挂载页面（SessionBoundary）。

import { AppShell, Group, NavLink, Text } from "@mantine/core"
import { Link, Outlet } from "@tanstack/react-router"
import type { ReactNode } from "react"
import { type MenuItem, SessionBoundary, useNavigation } from "./shell"

/** 商户侧布局的选项。 */
export interface ConsoleLayoutProps {
  title: string // 顶栏左侧的名称
  user?: ReactNode // 顶栏右侧，通常是 UserMenu
  children?: ReactNode // 页面内容；默认是子路由
}

/** 导航中的页面：分组展开成其中的页面。 */
function pages(menu: MenuItem[]): MenuItem[] {
  return menu.flatMap((item) => (item.children ? pages(item.children) : [item]))
}

/** 商户侧布局：侧栏导航（占位）。 */
export function ConsoleSideLayout({ title, user, children }: ConsoleLayoutProps): ReactNode {
  const { menu, active } = useNavigation()
  return (
    <AppShell header={{ height: 56 }} navbar={{ width: 220, breakpoint: "sm" }} padding="lg">
      <AppShell.Header>
        <Group h="100%" px="lg" justify="space-between">
          <Text fw={700}>{title}</Text>
          {user}
        </Group>
      </AppShell.Header>
      <AppShell.Navbar p="sm" aria-label="页面导航">
        {pages(menu).map((item) => (
          <NavLink key={item.id} component={Link} to={item.path} label={item.title} active={active.includes(item.id)} />
        ))}
      </AppShell.Navbar>
      <AppShell.Main>
        <SessionBoundary>{children ?? <Outlet />}</SessionBoundary>
      </AppShell.Main>
    </AppShell>
  )
}

/** 商户侧布局：顶部导航（占位）。 */
export function ConsoleTopLayout({ title, user, children }: ConsoleLayoutProps): ReactNode {
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
