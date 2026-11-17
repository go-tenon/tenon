// 标签栏：@go-tenon/shell 的多标签的外观。点击切换，中键或关闭按钮关闭，右键菜单有刷新、固定和批量关闭，可以拖动排序。

import { type TabView, useTabs } from "@go-tenon/shell"
import { CloseButton, Menu } from "@mantine/core"
import { IconPin } from "@tabler/icons-react"
import { type ReactNode, useState } from "react"
import { Icon } from "./icons"

/** 标签栏，AppLayout 开启 tabs 时显示在内容区顶部。 */
export function TabBar(): ReactNode {
  const tabs = useTabs()
  const [menu, setMenu] = useState<{ key: string; x: number; y: number } | null>(null)
  const [dragging, setDragging] = useState<string | null>(null)
  if (tabs.tabs.length === 0) return null
  const target = tabs.tabs.find((t) => t.key === menu?.key)
  return (
    <div className="tn-tabbar" role="tablist" aria-label="已打开的页面">
      {tabs.tabs.map((t) => (
        <Tab
          key={t.key}
          tab={t}
          onActivate={() => void tabs.activate(t.key)}
          onClose={() => void tabs.close(t.key)}
          onMenu={(x, y) => setMenu({ key: t.key, x, y })}
          onDragStart={() => setDragging(t.key)}
          onDrop={() => {
            if (dragging && dragging !== t.key) tabs.move(dragging, t.key)
            setDragging(null)
          }}
        />
      ))}
      <Menu opened={Boolean(target)} onChange={(o) => !o && setMenu(null)} position="bottom-start" offset={2}>
        <Menu.Target>
          <div style={{ position: "fixed", left: menu?.x ?? 0, top: menu?.y ?? 0, width: 1, height: 1 }} />
        </Menu.Target>
        {target && (
          <Menu.Dropdown>
            <Menu.Item onClick={() => void tabs.refresh(target.key)}>刷新</Menu.Item>
            <Menu.Item onClick={() => (target.pinned ? tabs.unpin(target.key) : tabs.pin(target.key))}>
              {target.pinned ? "取消固定" : "固定"}
            </Menu.Item>
            <Menu.Divider />
            <Menu.Item disabled={!target.closable} onClick={() => void tabs.close(target.key)}>
              关闭
            </Menu.Item>
            <Menu.Item onClick={() => void tabs.closeOthers(target.key)}>关闭其他</Menu.Item>
            <Menu.Item onClick={() => void tabs.closeRight(target.key)}>关闭右侧</Menu.Item>
            <Menu.Item onClick={() => void tabs.closeAll()}>全部关闭</Menu.Item>
          </Menu.Dropdown>
        )}
      </Menu>
    </div>
  )
}

function Tab(props: {
  tab: TabView
  onActivate: () => void
  onClose: () => void
  onMenu: (x: number, y: number) => void
  onDragStart: () => void
  onDrop: () => void
}): ReactNode {
  const { tab } = props
  return (
    <div
      className="tn-tab"
      role="tab"
      tabIndex={0}
      aria-selected={tab.active}
      data-active={tab.active || undefined}
      title={tab.title}
      draggable
      onClick={props.onActivate}
      onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && props.onActivate()}
      onAuxClick={(e) => e.button === 1 && tab.closable && props.onClose()}
      onContextMenu={(e) => {
        e.preventDefault()
        props.onMenu(e.clientX, e.clientY)
      }}
      onDragStart={props.onDragStart}
      onDragOver={(e) => e.preventDefault()}
      onDrop={props.onDrop}
    >
      {tab.icon && <Icon name={tab.icon} size={14} />}
      <span className="tn-tab-title">{tab.title}</span>
      {tab.dirty && <span className="tn-tab-dot" title="未保存" />}
      {tab.pinned ? (
        <IconPin size={12} stroke={1.8} style={{ opacity: 0.6 }} />
      ) : (
        tab.closable && (
          <CloseButton
            size="xs"
            aria-label={`关闭 ${tab.title}`}
            onClick={(e) => {
              e.stopPropagation()
              props.onClose()
            }}
          />
        )
      )}
    </div>
  )
}
