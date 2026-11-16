// iframe 中的页面与宿主页的连接：收到宿主的上下文、可见性和事件，向宿主报告关闭、未读数、提示和身份失效。

import { envelope, type FrameMessage, type HostContext, type HostEvent, readHostMessage } from "./protocol"

/** connectHost 的选项。 */
export interface ConnectOptions {
  /**
   * 允许的宿主来源，例如租户登记的域名。设置后只接受这些来源的消息；不设置时接受直接父窗口的第一条
   * 合法消息，并锁定它的来源。允许嵌入的网站同时由服务端的 frame-ancestors 限制（SPAOptions.FrameAncestorsFunc）。
   */
  origins?: string[]
  /** 测试用：当前窗口和宿主窗口，默认 window 和 window.parent。 */
  window?: Window
  parent?: Window
}

/** 与宿主的连接的状态。 */
export interface HostState {
  /** 宿主是否已经连接（收到了第一条消息）。 */
  connected: boolean
  /** 锁定的宿主来源。 */
  origin: string | null
  context: HostContext | null
  /** 挂件是否展开；没有收到时为 null。 */
  open: boolean | null
  /** 宿主要求关闭（卸载挂件）。 */
  shutdown: boolean
}

/** connectHost 的返回值。 */
export interface HostConnection {
  /** 当前状态；每次变化都是新的对象，可以交给 useSyncExternalStore。 */
  get(): HostState
  subscribe(listener: () => void): () => void
  /** 宿主转来的事件（track）。 */
  onEvent(listener: (event: HostEvent) => void): () => void
  /** 请宿主收起挂件。 */
  close(): void
  /** 请宿主展开挂件。 */
  open(): void
  /** 更新宿主按钮上的未读数，0 表示没有。 */
  setUnread(count: number): void
  /** 在宿主页显示一条提示（挂件收起时）。 */
  toast(message: string): void
  /** 宿主传来的身份已经失效（例如过期），宿主应当重新签发并调用 login。 */
  identityExpired(): void
  /** 停止监听。 */
  disconnect(): void
}

/**
 * 连接宿主页。在 iframe 中的页面启动时调用一次：立即发出 ready，宿主随后发来上下文。
 * 不在 iframe 中（直接打开页面）时也可以调用，状态保持未连接，发出的消息被忽略。
 */
export function connectHost(options: ConnectOptions = {}): HostConnection {
  const self = options.window ?? window
  const parent = options.parent ?? self.parent
  const embedded = parent !== self
  let state: HostState = { connected: false, origin: null, context: null, open: null, shutdown: false }
  const listeners = new Set<() => void>()
  const eventListeners = new Set<(event: HostEvent) => void>()
  const set = (next: Partial<HostState>) => {
    state = { ...state, ...next }
    for (const l of listeners) l()
  }

  const send = (message: FrameMessage) => {
    if (!embedded) return
    // 握手之前不知道宿主的来源，ready 不带任何数据，可以发给任何来源；其他消息只发给锁定的来源。
    const target = state.origin ?? (message.type === "ready" ? "*" : null)
    if (!target) return
    parent.postMessage(envelope(message), target)
  }

  const onMessage = (event: MessageEvent) => {
    // 只接受直接父窗口的消息：宿主页中其他 iframe 发来的同样形状的消息不能锁定来源。
    if (event.source !== parent) return
    if (options.origins && !options.origins.includes(event.origin)) return
    if (state.origin !== null && event.origin !== state.origin) return
    const message = readHostMessage(event.data)
    if (!message) return
    const lock = state.origin === null ? { connected: true, origin: event.origin } : {}
    switch (message.type) {
      case "context":
        set({ ...lock, context: message.payload })
        return
      case "visibility":
        set({ ...lock, open: message.payload.open })
        return
      case "track":
        if (Object.keys(lock).length) set(lock)
        for (const l of eventListeners) l(message.payload)
        return
      case "shutdown":
        set({ ...lock, shutdown: true })
        return
    }
  }

  self.addEventListener("message", onMessage)
  send({ type: "ready" })

  return {
    get: () => state,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    onEvent(listener) {
      eventListeners.add(listener)
      return () => eventListeners.delete(listener)
    },
    close: () => send({ type: "close" }),
    open: () => send({ type: "open" }),
    setUnread: (count) => send({ type: "unread", payload: { count: Math.max(0, Math.floor(count)) } }),
    toast: (message) => send({ type: "toast", payload: { message } }),
    identityExpired: () => send({ type: "identity-expired" }),
    disconnect: () => self.removeEventListener("message", onMessage),
  }
}
