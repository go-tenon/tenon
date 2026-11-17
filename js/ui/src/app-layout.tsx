// 应用外壳：运营后台和 SaaS 控制台共用。顶栏占满整行：左边是收起侧栏的按钮和标志，中间是多标签（开启时），
// 右边是操作、租户切换和用户菜单；
// 侧栏在顶栏下面，顶部是页面搜索，下面是 shell/ 中的导航模型（按权限过滤的菜单），可以收起为只有图标的窄栏。
// 窄屏时侧栏是顶栏下面的抽屉。密度、尺寸由主题的预设决定。
// 每个页面都有的内容是 AppLayout 的参数；某个页面才有的内容由页面用 Fill 放进插槽（actions、aside、footer）。

import {
  ActionIcon,
  AppShell,
  Box,
  Burger,
  Group,
  Kbd,
  Menu,
  NavLink,
  ScrollArea,
  Text,
  ThemeIcon,
  Tooltip,
  UnstyledButton,
  useMantineTheme,
} from "@mantine/core"
import { useOs } from "@mantine/hooks"
import { Spotlight, type SpotlightActionData, spotlight } from "@mantine/spotlight"
import {
  IconChevronRight,
  IconLayoutSidebarLeftCollapse,
  IconLayoutSidebarLeftExpand,
  IconSearch,
} from "@tabler/icons-react"
import { Link, Outlet, useNavigate, useRouterState } from "@tanstack/react-router"
import { type CSSProperties, type ReactNode, useEffect, useMemo, useRef, useState } from "react"
import { Icon } from "./icons"
import { TabsAvailableProvider } from "./layout-context"
import { NavLinkLink } from "./links"
import { TenantSwitcher } from "./menus"
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
  /** 产品名称，显示在顶栏左侧。 */
  title?: string
  /** 顶栏左侧的标志；默认是名称的首字。 */
  logo?: ReactNode
  /** 顶栏右侧的用户菜单，通常是 UserMenu。 */
  user?: ReactNode
  /** 顶栏中每个页面都有的操作，例如 NotificationsMenu、OperationsMenu。某个页面才有的操作用 <Fill name="actions">。 */
  actions?: ReactNode
  /** 侧栏顶部的页面搜索（Ctrl+K 或 / 打开），默认开启；false 时不显示。 */
  search?: boolean
  /** 开启多标签：true 或者 TabsOutlet 的选项。用户可以在 UserMenu 中关掉，窄屏时不显示标签栏。 */
  tabs?: boolean | TabsOutletProps
  /** 内容区的最大宽度，例如 1200；默认铺满。 */
  maxWidth?: number
}

/**
 * 需要登录的布局路由的组件：
 *
 *   export const Route = createFileRoute("/_app")({ beforeLoad: requireAuth, component: Layout })
 *   function Layout() {
 *     return <AppLayout title="博客后台" user={<UserMenu name={me.name} logout={logout} />} />
 *   }
 *
 * 多租户的应用（createApp 设置了 tenancy）在顶栏显示租户切换。
 */
