// AI 对话：在 Vercel AI SDK 的 useChat 之上，按框架的 AI 对话协议接好消息流、历史、停止、插话和工具的审批。
// 这是单独的入口 @go-tenon/shell/chat：只有用到 AI 对话的前端才需要安装 ai 和 @ai-sdk/react。

import { useChat } from "@ai-sdk/react"
import { type ChatPaths, chatControls, chatTransport, describeError } from "@go-tenon/client"
import { useQuery } from "@tanstack/react-query"
import { DefaultChatTransport, lastAssistantMessageIsCompleteWithApprovalResponses, type UIMessage } from "ai"
import { useCallback, useMemo } from "react"
import { useApp } from "./app"

/** 对话的接口：api 是发送接口的地址，例如 /api/v1/assistant/chat；其他接口按约定拼接，见 chatControls。 */
export interface AssistantOptions {
  api: string
  /** 对话的 ID。 */
  id: string
  paths?: ChatPaths
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
export function useAssistantChat({ api, id, paths, messages }: AssistantOptions & { messages: UIMessage[] }) {
  const app = useApp()
  const authed = useMemo(() => app.session.fetch(fetch), [app])
  const controls = useMemo(() => chatControls<UIMessage>(api, authed, paths), [api, authed, paths])
  const transport = useMemo(() => new DefaultChatTransport(chatTransport(api, authed)), [api, authed])
  const chat = useChat({
    id,
    messages,
    transport,
    resume: true,
    // 审批了全部等待的工具调用后，自动发送审批结果，回答随即继续。
    sendAutomaticallyWhen: lastAssistantMessageIsCompleteWithApprovalResponses,
  })
  const busy = chat.status === "submitted" || chat.status === "streaming"

  /** 发送：回答进行中时插话，回答恰好已经结束（409 ai.finished）时改为发送新消息。 */
  const send = useCallback(
    async (text: string) => {
      if (!busy) {
        await chat.sendMessage({ text })
        return
      }
      try {
        await controls.steer(id, text)
      } catch (err) {
        if (describeError(err).code !== "ai.finished") throw err
        await chat.sendMessage({ text })
      }
    },
    [busy, chat, controls, id],
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
    send,
    stop,
    respond,
  }
}

export type AssistantChat = ReturnType<typeof useAssistantChat>
