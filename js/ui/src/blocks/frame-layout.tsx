// FrameLayout：顶栏加侧栏菜单的布局，信息密集，适合表格和表单多的后台，例如运营后台、商家后台、内部系统。
// 顶栏占满整行：左边是收起侧栏的按钮和标志，中间是多标签（开启时），
// 右边是操作和用户菜单；
// 侧栏在顶栏下面，顶部是页面搜索，下面是导航模型（按权限过滤的菜单），可以收起为只有图标的窄栏。
// 窄屏时侧栏是顶栏下面的抽屉。顶栏和侧栏的尺寸是这个布局自己的，控件和内边距的密度随主题的预设。
// 每个页面都有的内容是 FrameLayout 的参数；某个页面才有的内容由页面用 Fill 放进插槽（actions、aside、footer）。
// staticData.fullBleed 的页面铺满顶栏以下的内容区，例如聊天、收件箱这类多栏的页面。

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
  Switch,
  ThemeIcon,
  Tooltip,
  UnstyledButton,
  useMantineTheme,
} from "@mantine/core"
import { useOs } from "@mantine/hooks"
import { Spotlight, type SpotlightActionData, spotlight } from "@mantine/spotlight"
import {
  IconBrowser,
  IconChevronRight,
  IconLayoutSidebarLeftCollapse,
  IconLayoutSidebarLeftExpand,
  IconSearch,
} from "@tabler/icons-react"
import { Link, Outlet, useNavigate, useRouterState } from "@tanstack/react-router"
import { type CSSProperties, type ReactNode, useEffect, useMemo, useRef, useState } from "react"
import { SessionBoundary } from "../app/app"
import { formatDocumentTitle, useDocumentTitle, usePageTitle } from "../app/document-title"
import { Fill, Slot, useFullBleed, useLayoutState, useNarrow, useNavigation, useSlotFilled } from "../app/layout"
import type { MenuItem } from "../app/menu"
import { ContentTransitionProvider, PageProgress } from "../app/tabs/progress"
import { TabBar } from "../app/tabs/tab-bar"
import { TabsOutlet, type TabsOutletProps } from "../app/tabs/tabs"
import { NewVersionNotice } from "../app/version-notice"
import { NavLinkLink } from "../components/links"
import { LoadingOverlay, useAnyFetching, useSmoothTransition } from "../components/loading"
import { MenuBadge } from "../components/menu-badge"
import { useText } from "../i18n/i18n"
import { Icon } from "../theme/icons"
import { layoutMetrics } from "../theme/theme"

// 布局自己的尺寸（像素）：顶栏高度，侧栏展开和收起为图标时的宽度。
const headerHeight = 52
const navbarWidth = 224
const navbarCollapsedWidth = 56

/** FrameLayout 的选项。 */
export interface FrameLayoutProps {
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
  /** 内容区的最大宽度，例如 1200；默认铺满。staticData.fullBleed 的页面不受它限制。 */
  maxWidth?: number
  /** 浏览器标签的标题是“页面 - 产品名称”，默认开启：页面是当前标签的标题（开启多标签时）或路由的 staticData.title。
   * false 时不设置，标题由应用自己管理。
   */
  documentTitle?: boolean
  /** 内容区顶部的通栏提示，例如 ImpersonationBanner。 */
  banner?: ReactNode
}

/**
 * 需要登录的布局路由的组件：
 *
 *   export const Route = createFileRoute("/_app")({ beforeLoad: requireAuth, component: Layout })
 *   function Layout() {
 *     return <FrameLayout title="博客后台" user={<UserMenu name={me.name} logout={logout} />} />
 *   }
 */
