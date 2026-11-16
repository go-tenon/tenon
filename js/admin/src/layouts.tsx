// 布局：按 @go-tenon/shell 的布局约定实现——读取导航模型和布局状态，把插槽放到自己的位置上。
// 页面只往插槽中放内容（<Fill name="actions">），不关心当前是哪一种布局。

import { ArrowLeftOutlined, MenuFoldOutlined, MenuUnfoldOutlined } from "@ant-design/icons"
import { ProLayout } from "@ant-design/pro-components"
import {
  type MenuItem,
  pageUnder,
  Slot,
  TabsOutlet,
  type TabsOutletProps,
  useLayoutState,
  useLayoutVariant,
  useNavigation,
  useSlotFilled,
} from "@go-tenon/shell"
import { Outlet, useRouter, useRouterState } from "@tanstack/react-router"
import { Breadcrumb, Button, Card, Layout, Menu, type MenuProps, Space, Tooltip, theme } from "antd"
import type { MouseEvent, ReactNode } from "react"
import { useAdmin } from "./provider"
import { TabBar } from "./tabbar"

/** 应用内的链接：保留浏览器的默认行为（新窗口打开等），普通点击由路由跳转。 */
export function NavLink({ to, children }: { to: string; children: ReactNode }): ReactNode {
  const router = useRouter()
  const onClick = (e: MouseEvent<HTMLAnchorElement>) => {
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
    e.preventDefault()
    void router.navigate({ href: to })
  }
  return (
    <a href={to} onClick={onClick}>
      {children}
    </a>
  )
}

/** 页面区域：多标签时是标签栏和 TabsOutlet，否则是 Outlet。 */
function Content({ tabs }: { tabs?: boolean | TabsOutletProps }): ReactNode {
  if (!tabs) return <Outlet />
  return (
    <>
      <TabBar />
      <TabsOutlet {...(typeof tabs === "object" ? tabs : {})} />
    </>
  )
}

/** 面包屑：最后一级是当前页面；分组跳到它下面第一个可以进入的页面。 */
export function Crumbs(): ReactNode {
  const { crumbs, menu } = useNavigation()
  return (
    <Breadcrumb
      items={crumbs.map((c, i) => {
        const target = i < crumbs.length - 1 ? pageUnder(menu, c.path) : undefined
        return { key: c.path, title: target ? <NavLink to={target}>{c.title}</NavLink> : c.title }
      })}
    />
  )
}

type ProRoute = { path: string; name: string; icon?: ReactNode; children?: ProRoute[] }

function toProRoutes(items: readonly MenuItem[], icons: Record<string, ReactNode>): ProRoute[] {
  return items.map((m) => ({
    path: m.path,
    name: m.badge ? `${m.title} (${m.badge === true ? "•" : m.badge})` : m.title,
    icon: m.icon ? icons[m.icon] : undefined,
    children: m.children ? toProRoutes(m.children, icons) : undefined,
  }))
}

/** 侧栏、顶部导航、顶栏 + 侧栏三种布局。 */
export type AdminVariant = "side" | "top" | "mix"

/** AdminLayout 的选项。 */
export interface AdminLayoutProps {
  title: string
  logo?: ReactNode
  /**
   * 布局：side（默认）左侧导航可收起为图标；top 导航在顶栏；mix 一级导航在顶栏、二级在侧栏。
   * 传数组时用户可以在这几种之间切换（见 useLayoutVariant），第一种是默认的。
   */
  variant?: AdminVariant | [AdminVariant, ...AdminVariant[]]
  /** 开启多标签：true 或 TabsOutlet 的选项。 */
  tabs?: boolean | TabsOutletProps
  /** 侧栏的宽度，默认 220。 */
  siderWidth?: number
}

/**
 * 运营后台的主布局：基于 ProLayout。用作受保护布局路由的 component：
 *
 *   createFileRoute("/_app")({ beforeLoad: requireAuth, component: () => <AdminLayout title="运营后台" tabs /> })
 *
 * 插槽：brand（菜单头部）、search 和 actions、tenant（顶栏右侧）、user（头像位置）、footer（页脚）。
 */
