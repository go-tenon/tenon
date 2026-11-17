// 页面顶部的进度条（@mantine/nprogress）：应用内跳转到还在加载的页面时、重新加载标签时显示，读取完成后走完。
// 第一次打开（包括按浏览器的刷新）时不显示：那是整页加载，浏览器自己显示进度。
// 重新加载标签时主内容区先淡出，读取完成后淡入；淡出、淡入由 AppLayout 提供。

import { createNprogress, NavigationProgress } from "@mantine/nprogress"
import type { QueryClient } from "@tanstack/react-query"
import { useRouterState } from "@tanstack/react-router"
import { createContext, type ReactNode, useContext, useEffect, useRef } from "react"

const [store, progress] = createNprogress()

/** 页面顶部的进度条：放在布局中一次。应用内跳转、路由在加载新页面（代码、loader）时显示。 */
export function PageProgress(): ReactNode {
  const pending = useRouterState({ select: (s) => s.status === "pending" })
  const started = useRef(false)
  // 第一次打开时的加载不算：等路由第一次空闲之后才跟随它。
  const ready = useRef(false)
  useEffect(() => {
    if (!ready.current) {
      if (!pending) ready.current = true
      return
    }
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

/** 主内容区的过渡：淡出（等动画完成）、淡入。 */
export interface ContentTransition {
  out(): Promise<void>
  in(): void
}

/**
 * 带进度条地重新加载：进度条开始，执行 reload（返回 false 表示用户取消，例如有未保存的修改时选择留下），
 * 主内容区淡出，等页面的请求都完成后淡入，进度条走完。
 */
export async function reloadWithProgress(
  reload: () => Promise<boolean>,
  queryClient: QueryClient,
  transition: ContentTransition,
): Promise<void> {
  progress.start()
  if (!(await reload())) {
    progress.reset()
    return
  }
  await transition.out()
  await settled(queryClient)
  transition.in()
  progress.complete()
}

const none: ContentTransition = { out: async () => {}, in: () => {} }
const TransitionContext = createContext<ContentTransition>(none)

/** AppLayout 提供：主内容区的淡出、淡入。 */
export const ContentTransitionProvider = TransitionContext.Provider

/** 主内容区的过渡（不在 AppLayout 中时什么也不做）。 */
export function useContentTransition(): ContentTransition {
  return useContext(TransitionContext)
}