export function FrameLayout({
  title,
  logo,
  user,
  actions,
  search = true,
  tabs,
  maxWidth,
  documentTitle = true,
  banner,
}: FrameLayoutProps): ReactNode {
  const theme = useMantineTheme()
  const m = layoutMetrics(theme)
  const [state, setState] = useLayoutState()
  const t = useText()
  const narrow = useNarrow()
  const collapsed = state.collapsed && !narrow
  const aside = useSlotFilled("aside")
  const fullBleed = useFullBleed()
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const routerPending = useRouterState({ select: (s) => s.status === "pending" })
  const fetching = useAnyFetching()
  const tabsOn = Boolean(tabs) && state.tabs
  const pageTitle = usePageTitle(tabsOn)
  useDocumentTitle(documentTitle ? formatDocumentTitle(pageTitle, title) : undefined)
  const showTabBar = tabsOn && !narrow

  // 窄屏时打开页面后收起导航抽屉。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 只在地址变化时收起
  useEffect(() => {
    if (state.navOpen) setState({ navOpen: false })
  }, [pathname])

  // 切换页面（点顶栏标签、点侧栏菜单、搜索跳转）与刷新标签统一使用轻磨砂居中转圈覆层：
  // 缓存或保活页面保底展示 220ms，首次打开或慢请求随页面查询与路由状态自动延长直到返回。
  const routeState = useSmoothTransition(pathname, pathname, fetching || routerPending)
  const [reloading, setReloading] = useState(false)
  const reloadStartRef = useRef(0)
  const reloadTimerRef = useRef<number | undefined>(undefined)
  useEffect(
    () => () => {
      if (reloadTimerRef.current !== undefined) window.clearTimeout(reloadTimerRef.current)
    },
    [],
  )
  const transition = useMemo(
    () => ({
      async out() {
        if (reloadTimerRef.current !== undefined) {
          window.clearTimeout(reloadTimerRef.current)
          reloadTimerRef.current = undefined
        }
        reloadStartRef.current = Date.now()
        setReloading(true)
      },
      in() {
        const remain = 220 - (Date.now() - reloadStartRef.current)
        if (remain <= 0) {
          setReloading(false)
          return
        }
        reloadTimerRef.current = window.setTimeout(() => {
          reloadTimerRef.current = undefined
          setReloading(false)
        }, remain)
      },
    }),
    [],
  )
  const busy = routeState.busy || reloading

  const vars = { "--tn-header-height": `${headerHeight}px` } as CSSProperties
  const collapseLabel = collapsed ? t("tenon.frame.expand_sidebar") : t("tenon.frame.collapse_sidebar")
  return (
    <AppShell
      className="tn-shell"
      style={vars}
      header={{ height: headerHeight }}
      navbar={{
        width: collapsed ? navbarCollapsedWidth : navbarWidth,
        breakpoint: "sm",
        collapsed: { mobile: !state.navOpen },
      }}
      aside={aside ? { width: 320, breakpoint: "md", collapsed: { mobile: true } } : undefined}
      padding={0}
    >
      <AppShell.Header className="tn-header">
        <div className="tn-header-inner">
          {/* 有标签栏时，标签栏从侧栏展开时的边线往右 6px 开始（选中的第一个标签向左外翻的圆弧正好落在边线上）；收起侧栏时标签不动。
              名称长、放不下时这一块变宽，标签栏跟在名称后面（间隔 16px），不会盖住名称。 */}
          <div
            className="tn-header-start"
            style={showTabBar ? { minWidth: `calc(${navbarWidth + 6}px - var(--mantine-spacing-md))` } : undefined}
          >
            <Burger
              hiddenFrom="sm"
              size="sm"
              opened={state.navOpen}
              onClick={() => setState({ navOpen: !state.navOpen })}
              aria-label={state.navOpen ? t("tenon.frame.close_nav") : t("tenon.frame.open_nav")}
            />
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
            <Link to="/" className="tn-brand">
              {logo ?? <span className="tn-brand-mark">{(title ?? "T").slice(0, 1)}</span>}
              {title && <span className="tn-brand-title">{title}</span>}
            </Link>
          </div>
          {showTabBar ? (
            <ContentTransitionProvider value={transition}>
              <TabBar />
            </ContentTransitionProvider>
          ) : (
            <Box style={{ flex: 1 }} />
          )}
          <Group gap="xs" wrap="nowrap">
            <Slot name="actions" />
            {actions}
            {user}
            {tabs && (
              <Fill name="user-menu">
                <TabsSwitch />
              </Fill>
            )}
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
        {banner}
        {/* 铺满的页面（staticData.fullBleed）没有内边距和最大宽度，高度是顶栏以下的整个窗口，页面中的各栏自己滚动。 */}
        <div
          className="tn-frame-stage"
          data-full-bleed={fullBleed || undefined}
          data-loading-active={busy || undefined}
          aria-busy={busy || undefined}
        >
          <Box
            className="tn-content"
            data-full-bleed={fullBleed || undefined}
            p={fullBleed ? 0 : m.contentPadding}
            maw={fullBleed ? undefined : maxWidth}
          >
            {/* 身份变化时只重新挂载页面，布局和 HTML 文档保持不变。 */}
            <SessionBoundary>
              {tabsOn ? (
                <TabsOutlet home transitioning={busy} {...(typeof tabs === "object" ? tabs : {})} />
              ) : (
                <Outlet />
              )}
            </SessionBoundary>
          </Box>
          <LoadingOverlay visible={busy} sticky />
        </div>
        <Slot name="footer" />
      </AppShell.Main>
      {search && <PageSearch />}
      <PageProgress />
      <NewVersionNotice />
    </AppShell>
  )
}

