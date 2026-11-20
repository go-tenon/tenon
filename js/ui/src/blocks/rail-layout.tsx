// RailLayout：双栏导航的布局，适合以模块切换为主的应用，例如 SaaS 控制台、聊天工具。
// 最左侧是模块的图标栏（标志、模块、用户菜单），旁边是当前模块的二级导航，二级导航可以收起；
// 设置从用户菜单进入，二级导航换成按分组列出的设置项和搜索。窄屏时二级导航是抽屉。
// 导航来自路由的 staticData（title、icon、order），与 FrameLayout 相同：第一级是模块，模块下的页面在二级导航中，
// 模块下的分组是二级导航中的一节；settings 路径下的页面不进图标栏。身份变化时只重新挂载页面（SessionBoundary）。
// 模块可以用 <Fill name="nav"> 放自己的二级导航（例如带数量的收件箱列表），staticData.fullBleed 的页面铺满内容区；
// 二级导航的收起状态按账号保存，useCollapseNav 可以临时收起它。useMenuBadge 登记的徽标显示在图标栏和二级导航中。

import {
  ActionIcon,
  Box,
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
import { useMediaQuery } from "@mantine/hooks"
import { IconLayoutSidebarLeftCollapse, IconLayoutSidebarLeftExpand, IconSearch } from "@tabler/icons-react"
import { Link, Outlet, useNavigate } from "@tanstack/react-router"
import { type CSSProperties, type ReactNode, useEffect, useState } from "react"
import { SessionBoundary } from "../app/app"
import { formatDocumentTitle, useDocumentTitle, usePageTitle } from "../app/document-title"
import {
  Slot,
  useFullBleed,
  useLayoutState,
  useNavCollapseRequested,
  useNavigation,
  useSlotFilled,
} from "../app/layout"
import { firstPage, type MenuItem } from "../app/menu"
import { MenuBadge } from "../components/menu-badge"
import { PageBreadcrumbs } from "../components/page"
import { Icon } from "../theme/icons"

/** RailLayout 的选项。 */
export interface RailLayoutProps {
  title: string // 应用的名称：图标栏顶部标志的说明
  logo?: ReactNode // 图标栏顶部的标志，默认是名称的首字
  user?: ReactNode // 图标栏底部，通常是 <UserMenu position="right-end" />，其中放进入设置的菜单项
  settings?: string // 设置页面所在的路径，默认 /settings：其下的页面不进图标栏，从用户菜单进入
  maxWidth?: number // 内容区的最大宽度，默认 1152；staticData.fullBleed 的页面铺满，不受它限制
  navWidth?: number // 二级导航的宽度，默认 260
  documentTitle?: boolean // 浏览器标签的标题是“页面 - 应用的名称”，页面是路由的 staticData.title；默认开启，false 时不设置
  children?: ReactNode // 页面内容；默认是子路由
}

/** NavItems 渲染二级导航中的链接，分组展开成一节（分组标题和其中的页面）。 */
function NavItems({ items, active, onNavigate }: { items: MenuItem[]; active: string[]; onNavigate(): void }) {
  return items.map((item) =>
    item.children ? (
      <Stack key={item.id} gap={2} className="tn-rail-section">
        <Text size="xs" c="dimmed" fw={600} px={10} pt={4}>
          {item.title}
        </Text>
        <NavItems items={item.children} active={active} onNavigate={onNavigate} />
      </Stack>
    ) : (
      <NavLink
        key={item.id}
        component={Link}
        to={item.path}
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
  settings = "/settings",
  maxWidth = 1152,
  navWidth = 260,
  documentTitle = true,
  children,
}: RailLayoutProps): ReactNode {
  const { menu, active } = useNavigation()
  const pageTitle = usePageTitle(false)
  useDocumentTitle(documentTitle ? formatDocumentTitle(pageTitle, title) : undefined)
  const navigate = useNavigate()
  const compact = useMediaQuery("(max-width: 48em)") ?? false
  const fullBleed = useFullBleed()
  const customNav = useSlotFilled("nav")
  // 宽屏时二级导航是否收起按账号保存；有组件用 useCollapseNav 要求时临时收起，用户这时点展开则以用户为准，
  // 直到下一次要求。窄屏时二级导航是抽屉，默认关闭。
  const [layout, setLayout] = useLayoutState()
  const requested = useNavCollapseRequested()
  const [released, setReleased] = useState(false)
  useEffect(() => {
    if (!requested) setReleased(false)
  }, [requested])
  const expanded = !layout.collapsed && !(requested && !released)
  const setExpanded = (next: boolean) => {
    if (next && requested) setReleased(true)
    setLayout({ collapsed: !next })
  }
  const [drawer, setDrawer] = useState(false)
  const [search, setSearch] = useState("")
  const open = compact ? drawer : expanded
  const setOpen = compact ? setDrawer : setExpanded

  const modules = menu.filter((m) => m.path !== settings)
  const settingsGroup = menu.find((m) => m.path === settings)
  const inSettings = !!settingsGroup && active[0] === settingsGroup.id

  const current = inSettings ? settingsGroup : (modules.find((m) => m.id === active[0]) ?? modules[0])
  // 只有一个页面的模块（例如首页），二级导航中就是它自己。
  const items = current ? (current.children ?? [current]) : []

  const secondary = (
    <>
      <Group className="tn-rail-secondary-header" gap="xs" wrap="nowrap">
        <Text fw={600} truncate style={{ flex: 1 }}>
          {current?.title}
        </Text>
        {!compact && !inSettings && (
          <ActionIcon variant="subtle" color="gray" aria-label="收起二级导航" onClick={() => setExpanded(false)}>
            <IconLayoutSidebarLeftCollapse size={18} stroke={1.6} />
          </ActionIcon>
        )}
      </Group>
      <ScrollArea className="tn-rail-secondary-content" type="hover">
        {/* 二级导航的内容：模块用 <Fill name="nav"> 放的面板，或者按路由生成的链接 */}
        <Stack gap={2} p={8} pt={4} component="nav" aria-label={current?.title}>
          {customNav && !inSettings && <Slot name="nav" />}
          {inSettings && (
            <TextInput
              placeholder="搜索"
              aria-label="搜索设置"
              leftSection={<IconSearch size={14} />}
              value={search}
              onChange={(e) => setSearch(e.currentTarget.value)}
              mb={6}
            />
          )}
          {(!customNav || inSettings) && (
            <NavItems
              items={inSettings ? filterItems(items, search.trim()) : items}
              active={active}
              onNavigate={() => compact && setDrawer(false)}
            />
          )}
        </Stack>
      </ScrollArea>
    </>
  )

  const vars = { "--tn-rail-secondary": `${navWidth}px` } as CSSProperties
  return (
    <div className="tn-rail" data-open={open || undefined} style={vars}>
      <a className="tn-skip-link" href="#main-content">
        跳转到内容
      </a>
      <nav className="tn-rail-rail" aria-label="模块导航">
        <div className="tn-rail-logo" title={title}>
          {logo ?? <span className="tn-brand-mark">{title.slice(0, 1)}</span>}
        </div>
        <Stack gap={6} align="center" className="tn-rail-modules">
          {modules.map((m) => {
            const isActive = !inSettings && current?.id === m.id
            // 当前模块的二级导航收起时，鼠标移上去图标换成“展开”。
            const expandHere = isActive && !compact && !expanded
            return (
              <Tooltip key={m.id} label={expandHere ? "展开" : m.title} position="right" openDelay={350}>
                <UnstyledButton
                  className="tn-rail-module"
                  aria-label={m.title}
                  aria-current={isActive ? "page" : undefined}
                  data-expand={expandHere || undefined}
                  onClick={() => {
                    if (!isActive) {
                      const to = m.children ? firstPage(m.children) : m.path
                      if (to) void navigate({ to })
                    }
                    setOpen(true)
                  }}
                >
                  <span className="tn-rail-module-icon">
                    <Icon name={m.icon} size={18} />
                  </span>
                  {m.badge !== undefined && (
                    <span className="tn-rail-module-badge">
                      <MenuBadge value={m.badge} />
                    </span>
                  )}
                  {expandHere && (
                    <IconLayoutSidebarLeftExpand className="tn-rail-module-expand" size={18} stroke={1.6} />
                  )}
                </UnstyledButton>
              </Tooltip>
            )
          })}
        </Stack>
        <div className="tn-rail-footer">{user}</div>
      </nav>
      <div className="tn-rail-workspace">
        {compact ? (
          <Drawer opened={drawer} onClose={() => setDrawer(false)} size={288} padding={0} withCloseButton={false}>
            <div className="tn-rail-secondary-drawer">{secondary}</div>
          </Drawer>
        ) : (
          <aside className="tn-rail-secondary" aria-label="二级导航" aria-hidden={!expanded} inert={!expanded}>
            <div className="tn-rail-secondary-inner">{secondary}</div>
          </aside>
        )}
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
      </div>
    </div>
  )
}
