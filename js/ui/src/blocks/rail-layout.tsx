// RailLayout：双栏导航的布局，适合以模块切换为主的应用，例如 SaaS 控制台、聊天工具。
// 最左侧是模块的图标栏（标志、顶部快捷项、模块、用户菜单），旁边是当前模块的二级导航，二级导航可以收起；
// 设置从用户菜单进入，二级导航换成按分组列出的设置项和搜索。窄屏时二级导航是抽屉。
// 导航来自路由的 staticData（title、icon、order），与 FrameLayout 相同：第一级是模块，模块下的页面在二级导航中，
// 模块下的分组是二级导航中的一节；settings 路径下的页面不进图标栏。身份变化时只重新挂载页面（SessionBoundary）。
// 模块可以用 <Fill name="nav"> 放自己的二级导航（例如带数量的收件箱列表），staticData.fullBleed 的页面铺满内容区；
// 二级导航的收起状态按账号保存，useCollapseNav 可以临时收起它，useNavControl 供页面感知与切换二级导航状态。

import {
  ActionIcon,
  Box,
  Collapse,
  Drawer,
  Group,
  NavLink,
  ScrollArea,
  Stack,
  Text,
  TextInput,
  Tooltip,
  UnstyledButton,
} from "@mantine/core"
import {
  IconChevronRight,
  IconLayoutSidebarLeftCollapse,
  IconLayoutSidebarLeftExpand,
  IconSearch,
} from "@tabler/icons-react"
import { Link, Outlet, useNavigate, useRouterState } from "@tanstack/react-router"
import { type CSSProperties, type ReactNode, useCallback, useEffect, useMemo, useState } from "react"
import { SessionBoundary } from "../app/app"
import { formatDocumentTitle, useDocumentTitle, usePageTitle } from "../app/document-title"
import {
  NavControlProvider,
  Slot,
  useFullBleed,
  useLayoutState,
  useNarrow,
  useNavCollapseRequested,
  useNavigation,
  useRailSectionState,
  useSlotFilled,
} from "../app/layout"
import { firstPage, type MenuItem } from "../app/menu"
import { NewVersionNotice } from "../app/version-notice"
import { LoadingOverlay, useDelayedLoading } from "../components/loading"
import { MenuBadge } from "../components/menu-badge"
import { PageBreadcrumbs } from "../components/page"
import { useText } from "../i18n/i18n"
import { Icon } from "../theme/icons"

/** RailLayout 的选项。 */
export interface RailLayoutProps {
  title: string // 应用的名称：图标栏顶部标志的说明
  logo?: ReactNode // 图标栏顶部的标志，默认是名称的首字
  user?: ReactNode // 图标栏底部，通常是 <UserMenu position="right-end" />，其中放进入设置的菜单项
  railTop?: ReactNode // 图标栏顶部（标志下方、模块列表上方）的快捷入口，例如全局搜索按钮
  railBottom?: ReactNode // 图标栏底部（用户菜单上方）的附加操作
  settings?: string // 设置页面所在的路径，默认 /settings：其下的页面不进图标栏，从用户菜单进入
  maxWidth?: number // 内容区的最大宽度，默认 1152；staticData.fullBleed 的页面铺满，不受它限制
  railWidth?: number // 最左侧模块图标栏的宽度，默认 56
  navWidth?: number // 二级导航的宽度，默认 260
  headerHeight?: number // 顶栏基准高度（像素），默认 52，写入 --tn-rail-header 供各栏齐平
  settingsSearch?: boolean // 是否在设置页二级导航顶部显示搜索框，默认 false
  compactQuery?: string // 窄屏抽屉模式的媒体查询，默认 "(max-width: 48em)"
  documentTitle?: boolean // 浏览器标签的标题是“页面 - 应用的名称”，页面是路由的 staticData.title；默认开启，false 时不设置
  banner?: ReactNode // 布局最顶部的通栏提示，例如 ImpersonationBanner
  children?: ReactNode // 页面内容；默认是子路由
}

/** RailSection 的选项。 */
export interface RailSectionProps {
  /** 分组标题。 */
  title: ReactNode
  /** 标题行右侧的附加操作（例如新建按钮），与折叠按钮分离以避免嵌套交互元素。 */
  action?: ReactNode
  /** 是否允许点击标题折叠分组，默认 false。 */
  collapsible?: boolean
  /** 允许折叠时是否默认展开，默认 true。 */
  defaultOpened?: boolean
  /** 可选持久化键：传入时按账号记住分组的展开/收起状态。 */
  storeKey?: string
  children: ReactNode
}

