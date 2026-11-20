// AI 对话：在 Vercel AI SDK 的 useChat 之上，按框架的 AI 对话协议接好消息流、历史、停止、插话和工具的审批。
// 这是单独的入口 @go-tenon/ui/chat：只有用到 AI 对话的前端才需要安装 ai 和 @ai-sdk/react。

import { useChat } from "@ai-sdk/react"
import { type ChatPaths, chatControls, chatTransport, describeError } from "@go-tenon/client"
import { useQuery } from "@tanstack/react-query"
import {
  DefaultChatTransport,
  type FileUIPart,
  lastAssistantMessageIsCompleteWithApprovalResponses,
  type UIMessage,
} from "ai"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useApp } from "../app/app"
import { text as uiText } from "../i18n/i18n"

/** 对话的接口：api 是发送接口的地址，例如 /api/v1/assistant/chat；其他接口按约定拼接，见 chatControls。 */
export interface AssistantOptions {
  api: string
  /** 对话的 ID。 */
  id: string
  paths?: ChatPaths
}

/** 附件：允许的类型和大小，以及可选的上传。 */
export interface AttachmentOptions {
  /** 允许的类型，与 <input accept> 的写法相同，例如 "image/*,.pdf"；默认 image/*,application/pdf。 */
  accept?: string
  /** 单个文件的最大字节数，默认 4 MiB。 */
  maxFileSize?: number
  /**
   * 以 data URL 发送时，一条消息中文件的总字节数上限，默认 5 MiB。data URL 比文件大约三分之一，
   * 默认值对应 8 MiB 的请求体上限；设置了 upload 时不检查。
   */
  maxTotalSize?: number
  /** 一条消息最多带几个文件，默认 5。 */
  maxFiles?: number
  /**
   * 上传文件，返回模型服务能下载的链接（例如有效期足够长的签名地址），消息中只放链接。
   * 不设置时文件以 data URL 随消息发送。
   */
  upload?: (file: File) => Promise<string>
}

const defaultAccept = "image/*,application/pdf"
const defaultMaxFileSize = 4 << 20
const defaultMaxFiles = 5
const defaultMaxTotalSize = 5 << 20

/** 附件不符合限制。message 可以直接显示给用户。 */
export class AttachmentError extends Error {
  constructor(
    message: string,
    readonly reason: "type" | "size" | "total" | "count" | "busy" | "disabled",
  ) {
    super(message)
    this.name = "AttachmentError"
  }
}

/** 文件是否符合 accept：.pdf 这样的扩展名、image/* 这样的大类，或完整的 MIME 类型。 */
function acceptsFile(accept: string, file: File): boolean {
  const name = file.name.toLowerCase()
  const type = file.type.toLowerCase()
  return accept
    .split(",")
    .map((a) => a.trim().toLowerCase())
    .filter(Boolean)
    .some((a) =>
      a.startsWith(".") ? name.endsWith(a) : a.endsWith("/*") ? type.startsWith(a.slice(0, -1)) : type === a,
    )
}

const mib = (n: number) => `${Math.round((n / (1 << 20)) * 10) / 10} MiB`

/**
 * 在已有的附件 current 之后加入 incoming：返回其中符合限制的文件，以及第一个不符合的原因。
 * 加入附件（useAttachments）和发送（send）都按它检查。
 */
function admit(
  current: readonly File[],
  incoming: Iterable<File> | ArrayLike<File>,
  opts: AttachmentOptions,
): { accepted: File[]; error?: AttachmentError } {
  const accept = opts.accept ?? defaultAccept
  const maxSize = opts.maxFileSize ?? defaultMaxFileSize
  const maxFiles = opts.maxFiles ?? defaultMaxFiles
  // 设置了 upload 时消息中只有链接，不限制总大小。
  const maxTotal = opts.upload ? Number.POSITIVE_INFINITY : (opts.maxTotalSize ?? defaultMaxTotalSize)
  let total = current.reduce((n, f) => n + f.size, 0)
  const accepted: File[] = []
  let error: AttachmentError | undefined
  for (const file of Array.from(incoming)) {
    if (current.length + accepted.length >= maxFiles) {
      error ??= new AttachmentError(uiText("tenon.chat.too_many_files", { count: maxFiles }), "count")
      break
    }
    if (!file.type || !acceptsFile(accept, file)) {
      error ??= new AttachmentError(uiText("tenon.chat.unsupported_file", { name: file.name }), "type")
    } else if (file.size > maxSize) {
      error ??= new AttachmentError(
        uiText("tenon.chat.file_too_large", { name: file.name, size: mib(maxSize) }),
        "size",
      )
    } else if (total + file.size > maxTotal) {
      error ??= new AttachmentError(uiText("tenon.chat.total_too_large", { size: mib(maxTotal) }), "total")
    } else {
      total += file.size
      accepted.push(file)
    }
  }
  return { accepted, error }
}

function dataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result))
    r.onerror = () => reject(r.error)
    r.readAsDataURL(file)
  })
}