export function AppLayout({ title, logo, user, actions, search = true, tabs, maxWidth }: AppLayoutProps): ReactNode {
  const theme = useMantineTheme()
  const m = layoutMetrics(theme)
  const [state, setState] = useLayoutState()
  const narrow = useNarrow()
  const collapsed = state.collapsed && !narrow
  const aside = useSlotFilled("aside")
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const tabsOn = Boolean(tabs) && state.tabs
  const showTabBar = tabsOn && !narrow

  // 窄屏时打开页面后收起导航抽屉。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 只在地址变化时收起
  useEffect(() => {
    if (state.navOpen) setState({ navOpen: false })
  }, [pathname])

  // 切换页面（点标签、点菜单、搜索跳转）时，新页面淡入，同时从下方上移 6px。只看路径：翻页、筛选不算。
  const content = useRef<HTMLDivElement>(null)
  const first = useRef(true)
  // biome-ignore lint/correctness/useExhaustiveDependencies: 只在路径变化时播放
  useEffect(() => {
    if (first.current) {
      first.current = false
      return
    }
    const el = content.current
    if (!el?.animate || window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) return
    el.animate(
      [
        { opacity: 0, transform: "translateY(6px)" },
        { opacity: 1, transform: "none" },
      ],
      {
        duration: 180,
        easing: "ease-out",
      },
    )
  }, [pathname])

  const vars = { "--tn-header-height": `${m.headerHeight}px` } as CSSProperties
  const collapseLabel = collapsed ? "展开侧栏" : "收起侧栏"
  return (
    <AppShell
      className="tn-shell"
      style={vars}
      header={{ height: m.headerHeight }}
      navbar={{
        width: collapsed ? m.navbarCollapsedWidth : m.navbarWidth,
        breakpoint: "sm",
        collapsed: { mobile: !state.navOpen },
      }}
      aside={aside ? { width: 320, breakpoint: "md", collapsed: { mobile: true } } : undefined}
      padding={0}
    >
      <AppShell.Header className="tn-header">
        <div className="tn-header-inner">
          {/* 侧栏展开时，这一块正好宽到侧栏与内容区之间的竖线，标签栏从竖线开始；收起时竖线太靠左，标签跟在标志后面。 */}
          <div
            className="tn-header-start"
            style={
              showTabBar && !collapsed
                ? { width: `calc(${m.navbarWidth}px - var(--mantine-spacing-md))`, paddingRight: 0 }
                : undefined
            }
          >
            <Burger
              hiddenFrom="sm"
              size="sm"
              opened={state.navOpen}
              onClick={() => setState({ navOpen: !state.navOpen })}
              aria-label={state.navOpen ? "关闭导航" : "打开导航"}
            />
            <Tooltip label={collapseLabel}>
              <ActionIcon
                visibleFrom="sm"
                size={32}
                aria-label={collapseLabel}
                onClick={() => setState({ collapsed: !state.collapsed })}
              >
                {collapsed ? (
                  <IconLayoutSidebarLeftExpand size={18} stroke={1.5} />
                ) : (
                  <IconLayoutSidebarLeftCollapse size={18} stroke={1.5} />
                )}
              </ActionIcon>
            </Tooltip>
            <Link to="/" className="tn-brand">
              {logo ?? <span className="tn-brand-mark">{(title ?? "T").slice(0, 1)}</span>}
              {title && <span className="tn-brand-title">{title}</span>}
            </Link>
          </div>
          {showTabBar ? <TabBar /> : <Box style={{ flex: 1 }} />}
          <Group gap="xs" wrap="nowrap">
            <Slot name="actions" />
            {actions}
            <TenantSwitcher />
            <TabsAvailableProvider value={Boolean(tabs)}>{user}</TabsAvailableProvider>
          </Group>
        </div>
      </AppShell.Header>

      <AppShell.Navbar className="tn-navbar">
        {search && <NavSearch collapsed={collapsed} />}
        <AppShell.Section grow component={ScrollArea} scrollbarSize={6}>
          <Nav collapsed={collapsed} />
        </AppShell.Section>
      </AppShell.Navbar>

      {aside && (
        <AppShell.Aside className="tn-navbar">
          <ScrollArea p={m.contentPadding}>
            <Slot name="aside" />
          </ScrollArea>
        </AppShell.Aside>
      )}

      <AppShell.Main>
        <Box ref={content} className="tn-content" p={m.contentPadding} maw={maxWidth}>
          {tabsOn ? <TabsOutlet home {...(typeof tabs === "object" ? tabs : {})} /> : <Outlet />}
        </Box>
        <Slot name="footer" />
      </AppShell.Main>
      {search && <PageSearch />}
      <NewVersionNotice />
    </AppShell>
  )
}

// ———————————————————————————— 页面搜索 ————————————————————————————

/** 侧栏顶部的搜索入口；收起时是一个图标。 */
function NavSearch({ collapsed }: { collapsed: boolean }): ReactNode {
  const os = useOs()
  const shortcut = os === "macos" || os === "ios" ? "⌘ K" : "Ctrl K"
  if (collapsed) {
    return (
      <div className="tn-nav-search-rail">
        <Tooltip label={`搜索  ${shortcut}`} position="right">
          <UnstyledButton className="tn-nav-rail-item" aria-label="搜索页面" onClick={spotlight.open}>
            <IconSearch size={18} stroke={1.5} />
          </UnstyledButton>
        </Tooltip>
        <div className="tn-nav-divider" />
      </div>
    )
  }
  return (
    <div className="tn-nav-search-wrap">
      <UnstyledButton className="tn-nav-search" onClick={spotlight.open}>
        <IconSearch size={15} stroke={1.5} />
        <span>搜索</span>
        <Kbd size="xs" visibleFrom="sm">
          {shortcut}
        </Kbd>
      </UnstyledButton>
    </div>
  )
}

/** 页面搜索：菜单中可以进入的页面（已经按权限过滤），说明是所在的分组。 */
function PageSearch(): ReactNode {
  const { menu } = useNavigation()
  const navigate = useNavigate()
  const actions = useMemo<SpotlightActionData[]>(
    () =>
      pages(menu).map(({ item, trail, icon }) => ({
        id: item.id,
        label: item.title,
        description: trail.length > 0 ? trail.join(" / ") : undefined,
        leftSection: <Icon name={icon ?? "apps"} size={18} stroke={1.5} />,
        onClick: () => void navigate({ to: item.path }),
      })),
    [menu, navigate],
  )
  return (
    <Spotlight
      actions={actions}
      shortcut={["mod + K", "/"]}
      nothingFound="没有找到页面"
      highlightQuery
      limit={20}
      scrollable
      maxHeight={420}
      searchProps={{ leftSection: <IconSearch size={16} stroke={1.5} />, placeholder: "搜索页面…" }}
    />
  )
}