/** RailSection 是二级导航中的一个分组：上方细分隔线、小号分组标题（可选折叠与右侧操作）和组内导航项。 */
export function RailSection({
  title,
  action,
  collapsible = false,
  defaultOpened = true,
  storeKey,
  children,
}: RailSectionProps): ReactNode {
  const [opened, setOpened] = useRailSectionState(collapsible ? storeKey : undefined, defaultOpened)
  return (
    <Stack gap={2} className="tn-rail-section">
      <div className="tn-rail-section-header">
        {collapsible ? (
          <UnstyledButton
            className="tn-rail-section-title"
            data-collapsible=""
            data-opened={opened || undefined}
            aria-expanded={opened}
            onClick={() => setOpened(!opened)}
          >
            <IconChevronRight size={12} stroke={2} className="tn-rail-section-chevron" />
            <span className="tn-rail-section-label">{title}</span>
          </UnstyledButton>
        ) : (
          <div className="tn-rail-section-title">
            <span className="tn-rail-section-label">{title}</span>
          </div>
        )}
        {action && <div className="tn-rail-section-action">{action}</div>}
      </div>
      {collapsible ? (
        <Collapse expanded={opened}>
          <Stack gap={2}>{children}</Stack>
        </Collapse>
      ) : (
        children
      )}
    </Stack>
  )
}

/** 从当前路由的查询参数中挑出 keys 指定的非空参数，供同模块二级导航切换时保留共享筛选条件。 */
function pickSearch(
  current: Record<string, unknown>,
  keys: readonly string[] | undefined,
): Record<string, unknown> | undefined {
  if (!keys?.length) return undefined
  const out: Record<string, unknown> = {}
  let any = false
  for (const k of keys) {
    const v = current[k]
    if (v !== undefined && v !== null && v !== "") {
      out[k] = v
      any = true
    }
  }
  return any ? out : undefined
}

/** NavItems 渲染二级导航中的链接，分组展开成一节（分组标题和其中的页面）。 */
function NavItems({ items, active, onNavigate }: { items: MenuItem[]; active: string[]; onNavigate(): void }) {
  return items.map((item) =>
    item.children ? (
      <RailSection key={item.id} title={item.title}>
        <NavItems items={item.children} active={active} onNavigate={onNavigate} />
      </RailSection>
    ) : (
      <NavLink
        key={item.id}
        component={Link}
        to={item.path}
        search={
          item.preserveSearch?.length
            ? (((prev: Record<string, unknown>) => pickSearch(prev, item.preserveSearch) ?? {}) as never)
            : undefined
        }
        label={item.title}
        leftSection={item.icon ? <Icon name={item.icon} size={16} /> : undefined}
        rightSection={<MenuBadge value={item.badge} />}
        active={active.includes(item.id)}
        onClick={onNavigate}
      />
    ),
  )
}

/** filterItems 只保留标题含有 q 的页面；分组中没有剩下的页面时去掉分组。 */
function filterItems(items: MenuItem[], q: string): MenuItem[] {
  if (!q) return items
  return items.flatMap((item) => {
    if (!item.children) return item.title.includes(q) ? [item] : []
    const children = filterItems(item.children, q)
    return children.length > 0 ? [{ ...item, children }] : []
  })
}

