// AI 对话：对接 ai/chat 的接口，交给 Vercel AI SDK 的 useChat。

import { ensureOK, type FetchLike, globalFetch } from "./fetch"

/** 对话各接口的路径，默认按 {api}、{api}/{id}/stream、/stop、/steer、/messages 拼接。 */
export interface ChatPaths {
  stop?: (id: string) => string
  steer?: (id: string) => string
  messages?: (id: string) => string
}

/**
 * 返回交给 AI SDK 的 DefaultChatTransport 的选项：api 是发送接口的地址；fetch 通常是 session.fetch(fetch)，
 * 请求带上 access token，401 时刷新再重试；发送时只发最后一条消息（历史保存在服务端）。恢复进行中的回答时，
 * AI SDK 默认请求 {api}/{id}/stream，与服务端的约定相同。
 *
 * 响应不是 2xx 时抛出 HttpError（读出 Problem Details），useChat 的 error 因此可以用 describeError 描述，
 * 例如 409 ai.busy；不然 AI SDK 会把整个响应体作为错误信息。
 *
 *	useChat({ id, resume: true, transport: new DefaultChatTransport(chatTransport("/api/assistant/chat", session.fetch(fetch))) })
 */
export function chatTransport<F extends FetchLike>(
  api: string,
  fetch?: F,
): {
  api: string
  fetch: F
  prepareSendMessagesRequest: (options: { id: string; messages: readonly unknown[] }) => {
    body: { id: string; message: unknown }
  }
} {
  const f: FetchLike = fetch ?? globalFetch()
  return {
    api,
    fetch: (async (url: string, init?: Parameters<FetchLike>[1]) => ensureOK(await f(url, init))) as F,
    prepareSendMessagesRequest: ({ id, messages }) => ({ body: { id, message: messages[messages.length - 1] } }),
  }
}

/** 对话的停止、插话和历史。 */
export interface ChatControls<M> {
  /** 停止进行中的回答；没有进行中的回答时直接成功。 */
  stop(id: string): Promise<void>
  /** 在进行中的回答里插话；回答已经结束时请求失败，改为发送新消息。 */
  steer(id: string, text: string): Promise<void>
  /** 读取对话的历史，交给 useChat 的 messages。 */
  messages(id: string): Promise<M[]>
}

/** 返回对话的停止、插话和历史，api 与 chatTransport 相同，fetch 通常是 session.fetch()。失败时抛出 HttpError。 */
export function chatControls<M = unknown>(
  api: string,
  fetch: FetchLike = globalFetch(),
  paths: ChatPaths = {},
): ChatControls<M> {
  const at = (id: string, action: string) => `${api}/${encodeURIComponent(id)}/${action}`
  const json = { "Content-Type": "application/json" }
  return {
    async stop(id) {
      await ensureOK(await fetch(paths.stop?.(id) ?? at(id, "stop"), { method: "POST" }))
    },
    async steer(id, text) {
      await ensureOK(
        await fetch(paths.steer?.(id) ?? at(id, "steer"), {
          method: "POST",
          headers: json,
          body: JSON.stringify({ text }),
        }),
      )
    },
    async messages(id) {
      const res = await ensureOK(await fetch(paths.messages?.(id) ?? at(id, "messages"), { method: "GET" }))
      return (await res.json()) as M[]
    },
  }
}
