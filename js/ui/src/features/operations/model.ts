// 后台操作：导入、导出、批量处理这类耗时的操作。发起后登记到操作中心（trackOperation），
// 页面和顶栏的任务中心读取它们的进度（useOperations、useOperation）。进度来自 @go-tenon/client 的 watchOperation。

import { type OperationLike, progressRatio, watchOperation } from "@go-tenon/client"
import { useSyncExternalStore } from "react"
import { type App, useApp } from "../../app/app"
import { createStore, perOwner } from "../../app/store"

/** 进度事件流：生成的客户端的进度接口，例如 (o) => api.article.bulkPublishProgress(id, o)。 */
export type OperationStream = (options: {
  signal?: AbortSignal
}) => AsyncIterable<{ event: string; data: OperationLike }>

/** 操作中心中的一项。 */
export interface TrackedOperation {
  id: string
  /** 显示的名称，例如“批量发布 12 篇文章”。 */
  title: string
  /** 最新的进度；还没有收到时为空。 */
  operation?: OperationLike
  /** 进度的比例（0 到 1）；不知道总数时为空。 */
  ratio?: number
  /** completed 或 failed。 */
  finished: boolean
  /** 读取进度失败（例如没有权限）。 */
  error?: unknown
  startedAt: number
}

interface Entry extends TrackedOperation {
  abort: AbortController
}

const operationsOf = perOwner(() => createStore<readonly Entry[]>([]))

// 每个应用订阅一次会话：退出登录、切换身份时清空操作中心，上一个身份的操作不会留下。
const subscribed = new WeakSet<App>()

function update(app: App, id: string, change: (e: Entry) => Entry): void {
  const store = operationsOf(app)
  store.set(store.get().map((e) => (e.id === id ? change(e) : e)))
}

/**
 * 登记一个后台操作并开始读取它的进度。发起操作的接口返回操作的 ID 后调用：
 *
 *   const op = await api.article.bulkPublish({ ids })
 *   trackOperation(app, { id: op.id, title: `批量发布 ${ids.length} 篇文章`, open: (o) => api.article.bulkPublishProgress(op.id, o) })
 *
 * 同一个 ID 只登记一次。操作结束后仍留在操作中心，直到用户移除（dismissOperation）或者退出登录。
 */
export function trackOperation(app: App, op: { id: string; title: string; open: OperationStream }): void {
  const store = operationsOf(app)
  if (!subscribed.has(app)) {
    subscribed.add(app)
    app.session.subscribe(() => clearOperations(app))
  }
  if (store.get().some((e) => e.id === op.id)) return
  const abort = new AbortController()
  store.set([{ id: op.id, title: op.title, finished: false, startedAt: Date.now(), abort }, ...store.get()])
  // watchOperation 传入的是这里的 abort.signal，生成的客户端要的是 AbortSignal。
  watchOperation((o) => op.open({ signal: o.signal as AbortSignal | undefined }), {
    signal: abort.signal,
    onProgress: (operation) => update(app, op.id, (e) => ({ ...e, operation, ratio: progressRatio(operation) })),
  }).then(
    (operation) => update(app, op.id, (e) => ({ ...e, operation, ratio: progressRatio(operation), finished: true })),
    (error) => {
      if (!abort.signal.aborted) update(app, op.id, (e) => ({ ...e, error, finished: true }))
    },
  )
}

/** 从操作中心移除一项，停止读取它的进度。 */
export function dismissOperation(app: App, id: string): void {
  const store = operationsOf(app)
  const entry = store.get().find((e) => e.id === id)
  entry?.abort.abort()
  store.set(store.get().filter((e) => e.id !== id))
}

/** 操作中心：登记过的操作，新的在前。 */
export function useOperations(): readonly TrackedOperation[] {
  const store = operationsOf(useApp())
  return useSyncExternalStore(store.subscribe, store.get, store.get)
}

/** 一个操作的进度；没有登记时为空。 */
export function useOperation(id: string | undefined): TrackedOperation | undefined {
  return useOperations().find((o) => o.id === id)
}

/** 清空操作中心，停止读取进度。退出登录、切换身份时自动调用。 */
export function clearOperations(app: App): void {
  const store = operationsOf(app)
  for (const e of store.get()) e.abort.abort()
  store.set([])
}