export function AdminLayout({ title, logo, variant = "side", tabs, siderWidth = 220 }: AdminLayoutProps): ReactNode {
  const nav = useNavigation()
  const { icons } = useAdmin()
  const router = useRouter()
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const [state, setState] = useLayoutState()
  const allowed = (Array.isArray(variant) ? variant : [variant]) as [AdminVariant, ...AdminVariant[]]
  const [layout] = useLayoutVariant(allowed)
  const hasUser = useSlotFilled("user")
  const hasBrand = useSlotFilled("brand")

  return (
    <ProLayout
      title={title}
      logo={logo ?? false}
      layout={layout}
      splitMenus={layout === "mix"}
      fixSiderbar
      siderWidth={siderWidth}
      collapsed={state.collapsed}
      onCollapse={(collapsed) => setState({ collapsed })}
      location={{ pathname }}
      route={{ path: "/", children: toProRoutes(nav.menu, icons) }}
      menuItemRender={(item, dom) => (item.path ? <NavLink to={item.path}>{dom}</NavLink> : dom)}
      onMenuHeaderClick={() => void router.navigate({ href: "/" })}
      menuHeaderRender={hasBrand ? () => <Slot name="brand" /> : undefined}
      headerContentRender={layout === "side" ? () => <Crumbs /> : undefined}
      actionsRender={() => [
        <Slot key="search" name="search" />,
        <Slot key="actions" name="actions" />,
        <Slot key="tenant" name="tenant" />,
      ]}
      avatarProps={hasUser ? { render: () => <Slot name="user" /> } : undefined}
      footerRender={() => <Slot name="footer" />}
      contentStyle={{ padding: tabs ? 0 : undefined }}
      ErrorBoundary={false}
    >
      <Content tabs={tabs} />
    </ProLayout>
  )
}

function toMenuItems(items: readonly MenuItem[], icons: Record<string, ReactNode>): MenuProps["items"] {
  return items.map((m) =>
    m.children
      ? {
          key: m.id,
          icon: m.icon ? icons[m.icon] : undefined,
          label: m.title,
          children: toMenuItems(m.children, icons),
        }
      : {
          key: m.id,
          icon: m.icon ? icons[m.icon] : undefined,
          label: (
            <NavLink to={m.path}>{m.badge ? `${m.title} (${m.badge === true ? "•" : m.badge})` : m.title}</NavLink>
          ),
        },
  )
}

/** 顶栏：面包屑在左，插槽在右。 */
function Bar({ left }: { left?: ReactNode }): ReactNode {
  const { token } = theme.useToken()
  return (
    <Layout.Header
      style={{
        display: "flex",
        alignItems: "center",
        gap: 12,
        padding: "0 16px",
        background: token.colorBgContainer,
        borderBottom: `1px solid ${token.colorBorderSecondary}`,
      }}
    >
      {left ?? <Crumbs />}
      <Space style={{ marginLeft: "auto" }}>
        <Slot name="search" />
        <Slot name="actions" />
        <Slot name="tenant" />
        <Slot name="user" />
      </Space>
    </Layout.Header>
  )
}

/** RailLayout 的选项。 */
export interface RailLayoutProps {
  /** 图标栏顶部的标志。 */
  logo?: ReactNode
  tabs?: boolean | TabsOutletProps
}

/**
 * 图标栏 + 二级面板：最左侧的窄图标栏切换第一级菜单（模块），旁边是模块内的菜单。适合客服、IM、项目管理这类工作台。
 * 二级面板的宽度保存在布局状态中。
 */
export function RailLayout({ logo, tabs }: RailLayoutProps): ReactNode {
  const nav = useNavigation()
  const { icons } = useAdmin()
  const router = useRouter()
  const [state] = useLayoutState()
  const { token } = theme.useToken()
  return (
    <Layout style={{ minHeight: "100vh" }}>
      <Layout.Sider width={64} theme="light" style={{ borderRight: `1px solid ${token.colorBorderSecondary}` }}>
        <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 8, padding: "12px 0" }}>
          {logo}
          {nav.modules.map((m) => (
            <Tooltip key={m.id} title={m.title} placement="right">
              <Button
                type={nav.module?.id === m.id ? "primary" : "text"}
                icon={(m.icon && icons[m.icon]) || <span>{m.title.slice(0, 1)}</span>}
                aria-label={m.title}
                onClick={() => void router.navigate({ href: pageUnder(nav.menu, m.path) ?? m.path })}
              />
            </Tooltip>
          ))}
        </div>
      </Layout.Sider>
      {nav.secondary.length > 0 && (
        <Layout.Sider
          width={state.secondaryWidth ?? 220}
          theme="light"
          style={{ borderRight: `1px solid ${token.colorBorderSecondary}` }}
        >
          <Menu
            mode="inline"
            selectedKeys={nav.active}
            items={toMenuItems(nav.secondary, icons)}
            style={{ borderInlineEnd: 0 }}
          />
        </Layout.Sider>
      )}
      <Layout>
        <Bar />
        <Layout.Content style={{ padding: tabs ? 0 : 16 }}>
          <Content tabs={tabs} />
        </Layout.Content>
      </Layout>
    </Layout>
  )
}

/** CenteredLayout 的选项。 */
export interface CenteredLayoutProps {
  title?: ReactNode
  logo?: ReactNode
  /** 内容的最大宽度（像素），默认 720。 */
  width?: number
  children?: ReactNode
}

