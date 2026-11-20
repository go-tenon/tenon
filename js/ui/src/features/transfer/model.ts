// 导入与导出：大表格在后台的队列任务中处理（见后台导入、后台导出两章）。
// 导入：上传文件 → 发起导入 → 读取进度 → 结束后显示结果，可以下载没有导入的行；
// 导出：发起导出 → 读取进度 → 完成后下载。进度同时出现在操作中心（trackOperation）。
// 接口由应用定义，通过 ImportSource、ExportSource 调用；外观（按钮和对话框）只负责渲染。

import type { OperationLike } from "@go-tenon/client"
import { useCallback, useRef, useState } from "react"
import { useApp } from "../../app/app"
import { text as uiText } from "../../i18n/i18n"
import { type OperationStream, type TrackedOperation, trackOperation, useOperation } from "../operations/model"

/** 导入、导出所处的阶段。 */
export type TransferPhase = "idle" | "uploading" | "starting" | "running" | "completed" | "failed"

/** 导入的接口，通常是生成的客户端的几个方法。 */
export interface ImportSource {
  /** 显示在操作中心的名称，默认“导入”。 */
  title?: string
  /** 上传表格，返回文件 ID。通常用 @go-tenon/client 的 uploadFile；onProgress 报告上传的比例（0 到 1）。 */
  upload(file: File, options: { onProgress?: (ratio: number) => void }): Promise<string>
  /** 发起导入，返回导入的 ID，例如 (fileId) => api.article.importStart({ file_id: fileId })。 */
  start(fileId: string): Promise<{ id: string }>
  /** 导入的进度，例如 (id) => (o) => api.article.importProgress(id, o)。 */
  progress(id: string): OperationStream
  /** 下载没有导入的行，例如用 saveBlob 保存 api.article.importFailures(id) 的结果。没有时不提供下载。 */
  failures?(id: string): Promise<void>
}

/** 导出的接口。 */
export interface ExportSource {
  /** 显示在操作中心的名称，默认“导出”。 */
  title?: string
  /** 发起导出，返回导出的 ID。 */
  start(): Promise<{ id: string }>
  /** 导出的进度。 */
  progress(id: string): OperationStream
  /**
   * 完成后下载的文件。默认取进度事件中的 url 和 filename（sheet/exports 的导出就是这样）。
   */
  file?(operation: OperationLike): { url: string; filename?: string } | undefined
}

/** useImport、useExport 共同的状态。 */
interface TransferState {
  phase: TransferPhase
  /** 后台操作的进度；发起之前为空。 */
  operation?: TrackedOperation
  /** 上传、发起时的错误；进度中的失败见 operation.operation.error。 */
  error?: unknown
  /** 回到 idle，例如关闭对话框后再导入一个文件。 */
  reset(): void
}

/** useImport 的返回值。 */
export interface Import extends TransferState {
  /** 上传的比例（0 到 1），上传时才有。 */
  uploaded?: number
  /** 上传并导入 file。 */
  start(file: File): Promise<void>
  /** 下载没有导入的行；导入结束、有失败的行，并且 ImportSource 提供了 failures 时可用。 */
  downloadFailures?: () => Promise<void>
}

/** useExport 的返回值。 */
export interface Export extends TransferState {
  start(): Promise<void>
  /** 完成后下载的文件。 */
  file?: { url: string; filename?: string }
}

/** 由操作的进度得出阶段。 */
function phaseOf(op: TrackedOperation | undefined, local: TransferPhase): TransferPhase {
  if (!op) return local
  if (op.error) return "failed"
  const status = op.operation?.status
  if (status === "completed") return "completed"
  if (status === "failed") return "failed"
  return "running"
}

/** 发起一个后台操作的共同部分：记下操作的 ID，登记到操作中心。 */
function useTransfer(title: string) {
  const app = useApp()
  const [id, setId] = useState<string>()
  const [local, setLocal] = useState<TransferPhase>("idle")
  const [error, setError] = useState<unknown>()
  const operation = useOperation(id)
  const busy = useRef(false)
  const run = useCallback(
    async (steps: () => Promise<{ id: string; open: OperationStream }>) => {
      if (busy.current) return
      busy.current = true
      setError(undefined)
      try {
        const { id, open } = await steps()
        trackOperation(app, { id, title, open })
        setId(id)
      } catch (err) {
        setError(err)
        setLocal("failed")
      } finally {
        busy.current = false
      }
    },
    [app, title],
  )
  const reset = useCallback(() => {
    setId(undefined)
    setLocal("idle")
    setError(undefined)
  }, [])
  return { id, operation, phase: phaseOf(operation, local), setLocal, error, run, reset }
}

/**
 * 导入的模型：上传 → 发起 → 读取进度 → 结果。进度同时出现在操作中心。
 *
 *   const imp = useImport({ upload, start: (fileId) => api.article.importStart({ file_id: fileId }), progress })
 *   await imp.start(file)
 */
export function useImport(source: ImportSource): Import {
  const t = useTransfer(source.title ?? uiText("tenon.transfer.import"))
  const [uploaded, setUploaded] = useState<number>()
  const start = (file: File) =>
    t.run(async () => {
      t.setLocal("uploading")
      setUploaded(0)
      const fileId = await source.upload(file, { onProgress: setUploaded })
      t.setLocal("starting")
      const { id } = await source.start(fileId)
      return { id, open: source.progress(id) }
    })
  const failures = source.failures
  const failed = t.operation?.operation?.failed ?? 0
  return {
    phase: t.phase,
    operation: t.operation,
    error: t.error,
    uploaded: t.phase === "uploading" ? uploaded : undefined,
    start,
    downloadFailures:
      failures && t.id && t.operation?.finished && failed > 0 ? () => failures(t.id as string) : undefined,
    reset: () => {
      setUploaded(undefined)
      t.reset()
    },
  }
}

/** sheet/exports 的进度事件带着下载地址和文件名。 */
function defaultFile(op: OperationLike): { url: string; filename?: string } | undefined {
  const o = op as OperationLike & { url?: string; filename?: string }
  return o.url ? { url: o.url, filename: o.filename } : undefined
}

/**
 * 导出的模型：发起 → 读取进度 → 完成后下载。进度同时出现在操作中心。
 *
 *   const exp = useExport({ start: () => api.article.exportStart({ format: "xlsx" }), progress })
 *   exp.file && <a href={exp.file.url} download={exp.file.filename}>下载</a>
 */
export function useExport(source: ExportSource): Export {
  const t = useTransfer(source.title ?? uiText("tenon.transfer.export"))
  const start = () =>
    t.run(async () => {
      t.setLocal("starting")
      const { id } = await source.start()
      return { id, open: source.progress(id) }
    })
  const op = t.operation?.operation
  return {
    phase: t.phase,
    operation: t.operation,
    error: t.error,
    start,
    file: t.phase === "completed" && op ? (source.file ?? defaultFile)(op) : undefined,
    reset: t.reset,
  }
}
