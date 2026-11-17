// 页面顶部的进度条（@mantine/nprogress）：跳转到还在加载的页面时、重新加载标签时显示，读取完成后走完。
// 内容区的淡入由 AppLayout 提供（与切换页面时相同），重新加载时也播放一次。

import { createNprogress, NavigationProgress } from "@mantine/nprogress"
import type { QueryClient } from "@tanstack/react-query"
import { useRouterState } from "@tanstack/react-router"
import { createContext, type ReactNode, useContext, useEffect, useRef } from "react"

const [store, progress] = createNprogress()

/** 页面顶部的进度条：放在布局中一次。路由在加载新页面（代码、loader）时显示。 */
export function PageProgress(): ReactNode {
  const pending = useRouterState({ select: (s) => s.status === "pending" })
  const started = useRef(false)
  useEffect(() => {
    if (pending && !started.current) {
      started.current = true
      progress.start()
    } else if (!pending && started.current) {
      started.current = false
      progress.complete()
    }
  }, [pending])
  return <NavigationProgress store={store} size={2} />
}

/** 等到没有正在进行的请求：重新挂载的页面开始读取后再判断，最多等 15 秒。 */
async function settled(queryClient: QueryClient): Promise<void> {
  await new Promise((r) => setTimeout(r, 0))
  if (queryClient.isFetching() === 0) return
  await new Promise<void>((resolve) => {
    const timer = setTimeout(done, 15_000)
    const unsubscribe = queryClient.getQueryCache().subscribe(() => {
      if (queryClient.isFetching() === 0) done()
    })
    function done() {
      clearTimeout(timer)
      unsubscribe()
      resolve()
    }
  })
}

/**
 * 带进度条地重新加载：进度条开始，执行 reload（返回 false 表示用户取消，例如有未保存的修改时选择留下），
 * 内容区淡入一次，等页面的请求都完成后进度条走完。
 */
export async function reloadWithProgress(
  reload: () => Promise<boolean>,
  queryClient: QueryClient,
  flash: () => void,
): Promise<void> {
  progress.start()
  if (!(await reload())) {
    progress.reset()
    return
  }
  flash()
  await settled(queryClient)
  progress.complete()
}

const FlashContext = createContext<() => void>(() => {})

/** AppLayout 提供：内容区播放一次进入的动画。 */
export const ContentFlashProvider = FlashContext.Provider

/** 内容区播放一次进入的动画（不在 AppLayout 中时什么也不做）。 */
export function useContentFlash(): () => void {
  return useContext(FlashContext)
}