/**
 * 居中窄栏：没有侧栏，内容限宽居中，适合简单的用户端、个人中心。顶栏左侧是 brand 插槽（默认是 logo 和 title），
 * 右侧是 actions 和 user 插槽。children 默认是 Outlet。
 */
export function CenteredLayout({ title, logo, width = 720, children }: CenteredLayoutProps): ReactNode {
  const { token } = theme.useToken()
  const hasBrand = useSlotFilled("brand")
  const column = { width: "100%", maxWidth: width, margin: "0 auto", padding: "0 16px" }
  return (
    <Layout style={{ minHeight: "100vh" }}>
      <Layout.Header
        style={{
          padding: 0,
          background: token.colorBgContainer,
          borderBottom: `1px solid ${token.colorBorderSecondary}`,
        }}
      >
        <div style={{ ...column, display: "flex", alignItems: "center", gap: 12, height: "100%" }}>
          {hasBrand ? (
            <Slot name="brand" />
          ) : (
            <Space>
              {logo}
              <span style={{ fontWeight: 600 }}>{title}</span>
            </Space>
          )}
          <Space style={{ marginLeft: "auto" }}>
            <Slot name="actions" />
            <Slot name="user" />
          </Space>
        </div>
      </Layout.Header>
      <Layout.Content style={{ ...column, paddingTop: 24, paddingBottom: 24 }}>{children ?? <Outlet />}</Layout.Content>
      <Slot name="footer" />
    </Layout>
  )
}

/**
 * 设置类页面：页面内左侧是二级导航，右侧是内容。用作 /settings 这类布局路由的 component；
 * items 默认是当前第一级菜单下的菜单。
 */
export function SettingsLayout({ items }: { items?: MenuItem[] }): ReactNode {
  const nav = useNavigation()
  const { icons } = useAdmin()
  return (
    <Card styles={{ body: { display: "flex", gap: 24, padding: 0 } }}>
      <Menu
        mode="inline"
        selectedKeys={nav.active}
        items={toMenuItems(items ?? nav.secondary, icons)}
        style={{ width: 200, flex: "none" }}
      />
      <div style={{ flex: 1, minWidth: 0, padding: 24 }}>
        <Outlet />
      </div>
    </Card>
  )
}

/**
 * 全屏：只保留返回和操作入口，适合编辑器、画布、数据大屏。back 是返回的地址，默认是浏览器的上一页。
 */
export function FullscreenLayout({ back }: { back?: string }): ReactNode {
  const router = useRouter()
  const { crumbs } = useNavigation()
  return (
    <Layout style={{ minHeight: "100vh" }}>
      <Bar
        left={
          <Space>
            <Button
              type="text"
              icon={<ArrowLeftOutlined />}
              aria-label="返回"
              onClick={() => (back ? void router.navigate({ href: back }) : router.history.back())}
            />
            <span>{crumbs.at(-1)?.title}</span>
          </Space>
        }
      />
      <Layout.Content>
        <Outlet />
      </Layout.Content>
    </Layout>
  )
}

/** AuthLayout 的选项。 */
export interface AuthLayoutProps {
  title: string
  logo?: ReactNode
  /** 左侧的品牌区（例如插图）：有它时左右分栏，否则卡片居中。 */
  side?: ReactNode
  children?: ReactNode
}

/** 登录、注册、找回密码等页面的布局。children 默认是 Outlet。 */
export function AuthLayout({ title, logo, side, children }: AuthLayoutProps): ReactNode {
  const { token } = theme.useToken()
  const card = (
    <Card style={{ width: 380, maxWidth: "100%" }}>
      <Space direction="vertical" size="large" style={{ width: "100%" }}>
        <Space>
          {logo}
          <span style={{ fontSize: token.fontSizeHeading4, fontWeight: 600 }}>{title}</span>
        </Space>
        {children ?? <Outlet />}
      </Space>
    </Card>
  )
  return (
    <Layout style={{ minHeight: "100vh", flexDirection: side ? "row" : "column" }}>
      {side && <div style={{ flex: 1, background: token.colorPrimary }}>{side}</div>}
      <div
        style={{
          flex: side ? "0 0 480px" : 1,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          padding: 24,
        }}
      >
        {card}
      </div>
    </Layout>
  )
}

/** 布局状态中的侧栏开关按钮，供自定义的布局使用。 */
export function CollapseButton(): ReactNode {
  const [state, setState] = useLayoutState()
  return (
    <Button
      type="text"
      aria-label={state.collapsed ? "展开侧边栏" : "收起侧边栏"}
      icon={state.collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
      onClick={() => setState({ collapsed: !state.collapsed })}
    />
  )
}
