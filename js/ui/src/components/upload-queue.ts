// 文件上传队列：选择、粘贴截图或拖入文件后立即上传，统一管理预览、取消、重试和数量/大小/类型校验。

import { describeError } from "@go-tenon/client"
import { type ClipboardEvent, type DragEvent, useCallback, useEffect, useRef, useState } from "react"
import { text as uiText } from "../i18n/i18n"
import { formatFileSize } from "../theme/format"
import { errorMessage } from "./feedback"

/** UploadQueueItem 是队列中的一个待发送文件。 */
export interface UploadQueueItem<T> {
  id: string
  file: File
  /** 图片的本地预览地址（object URL）；非图片为空，移除或卸载时自动释放。 */
  preview?: string
  status: "uploading" | "ready" | "error"
  /** 上传成功后返回的结果（例如服务端返回的文件记录）。 */
  result?: T
  /** 上传失败时的提示文字。 */
  error?: string
}

/** UploadQueueOptions 是 useUploadQueue 的选项。 */
export interface UploadQueueOptions<T> {
  /** 执行单个文件上传；取消移除时触发 signal。 */
  upload: (file: File, options: { signal: AbortSignal }) => Promise<T>
  /** 允许的文件类型，与 <input accept> 写法相同，例如 "image/png,image/jpeg,.pdf"。不传时不限制。 */
  accept?: string
  /** 队列中最多同时容纳的文件数，默认 10。 */
  maxFiles?: number
  /** 单个文件最大字节数，可传函数按文件分别限定（例如图片 10 MiB、文档 20 MiB）。 */
  maxSize?: number | ((file: File) => number)
  /** 队列校验不通过或上传失败时的回调，便于展示通知。 */
  onError?: (message: string) => void
}

let seq = 0

/** 检查 file 是否符合 accept（支持 .ext、image/*、完整 MIME）。扩展名匹配不依赖浏览器是否提供 file.type。 */
function matchesAccept(accept: string, file: File): boolean {
  const name = file.name.toLowerCase()
  const type = file.type.toLowerCase()
  return accept
    .split(",")
    .map((a) => a.trim().toLowerCase())
    .filter(Boolean)
    .some((a) => {
      if (a.startsWith(".")) return name.endsWith(a)
      if (!type) return false
      return a.endsWith("/*") ? type.startsWith(a.slice(0, -1)) : type === a
    })
}

/**
 * 管理文件上传队列：加入文件后立即调用 upload 上传，支持粘贴截图（handlePaste）、拖拽（handleDrop）、
 * 单条重试（retry）、取消移除（remove）与清空（clear）。组件卸载时自动中止未完成的上传并释放预览地址。
 */
