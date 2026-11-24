import { afterEach, describe, expect, it } from "vitest"
import { createI18n } from "./i18n"
import { registerRequestStateStore } from "./request-store"
import { createSession } from "./session"

describe("SSR 请求级状态隔离", () => {
  afterEach(() => {
    registerRequestStateStore(undefined)
  })

  it("并发请求的 session 与 i18n 单例互不串号", async () => {
    let active: Map<object, unknown> | undefined
    registerRequestStateStore(() => active)

    const session = createSession({ load: async () => null })
    const i18n = createI18n({
      messages: { "zh-CN": { hello: "你好" }, en: { hello: "Hello" } },
      defaultLanguage: "zh-CN",
    })

    const reqA = new Map<object, unknown>()
    const reqB = new Map<object, unknown>()

    // 请求 A 写入身份与英文语言
    active = reqA
    session.setIdentity({ account: "alice", tenant: "acme" })
    i18n.setLanguage("en")
    expect(session.identity()).toEqual({ account: "alice", tenant: "acme" })
    expect(i18n.language()).toBe("en")
    expect(i18n.t("hello")).toBe("Hello")

    // 请求 B 保持未登录和默认中文，互不污染
    active = reqB
    expect(session.signedIn()).toBe(false)
    expect(session.identity()).toBeNull()
    expect(i18n.language()).toBe("zh-CN")
    expect(i18n.t("hello")).toBe("你好")

    session.setIdentity({ account: "bob", tenant: "globex" })
    expect(session.identity()).toEqual({ account: "bob", tenant: "globex" })

    // 切回请求 A 仍保持 alice 与 en
    active = reqA
    expect(session.identity()).toEqual({ account: "alice", tenant: "acme" })
    expect(i18n.language()).toBe("en")
  })
})
