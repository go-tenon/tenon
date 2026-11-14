// 后台操作：读取进度的事件流，直到操作结束。

import { type AbortSignalLike, abortError, sleep } from "./fetch"

/** 后台操作的进度，与服务端 operation.Operation 的 JSON 相同。 */
export interface OperationLike {
  id: string
  kind?: string
  status: string
  total: number
  processed: number
  failed: number
  error?: string
}

/** 报告操作是否已经结束：completed 或 failed。 */
export function isFinished(op: Pick<OperationLike, "status">): boolean {
  return op.status === "completed" || op.status === "failed"
}

/** 完成的比例（0 到 1）；total 为 0（还不知道总数）时返回 undefined。 */
export function progressRatio(op: Pick<OperationLike, "total" | "processed">): number | undefined {
  return op.total > 0 ? Math.min(op.processed / op.total, 1) : undefined
}

/** watchOperation 的选项。 */
export interface WatchOptions<O> {
  /** 每次收到进度时调用，包括连接后的第一次。 */
  onProgress?: (op: O) => void
  /** 停止等待。 */
  signal?: AbortSignalLike
  /** 连接断开或失败后重连前等待的毫秒数，默认 1000。 */
  retryDelay?: number
  /** 连续失败多少次后放弃，默认 10；收到事件后重新计数。 */
  maxRetries?: number
}

/**
 * 读取进度的事件流，直到操作结束（completed 或 failed），返回最终的状态。open 打开事件流，通常是生成客户端的方法：
 *
 *	const done = await watchOperation((o) => api.operation.progress(id, o), { onProgress: setProgress })
 *
 * 连接断开（例如服务器重启）后自动重连；服务端在连接时先推送一次当前进度，所以重连不会错过结束。
 */
export async function watchOperation<O extends OperationLike>(
  open: (options: { signal?: AbortSignalLike }) => AsyncIterable<{ event: string; data: O }>,
  options: WatchOptions<O> = {},
): Promise<O> {
  const { signal, retryDelay = 1000, maxRetries = 10 } = options
  let failures = 0
  for (;;) {
    if (signal?.aborted) throw abortError()
    try {
      for await (const ev of open({ signal })) {
        if (ev.event !== "progress") continue
        failures = 0
        options.onProgress?.(ev.data)
        if (isFinished(ev.data)) return ev.data
      }
    } catch (err) {
      if (signal?.aborted) throw abortError()
      if (typeof err === "object" && err !== null && typeof (err as { status?: unknown }).status === "number") {
        const status = (err as { status: number }).status
        if (status < 500) throw err // 没有权限、操作不存在等，重连也不会好
      }
      if (++failures > maxRetries) throw err
    }
    await sleep(retryDelay, signal)
  }
}
