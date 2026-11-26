// 多栏面板（Pane）：供 RailLayout / FrameLayout 下 staticData.fullBleed 的多栏工作台页面使用。
// 提供与 --tn-rail-header（默认 52px）齐平的顶栏（PaneHeader）、带紧凑控件密度的工具条（PaneToolbar）、
// 独立纵向滚动的正文区（PaneBody）以及支持鼠标拖拽、键盘微调和按账号持久化的栏宽调节（resizable）。

import { ActionIcon, Box, Drawer, Group, Stack, Text, Tooltip } from "@mantine/core"
import { IconChevronLeft, IconLayoutSidebarLeftExpand } from "@tabler/icons-react"
import {
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react"
import { useOptionalApp } from "../app/app"
import { useCollapseNav, useNarrow, useNavControl } from "../app/layout"
import { noAccount } from "../app/snapshot"
import { LoadingOverlay } from "../components/loading"
import { useText } from "../i18n/i18n"

const noopSubscribe = () => () => {}

/** 可拖拽调节栏宽的配置。 */
export interface PaneResizeOptions {
  /** 持久化键：传入时通过 app.storage 按账号保存栏宽。 */
  storageKey?: string
  /** 默认宽度（像素）。 */
  defaultWidth: number
  /** 最小宽度（像素）。 */
  min: number
  /** 最大宽度（像素）。 */
  max: number
  /** 拖拽手柄贴在面板的左侧还是右侧。 */
  side: "left" | "right"
}

/** useResizableWidth 的返回值。 */
export interface ResizableWidth {
  width: number
  min: number
  max: number
  dragging: boolean
  onPointerDown: (e: ReactPointerEvent<HTMLDivElement>) => void
  onDoubleClick: () => void
  onKeyDown: (e: ReactKeyboardEvent<HTMLDivElement>) => void
}

/**
 * 管理可拖拽与键盘方向键微调的栏宽（像素）。传入 storageKey 且处于 AppRoot 内时，
 * 按账号持久化到 app.storage；双击分隔手柄或按 Home 恢复默认宽度。
 */
export function useResizableWidth({ storageKey, defaultWidth, min, max, side }: PaneResizeOptions): ResizableWidth {
  const app = useOptionalApp()
  const account = useSyncExternalStore(
    app ? app.session.subscribe : noopSubscribe,
    () => app?.session.identity()?.account ?? "",
    noAccount,
  )
  const fullKey = app && storageKey ? `pane-width:${account}:${storageKey}` : undefined
  const clamp = useCallback((v: number) => Math.min(max, Math.max(min, Math.round(v))), [min, max])

  const readSaved = useCallback((): number => {
    if (!fullKey || !app) return defaultWidth
    const saved = app.storage.get<number>(fullKey)
    return typeof saved === "number" && Number.isFinite(saved) && saved >= min && saved <= max ? saved : defaultWidth
  }, [app, defaultWidth, fullKey, max, min])

  const [width, setWidth] = useState<number>(readSaved)
  const [dragging, setDragging] = useState(false)
  const latest = useRef(width)
  latest.current = width

  // 账号或 storageKey 变化时重新读取对应宽度。
  useEffect(() => {
    setWidth(readSaved())
  }, [readSaved])

  const persist = useCallback(
    (next: number) => {
      if (fullKey && app) app.storage.set(fullKey, next)
    },
    [app, fullKey],
  )

  const onPointerDown = useCallback(
    (e: ReactPointerEvent<HTMLDivElement>) => {
      if (e.button !== 0) return
      e.preventDefault()
      const startX = e.clientX
      const startW = latest.current
      setDragging(true)
      document.body.style.cursor = "col-resize"
      document.body.style.userSelect = "none"

      const onMove = (ev: PointerEvent) => {
        const delta = side === "right" ? ev.clientX - startX : startX - ev.clientX
        setWidth(clamp(startW + delta))
      }
      const onUp = () => {
        setDragging(false)
        document.body.style.cursor = ""
        document.body.style.userSelect = ""
        window.removeEventListener("pointermove", onMove)
        window.removeEventListener("pointerup", onUp)
        persist(latest.current)
      }
      window.addEventListener("pointermove", onMove)
      window.addEventListener("pointerup", onUp)
    },
    [clamp, persist, side],
  )

  const onDoubleClick = useCallback(() => {
    setWidth(defaultWidth)
    if (fullKey && app) app.storage.remove(fullKey)
  }, [app, defaultWidth, fullKey])

  const onKeyDown = useCallback(
    (e: ReactKeyboardEvent<HTMLDivElement>) => {
      if (e.key === "Home") {
        e.preventDefault()
        onDoubleClick()
        return
      }
      if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return
      e.preventDefault()
      const step = e.shiftKey ? 48 : 16
      const dir = e.key === "ArrowRight" ? 1 : -1
      const delta = side === "right" ? dir * step : -dir * step
      const next = clamp(latest.current + delta)
      setWidth(next)
      persist(next)
    },
    [clamp, onDoubleClick, persist, side],
  )

  // 组件卸载时恢复全局指针样式。
  useEffect(
    () => () => {
      document.body.style.cursor = ""
      document.body.style.userSelect = ""
    },
    [],
  )

  return { width, min, max, dragging, onPointerDown, onDoubleClick, onKeyDown }
}

/** Pane 的选项。 */
export interface PaneProps {
  /** 固定宽度（传入 resizable 时忽略，由可调宽状态接管）；不传时自适应占满剩余空间。 */
  width?: number | string
  /** 面板的边线位置，默认 none。 */
  border?: "left" | "right" | "both" | "none"
  /** 开启可拖拽与键盘微调栏宽；为 false 或不传时不可拖拽。 */
  resizable?: PaneResizeOptions | false
  className?: string
  style?: CSSProperties
  children: ReactNode
}

/** 多栏工作台中的单个面板：纵向 flex 容器，支持固定宽度、弹性占满、边线分隔与可拖拽调宽。 */
export function Pane({ width, border = "none", resizable, className, style, children }: PaneProps): ReactNode {
  if (resizable) {
    return (
      <ResizablePane border={border} resizable={resizable} className={className} style={style}>
        {children}
      </ResizablePane>
    )
  }
  const sized = width !== undefined
  return (
    <Box
      className={className ? `tn-pane ${className}` : "tn-pane"}
      data-border={border === "none" ? undefined : border}
      style={{
        ...(sized ? { width, flex: "none" } : { flex: 1 }),
        ...style,
      }}
    >
      {children}
    </Box>
  )
}

/** 带可拖拽分隔手柄的面板实现：将 hook 调用限定在开启 resizable 的分支内。 */
function ResizablePane({
  border,
  resizable,
  className,
  style,
  children,
}: {
  border: "left" | "right" | "both" | "none"
  resizable: PaneResizeOptions
  className?: string
  style?: CSSProperties
  children: ReactNode
}): ReactNode {
  const t = useText()
  const size = useResizableWidth(resizable)
  const handlePos: CSSProperties = resizable.side === "right" ? { right: -3 } : { left: -3 }
  return (
    <Box
      className={className ? `tn-pane ${className}` : "tn-pane"}
      data-border={border === "none" ? undefined : border}
      style={{ width: size.width, flex: "none", ...style }}
    >
      {children}
      {/* biome-ignore lint/a11y/useSemanticElements: 可拖拽与键盘微调的分栏手柄遵循 ARIA Window Splitter 规范 */}
      <div
        role="separator"
        tabIndex={0}
        aria-orientation="vertical"
        aria-label={t("tenon.pane.resize")}
        aria-valuenow={size.width}
        aria-valuemin={size.min}
        aria-valuemax={size.max}
        className="tn-pane-resize-handle"
        data-dragging={size.dragging || undefined}
        style={handlePos}
        onPointerDown={size.onPointerDown}
        onDoubleClick={size.onDoubleClick}
        onKeyDown={size.onKeyDown}
      />
    </Box>
  )
}

/** PaneHeader 的选项。 */
export interface PaneHeaderProps {
  /** 面板标题。传入 children 时直接渲染 children，忽略结构化插槽。 */
  title?: ReactNode
  /** 标题下方的副标题或元信息行。 */
  subtitle?: ReactNode
  /** 标题左侧的附加内容（例如头像）。 */
  left?: ReactNode
  /** 顶栏右侧的操作区。 */
  actions?: ReactNode
  /** 当布局二级导航收起时在最左侧自动显示展开按钮，默认 false。 */
  showNavToggle?: boolean
  /** 窄屏或内页返回按钮回调。 */
  back?: () => void
  /** 是否锁定高度为 --tn-rail-header（52px），默认 true；窄屏需折行时传 false。 */
  fixed?: boolean
  className?: string
  style?: CSSProperties
  children?: ReactNode
}

/** 面板顶栏：与 RailLayout 的 --tn-rail-header（52px）齐平，统一水平内边距与底边线。 */
export function PaneHeader({
  title,
  subtitle,
  left,
  actions,
  showNavToggle = false,
  back,
  fixed = true,
  className,
  style,
  children,
}: PaneHeaderProps): ReactNode {
  const t = useText()
  const nav = useNavControl()
  const navToggle = showNavToggle && nav && nav.hasNav !== false && !nav.open && (
    <Tooltip label={t("tenon.rail.expand_nav")} openDelay={350}>
      <ActionIcon variant="subtle" color="gray" onClick={nav.openNav} aria-label={t("tenon.rail.expand_nav")}>
        <IconLayoutSidebarLeftExpand size={18} stroke={1.6} />
      </ActionIcon>
    </Tooltip>
  )
  const backButton = back && (
    <ActionIcon variant="subtle" color="gray" onClick={back} aria-label={t("tenon.common.back")}>
      <IconChevronLeft size={18} stroke={1.6} />
    </ActionIcon>
  )
  return (
    <Group
      gap="xs"
      wrap={fixed ? "nowrap" : "wrap"}
      className={className ? `tn-pane-header ${className}` : "tn-pane-header"}
      data-fixed={fixed || undefined}
      style={style}
    >
      {navToggle}
      {backButton}
      {children ?? (
        <>
          {left}
          <Box style={{ flex: 1, minWidth: 0 }}>
            {typeof title === "string" ? (
              <Text fw={600} size="sm" truncate lh={1.25}>
                {title}
              </Text>
            ) : (
              title
            )}
            {subtitle &&
              (typeof subtitle === "string" ? (
                <Text size="xs" c="dimmed" truncate lh={1.25}>
                  {subtitle}
                </Text>
              ) : (
                subtitle
              ))}
          </Box>
          {actions}
        </>
      )}
    </Group>
  )
}

/** PaneToolbar 的选项。 */
export interface PaneToolbarProps {
  /** 子元素纵向间距，默认 8。 */
  gap?: number | string
  className?: string
  style?: CSSProperties
  children: ReactNode
}

/** 面板筛选与搜索工具条：统一浅底分层、水平边距与紧凑控件密度（30px 高、13px 字号）。 */
export function PaneToolbar({ gap = 8, className, style, children }: PaneToolbarProps): ReactNode {
  return (
    <Stack
      gap={gap}
      className={className ? `tn-pane-toolbar ${className}` : "tn-pane-toolbar"}
      data-tn-density="dense"
      style={style}
    >
      {children}
    </Stack>
  )
}

/** PaneBody 的选项。 */
export interface PaneBodyProps {
  /** 是否在当前可见视口内叠加轻磨砂加载覆层与居中转圈。 */
  loading?: boolean
  className?: string
  style?: CSSProperties
  children: ReactNode
}

/** 面板主滚动区：占据顶栏与工具条之外的剩余高度并独立纵向滚动；传入 loading 时在可见视口中央显示加载覆层。 */
export function PaneBody({ loading, className, style, children }: PaneBodyProps): ReactNode {
  const body = (
    <Box className={className ? `tn-pane-body ${className}` : "tn-pane-body"} style={style}>
      {children}
    </Box>
  )
  if (loading === undefined) return body
  return (
    <div className="tn-pane-body-shell" data-loading-active={loading || undefined} aria-busy={loading || undefined}>
      {body}
      <LoadingOverlay visible={loading} />
    </div>
  )
}

/** useAsidePaneState 的选项。 */
export interface AsidePaneStateOptions {
  /** 宽屏下首次访问是否默认展开，默认 false。 */
  defaultOpen?: boolean
  /** 窄屏媒体查询：进入窄屏时自动收起（不覆写宽屏保存的偏好），默认 "(max-width: 48em)"。 */
  compactQuery?: string
}

/** useAsidePaneState 的返回值。 */
export interface AsidePaneState {
  opened: boolean
  open: () => void
  close: () => void
  toggle: () => void
  setOpened: (next: boolean) => void
}

/**
 * 管理右侧详情栏的展开/收起状态：在非窄屏下通过 app.storage 按账号持久化偏好，
 * 切换到窄屏时自动收起以免遮挡主内容区。
 */
export function useAsidePaneState(
  storageKey: string,
  { defaultOpen = false, compactQuery = "(max-width: 48em)" }: AsidePaneStateOptions = {},
): AsidePaneState {
  const app = useOptionalApp()
  const compact = useNarrow(compactQuery)
  const account = useSyncExternalStore(
    app ? app.session.subscribe : noopSubscribe,
    () => app?.session.identity()?.account ?? "",
    noAccount,
  )
  const fullKey = app && storageKey ? `aside-open:${account}:${storageKey}` : undefined

  const readSaved = useCallback((): boolean => {
    if (!fullKey || !app) return defaultOpen
    const saved = app.storage.get<boolean>(fullKey)
    return typeof saved === "boolean" ? saved : defaultOpen
  }, [app, defaultOpen, fullKey])

  const [opened, setOpenedState] = useState<boolean>(() => (compact ? false : readSaved()))

  // 账号或 storageKey 变化时按对应账号的偏好重置
  useEffect(() => {
    setOpenedState(compact ? false : readSaved())
  }, [compact, readSaved])

  const setOpened = useCallback(
    (next: boolean) => {
      setOpenedState(next)
      if (!compact && fullKey && app) {
        app.storage.set(fullKey, next)
      }
    },
    [app, compact, fullKey],
  )

  const open = useCallback(() => setOpened(true), [setOpened])
  const close = useCallback(() => setOpened(false), [setOpened])
  const toggle = useCallback(() => {
    setOpenedState((prev) => {
      const next = !prev
      if (!compact && fullKey && app) {
        app.storage.set(fullKey, next)
      }
      return next
    })
  }, [app, compact, fullKey])

  return { opened, open, close, toggle, setOpened }
}

/** AsidePane 的选项。 */
export interface AsidePaneProps {
  /** 是否展开右侧详情栏。 */
  opened: boolean
  /** 关闭回调（抽屉点遮罩或按 Esc 时触发）。 */
  onClose: () => void
  /** 可拖拽栏宽配置（边线和拖拽手柄固定在左侧）；不传时按 width 固定宽度渲染。 */
  resizable?: Omit<PaneResizeOptions, "side">
  /** 未传 resizable 时的内联宽度及抽屉宽度（像素），默认取 resizable.defaultWidth 或 340。 */
  width?: number
  /** 达到该媒体查询时以内联分栏显示，否则退化为右侧浮层抽屉；默认 "(min-width: 1360px)"。 */
  inlineQuery?: string
  /** 达到该媒体查询时允许与二级导航并排显示；未达到且内联展开时自动临时收起二级导航；默认 "(min-width: 1600px)"。 */
  keepNavQuery?: string
  /** 窄屏媒体查询：窄屏展开时使用全屏宽度抽屉，保留主内容区滚动位置；默认 "(max-width: 48em)"。 */
  compactQuery?: string
  className?: string
  children: ReactNode
}

/**
 * 响应式右侧详情栏：宽屏内联为可拖拽调节宽度的右侧 Pane（中等宽屏自动临时收起二级导航），
 * 中屏退化为右侧浮层 Drawer，窄屏以 100% 宽度覆盖展示。
 */
export function AsidePane({
  opened,
  onClose,
  resizable,
  width,
  inlineQuery = "(min-width: 1360px)",
  keepNavQuery = "(min-width: 1600px)",
  compactQuery = "(max-width: 48em)",
  className,
  children,
}: AsidePaneProps): ReactNode {
  const compact = useNarrow(compactQuery)
  const canInline = useNarrow(inlineQuery)
  const wide = useNarrow(keepNavQuery)
  const inline = opened && !compact && canInline

  useCollapseNav(inline && !wide)

  if (inline) {
    const paneWidth = width ?? resizable?.defaultWidth ?? 340
    return (
      <Pane
        width={resizable ? undefined : paneWidth}
        border="left"
        resizable={resizable ? { ...resizable, side: "left" } : false}
        className={className}
      >
        {children}
      </Pane>
    )
  }

  const drawerSize = compact ? "100%" : (width ?? resizable?.defaultWidth ?? 340)
  return (
    <Drawer
      opened={opened}
      onClose={onClose}
      position="right"
      size={drawerSize}
      padding={0}
      withCloseButton={false}
      transitionProps={compact ? { duration: 0 } : undefined}
    >
      <Box h="100dvh" style={{ display: "flex", flexDirection: "column", minWidth: 0 }}>
        {opened && children}
      </Box>
    </Drawer>
  )
}
