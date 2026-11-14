// 实时频道：Centrifuge SDK 的认证选项。

import type { Session } from "./session"

/**
 * 返回交给 Centrifuge SDK 的认证选项 { token, getToken }：连接时使用当前的 access token；token 过期后 SDK 调用
 * getToken，它通过会话刷新（与其他请求共用同一次刷新），刷新失败时返回空字符串，SDK 停止重连。
 *
 *	new Centrifuge("wss://example.com/realtime", { ...realtimeAuth(session) })
 */
export function realtimeAuth(session: Pick<Session, "accessToken" | "auth">): {
  token: string
  getToken: () => Promise<string>
} {
  return {
    token: session.accessToken(),
    getToken: async () => (await session.auth.refresh()) ?? "",
  }
}
