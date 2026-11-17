// 应用外壳：运营后台和 SaaS 控制台共用。侧栏导航来自 shell/ 中的导航模型（按权限过滤的菜单），
// 顶栏有面包屑和插槽（search、actions、tenant、user），可以开启多标签。密度、尺寸由主题的预设决定。

import {
  ActionIcon,
  AppShell,
  Box,
  Breadcrumbs,
  Burger,
  Group,
  Menu,
  NavLink,
  ScrollArea,
  Text,
  Tooltip,
  UnstyledButton,
  useMantineTheme,
} from "@mantine/core"
import { IconLayoutSidebarLeftCollapse, IconLayoutSidebarLeftExpand } from "@tabler/icons-react"
import { Link, Outlet, useNavigate, useRouterState } from "@tanstack/react-router"
import { type CSSProperties, type ReactNode, useEffect, useState } from "react"
import { ColorSchemeToggle } from "./color-scheme"
import { Icon } from "./icons"
import { NavLinkLink } from "./links"
import {
  type MenuItem,
  Slot,
  TabsOutlet,
  type TabsOutletProps,
  useLayoutState,
  useNarrow,
  useNavigation,
  useSlotFilled,
} from "./shell"
import { TabBar } from "./tab-bar"
import { layoutMetrics } from "./theme"
import { NewVersionNotice } from "./version"

/** AppLayout 的选项。 */
export interface AppLayoutProps {
  /** 产品名称，显示在侧栏顶部；也可以用 brand 插槽放标志。 */
  title?: string
  /** 侧栏顶部的标志；默认是名称的首字。 */
  logo?: ReactNode
  /** 开启多标签：true 或者 TabsOutlet 的选项。 */
  tabs?: boolean | TabsOutletProps
  /** 内容区的最大宽度，例如 1200；默认铺满。 */
  maxWidth?: number
  /** 不显示配色切换按钮。 */
  hideColorScheme?: boolean
}

/**
 * 需要登录的布局路由的组件：
 *
 *   export const Route = createFileRoute("/_app")({ beforeLoad: requireAuth, component: () => <AppLayout title="博客后台" /> })
 */
export function AppLayout({ title, logo, tabs, maxWidth, hideColorScheme }: AppLayoutProps): ReactNode {
  const theme = useMantineTheme()
  const m = layoutMetrics(theme)
  const [state, setState] = useLayoutState()
  const narrow = useNarrow()
  const collapsed = state.collapsed && !narrow
  const aside = useSlotFilled("aside")
  const pathname = useRouterState({ select: (s) => s.location.pathname })

  // 窄屏时打开页面后收起导航抽屉。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 只在地址变化时收起
  useEffect(() => {
    if (state.navOpen) setState({ navOpen: false })
  }, [pathname])

  const vars = { "--tn-header-height": `${m.headerHeight}px` } as CSSProperties
  return (
    <AppShell
      className="tn-shell"
      style={vars}
      layout="alt"
      header={{ height: m.headerHeight }}
      navbar={{
        width: collapsed ? m.navbarCollapsedWidth : m.navbarWidth,
        breakpoint: "sm",
        collapsed: { mobile: !state.navOpen },
      }}
      aside={aside ? { width: 320, breakpoint: "md", collapsed: { mobile: true } } : undefined}
      padding={0}
    >
      <AppShell.Navbar className="tn-navbar">
        <AppShell.Section
          className="tn-brand"
          style={collapsed ? { paddingInline: 0, justifyContent: "center" } : undefined}
        >
          <Link to="/" style={{ display: "contents", color: "inherit" }}>
            {logo ?? <span className="tn-brand-mark">{(title ?? "T").slice(0, 1)}</span>}
            {!collapsed && title && <span>{title}</span>}
          </Link>
          {!collapsed && <Slot name="brand" />}
        </AppShell.Section>
        <AppShell.Section grow component={ScrollArea} scrollbarSize={6}>
          <Nav collapsed={collapsed} />
        </AppShell.Section>
        {!narrow && (
          <AppShell.Section className="tn-navbar-footer">
            <Tooltip label={collapsed ? "展开侧栏" : "收起侧栏"} position="right">
              <ActionIcon
                aria-label={collapsed ? "展开侧栏" : "收起侧栏"}
                onClick={() => setState({ collapsed: !state.collapsed })}
                size="md"
              >
                {collapsed ? (
                  <IconLayoutSidebarLeftExpand size={18} stroke={1.6} />
                ) : (
                  <IconLayoutSidebarLeftCollapse size={18} stroke={1.6} />
                )}
              </ActionIcon>
            </Tooltip>
          </AppShell.Section>
        )}
      </AppShell.Navbar>

      <AppShell.Header className="tn-header">
        <div className="tn-header-inner">
          <Burger
            hiddenFrom="sm"
            size="sm"
            opened={state.navOpen}
            onClick={() => setState({ navOpen: !state.navOpen })}
            aria-label="打开导航"
          />
          <Crumbs />
          <Box style={{ flex: 1 }} />
          <Group gap={4} wrap="nowrap">
            <Slot name="search" />
            <Slot name="actions" />
            {!hideColorScheme && <ColorSchemeToggle />}
            <Slot name="tenant" />
            <Slot name="user" />
          </Group>
        </div>
      </AppShell.Header>

      {aside && (
        <AppShell.Aside className="tn-navbar">
          <ScrollArea p={m.contentPadding}>
            <Slot name="aside" />
          </ScrollArea>
        </AppShell.Aside>
      )}

      <AppShell.Main>
        {tabs && <TabBar />}
        <Box className="tn-content" p={m.contentPadding} maw={maxWidth}>
          {tabs ? <TabsOutlet {...(typeof tabs === "object" ? tabs : {})} /> : <Outlet />}
        </Box>
        <Slot name="footer" />
      </AppShell.Main>
      <NewVersionNotice />
    </AppShell>
  )
}