// ———————————————————————————— 页面搜索 ————————————————————————————

/** 侧栏顶部的搜索入口；收起时是一个图标。 */
function NavSearch({ collapsed }: { collapsed: boolean }): ReactNode {
  const os = useOs()
  const t = useText()
  const shortcut = os === "macos" || os === "ios" ? "⌘ K" : "Ctrl K"
  if (collapsed) {
    return (
      <div className="tn-nav-search-rail">
        <Tooltip label={`${t("tenon.common.search")}  ${shortcut}`} position="right">
          <UnstyledButton
            className="tn-nav-rail-item"
            aria-label={t("tenon.frame.search_pages")}
            onClick={spotlight.open}
          >
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
        <span>{t("tenon.common.search")}</span>
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
  const t = useText()
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
      nothingFound={t("tenon.frame.no_pages")}
      highlightQuery
      limit={20}
      scrollable
      maxHeight={420}
      searchProps={{
        leftSection: <IconSearch size={16} stroke={1.5} />,
        placeholder: t("tenon.frame.search_pages_placeholder"),
      }}
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
  const t = useText()
  if (collapsed) {
    return (
      <nav className="tn-nav tn-nav-collapsed" aria-label={t("tenon.frame.main_nav")}>
        {menu.map((item) => (
          <CollapsedItem key={item.id} item={item} active={active} />
        ))}
      </nav>
    )
  }
  return (
    <nav className="tn-nav" aria-label={t("tenon.frame.main_nav")}>
      {menu.map((item) => (
        <Item key={item.id} item={item} active={active} />
      ))}
    </nav>
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
      rightSection={<MenuBadge value={item.badge} />}
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
              rightSection={<MenuBadge value={c.badge} />}
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

/** 多标签的开关，按账号保存。窄屏时不显示标签栏，也不显示开关。 */
function TabsSwitch(): ReactNode {
  const [layout, setLayout] = useLayoutState()
  const t = useText()
  return (
    <Box visibleFrom="sm" className="tn-menu-section tn-menu-toggles">
      <UnstyledButton
        className="tn-menu-toggle"
        role="switch"
        aria-checked={layout.tabs}
        onClick={() => setLayout({ tabs: !layout.tabs })}
      >
        <IconBrowser size={18} stroke={1.5} />
        <span>{t("tenon.frame.tabs")}</span>
        <Switch size="xs" checked={layout.tabs} readOnly tabIndex={-1} aria-hidden style={{ pointerEvents: "none" }} />
      </UnstyledButton>
    </Box>
  )
}