export function useUploadQueue<T>(options: UploadQueueOptions<T>) {
  const [items, setItems] = useState<UploadQueueItem<T>[]>([])
  const live = useRef(items)
  live.current = items
  const opts = useRef(options)
  opts.current = options
  const controllers = useRef(new Map<string, AbortController>())

  const start = useCallback((id: string, file: File) => {
    controllers.current.get(id)?.abort()
    const ctrl = new AbortController()
    controllers.current.set(id, ctrl)
    void opts.current
      .upload(file, { signal: ctrl.signal })
      .then((result) => {
        if (ctrl.signal.aborted) return
        controllers.current.delete(id)
        setItems((cur) => cur.map((x) => (x.id === id ? { ...x, status: "ready", result, error: undefined } : x)))
      })
      .catch((err: unknown) => {
        if (ctrl.signal.aborted) return
        controllers.current.delete(id)
        const msg = errorMessage(describeError(err))
        setItems((cur) => cur.map((x) => (x.id === id ? { ...x, status: "error", error: msg } : x)))
        opts.current.onError?.(msg)
      })
  }, [])

  useEffect(() => {
    const map = controllers.current
    return () => {
      for (const ctrl of map.values()) ctrl.abort()
      map.clear()
      for (const it of live.current) if (it.preview) URL.revokeObjectURL(it.preview)
    }
  }, [])

  const add = useCallback(
    (incoming: Iterable<File> | ArrayLike<File>) => {
      const { accept, maxFiles = 10, maxSize, onError } = opts.current
      const existing = live.current.length
      const next: UploadQueueItem<T>[] = []
      let firstError: string | undefined

      for (const file of Array.from(incoming)) {
        if (existing + next.length >= maxFiles) {
          firstError ??= uiText("tenon.upload.too_many_files", { count: maxFiles })
          break
        }
        if (accept && !matchesAccept(accept, file)) {
          firstError ??= uiText("tenon.upload.unsupported_file", { name: file.name })
          continue
        }
        const limit = typeof maxSize === "function" ? maxSize(file) : maxSize
        if (limit && file.size > limit) {
          firstError ??= uiText("tenon.upload.file_too_large", { name: file.name, size: formatFileSize(limit) })
          continue
        }
        const id = `uq-${++seq}`
        const preview = file.type.startsWith("image/") ? URL.createObjectURL(file) : undefined
        next.push({ id, file, preview, status: "uploading" })
      }

      if (firstError) onError?.(firstError)
      if (next.length === 0) return
      setItems((cur) => [...cur, ...next])
      for (const it of next) start(it.id, it.file)
    },
    [start],
  )

  const retry = useCallback(
    (id: string) => {
      const item = live.current.find((x) => x.id === id)
      if (item?.status !== "error") return
      setItems((cur) => cur.map((x) => (x.id === id ? { ...x, status: "uploading", error: undefined } : x)))
      start(id, item.file)
    },
    [start],
  )

  const remove = useCallback((id: string) => {
    controllers.current.get(id)?.abort()
    controllers.current.delete(id)
    setItems((cur) => {
      const target = cur.find((x) => x.id === id)
      if (target?.preview) URL.revokeObjectURL(target.preview)
      return cur.filter((x) => x.id !== id)
    })
  }, [])

  const clear = useCallback(() => {
    for (const ctrl of controllers.current.values()) ctrl.abort()
    controllers.current.clear()
    setItems((cur) => {
      for (const it of cur) if (it.preview) URL.revokeObjectURL(it.preview)
      return []
    })
  }, [])

  /** 处理输入框的粘贴事件：剪贴板含图片或文件时拦截默认粘贴并加入上传队列，返回是否已处理。 */
  const handlePaste = useCallback(
    (e: ClipboardEvent | globalThis.ClipboardEvent): boolean => {
      const data = e.clipboardData
      if (!data) return false
      const files: File[] = []
      if (data.items) {
        for (const item of Array.from(data.items)) {
          if (item.kind !== "file") continue
          const file = item.getAsFile()
          if (file) files.push(file)
        }
      } else if (data.files) {
        files.push(...Array.from(data.files))
      }
      if (files.length === 0) return false
      e.preventDefault()
      add(files)
      return true
    },
    [add],
  )

  /** 处理拖放事件：拖入文件时拦截并加入上传队列，返回是否已处理。 */
  const handleDrop = useCallback(
    (e: DragEvent | globalThis.DragEvent): boolean => {
      const list = e.dataTransfer?.files
      if (!list || list.length === 0) return false
      e.preventDefault()
      add(list)
      return true
    },
    [add],
  )

  const uploading = items.some((x) => x.status === "uploading")
  const hasError = items.some((x) => x.status === "error")
  const ready = items
    .filter((x): x is UploadQueueItem<T> & { result: T } => x.status === "ready" && x.result !== undefined)
    .map((x) => x.result)

  return {
    items,
    uploading,
    hasError,
    ready,
    add,
    retry,
    remove,
    clear,
    handlePaste,
    handleDrop,
  }
}

/** useUploadQueue 返回的状态和操作。 */
export type UploadQueue<T> = ReturnType<typeof useUploadQueue<T>>
