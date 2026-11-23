// 标签栏：多标签（tabs.tsx）的外观，FrameLayout 把它放在顶栏中间。
//   - 标签上没有关闭按钮：用右键菜单关闭，中键点击也能关闭；固定的标签显示图钉，有未保存修改的显示圆点。
//   - 右键菜单：关闭、固定、重新加载、关闭其他标签页、关闭右侧标签页。
//   - 拖动排序（dnd-kit）：固定和未固定的标签各是一组，只能在组内拖动。
//   - 放不下时两端出现箭头，点击向那一侧滚动，被挡住的一端渐隐；切换时当前标签滚动到可见的位置。
//   - 最后一个标签右边是刷新按钮，重新加载当前页面。

import { closestCenter, DndContext, type DragEndEvent, PointerSensor, useSensor, useSensors } from "@dnd-kit/core"
import { restrictToHorizontalAxis, restrictToParentElement } from "@dnd-kit/modifiers"
import { horizontalListSortingStrategy, SortableContext, useSortable } from "@dnd-kit/sortable"
import { CSS } from "@dnd-kit/utilities"
import { ActionIcon, Menu, UnstyledButton } from "@mantine/core"
import {
  IconArrowBarToRight,
  IconChevronLeft,
  IconChevronRight,
  IconPin,
  IconPinnedOff,
  IconRefresh,
  IconSquareX,
  IconX,
} from "@tabler/icons-react"
import { useQueryClient } from "@tanstack/react-query"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { useText } from "../../i18n/i18n"
import { Icon } from "../../theme/icons"
import { reloadWithProgress, useContentTransition } from "./progress"
import { type Tabs, type TabView, useTabs } from "./tabs"

/** 标签栏，FrameLayout 开启多标签时显示在顶栏中间。 */
export function TabBar(): ReactNode {
  const tabs = useTabs()
  const t = useText()
  const scroller = useRef<HTMLDivElement>(null)
  // 拖动 6px 以上才算拖动，点击、中键照常。
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }))
  const [menu, setMenu] = useState<{ key: string; x: number; y: number } | null>(null)
  const queryClient = useQueryClient()
  const transition = useContentTransition()
  // 重新加载（刷新按钮、右键菜单）：页面顶部的进度条走起来，主内容区进入轻磨砂加载态，请求完成后收起。
  const reload = (key: string) => void reloadWithProgress(() => tabs.refresh(key), queryClient, transition)
  const activeKey = tabs.active?.key

  useEffect(() => {
    if (!activeKey) return
    scroller.current
      ?.querySelector<HTMLElement>("[data-active]")
      ?.scrollIntoView?.({ block: "nearest", inline: "nearest" })
  }, [activeKey])

  // 放不下时：两端显示箭头，点击向那一侧滚动；还有被挡住的标签的一端渐隐，那一侧的箭头可以点。
  // 按第一个和最后一个标签是否完整可见来判断：选中标签外翻的底角会多出几像素的可滚动宽度，不能用 scrollWidth。
  const [edges, setEdges] = useState({ left: false, right: false })
  const count = tabs.tabs.length
  // biome-ignore lint/correctness/useExhaustiveDependencies: 标签增减时重新观察
  useEffect(() => {
    const el = scroller.current
    if (!el) return
    const update = () => {
      const all = el.querySelectorAll<HTMLElement>("[role=tab]")
      const box = el.getBoundingClientRect()
      const left = all.length > 0 && all[0].getBoundingClientRect().left < box.left - 1
      const right = all.length > 0 && all[all.length - 1].getBoundingClientRect().right > box.right + 1
      setEdges((e) => (e.left === left && e.right === right ? e : { left, right }))
    }
    update()
    el.addEventListener("scroll", update, { passive: true })
    if (typeof ResizeObserver === "undefined") return () => el.removeEventListener("scroll", update)
    const ro = new ResizeObserver(update)
    ro.observe(el)
    for (const child of el.children) ro.observe(child)
    return () => {
      el.removeEventListener("scroll", update)
      ro.disconnect()
    }
  }, [count])
  const overflow = edges.left || edges.right
  const scrollBy = (dir: -1 | 1) => {
    const el = scroller.current
    el?.scrollBy?.({ left: dir * Math.max(120, el.clientWidth * 0.7), behavior: "smooth" })
  }

  if (count === 0) return <div className="tn-tabbar" />

  // 竖线画在标签的左侧：前面还有标签，并且这两个标签都没有选中。按整行的顺序计算，所以跨组的也有。
  const separated = (i: number) => i > 0 && !tabs.tabs[i].active && !tabs.tabs[i - 1].active
  const onDragEnd = ({ active, over }: DragEndEvent) => {
    if (over && active.id !== over.id) tabs.move(String(active.id), String(over.id))
  }
  // 两组各自一个拖动区域：拖动被限制在所在的组内，固定和未固定的标签不能互换位置。
  const group = (list: TabView[]) =>
    list.length > 0 && (
      <DndContext
        sensors={sensors}
        collisionDetection={closestCenter}
        modifiers={[restrictToHorizontalAxis, restrictToParentElement]}
        onDragEnd={onDragEnd}
      >
        <SortableContext items={list.map((t) => t.key)} strategy={horizontalListSortingStrategy}>
          <div className="tn-tab-group">
            {list.map((t) => (
              <TabItem
                key={t.key}
                tab={t}
                tabs={tabs}
                separator={separated(tabs.tabs.indexOf(t))}
                onMenu={(x, y) => setMenu({ key: t.key, x, y })}
              />
            ))}
          </div>
        </SortableContext>
      </DndContext>
    )
  const target = tabs.tabs.find((t) => t.key === menu?.key)

  return (
    <div className="tn-tabbar">
      {/* 箭头和标签站在同一条底线上，看起来是一个整体。 */}
      <div className="tn-tab-strip">
        {overflow && (
          <UnstyledButton
            className="tn-tab-arrow"
            aria-label={t("tenon.tabs.scroll_left")}
            disabled={!edges.left}
            onClick={() => scrollBy(-1)}
          >
            <IconChevronLeft size={15} stroke={1.8} />
          </UnstyledButton>
        )}
        <div
          className="tn-tab-scroller"
          ref={scroller}
          role="tablist"
          aria-label={t("tenon.tabs.opened")}
          data-fade-left={edges.left || undefined}
          data-fade-right={edges.right || undefined}
        >
          {group(tabs.tabs.filter((t) => t.pinned))}
          {group(tabs.tabs.filter((t) => !t.pinned))}
        </div>
        {overflow && (
          <UnstyledButton
            className="tn-tab-arrow"
            aria-label={t("tenon.tabs.scroll_right")}
            disabled={!edges.right}
            onClick={() => scrollBy(1)}
          >
            <IconChevronRight size={15} stroke={1.8} />
          </UnstyledButton>
        )}
      </div>
      <ActionIcon
        size={32}
        className="tn-tab-reload"
        aria-label={t("tenon.tabs.reload_current")}
        disabled={!activeKey}
        onClick={() => activeKey && reload(activeKey)}
      >
        <IconRefresh size={16} stroke={1.6} />
      </ActionIcon>
      <Menu
        opened={Boolean(target)}
        onChange={(o) => !o && setMenu(null)}
        position="bottom-start"
        offset={2}
        width={190}
      >
        <Menu.Target>
          <div style={{ position: "fixed", left: menu?.x ?? 0, top: menu?.y ?? 0, width: 1, height: 1 }} />
        </Menu.Target>
        {target && <TabMenu tab={target} tabs={tabs} reload={reload} />}
      </Menu>
    </div>
  )
}

