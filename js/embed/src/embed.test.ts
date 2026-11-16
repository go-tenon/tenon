// @vitest-environment happy-dom
// 宿主页与 iframe 之间的协议、iframe 一端的连接和宿主一端的挂件。
import { afterEach, describe, expect, it, vi } from "vitest"
import { connectHost } from "./frame"
import { createWidget } from "./host"
import { envelope, readFrameMessage, readHostMessage } from "./protocol"

afterEach(() => {
  document.body.innerHTML = ""
})

describe("protocol", () => {
  it("只接受带命名空间和版本的消息，并收窄内容", () => {
    expect(readHostMessage({ type: "context" })).toBeNull()
    expect(readHostMessage({ ns: "tenon:embed", v: 2, type: "shutdown" })).toBeNull()
    expect(
      readHostMessage(
        envelope({
          type: "context",
          payload: {
            page: { url: "https://shop.example.com/a", title: "A", referrer: "" },
            identity: "  ",
            locale: "zh-CN",
            data: { sku: "x" },
          },
        }),
      ),
    ).toEqual({
      type: "context",
      payload: {
        page: { url: "https://shop.example.com/a", title: "A", referrer: "" },
        identity: null,
        locale: "zh-CN",
        data: { sku: "x" },
      },
    })
    expect(readHostMessage({ ns: "tenon:embed", v: 1, type: "visibility", payload: { open: "yes" } })).toBeNull()
    expect(readHostMessage({ ns: "tenon:embed", v: 1, type: "track", payload: { event: " " } })).toBeNull()
    expect(readFrameMessage({ ns: "tenon:embed", v: 1, type: "unread", payload: { count: -1 } })).toBeNull()
    expect(readFrameMessage({ ns: "tenon:embed", v: 1, type: "unread", payload: { count: 2.7 } })).toEqual({
      type: "unread",
      payload: { count: 2 },
    })
    expect(readFrameMessage({ ns: "tenon:embed", v: 1, type: "eval", payload: {} })).toBeNull()
  })
})

/** 一个假的父窗口：记录 iframe 发给它的消息。 */
function fakeParent() {
  const sent: { data: unknown; origin: string }[] = []
  return {
    sent,
    win: { postMessage: (data: unknown, origin: string) => sent.push({ data, origin }) } as unknown as Window,
  }
}

const message = (source: unknown, origin: string, data: unknown) =>
  new MessageEvent("message", { source: source as Window, origin, data })

describe("connectHost", () => {
  it("发出 ready；锁定直接父窗口的第一个来源，之后只接受它；消息只发给锁定的来源", () => {
    const parent = fakeParent()
    const other = fakeParent()
    const host = connectHost({ parent: parent.win })
    expect(parent.sent).toEqual([{ data: envelope({ type: "ready" }), origin: "*" }])
    host.setUnread(3) // 握手之前不发送
    expect(parent.sent).toHaveLength(1)

    const ctx = {
      page: { url: "https://shop.example.com/", title: "店铺", referrer: "" },
      identity: "sig",
      locale: "zh-CN",
      data: {},
    }
    // 宿主页中其他窗口发来的同样形状的消息被忽略，不能锁定来源。
    window.dispatchEvent(message(other.win, "https://evil.example.com", envelope({ type: "context", payload: ctx })))
    expect(host.get().connected).toBe(false)

    window.dispatchEvent(message(parent.win, "https://shop.example.com", envelope({ type: "context", payload: ctx })))
    expect(host.get()).toMatchObject({
      connected: true,
      origin: "https://shop.example.com",
      context: { identity: "sig" },
    })

    // 来源锁定后，同一个父窗口换了来源（例如导航到其他网站）也不接受。
    window.dispatchEvent(
      message(parent.win, "https://evil.example.com", envelope({ type: "visibility", payload: { open: true } })),
    )
    expect(host.get().open).toBeNull()

    const events: unknown[] = []
    host.onEvent((e) => events.push(e))
    window.dispatchEvent(
      message(parent.win, "https://shop.example.com", envelope({ type: "visibility", payload: { open: true } })),
    )
    window.dispatchEvent(
      message(
        parent.win,
        "https://shop.example.com",
        envelope({ type: "track", payload: { event: "add-to-cart", properties: { sku: "x" } } }),
      ),
    )
    expect(host.get().open).toBe(true)
    expect(events).toEqual([{ event: "add-to-cart", properties: { sku: "x" } }])

    host.setUnread(3)
    host.close()
    expect(parent.sent.slice(1)).toEqual([
      { data: envelope({ type: "unread", payload: { count: 3 } }), origin: "https://shop.example.com" },
      { data: envelope({ type: "close" }), origin: "https://shop.example.com" },
    ])
    host.disconnect()
  })

  it("设置了允许的来源时，只接受这些来源", () => {
    const parent = fakeParent()
    const host = connectHost({ parent: parent.win, origins: ["https://shop.example.com"] })
    window.dispatchEvent(message(parent.win, "https://other.example.com", envelope({ type: "shutdown" })))
    expect(host.get().connected).toBe(false)
    window.dispatchEvent(message(parent.win, "https://shop.example.com", envelope({ type: "shutdown" })))
    expect(host.get()).toMatchObject({ connected: true, shutdown: true })
    host.disconnect()
  })

  it("不在 iframe 中时不发送任何消息", () => {
    const post = vi.spyOn(window, "postMessage")
    const host = connectHost()
    host.close()
    expect(post).not.toHaveBeenCalled()
    host.disconnect()
  })
})