/** 待发送的一个附件。preview 是图片的预览地址（object URL），其他文件没有。 */
export interface PendingAttachment {
  id: string
  file: File
  preview?: string
}

let attachmentSeq = 0

/**
 * 待发送的附件：选择、粘贴或拖入文件时 add，逐个 remove，发送后 clear。不符合限制的文件不加入，
 * error 是可以直接显示的原因，下一次 add、remove 或 clear 时清除。图片的预览地址在移除和卸载时释放。
 */
export function useAttachments(options: AttachmentOptions = {}) {
  const [items, setItems] = useState<PendingAttachment[]>([])
  const [error, setError] = useState<AttachmentError>()
  const live = useRef(items)
  live.current = items
  useEffect(() => () => release(live.current), [])
  const opts = useRef(options)
  opts.current = options

  const add = useCallback((files: Iterable<File> | ArrayLike<File>) => {
    const { accepted, error } = admit(
      live.current.map((a) => a.file),
      files,
      opts.current,
    )
    setError(error)
    const added = accepted.map((file) => ({
      id: `att-${++attachmentSeq}`,
      file,
      preview: file.type.startsWith("image/") ? URL.createObjectURL(file) : undefined,
    }))
    if (added.length) setItems((cur) => [...cur, ...added])
  }, [])
  const remove = useCallback((id: string) => {
    setError(undefined)
    setItems((cur) => {
      release(cur.filter((a) => a.id === id))
      return cur.filter((a) => a.id !== id)
    })
  }, [])
  const clear = useCallback(() => {
    setError(undefined)
    setItems((cur) => {
      release(cur)
      return []
    })
  }, [])
  return {
    items,
    files: items.map((a) => a.file),
    error: error?.message,
    accept: options.accept ?? defaultAccept,
    add,
    remove,
    clear,
  }
}

function release(items: readonly PendingAttachment[]) {
  for (const a of items) if (a.preview) URL.revokeObjectURL(a.preview)
}

/** useAttachments 返回的附件状态和操作。 */
export type Attachments = ReturnType<typeof useAttachments>

/** 消息中的文件片段，例如显示在用户的气泡中。 */
export function fileParts(parts: UIMessage["parts"]): FileUIPart[] {
  return parts.filter((p): p is FileUIPart => p.type === "file")
}

/** 对话的历史：在显示对话之前读取，交给 useAssistantChat。 */
export function useAssistantHistory({ api, id, paths }: AssistantOptions) {
  const app = useApp()
  const controls = useMemo(() => chatControls<UIMessage>(api, app.session.fetch(fetch), paths), [api, app, paths])
  return useQuery({
    queryKey: ["tenon", "chat", api, id, ...app.session.scope()],
    queryFn: () => controls.messages(id),
    enabled: app.signedIn(),
    staleTime: Number.POSITIVE_INFINITY,
  })
}

/** 等待审批的一次工具调用。 */
export interface PendingApproval {
  /** 交给 respond 的审批 ID。 */
  id: string
  toolName: string
  input: unknown
}

type ToolPartLike = { type: string; state?: string; input?: unknown; approval?: { id: string } }

/** 最后一条回答中等待审批的工具调用。 */
export function pendingApprovals(messages: readonly UIMessage[]): PendingApproval[] {
  const last = [...messages].reverse().find((m) => m.role === "assistant")
  const parts = (last?.parts ?? []) as ToolPartLike[]
  return parts
    .filter((p) => p.state === "approval-requested" && p.approval && p.type.startsWith("tool-"))
    .map((p) => ({ id: p.approval?.id ?? "", toolName: p.type.slice("tool-".length), input: p.input }))
}

/**
 * 对话：消息、状态和操作。messages 是读取到的历史（useAssistantHistory），所以在历史读取之后再渲染用它的组件：
 * 进行中的回答随即接上（resume）。
 */
