// 切换加载过渡组件与钩子：在切换 tab、筛选、排序或翻页时提供最短展示时长的轻磨砂加载反馈，
// 慢网络请求随实际请求时长自动延长，本地切换与缓存命中保持统一视觉节奏。

import { Loader } from "@mantine/core"
import { type CSSProperties, type ReactNode, useEffect, useRef, useState } from "react"

/** useSmoothTransition 的选项。 */
export interface SmoothTransitionOptions {
  /** 切换后加载态的最短展示时长（毫秒），默认 220；设为 0 时仅跟随 fetching 状态。 */
  minMs?: number
}

/**
 * 统一切换加载状态机：当 changeKey 变化（切换 tab、筛选、排序或翻页）时立即进入 busy 状态并暂留旧数据；
 * 本地切换或命中缓存时保底展示 minMs 毫秒，网络请求耗时更长时随 fetching 自动延长直到请求完成。
 */
export function useSmoothTransition<T>(
  value: T,
  changeKey: string,
  fetching: boolean,
  options?: SmoothTransitionOptions,
): { data: T; busy: boolean } {
  const minMs = options?.minMs ?? 220
  const [displayed, setDisplayed] = useState<T>(value)
  const [busy, setBusy] = useState(false)
  const [minReady, setMinReady] = useState(true)
  const lastKeyRef = useRef(changeKey)
  const timerRef = useRef<number | undefined>(undefined)

  // 当切换键变化时进入加载态，并按 minMs 启动最短展示计时器
  useEffect(() => {
    if (changeKey === lastKeyRef.current) return
    lastKeyRef.current = changeKey
    if (displayed === undefined) return

    if (timerRef.current !== undefined) window.clearTimeout(timerRef.current)
    if (minMs <= 0) {
      setBusy(fetching)
      setMinReady(true)
      return
    }
    setBusy(true)
    setMinReady(false)
    timerRef.current = window.setTimeout(() => {
      timerRef.current = undefined
      setMinReady(true)
    }, minMs)
  }, [changeKey, displayed, fetching, minMs])

  useEffect(
    () => () => {
      if (timerRef.current !== undefined) window.clearTimeout(timerRef.current)
    },
    [],
  )

  // 计时到达且网络请求完成后提交新数据并结束加载态；首次加载和静默后台刷新直接同步最新数据
  useEffect(() => {
    if (!busy) {
      if (!Object.is(displayed, value)) setDisplayed(value)
      return
    }
    if (minReady && !fetching) {
      setDisplayed(value)
      setBusy(false)
    }
  }, [busy, displayed, fetching, minReady, value])

  return { data: displayed, busy }
}

/** LoadingOverlay 的选项。 */
export interface LoadingOverlayProps {
  /** 是否显示轻磨砂覆层与居中转圈。 */
  visible: boolean
  /** 居中转圈尺寸（像素），默认 22。 */
  loaderSize?: number
  className?: string
  style?: CSSProperties
}

/** 轻磨砂居中转圈覆层：覆盖在相对定位的父容器上，明暗主题自动跟随 --tn-surface。 */
export function LoadingOverlay({ visible, loaderSize = 22, className, style }: LoadingOverlayProps): ReactNode {
  return (
    <div
      className={className ? `tn-loading-overlay ${className}` : "tn-loading-overlay"}
      data-active={visible || undefined}
      aria-hidden={!visible || undefined}
      style={style}
    >
      {visible && <Loader size={loaderSize} />}
    </div>
  )
}

/** LoadingStage 的选项。 */
export interface LoadingStageProps {
  /** 是否处于加载切换态。 */
  loading: boolean
  /** 居中转圈尺寸（像素），默认 22。 */
  loaderSize?: number
  className?: string
  style?: CSSProperties
  children: ReactNode
}

/** 局部刷新容器：包裹图表、表格等区域，切换时在其上方叠加轻磨砂覆层与居中转圈。 */
export function LoadingStage({ loading, loaderSize = 22, className, style, children }: LoadingStageProps): ReactNode {
  return (
    <div
      className={className ? `tn-loading-stage ${className}` : "tn-loading-stage"}
      aria-busy={loading || undefined}
      style={style}
    >
      {children}
      <LoadingOverlay visible={loading} loaderSize={loaderSize} />
    </div>
  )
}
