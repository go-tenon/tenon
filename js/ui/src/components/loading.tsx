// 切换加载过渡组件与钩子：采用“延迟出现 + 最短停留”策略，快速切页、本地切换与缓存命中零延迟直出，
// 仅在真实请求或路由加载超过 delayMs 时叠加轻磨砂加载覆层，避免短请求闪烁。

import { Loader } from "@mantine/core"
import { QueryClientContext } from "@tanstack/react-query"
import {
  type CSSProperties,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react"

/** 返回当前是否存在进行中的 TanStack Query 请求；未处于 QueryClientProvider 中时返回 false。 */
export function useAnyFetching(): boolean {
  const client = useContext(QueryClientContext)
  const subscribe = useCallback(
    (onStoreChange: () => void) => (client ? client.getQueryCache().subscribe(onStoreChange) : () => {}),
    [client],
  )
  const getSnapshot = useCallback(() => (client ? client.isFetching() > 0 : false), [client])
  return useSyncExternalStore(subscribe, getSnapshot, () => false)
}

/** useDelayedLoading 的选项。 */
export interface DelayedLoadingOptions {
  /** 进入加载态前等待的毫秒数（默认 150）；在此之前完成的快请求不展示加载覆层。 */
  delayMs?: number
  /** 加载覆层一旦展示后的最短停留毫秒数（默认 200），避免一闪而过。 */
  minMs?: number
}

/**
 * 延迟加载状态钩子：active 持续超过 delayMs 才返回 true；一旦返回 true，在 active 结束后至少保持 minMs 再收回。
 * 快请求与缓存命中不触发加载态，慢请求平滑展示并避免闪烁。
 */
export function useDelayedLoading(active: boolean, options?: DelayedLoadingOptions): boolean {
  const delayMs = options?.delayMs ?? 150
  const minMs = options?.minMs ?? 200
  const [visible, setVisible] = useState(false)
  const visibleRef = useRef(false)
  const shownAtRef = useRef(0)
  const delayTimerRef = useRef<number | undefined>(undefined)
  const hideTimerRef = useRef<number | undefined>(undefined)

  useEffect(() => {
    const clearDelay = () => {
      if (delayTimerRef.current !== undefined) {
        window.clearTimeout(delayTimerRef.current)
        delayTimerRef.current = undefined
      }
    }
    const clearHide = () => {
      if (hideTimerRef.current !== undefined) {
        window.clearTimeout(hideTimerRef.current)
        hideTimerRef.current = undefined
      }
    }

    if (active) {
      clearHide()
      if (visibleRef.current) return
      if (delayMs <= 0) {
        visibleRef.current = true
        shownAtRef.current = Date.now()
        setVisible(true)
        return
      }
      clearDelay()
      delayTimerRef.current = window.setTimeout(() => {
        delayTimerRef.current = undefined
        visibleRef.current = true
        shownAtRef.current = Date.now()
        setVisible(true)
      }, delayMs)
      return
    }

    clearDelay()
    if (!visibleRef.current) return
    const remain = minMs - (Date.now() - shownAtRef.current)
    if (remain <= 0) {
      visibleRef.current = false
      setVisible(false)
      return
    }
    clearHide()
    hideTimerRef.current = window.setTimeout(() => {
      hideTimerRef.current = undefined
      visibleRef.current = false
      setVisible(false)
    }, remain)
  }, [active, delayMs, minMs])

  useEffect(
    () => () => {
      if (delayTimerRef.current !== undefined) window.clearTimeout(delayTimerRef.current)
      if (hideTimerRef.current !== undefined) window.clearTimeout(hideTimerRef.current)
    },
    [],
  )

  return visible
}

/** useSmoothTransition 的选项。 */
export interface SmoothTransitionOptions {
  /** 请求耗时超过该毫秒数才展示加载覆层，默认 150；设为 0 时有请求即立即展示。 */
  delayMs?: number
  /** 加载覆层展示后的最短停留时长（毫秒），默认 200。 */
  minMs?: number
}

/**
 * 局部数据切换过渡钩子：当 changeKey 变化（筛选、排序、翻页）且 fetching 为真时暂留旧数据；
 * 本地切换、命中缓存或快于 delayMs 的请求直接更新且不弹加载覆层，慢请求超过 delayMs 后展示覆层并保底停留 minMs。
 */
export function useSmoothTransition<T>(
  value: T,
  changeKey: string,
  fetching: boolean,
  options?: SmoothTransitionOptions,
): { data: T; busy: boolean } {
  const [displayed, setDisplayed] = useState<T>(value)
  const [prevKey, setPrevKey] = useState(changeKey)
  const [pending, setPending] = useState(false)

  const keyChanged = prevKey !== changeKey
  const active = pending || (keyChanged && fetching && displayed !== undefined)
  const busy = useDelayedLoading(active, options)

  // changeKey 变化时：仍在拉数则暂留旧画面，已有数据或本地切换直接切出。
  useEffect(() => {
    if (changeKey === prevKey) return
    setPrevKey(changeKey)
    if (fetching && displayed !== undefined) {
      setPending(true)
    } else {
      setPending(false)
      setDisplayed(value)
    }
  }, [changeKey, displayed, fetching, prevKey, value])

  // 请求结束且覆层收回后提交新数据；首次加载和静默后台刷新直接同步最新数据。
  useEffect(() => {
    if (keyChanged) return
    if (pending) {
      if (!fetching) setPending(false)
      return
    }
    if (!busy && !Object.is(displayed, value)) {
      setDisplayed(value)
    }
  }, [busy, displayed, fetching, keyChanged, pending, value])

  return { data: displayed, busy }
}

/** LoadingOverlay 的选项。 */
export interface LoadingOverlayProps {
  /** 是否显示轻磨砂覆层与居中转圈。 */
  visible: boolean
  /** 居中转圈尺寸（像素），默认 22。 */
  loaderSize?: number
  /** 为 true 时在长文档滚动容器中将转圈固定居中于当前可视视口内。 */
  sticky?: boolean
  className?: string
  style?: CSSProperties
}

/** 轻磨砂居中转圈覆层：覆盖在相对定位的父容器上，明暗主题自动跟随 --tn-surface。 */
export function LoadingOverlay({ visible, loaderSize = 22, sticky, className, style }: LoadingOverlayProps): ReactNode {
  const loader = visible ? <Loader size={loaderSize} /> : null
  return (
    <div
      className={className ? `tn-loading-overlay ${className}` : "tn-loading-overlay"}
      data-active={visible || undefined}
      data-sticky={sticky || undefined}
      aria-hidden={!visible || undefined}
      style={style}
    >
      {sticky ? <div className="tn-loading-overlay-pin">{loader}</div> : loader}
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
      data-loading-active={loading || undefined}
      aria-busy={loading || undefined}
      style={style}
    >
      {children}
      <LoadingOverlay visible={loading} loaderSize={loaderSize} />
    </div>
  )
}
