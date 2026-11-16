// 宿主页上的挂件：入口按钮、面板和其中的 iframe。产品把它打包成一个小脚本（例如 /widget.js），
// 第三方网站用 <script> 引入。它不依赖任何包，样式放在 Shadow DOM 中，与宿主页面互不影响。

import { envelope, type FrameMessage, type HostContext, type HostMessage, readFrameMessage } from "./protocol"

/** createWidget 的选项。 */
export interface WidgetOptions {
  /** iframe 中页面的完整地址，例如 https://app.example.com/widget/acme。宿主只接受这个来源的消息。 */
  src: string
  /** 宿主后端签名的用户身份（见 Go 的 widget 包）；匿名访客不设置。 */
  identity?: string
  /** 宿主附带的其他数据，随上下文发给 iframe。 */
  data?: Record<string, unknown>
  /** 面板和 iframe 的名称，供读屏软件使用。 */
  title?: string
  /** 是否显示右下角的入口按钮，默认 true；不显示时由宿主调用 open。 */
  launcher?: boolean
  /** 入口按钮和面板在哪一侧，默认 right。 */
  position?: "right" | "left"
  /** 入口按钮的颜色，默认 #111827。 */
  color?: string
  /** 面板的大小（像素），默认 400×640；窄屏时面板占满屏幕。 */
  width?: number
  height?: number
  /** 挂件放在哪个元素中，默认 document.body。 */
  container?: HTMLElement
}

/** 挂件的事件。 */
export interface WidgetEvents {
  ready: () => void
  open: () => void
  close: () => void
  unread: (count: number) => void
  toast: (message: string) => void
  /** iframe 报告身份失效：宿主应当重新签发身份并调用 login。 */
  "identity-expired": () => void
}

/** createWidget 的返回值，通常挂在 window 上供宿主页调用。 */
export interface Widget {
  open(): void
  close(): void
  toggle(): void
  isOpen(): boolean
  /** 以签名的身份登录（宿主的用户登录后）：重新加载 iframe，丢弃上一个身份的状态。 */
  login(identity: string): void
  /** 回到匿名访客。 */
  logout(): void
  /** 把宿主页的事件转给 iframe，例如加入购物车。 */
  track(event: string, properties?: Record<string, unknown>): void
  on<K extends keyof WidgetEvents>(name: K, listener: WidgetEvents[K]): () => void
  /** 移除挂件。 */
  destroy(): void
}

const css = (o: Required<Pick<WidgetOptions, "position" | "color" | "width" | "height">>) => `
:host{all:initial}
.panel{box-sizing:border-box;position:fixed;z-index:2147483000;${o.position}:24px;bottom:96px;width:${o.width}px;height:${o.height}px;
max-width:calc(100vw - 48px);max-height:calc(100dvh - 120px);overflow:hidden;border-radius:16px;background:#fff;
box-shadow:0 24px 64px rgba(15,23,42,.24);opacity:0;visibility:hidden;transform:translateY(8px);
transition:opacity .18s ease-out,transform .18s ease-out,visibility .18s}
.panel[data-open="true"]{opacity:1;visibility:visible;transform:none}
.panel[data-launcher="false"]{bottom:24px}
iframe{display:block;width:100%;height:100%;border:0}
.launcher{position:fixed;z-index:2147483000;${o.position}:24px;bottom:24px;width:56px;height:56px;border:0;border-radius:50%;
background:${o.color};color:#fff;cursor:pointer;box-shadow:0 8px 24px rgba(15,23,42,.24);display:flex;align-items:center;justify-content:center}
.launcher:focus-visible{outline:3px solid ${o.color};outline-offset:3px}
.launcher svg{width:26px;height:26px}
.badge{position:absolute;top:-2px;right:-2px;min-width:20px;height:20px;padding:0 6px;box-sizing:border-box;border-radius:10px;
background:#ef4444;color:#fff;font:600 12px/20px system-ui,sans-serif;text-align:center}
.toast{position:fixed;z-index:2147483000;${o.position}:24px;bottom:92px;max-width:280px;padding:10px 14px;border-radius:12px;
background:#fff;color:#111827;font:14px/1.5 system-ui,sans-serif;box-shadow:0 8px 24px rgba(15,23,42,.18);cursor:pointer}
@media (max-width:640px){.panel{inset:0;width:100%;height:100%;max-width:none;max-height:none;border-radius:0}}
`

const chat =
  '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12a8.6 8.6 0 0 1-9 8.5 9.8 9.8 0 0 1-3.8-.8L3 21l1.4-4.7A8.2 8.2 0 0 1 3 12a8.6 8.6 0 0 1 9-8.5A8.6 8.6 0 0 1 21 12Z"/></svg>'
const cross =
  '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"/></svg>'