/** 面包屑：最后一级是当前页面，不是链接；窄屏时只显示当前页面。 */
function Crumbs(): ReactNode {
  const { crumbs } = useNavigation()
  const narrow = useNarrow()
  if (crumbs.length === 0) return null
  const shown = narrow ? crumbs.slice(-1) : crumbs
  return (
    <Breadcrumbs className="tn-crumbs" separator="/">
      {shown.map((c, i) =>
        i === shown.length - 1 ? (
          <span key={c.path} aria-current="page">
            {c.title}
          </span>
        ) : (
          <Link key={c.path} to={c.path}>
            {c.title}
          </Link>
        ),
      )}
    </Breadcrumbs>
  )
}

/** 侧栏导航：分组可以展开，当前页面所在的分组默认展开。收起时只显示图标，分组用弹出菜单。 */
function Nav({ collapsed }: { collapsed: boolean }): ReactNode {
  const { menu, active } = useNavigation()
  return (
    <nav className={collapsed ? "tn-nav tn-nav-collapsed" : "tn-nav"} aria-label="主导航">
      {menu.map((item) =>
        collapsed ? (
          <CollapsedItem key={item.id} item={item} active={active} />
        ) : (
          <Item key={item.id} item={item} active={active} />
        ),
      )}
    </nav>
  )
}

function Badge({ value }: { value: MenuItem["badge"] }): ReactNode {
  if (value === undefined || value === false) return null
  if (value === true) return <Box w={6} h={6} bg="red.6" style={{ borderRadius: "50%" }} />
  return (
    <Text component="span" fz={10} fw={600} c="white" bg="red.6" px={5} lh="16px" style={{ borderRadius: 8 }}>
      {value}
    </Text>
  )
}

function Item({ item, active, depth = 0 }: { item: MenuItem; active: string[]; depth?: number }): ReactNode {
  const isActive = active[depth] === item.id && active.length === depth + 1
  const inPath = active[depth] === item.id
  const [opened, setOpened] = useState(inPath)
  useEffect(() => {
    if (inPath) setOpened(true)
  }, [inPath])
  const icon = depth === 0 ? <Icon name={item.icon} /> : undefined
  if (item.children?.length) {
    return (
      <NavLink
        className="tn-nav-item"
        classNames={{ children: "tn-nav-children" }}
        label={item.title}
        leftSection={icon}
        opened={opened}
        onChange={setOpened}
        childrenOffset={0}
      >
        {item.children.map((c) => (
          <Item key={c.id} item={c} active={active} depth={depth + 1} />
        ))}
      </NavLink>
    )
  }
  return (
    <NavLinkLink
      className="tn-nav-item"
      to={item.path}
      label={item.title}
      leftSection={icon}
      rightSection={<Badge value={item.badge} />}
      active={isActive}
    />
  )
}

function CollapsedItem({ item, active }: { item: MenuItem; active: string[] }): ReactNode {
  const navigate = useNavigate()
  const isActive = active[0] === item.id
  const icon = <Icon name={item.icon ?? "apps"} size={20} />
  if (item.children?.length) {
    return (
      <Menu position="right-start" offset={10} trigger="click-hover" openDelay={80} closeDelay={150}>
        <Menu.Target>
          <UnstyledButton
            className="tn-nav-item"
            data-active={isActive || undefined}
            aria-label={item.title}
            style={{ display: "flex", width: "100%", alignItems: "center" }}
          >
            {icon}
          </UnstyledButton>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Label>{item.title}</Menu.Label>
          {flatten(item.children).map((c) => (
            <Menu.Item key={c.id} onClick={() => navigate({ to: c.path })}>
              {c.title}
            </Menu.Item>
          ))}
        </Menu.Dropdown>
      </Menu>
    )
  }
  return (
    <Tooltip label={item.title} position="right" openDelay={0}>
      <NavLinkLink
        className="tn-nav-item"
        to={item.path}
        aria-label={item.title}
        leftSection={icon}
        active={isActive}
      />
    </Tooltip>
  )
}

function flatten(items: MenuItem[]): MenuItem[] {
  return items.flatMap((i) => (i.children?.length ? flatten(i.children) : [i]))
}
