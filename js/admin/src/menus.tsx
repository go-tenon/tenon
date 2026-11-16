// 顶栏上的部件：用户菜单、租户切换、新版本提示。

import { CheckOutlined, LogoutOutlined, MoonOutlined, SwapOutlined, UserOutlined } from "@ant-design/icons"
import { confirmDiscard, useApp, useNewVersion, useTenant } from "@go-tenon/shell"
import { Avatar, Dropdown, type MenuProps, Modal, Space } from "antd"
import type { ReactNode } from "react"
import { type ColorMode, useAdmin } from "./provider"

/** UserMenu 的选项。 */
export interface UserMenuProps {
  /** 显示的名称，例如当前用户的姓名。 */
  name?: ReactNode
  avatar?: string
  /** 调用应用的登出接口（服务端撤销这次登录、清除 cookie）。之后清除会话，受保护的页面随之跳到登录页。 */
  logout?: () => Promise<unknown>
  /** 菜单中额外的项，排在颜色模式之前。 */
  items?: MenuProps["items"]
}

const modes: { key: ColorMode; label: string }[] = [
  { key: "system", label: "跟随系统" },
  { key: "light", label: "亮色" },
  { key: "dark", label: "暗色" },
]

/**
 * 用户菜单：头像和名称，下拉中是颜色模式和退出。退出前，标签中有未保存的修改时先询问。
 * 放在 user 插槽：<Fill name="user"><UserMenu name={me.name} logout={() => api.auth.logout()} /></Fill>
 */
export function UserMenu({ name, avatar, logout, items = [] }: UserMenuProps): ReactNode {
  const app = useApp()
  const { mode, setMode } = useAdmin()
  const signOut = async () => {
    if (!(await confirmDiscard(app))) return
    try {
      await logout?.()
    } finally {
      await app.session.signOut()
    }
  }
  const menu: MenuProps = {
    items: [
      ...items,
      {
        type: "group",
        label: "颜色模式",
        children: modes.map((m) => ({
          key: `mode:${m.key}`,
          label: m.label,
          icon: mode === m.key ? <CheckOutlined /> : <MoonOutlined style={{ visibility: "hidden" }} />,
        })),
      },
      { type: "divider" },
      { key: "logout", icon: <LogoutOutlined />, label: "退出登录", danger: true },
    ],
    onClick: ({ key }) => {
      if (key.startsWith("mode:")) setMode(key.slice(5) as ColorMode)
      else if (key === "logout") void signOut()
    },
  }
  return (
    <Dropdown menu={menu} placement="bottomRight">
      <Space style={{ cursor: "pointer" }} data-testid="user-menu">
        <Avatar size="small" src={avatar} icon={<UserOutlined />} />
        {name}
      </Space>
    </Dropdown>
  )
}

/** 租户切换：当前租户和全部租户，见 createApp 的 tenancy。放在 tenant 插槽。只有一个租户时只显示名称。 */
export function TenantSwitcher(): ReactNode {
  const { tenant, tenants, current, switchTo } = useTenant()
  if (!current) return null
  const label = tenant?.name ?? current
  if (tenants.length <= 1) return <span>{label}</span>
  return (
    <Dropdown
      menu={{
        selectedKeys: [current],
        items: tenants.map((t) => ({ key: t.id, label: t.name })),
        onClick: ({ key }) => void switchTo(key),
      }}
    >
      <Space style={{ cursor: "pointer" }} data-testid="tenant-switcher">
        <SwapOutlined />
        {label}
      </Space>
    </Dropdown>
  )
}

/**
 * 新版本提示：发布新版本后，提示打开着的页面刷新（见 @go-tenon/shell 的 useNewVersion）。放在根路由中一次。
 * 版本号和地址默认取自 tenonVersion 插件注入的值。
 */
export function NewVersionNotice(props: Parameters<typeof useNewVersion>[0] = {}): ReactNode {
  const { available, reload, dismiss } = useNewVersion(props)
  return (
    <Modal open={available} title="有新版本" okText="刷新" cancelText="稍后" onOk={reload} onCancel={dismiss} centered>
      系统已经更新。先保存手头的修改，再刷新页面使用新版本。
    </Modal>
  )
}