/** RailLayout 是 Rail 布局：双栏导航。 */
export function RailLayout({
  title,
  logo,
  user,
  railTop,
  railBottom,
  settings = "/settings",
  maxWidth = 1152,
  railWidth = 56,
  navWidth = 260,
  headerHeight = 52,
  settingsSearch = false,
  compactQuery = "(max-width: 48em)",
  documentTitle = true,
  banner,
  children,
}: RailLayoutProps): ReactNode {
  const { menu, active } = useNavigation()
  const t = useText()
  const pageTitle = usePageTitle(false)
  useDocumentTitle(documentTitle ? formatDocumentTitle(pageTitle, title) : undefined)
  const navigate = useNavigate()
  const href = useRouterState({ select: (s) => s.location.href })
  const routerPending = useRouterState({ select: (s) => s.status === "pending" })
  const compact = useNarrow(compactQuery)
  const fullBleed = useFullBleed()
  // 路由切页默认瞬切，仅当路由 loader 耗时超过延迟阈值时显示轻磨砂加载覆层。
  const busy = useDelayedLoading(routerPending)
  const customNav = useSlotFilled("nav")
  // 宽屏时二级导航是否收起按账号保存；有组件用 useCollapseNav 要求时临时收起，用户这时点展开则以用户为准，
  // 直到下一次要求。设置页不提供收起按钮，进入设置时始终展开。窄屏时二级导航是抽屉，默认关闭。
  const [layout, setLayout] = useLayoutState()
  const requested = useNavCollapseRequested()
  const [released, setReleased] = useState(false)
  useEffect(() => {
    if (!requested) setReleased(false)
  }, [requested])
  const [drawer, setDrawer] = useState(false)
  const [search, setSearch] = useState("")

  // 窄屏下路由或查询参数变化时自动收起导航抽屉。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 只在地址变化时关闭抽屉
  useEffect(() => {
    setDrawer(false)
  }, [href])

  const modules = menu.filter((m) => m.path !== settings)
  const settingsGroup = menu.find((m) => m.path === settings)
  const inSettings = !!settingsGroup && active[0] === settingsGroup.id

  const current = inSettings ? settingsGroup : (modules.find((m) => m.id === active[0]) ?? modules[0])
  // 设置页、存在子菜单或模块提供了自定义 nav 插槽时显示二级导航；单页模块隐藏冗余二级栏。
  const hasSecondary = inSettings || customNav || Boolean(current?.children?.length)
  const items = current ? (current.children ?? []) : []

  const expanded = hasSecondary && (inSettings || (!layout.collapsed && !(requested && !released)))
  const setExpanded = useCallback(
    (next: boolean) => {
      if (next && requested) setReleased(true)
      setLayout({ collapsed: !next })
    },
    [requested, setLayout],
  )
  const open = hasSecondary && (compact ? drawer : expanded)
  const setOpen = useCallback(
    (next: boolean) => {
      if (compact) setDrawer(next)
      else setExpanded(next)
    },
    [compact, setExpanded],
  )
  const navControl = useMemo(
    () => ({
      hasNav: hasSecondary,
      open,
      compact,
      openNav: () => {
        if (hasSecondary) setOpen(true)
      },
      closeNav: () => setOpen(false),
      toggleNav: () => {
        if (hasSecondary) setOpen(!open)
      },
    }),
    [hasSecondary, open, compact, setOpen],
  )

  const secondary = (
    <>
      <Group className="tn-rail-secondary-header" gap="xs" wrap="nowrap">
        <Text fw={600} truncate style={{ flex: 1 }}>
          {current?.title}
        </Text>
        {!compact && !inSettings && (
          <ActionIcon
            variant="subtle"
            color="gray"
            aria-label={t("tenon.rail.collapse_nav")}
            onClick={() => setExpanded(false)}
          >
            <IconLayoutSidebarLeftCollapse size={18} stroke={1.6} />
          </ActionIcon>
        )}
      </Group>
      <ScrollArea className="tn-rail-secondary-content" type="hover">
        {/* 二级导航的内容：模块用 <Fill name="nav"> 放的面板，或者按路由生成的链接 */}
        <Stack
          gap={2}
          p={8}
          component="nav"
          aria-label={current?.title}
          onClick={(e) => {
            // 窄屏抽屉中点击自定义 nav 插槽里的导航项时自动关闭抽屉。
            if (compact && (e.target as HTMLElement).closest(".mantine-NavLink-root, a[href]")) setDrawer(false)
          }}
        >
          {customNav && !inSettings && <Slot name="nav" />}
          {inSettings && settingsSearch && (
            <TextInput
              placeholder={t("tenon.common.search")}
              aria-label={t("tenon.rail.search_settings")}
              leftSection={<IconSearch size={14} />}
              value={search}
              onChange={(e) => setSearch(e.currentTarget.value)}
              mb={6}
            />
          )}
          {(!customNav || inSettings) && (
            <NavItems
              items={inSettings && settingsSearch ? filterItems(items, search.trim()) : items}
              active={active}
              onNavigate={() => compact && setDrawer(false)}
            />
          )}
        </Stack>
      </ScrollArea>
    </>
  )

  const vars = {
    "--tn-rail-rail": `${railWidth}px`,
    "--tn-rail-secondary": `${navWidth}px`,
    "--tn-rail-header": `${headerHeight}px`,
  } as CSSProperties
  const rail = (
    <div className="tn-rail" data-open={open || undefined} data-compact={compact || undefined} style={vars}>
      <a className="tn-skip-link" href="#main-content">
        {t("tenon.layout.skip_to_content")}
      </a>
      <nav className="tn-rail-rail" aria-label={t("tenon.rail.modules")}>
        <div className="tn-rail-logo" title={title}>
          {logo ?? <span className="tn-brand-mark">{title.slice(0, 1)}</span>}
        </div>
        {railTop && <div className="tn-rail-top">{railTop}</div>}
        <Stack gap={6} align="center" className="tn-rail-modules">
          {modules.map((m) => {
            const isActive = !inSettings && current?.id === m.id
            // 当前模块存在二级导航且处于收起态时，鼠标移上去图标换成“展开”。
            const expandHere = hasSecondary && isActive && !compact && !expanded
            return (
              <Tooltip
                key={m.id}
                label={expandHere ? t("tenon.common.expand") : m.title}
                position="right"
                openDelay={350}
              >
                <UnstyledButton
                  className="tn-rail-module"
                  aria-label={m.title}
                  aria-current={isActive ? "page" : undefined}
                  data-expand={expandHere || undefined}
                  onClick={() => {
                    if (!isActive) {
                      const to = m.children ? firstPage(m.children) : m.path
                      if (to) void navigate({ to })
                      setOpen(true)
                    } else if (!hasSecondary) {
                      return
                    } else if (compact) {
                      setDrawer((d) => !d)
                    } else {
                      setOpen(true)
                    }
                  }}
                >
                  <span className="tn-rail-module-icon">
                    <Icon name={m.icon} size={20} />
                  </span>
                  {m.badge !== undefined && (
                    <span className="tn-rail-module-badge">
                      <MenuBadge value={m.badge} />
                    </span>
                  )}
                  {expandHere && (
                    <IconLayoutSidebarLeftExpand className="tn-rail-module-expand" size={20} stroke={1.6} />
                  )}
                </UnstyledButton>
              </Tooltip>
            )
          })}
        </Stack>
        <div className="tn-rail-footer">
          {railBottom}
          {user}
        </div>
      </nav>
      <div className="tn-rail-workspace">
        {compact ? (
          hasSecondary && (
            <Drawer opened={drawer} onClose={() => setDrawer(false)} size={288} padding={0} withCloseButton={false}>
              <div className="tn-rail-secondary-drawer">{secondary}</div>
            </Drawer>
          )
        ) : (
          <aside
            className="tn-rail-secondary"
            aria-label={t("tenon.rail.secondary")}
            aria-hidden={!expanded}
            inert={!expanded}
          >
            <div className="tn-rail-secondary-inner">{secondary}</div>
          </aside>
        )}
        <div className="tn-rail-main-shell" data-loading-active={busy || undefined} aria-busy={busy || undefined}>
          <main id="main-content" tabIndex={-1} className="tn-rail-main" data-full-bleed={fullBleed || undefined}>
            {fullBleed ? (
              // 铺满的页面占满内容区的宽度和高度，没有面包屑，页面中的各栏自己滚动
              <div className="tn-rail-full">
                <SessionBoundary>{children ?? <Outlet />}</SessionBoundary>
              </div>
            ) : (
              <Box maw={maxWidth} mx="auto" className="tn-rail-page">
                {/* 设置中的页面不显示面包屑：二级导航已经标出了位置 */}
                <PageBreadcrumbs show={!inSettings}>
                  <SessionBoundary>{children ?? <Outlet />}</SessionBoundary>
                </PageBreadcrumbs>
              </Box>
            )}
          </main>
          <LoadingOverlay visible={busy} />
        </div>
      </div>
    </div>
  )
  return (
    <NavControlProvider value={navControl}>
      {banner ? (
        <div className="tn-rail-shell">
          <div className="tn-rail-banner">{banner}</div>
          {rail}
        </div>
      ) : (
        rail
      )}
      <NewVersionNotice />
    </NavControlProvider>
  )
}