describe("createWidget", () => {
  function setup(options: Partial<Parameters<typeof createWidget>[0]> = {}) {
    const widget = createWidget({
      src: "https://app.example.com/widget/acme",
      title: "客服",
      identity: "sig-1",
      ...options,
    })
    const root = document.body.lastElementChild as HTMLElement
    const shadow = root.shadowRoot as ShadowRoot
    const frame = shadow.querySelector("iframe") as HTMLIFrameElement
    // happy-dom 不加载 iframe：用一个假的窗口代替 contentWindow，记录发给它的消息。
    const sent: unknown[] = []
    const contentWindow = { postMessage: (data: unknown, origin: string) => sent.push({ data, origin }) }
    Object.defineProperty(frame, "contentWindow", { value: contentWindow })
    const fromFrame = (data: unknown, origin = "https://app.example.com", source: unknown = frame.contentWindow) =>
      window.dispatchEvent(message(source, origin, data))
    return { widget, shadow, frame, sent, fromFrame }
  }

  it("入口按钮展开和收起面板；ready 后发出上下文（带签名的身份），只发给 iframe 的来源", () => {
    const { widget, shadow, frame, sent, fromFrame } = setup()
    expect(frame.src).toBe("https://app.example.com/widget/acme")
    const button = shadow.querySelector("button") as HTMLButtonElement
    expect(button.getAttribute("aria-label")).toBe("客服")

    fromFrame(envelope({ type: "ready" }))
    expect(sent[0]).toMatchObject({
      origin: "https://app.example.com",
      data: { type: "context", payload: { identity: "sig-1", page: { url: location.href } } },
    })
    expect(sent[1]).toMatchObject({ data: { type: "visibility", payload: { open: false } } })

    button.click()
    expect(widget.isOpen()).toBe(true)
    expect(shadow.querySelector(".panel")?.getAttribute("data-open")).toBe("true")
    expect(button.getAttribute("aria-expanded")).toBe("true")
    expect(sent.at(-2)).toMatchObject({ data: { type: "visibility", payload: { open: true } } })

    fromFrame(envelope({ type: "close" }))
    expect(widget.isOpen()).toBe(false)
    widget.destroy()
    expect(document.body.children).toHaveLength(0)
  })

  it("只接受这个 iframe、这个来源的消息；未读数显示在按钮上；提示；身份失效的事件", () => {
    const { widget, shadow, fromFrame } = setup()
    const unread = vi.fn()
    const expired = vi.fn()
    widget.on("unread", unread)
    widget.on("identity-expired", expired)

    fromFrame(envelope({ type: "unread", payload: { count: 5 } }), "https://evil.example.com")
    fromFrame(envelope({ type: "unread", payload: { count: 5 } }), "https://app.example.com", window)
    expect(unread).not.toHaveBeenCalled()

    fromFrame(envelope({ type: "unread", payload: { count: 120 } }))
    const badge = shadow.querySelector(".badge") as HTMLElement
    expect(badge.hidden).toBe(false)
    expect(badge.textContent).toBe("99+")
    fromFrame(envelope({ type: "unread", payload: { count: 0 } }))
    expect(badge.hidden).toBe(true)

    fromFrame(envelope({ type: "toast", payload: { message: "第一条" } }))
    fromFrame(envelope({ type: "toast", payload: { message: "客服回复了你" } }))
    expect(shadow.querySelectorAll(".toast")).toHaveLength(1) // 新的提示替换旧的
    const toast = shadow.querySelector(".toast") as HTMLElement
    expect(toast.textContent).toBe("客服回复了你")
    toast.click()
    expect(widget.isOpen()).toBe(true)

    fromFrame(envelope({ type: "identity-expired" }))
    expect(expired).toHaveBeenCalledOnce()
    widget.destroy()
  })

  it("单页应用切换路由后重新发送页面；login 重新加载 iframe 并带上新的身份；track 转给 iframe", () => {
    vi.useFakeTimers()
    const { widget, frame, sent, fromFrame } = setup({ identity: undefined })
    fromFrame(envelope({ type: "ready" }))
    history.pushState({}, "", "/products/42")
    vi.advanceTimersByTime(300)
    expect(sent.at(-1)).toMatchObject({
      data: { type: "context", payload: { page: { url: location.href }, identity: null } },
    })

    widget.track("add-to-cart", { sku: "x" })
    expect(sent.at(-1)).toMatchObject({
      data: { type: "track", payload: { event: "add-to-cart", properties: { sku: "x" } } },
    })

    const before = sent.length
    widget.login("sig-2")
    widget.track("ignored") // 重新加载完成之前不发送
    expect(sent).toHaveLength(before)
    expect(frame.src).toBe("https://app.example.com/widget/acme")
    fromFrame(envelope({ type: "ready" }))
    expect(sent[before]).toMatchObject({ data: { type: "context", payload: { identity: "sig-2" } } })
    widget.destroy()
    vi.useRealTimers()
  })

  it("不显示入口按钮时由宿主调用 open", () => {
    const { widget, shadow } = setup({ launcher: false })
    expect(shadow.querySelector("button")).toBeNull()
    widget.open()
    expect(shadow.querySelector(".panel")?.getAttribute("data-open")).toBe("true")
    widget.destroy()
  })
})