/** 在宿主页上创建挂件。 */
export function createWidget(options: WidgetOptions): Widget {
  const frameURL = new URL(options.src, location.href)
  const frameOrigin = frameURL.origin
  const title = options.title ?? "Chat"
  const launcher = options.launcher ?? true
  let identity = options.identity ?? null
  let opened = false
  let ready = false
  let pageTimer = 0
  type Listener = (...args: never[]) => void
  const listeners = new Map<keyof WidgetEvents, Set<Listener>>()
  const emit = <K extends keyof WidgetEvents>(name: K, ...args: Parameters<WidgetEvents[K]>) => {
    for (const l of listeners.get(name) ?? []) {
      try {
        ;(l as (...a: unknown[]) => void)(...args)
      } catch (err) {
        console.warn(`widget ${name} listener failed`, err)
      }
    }
  }

  const root = document.createElement("div")
  const shadow = root.attachShadow({ mode: "open" })
  const style = document.createElement("style")
  style.textContent = css({
    position: options.position ?? "right",
    color: options.color ?? "#111827",
    width: options.width ?? 400,
    height: options.height ?? 640,
  })
  const panel = document.createElement("div")
  panel.className = "panel"
  panel.dataset.open = "false"
  panel.dataset.launcher = String(launcher)
  panel.setAttribute("role", "dialog")
  panel.setAttribute("aria-label", title)
  panel.setAttribute("aria-hidden", "true")
  const frame = document.createElement("iframe")
  frame.title = title
  frame.src = frameURL.href
  frame.referrerPolicy = "strict-origin-when-cross-origin"
  frame.allow = "clipboard-write"
  panel.appendChild(frame)
  shadow.append(style, panel)

  let button: HTMLButtonElement | null = null
  let badge: HTMLSpanElement | null = null
  if (launcher) {
    button = document.createElement("button")
    button.type = "button"
    button.className = "launcher"
    button.setAttribute("aria-label", title)
    button.setAttribute("aria-expanded", "false")
    button.innerHTML = chat
    badge = document.createElement("span")
    badge.className = "badge"
    badge.hidden = true
    button.appendChild(badge)
    button.addEventListener("click", () => toggle())
    shadow.appendChild(button)
  }
  ;(options.container ?? document.body).appendChild(root)

  const post = (message: HostMessage) => {
    if (!ready || !frame.contentWindow) return
    frame.contentWindow.postMessage(envelope(message), frameOrigin)
  }
  const context = (): HostContext => ({
    page: { url: location.href, title: document.title, referrer: document.referrer },
    identity,
    locale: document.documentElement.lang || navigator.language,
    data: options.data ?? {},
  })

  const setOpen = (next: boolean) => {
    if (next === opened) return
    opened = next
    panel.dataset.open = String(next)
    panel.setAttribute("aria-hidden", String(!next))
    if (button) {
      button.setAttribute("aria-expanded", String(next))
      button.innerHTML = next ? cross : chat
      if (badge) button.appendChild(badge)
    }
    post({ type: "visibility", payload: { open: next } })
    if (next) {
      post({ type: "context", payload: context() })
      frame.focus()
    } else {
      button?.focus()
    }
    emit(next ? "open" : "close")
  }
  const toggle = () => setOpen(!opened)

  const showToast = (message: string) => {
    if (opened) return
    // 同一时间只显示一条提示：新的提示替换旧的，不会叠在同一个位置。
    shadow.querySelector(".toast")?.remove()
    const toast = document.createElement("div")
    toast.className = "toast"
    toast.setAttribute("role", "status")
    toast.textContent = message
    toast.addEventListener("click", () => {
      toast.remove()
      setOpen(true)
    })
    shadow.appendChild(toast)
    setTimeout(() => toast.remove(), 6000)
  }

  const onMessage = (event: MessageEvent) => {
    // 只接受这个 iframe、这个来源发来的消息。
    if (event.source !== frame.contentWindow || event.origin !== frameOrigin) return
    const message = readFrameMessage(event.data)
    if (!message) return
    handle(message)
  }
  const handle = (message: FrameMessage) => {
    switch (message.type) {
      case "ready":
        ready = true
        post({ type: "context", payload: context() })
        post({ type: "visibility", payload: { open: opened } })
        emit("ready")
        return
      case "close":
        setOpen(false)
        return
      case "open":
        setOpen(true)
        return
      case "unread":
        if (badge) {
          badge.hidden = message.payload.count === 0
          badge.textContent = message.payload.count > 99 ? "99+" : String(message.payload.count)
        }
        emit("unread", message.payload.count)
        return
      case "toast":
        showToast(message.payload.message)
        emit("toast", message.payload.message)
        return
      case "identity-expired":
        emit("identity-expired")
        return
    }
  }
  window.addEventListener("message", onMessage)

  // 单页应用切换路由后（等标题更新）重新发送页面信息。
  const schedulePage = () => {
    clearTimeout(pageTimer)
    pageTimer = window.setTimeout(() => post({ type: "context", payload: context() }), 300)
  }
  const history = window.history
  const originals = { pushState: history.pushState, replaceState: history.replaceState }
  for (const name of ["pushState", "replaceState"] as const) {
    history[name] = function (this: History, ...args: Parameters<History["pushState"]>) {
      const result = originals[name].apply(this, args)
      schedulePage()
      return result
    }
  }
  window.addEventListener("popstate", schedulePage)
  window.addEventListener("hashchange", schedulePage)

  // 切换身份时重新加载 iframe，丢弃上一个身份的全部状态和进行中的请求。
  const reload = () => {
    ready = false
    frame.src = frameURL.href
  }

  return {
    open: () => setOpen(true),
    close: () => setOpen(false),
    toggle,
    isOpen: () => opened,
    login(next) {
      identity = next || null
      reload()
    },
    logout() {
      identity = null
      reload()
    },
    track(event, properties) {
      post({ type: "track", payload: properties ? { event, properties } : { event } })
    },
    on(name, listener) {
      let set = listeners.get(name)
      if (!set) {
        set = new Set()
        listeners.set(name, set)
      }
      const l = listener as Listener
      set.add(l)
      return () => {
        set.delete(l)
      }
    },
    destroy() {
      post({ type: "shutdown" })
      window.removeEventListener("message", onMessage)
      window.removeEventListener("popstate", schedulePage)
      window.removeEventListener("hashchange", schedulePage)
      history.pushState = originals.pushState
      history.replaceState = originals.replaceState
      clearTimeout(pageTimer)
      root.remove()
    },
  }
}
