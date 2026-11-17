// 可以拖动调整宽度的分栏：拖动分隔条或用方向键调整，宽度按账号保存在 app.storage 中。
// 外观（分隔条的样子、两侧放什么）由组件库决定，例如 shadcn registry 的 SplitView。

import {
  type KeyboardEvent,
  type PointerEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react"
import { useApp } from "./app"
import { noAccount } from "./snapshot"

/** useResizable 的选项。 */
export interface ResizableOptions {
  /** 区分不同的分栏，宽度按它保存，例如 "articles"。 */
  id: string
  /** 默认宽度（像素）。 */
  defaultSize: number
  /** 最小、最大宽度，默认 160 和 640。 */
  min?: number
  max?: number
  /** 方向键每次调整的像素，默认 16。 */
  step?: number
  /** 可以调整的一栏在分隔条的哪一侧：start（默认，左侧）或 end（右侧）。 */
  side?: "start" | "end"
}

/** 交给分隔条元素的属性：可以聚焦，方向键调整，拖动调整，双击恢复默认宽度。 */
export interface SeparatorProps {
  role: "separator"
  "aria-orientation": "vertical"
  "aria-valuenow": number
  "aria-valuemin": number
  "aria-valuemax": number
  tabIndex: 0
  onPointerDown: (e: PointerEvent<HTMLElement>) => void
  onKeyDown: (e: KeyboardEvent<HTMLElement>) => void
  onDoubleClick: () => void
}

/** useResizable 的返回值。 */
export interface Resizable {
  /** 当前宽度（像素）。 */
  size: number
  /** 正在拖动：外观可以据此禁止选中文字、改变分隔条的样式。 */
  dragging: boolean
  setSize: (size: number) => void
  /** 恢复默认宽度。 */
  reset: () => void
  separatorProps: SeparatorProps
}

/** 把宽度限制在 min 和 max 之间，并取整。 */
export function clampSize(size: number, min: number, max: number): number {
  return Math.round(Math.min(max, Math.max(min, size)))
}

/**
 * 可以调整宽度的一栏。宽度按账号保存在 app.storage 中，换个账号登录各是各的。
 *
 *   const pane = useResizable({ id: "articles", defaultSize: 280 })
 *   <aside style={{ width: pane.size }} /><div {...pane.separatorProps} />
 */
export function useResizable(options: ResizableOptions): Resizable {
  const { id, defaultSize, min = 160, max = 640, step = 16, side = "start" } = options
  const app = useApp()
  const account = useSyncExternalStore(app.session.subscribe, () => app.session.identity()?.account ?? "", noAccount)
  const key = `resize:${account}:${id}`
  const saved = useMemo(() => app.storage.get<number>(key), [app.storage, key])
  const [size, setLocal] = useState(() => clampSize(saved ?? defaultSize, min, max))
  const [dragging, setDragging] = useState(false)
  // 换了账号，读取那个账号保存的宽度。
  useEffect(() => setLocal(clampSize(saved ?? defaultSize, min, max)), [saved, defaultSize, min, max])

  const setSize = useCallback(
    (next: number) => {
      const value = clampSize(next, min, max)
      setLocal(value)
      app.storage.set(key, value)
    },
    [app.storage, key, min, max],
  )
  const sizeRef = useRef(size)
  sizeRef.current = size
  const direction = side === "start" ? 1 : -1

  const onPointerDown = useCallback(
    (e: PointerEvent<HTMLElement>) => {
      if (e.button !== 0) return
      e.preventDefault()
      const startX = e.clientX
      const startSize = sizeRef.current
      let last = startSize
      setDragging(true)
      const move = (ev: globalThis.PointerEvent) => {
        last = clampSize(startSize + (ev.clientX - startX) * direction, min, max)
        setLocal(last)
      }
      const up = () => {
        window.removeEventListener("pointermove", move)
        window.removeEventListener("pointerup", up)
        window.removeEventListener("pointercancel", up)
        setDragging(false)
        app.storage.set(key, last)
      }
      window.addEventListener("pointermove", move)
      window.addEventListener("pointerup", up)
      window.addEventListener("pointercancel", up)
    },
    [app.storage, key, min, max, direction],
  )

  const onKeyDown = useCallback(
    (e: KeyboardEvent<HTMLElement>) => {
      const delta = e.key === "ArrowRight" ? step * direction : e.key === "ArrowLeft" ? -step * direction : undefined
      if (delta !== undefined) setSize(sizeRef.current + delta)
      else if (e.key === "Home") setSize(min)
      else if (e.key === "End") setSize(max)
      else return
      e.preventDefault()
    },
    [setSize, step, direction, min, max],
  )

  const reset = useCallback(() => setSize(defaultSize), [setSize, defaultSize])

  return {
    size,
    dragging,
    setSize,
    reset,
    separatorProps: {
      role: "separator",
      "aria-orientation": "vertical",
      "aria-valuenow": size,
      "aria-valuemin": min,
      "aria-valuemax": max,
      tabIndex: 0,
      onPointerDown,
      onKeyDown,
      onDoubleClick: reset,
    },
  }
}
