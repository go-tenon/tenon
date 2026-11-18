// 页面顶部的进度条（@mantine/nprogress）：只在重新加载标签（刷新按钮、右键菜单）时显示，页面的请求都完成后走完。
// 页面加载（包括按浏览器的刷新）和跳转不显示，保持浏览器原本的行为。
// 重新加载标签时主内容区先淡出，读取完成后淡入；淡出、淡入由 FrameLayout 提供。

import { createNprogress, NavigationProgress } from "@mantine/nprogress"
import type { QueryClient } from "@tanstack/react-query"
import { createContext, type ReactNode, useContext } from "react"

const [store, progress] = createNprogress()

/** 页面顶部的进度条：放在布局中一次。 */
export function PageProgress(): ReactNode {
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

/** FrameLayout 提供：主内容区的淡出、淡入。 */
export const ContentTransitionProvider = TransitionContext.Provider

/** 主内容区的过渡（不在 FrameLayout 中时什么也不做）。 */
export function useContentTransition(): ContentTransition {
  return useContext(TransitionContext)
}