export function useAssistantChat({
  api,
  id,
  paths,
  messages,
  attachments = {},
}: AssistantOptions & {
  messages: UIMessage[]
  /** 附件的类型、大小和上传方式；false 时不能发送附件。 */
  attachments?: AttachmentOptions | false
}) {
  const app = useApp()
  const authed = useMemo(() => app.session.fetch(fetch), [app])
  const controls = useMemo(() => chatControls<UIMessage>(api, authed, paths), [api, authed, paths])
  // accepted：发送的请求得到了 2xx 响应，服务端收下了消息。
  const accepted = useRef(false)
  const transport = useMemo(
    () =>
      new DefaultChatTransport(
        chatTransport(api, async (input: RequestInfo | URL, init?: RequestInit) => {
          const res = await authed(input, init)
          if (init?.method === "POST" && res.ok) accepted.current = true
          return res
        }),
      ),
    [api, authed],
  )
  // 请求失败时 useChat 的 sendMessage 不抛出错误，只设置 error：用 onError 记下这次发送的错误。
  const failure = useRef<Error | undefined>(undefined)
  const chat = useChat({
    id,
    messages,
    transport,
    resume: true,
    // 审批了全部等待的工具调用后，自动发送审批结果，回答随即继续。
    sendAutomaticallyWhen: lastAssistantMessageIsCompleteWithApprovalResponses,
    onError: (err) => {
      failure.current = err
    },
  })
  const busy = chat.status === "submitted" || chat.status === "streaming"

  /**
   * 发送一条新消息。服务端没有收下它（请求出错或者响应不是 2xx）时，从对话中撤回这条消息并抛出错误，
   * 调用方可以把内容放回输入框。服务端已经收下、之后才出错的（例如回答中途断线），回答在服务端继续，
   * 断线后接上，这时不撤回也不抛出，错误在 error 中。
   */
  const submit = useCallback(
    async (message: Parameters<typeof chat.sendMessage>[0]) => {
      failure.current = undefined
      accepted.current = false
      await chat.sendMessage(message)
      const err = failure.current
      if (!err || accepted.current) return
      chat.setMessages((ms) => (ms[ms.length - 1]?.role === "user" ? ms.slice(0, -1) : ms))
      throw err
    },
    [chat],
  )

  /**
   * 发送：回答进行中时插话，回答恰好已经结束（409 ai.finished）时改为发送新消息。
   * 发送新消息的请求失败、服务端没有收下它时抛出错误，见 submit。
   * files 是附件，按 attachments 检查后随消息发送：设置了 upload 时上传后放链接，否则放 data URL。
   * 插话只能是文字，所以回答进行中带附件时抛出 AttachmentError。
   */
  const send = useCallback(
    async (text: string, files: readonly File[] = []) => {
      if (files.length > 0) {
        if (attachments === false) throw new AttachmentError(uiText("tenon.chat.files_disabled"), "disabled")
        if (busy) throw new AttachmentError(uiText("tenon.chat.files_busy"), "busy")
        const { error } = admit([], files, attachments)
        if (error) throw error
        const upload = attachments.upload ?? dataURL
        const parts: FileUIPart[] = await Promise.all(
          files.map(async (f) => ({
            type: "file" as const,
            mediaType: f.type,
            filename: f.name,
            url: await upload(f),
          })),
        )
        await submit(text ? { text, files: parts } : { files: parts })
        return
      }
      if (!busy) {
        await submit({ text })
        return
      }
      try {
        await controls.steer(id, text)
      } catch (err) {
        if (describeError(err).code !== "ai.finished") throw err
        await submit({ text })
      }
    },
    [busy, submit, controls, id, attachments],
  )

  /** 停止：服务端取消这次回答（客户端断开连接不会停止它），本地的消息流随之结束。 */
  const stop = useCallback(async () => {
    await controls.stop(id)
    await chat.stop()
  }, [chat, controls, id])

  /** 审批一次工具调用；全部审批后自动发送。 */
  const respond = useCallback(
    (approvalId: string, approved: boolean, reason?: string) =>
      chat.addToolApprovalResponse({ id: approvalId, approved, reason }),
    [chat],
  )

  return {
    messages: chat.messages,
    status: chat.status,
    error: chat.error,
    busy,
    pending: pendingApprovals(chat.messages),
    /** 传入的附件选项，false 时不能发送附件。 */
    attachments,
    send,
    stop,
    respond,
  }
}

/** useAssistantChat 返回的对话状态和操作。 */
export type AssistantChat = ReturnType<typeof useAssistantChat>

/** 错误的说明：附件的问题直接显示，其他按接口错误描述。 */
function messageOf(err: unknown): string {
  if (err instanceof AttachmentError) return err.message
  return describeError(err).message ?? (err instanceof Error ? err.message : String(err))
}

/**
 * 输入框：文字、待发送的附件和发送，外观只负责显示。
 *
 * - canSend：有文字或附件、没有等待审批的工具调用，并且回答进行中时只有文字（插话只能是文字）。
 * - submit：发送并清空输入框；没有发出去时（附件不符合限制、上传失败、服务端没有收下消息）恢复文字和附件，
 *   error 是原因。
 * - error：附件不符合限制、发送失败或对话出错的说明，可以直接显示。
 */
export function useAssistantComposer(chat: AssistantChat) {
  const canAttach = chat.attachments !== false
  const attachments = useAttachments(chat.attachments || undefined)
  const [text, setText] = useState("")
  const [failure, setFailure] = useState<unknown>()
  const awaiting = chat.pending.length > 0
  const files = attachments.files
  const canSend = !awaiting && (text.trim() !== "" || files.length > 0) && !(chat.busy && files.length > 0)

  const submit = async () => {
    if (!canSend) return
    const value = text
    setText("")
    attachments.clear()
    setFailure(undefined)
    try {
      await chat.send(value, files)
    } catch (err) {
      setFailure(err)
      setText(value)
      attachments.add(files)
    }
  }

  const problem = failure ?? chat.error
  return {
    text,
    setText,
    attachments,
    canAttach,
    awaiting,
    canSend,
    submit,
    error: attachments.error ?? (problem ? messageOf(problem) : undefined),
  }
}

/** useAssistantComposer 返回的输入框状态和操作。 */
export type AssistantComposer = ReturnType<typeof useAssistantComposer>