/** 菜单中可以进入的页面，带上级分组的名称和最近的图标。 */
function pages(
  items: readonly MenuItem[],
  trail: string[] = [],
  icon?: string,
): { item: MenuItem; trail: string[]; icon?: string }[] {
  return items.flatMap((item) =>
    item.children?.length
      ? pages(item.children, [...trail, item.title], item.icon ?? icon)
      : [{ item, trail, icon: item.icon ?? icon }],
  )
}

// ———————————————————————————— 侧栏菜单 ————————————————————————————

/** 侧栏导航：分组可以展开，当前页面所在的分组默认展开。收起时只显示图标，分组用弹出菜单。 */
function Nav({ collapsed }: { collapsed: boolean }): ReactNode {
  const { menu, active } = useNavigation()
  if (collapsed) {
    return (
      <nav className="tn-nav tn-nav-collapsed" aria-label="主导航">
        {menu.map((item) => (
          <CollapsedItem key={item.id} item={item} active={active} />
        ))}
      </nav>
    )
  }
  return (
    <nav className="tn-nav" aria-label="主导航">
      {menu.map((item) => (
        <Item key={item.id} item={item} active={active} />
      ))}
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

/**
 * 菜单项。第一级：浅色底的图标加名称；下级：缩进，左侧一条竖线，有图标时显示图标。
 * 当前页面是主色实底、白字；当前页面所在的分组名称是主色。
 */
function Item({ item, active, depth = 0 }: { item: MenuItem; active: string[]; depth?: number }): ReactNode {
  const isActive = active[depth] === item.id && active.length === depth + 1
  const inPath = active[depth] === item.id
  const [opened, setOpened] = useState(inPath)
  useEffect(() => {
    if (inPath) setOpened(true)
  }, [inPath])
  const icon =
    depth === 0 ? (
      <ThemeIcon variant={isActive ? "white" : "light"} size={26} radius="sm">
        <Icon name={item.icon ?? "apps"} size={16} stroke={1.5} />
      </ThemeIcon>
    ) : item.icon ? (
      <Icon name={item.icon} size={16} stroke={1.5} />
    ) : undefined
  const style = { "--tn-nav-depth": depth } as CSSProperties
  if (item.children?.length) {
    return (
      <NavLink
        component="button"
        className="tn-nav-item"
        classNames={{ children: "tn-nav-children" }}
        style={style}
        data-depth={depth}
        data-in-path={inPath || undefined}
        data-opened={opened || undefined}
        label={item.title}
        leftSection={icon}
        rightSection={<IconChevronRight size={14} stroke={1.5} className="tn-nav-chevron" />}
        disableRightSectionRotation
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
      style={style}
      data-depth={depth}
      to={item.path}
      label={item.title}
      leftSection={icon}
      rightSection={<Badge value={item.badge} />}
      active={isActive}
    />
  )
}

/** 收起时的一项：40px 的图标按钮。页面悬停显示名称；分组点击或悬停向右弹出子页面。当前分组是主色实底。 */
function CollapsedItem({ item, active }: { item: MenuItem; active: string[] }): ReactNode {
  const navigate = useNavigate()
  const isActive = active[0] === item.id
  const icon = <Icon name={item.icon ?? "apps"} size={20} stroke={1.5} />
  if (item.children?.length) {
    return (
      <Menu position="right-start" offset={10} trigger="click-hover" openDelay={60} closeDelay={150} width={200}>
        <Menu.Target>
          <UnstyledButton className="tn-nav-rail-item" data-active={isActive || undefined} aria-label={item.title}>
            {icon}
          </UnstyledButton>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Label>{item.title}</Menu.Label>
          {pages(item.children).map(({ item: c }) => (
            <Menu.Item
              key={c.id}
              className="tn-nav-menu-item"
              data-active={active.includes(c.id) || undefined}
              leftSection={c.icon ? <Icon name={c.icon} size={16} stroke={1.5} /> : undefined}
              rightSection={<Badge value={c.badge} />}
              onClick={() => void navigate({ to: c.path })}
            >
              {c.title}
            </Menu.Item>
          ))}
        </Menu.Dropdown>
      </Menu>
    )
  }
  return (
    <Tooltip label={item.title} position="right">
      <Link to={item.path} className="tn-nav-rail-item" data-active={isActive || undefined} aria-label={item.title}>
        {icon}
      </Link>
    </Tooltip>
  )
}