/** 标签的右键菜单。 */
function TabMenu({ tab, tabs, reload }: { tab: TabView; tabs: Tabs; reload: (key: string) => void }): ReactNode {
  const t = useText()
  const index = tabs.tabs.indexOf(tab)
  const others = tabs.tabs.some((o) => o.key !== tab.key && !o.pinned)
  const right = tabs.tabs.slice(index + 1).some((o) => !o.pinned)
  const icon = (C: typeof IconX) => <C size={15} stroke={1.6} />
  return (
    <Menu.Dropdown>
      <Menu.Item leftSection={icon(IconX)} disabled={!tab.closable} onClick={() => void tabs.close(tab.key)}>
        {t("tenon.common.close")}
      </Menu.Item>
      <Menu.Item
        leftSection={icon(tab.pinned ? IconPinnedOff : IconPin)}
        onClick={() => (tab.pinned ? tabs.unpin(tab.key) : tabs.pin(tab.key))}
      >
        {tab.pinned ? t("tenon.tabs.unpin") : t("tenon.tabs.pin")}
      </Menu.Item>
      <Menu.Item leftSection={icon(IconRefresh)} onClick={() => reload(tab.key)}>
        {t("tenon.tabs.reload")}
      </Menu.Item>
      <Menu.Divider />
      <Menu.Item leftSection={icon(IconSquareX)} disabled={!others} onClick={() => void tabs.closeOthers(tab.key)}>
        {t("tenon.tabs.close_others")}
      </Menu.Item>
      <Menu.Item
        leftSection={icon(IconArrowBarToRight)}
        disabled={!right}
        onClick={() => void tabs.closeRight(tab.key)}
      >
        {t("tenon.tabs.close_right")}
      </Menu.Item>
    </Menu.Dropdown>
  )
}

function TabItem({
  tab,
  tabs,
  separator,
  onMenu,
}: {
  tab: TabView
  tabs: Tabs
  separator: boolean
  onMenu: (x: number, y: number) => void
}): ReactNode {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: tab.key })
  const t = useText()
  return (
    <UnstyledButton
      ref={setNodeRef}
      style={{ transform: CSS.Translate.toString(transform), transition }}
      {...attributes}
      {...listeners}
      role="tab"
      aria-selected={tab.active}
      className="tn-tab"
      data-active={tab.active || undefined}
      data-pinned={tab.pinned || undefined}
      data-dirty={tab.dirty || undefined}
      data-dragging={isDragging || undefined}
      data-separator={separator || undefined}
      onClick={() => void tabs.activate(tab.key)}
      onAuxClick={(e) => {
        if (e.button === 1) {
          e.preventDefault()
          if (tab.closable) void tabs.close(tab.key)
        }
      }}
      onContextMenu={(e) => {
        e.preventDefault()
        onMenu(e.clientX, e.clientY)
      }}
    >
      {tab.icon && <Icon name={tab.icon} size={15} stroke={1.6} />}
      <span className="tn-tab-title">{tab.title}</span>
      {tab.dirty && <span className="tn-tab-dot" role="img" aria-label={t("tenon.tabs.unsaved")} />}
      {tab.pinned && <IconPin size={12} stroke={1.6} className="tn-tab-pin" aria-hidden />}
    </UnstyledButton>
  )
}
