// 标签栏：@go-tenon/shell 多标签模型（useTabs）的 antd 外观。右键菜单和关闭按钮共用一组操作，拖动排序用浏览器原生的拖放。

import { PushpinOutlined } from "@ant-design/icons"
import { type TabView, useTabs } from "@go-tenon/shell"
import { Badge, Dropdown, type MenuProps, Tabs, theme } from "antd"
import { type ReactNode, useState } from "react"
import { useAdmin } from "./provider"

/** 一个标签的右键菜单。 */
function tabMenu(tab: TabView, tabs: ReturnType<typeof useTabs>): MenuProps {
  const index = tabs.tabs.findIndex((t) => t.key === tab.key)
  const items: MenuProps["items"] = [
    { key: "refresh", label: "刷新" },
    { key: tab.pinned ? "unpin" : "pin", label: tab.pinned ? "取消固定" : "固定" },
    { type: "divider" },
    { key: "close", label: "关闭", disabled: !tab.closable },
    { key: "closeOthers", label: "关闭其他", disabled: tabs.tabs.length <= 1 },
    { key: "closeRight", label: "关闭右侧", disabled: index === tabs.tabs.length - 1 },
    { key: "closeAll", label: "关闭全部" },
  ]
  return {
    items,
    onClick: ({ key }) => {
      if (key === "refresh") void tabs.refresh(tab.key)
      else if (key === "pin") tabs.pin(tab.key)
      else if (key === "unpin") tabs.unpin(tab.key)
      else if (key === "close") void tabs.close(tab.key)
      else if (key === "closeOthers") void tabs.closeOthers(tab.key)
      else if (key === "closeRight") void tabs.closeRight(tab.key)
      else if (key === "closeAll") void tabs.closeAll()
    },
  }
}

/** 多标签的标签栏，放在页面区域的上方（AdminLayout 的 tabs 开启时自动放置）。 */
export function TabBar(): ReactNode {
  const tabs = useTabs()
  const { icons } = useAdmin()
  const { token } = theme.useToken()
  const [dragging, setDragging] = useState<string>()
  if (tabs.tabs.length === 0) return null

  const label = (tab: TabView) => (
    <Dropdown menu={tabMenu(tab, tabs)} trigger={["contextMenu"]}>
      {/* biome-ignore lint/a11y/noStaticElementInteractions: 拖动排序是鼠标的快捷方式；标签本身由 antd 提供 role="tab"，各项操作也在右键菜单中 */}
      <span
        draggable
        data-tab-key={tab.key}
        onDragStart={() => setDragging(tab.key)}
        onDragOver={(e) => e.preventDefault()}
        onDrop={() => {
          if (dragging && dragging !== tab.key) tabs.move(dragging, tab.key)
          setDragging(undefined)
        }}
        style={{ display: "inline-flex", alignItems: "center", gap: 6 }}
      >
        {tab.pinned && <PushpinOutlined />}
        {tab.icon && icons[tab.icon]}
        {tab.title}
        {tab.dirty && <Badge status="warning" title="有未保存的修改" />}
      </span>
    </Dropdown>
  )

  return (
    <Tabs
      type="editable-card"
      hideAdd
      size="small"
      activeKey={tabs.active?.key}
      onChange={(key) => void tabs.activate(key)}
      onEdit={(key, action) => {
        if (action === "remove" && typeof key === "string") void tabs.close(key)
      }}
      items={tabs.tabs.map((tab) => ({ key: tab.key, label: label(tab), closable: tab.closable }))}
      tabBarStyle={{ margin: 0, padding: "6px 12px 0", background: token.colorBgContainer }}
    />
  )
}
