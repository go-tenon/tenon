// 宿主页与嵌入的 iframe 之间的 postMessage 协议，两端共用。
//
// 消息都带着命名空间和版本 { ns: "tenon:embed", v: 1, type, payload }，与宿主页中其他脚本的 postMessage 区分开。
// iframe 发出 ready 后，宿主发出上下文；之后双方只接受对方的窗口和来源发来的消息，见 createWidget 和 connectHost。

export const NAMESPACE = "tenon:embed"
export const VERSION = 1

/** 宿主页面的信息：地址、标题、来源页。单页应用切换路由后重新发送。 */
export interface HostPage {
  url: string
  title: string
  referrer: string
}

/** 宿主发给 iframe 的上下文。 */
export interface HostContext {
  page: HostPage
  /** 宿主后端签名的用户身份（见 Go 的 widget 包），匿名访客为 null。 */
  identity: string | null
  /** 宿主页面的语言，例如 zh-CN。 */
  locale: string
  /** 宿主附带的其他数据，例如当前商品。 */
  data: Record<string, unknown>
}

/** 宿主调用 track 时转给 iframe 的事件。 */
export interface HostEvent {
  event: string
  properties?: Record<string, unknown>
}

/** iframe 发给宿主的消息。 */
export type FrameMessage =
  | { type: "ready" }
  | { type: "close" }
  | { type: "open" }
  | { type: "unread"; payload: { count: number } }
  | { type: "toast"; payload: { message: string } }
  | { type: "identity-expired" }

/** 宿主发给 iframe 的消息。 */
export type HostMessage =
  | { type: "context"; payload: HostContext }
  | { type: "visibility"; payload: { open: boolean } }
  | { type: "track"; payload: HostEvent }
  | { type: "shutdown" }

type Envelope = { ns: typeof NAMESPACE; v: typeof VERSION; type: string; payload?: unknown }

/** 包装一条消息。 */
export function envelope(message: FrameMessage | HostMessage): Envelope {
  return { ns: NAMESPACE, v: VERSION, ...message }
}

function isEnvelope(data: unknown): data is Envelope {
  if (data === null || typeof data !== "object") return false
  const d = data as Partial<Envelope>
  return d.ns === NAMESPACE && d.v === VERSION && typeof d.type === "string"
}

const record = (v: unknown): Record<string, unknown> | null =>
  v !== null && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : null
const str = (v: unknown) => (typeof v === "string" ? v : "")

/** 把收到的数据收窄为 iframe 发来的消息；不是协议中的消息时返回 null。 */
export function readFrameMessage(data: unknown): FrameMessage | null {
  if (!isEnvelope(data)) return null
  const payload = record(data.payload)
  switch (data.type) {
    case "ready":
    case "close":
    case "open":
    case "identity-expired":
      return { type: data.type }
    case "unread": {
      const count = Number(payload?.count)
      return Number.isFinite(count) && count >= 0 ? { type: "unread", payload: { count: Math.floor(count) } } : null
    }
    case "toast":
      return payload && str(payload.message) ? { type: "toast", payload: { message: str(payload.message) } } : null
  }
  return null
}

/** 把收到的数据收窄为宿主发来的消息；不是协议中的消息时返回 null。 */
export function readHostMessage(data: unknown): HostMessage | null {
  if (!isEnvelope(data)) return null
  const payload = record(data.payload)
  switch (data.type) {
    case "context": {
      if (!payload) return null
      const page = record(payload.page) ?? {}
      const identity = str(payload.identity).trim()
      return {
        type: "context",
        payload: {
          page: { url: str(page.url), title: str(page.title), referrer: str(page.referrer) },
          identity: identity || null,
          locale: str(payload.locale),
          data: record(payload.data) ?? {},
        },
      }
    }
    case "visibility":
      return payload && typeof payload.open === "boolean"
        ? { type: "visibility", payload: { open: payload.open } }
        : null
    case "track": {
      const event = str(payload?.event).trim()
      if (!event) return null
      const properties = record(payload?.properties)
      return { type: "track", payload: properties ? { event, properties } : { event } }
    }
    case "shutdown":
      return { type: "shutdown" }
  }
  return null
}
