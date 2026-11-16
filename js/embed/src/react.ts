// React 中的宿主连接：useHost 返回宿主的上下文和状态，以及向宿主报告的方法。

import { useMemo, useSyncExternalStore } from "react"
import { type ConnectOptions, connectHost, type HostConnection, type HostState } from "./frame"

let shared: HostConnection | null = null

/** 页面中只连接一次宿主：多个组件共用同一个连接。 */
export function hostConnection(options?: ConnectOptions): HostConnection {
  shared ??= connectHost(options)
  return shared
}

/**
 * iframe 中的页面读取宿主的上下文：
 *
 *	const host = useHost({ origins: tenant.domains })
 *	host.state.context?.identity // 宿主签名的身份，交给换取访客令牌的接口
 *	host.setUnread(3)
 */
export function useHost(options?: ConnectOptions): HostConnection & { state: HostState } {
  const connection = useMemo(() => hostConnection(options), [options])
  const state = useSyncExternalStore(connection.subscribe, connection.get, connection.get)
  return useMemo(() => ({ ...connection, state }), [connection, state])
}
