import { describe, expect, it } from "vitest"
import { createI18n, formatMessage, matchLanguage } from "./i18n"

const messages = {
  "zh-CN": {
    "inbox.empty": "没有会话",
    "inbox.waiting": "{{.count}} 个会话等待回复",
    "inbox.only_zh": "只有中文",
  },
  en: {
    "inbox.empty": "No conversations",
    "inbox.waiting": { one: "{{.count}} conversation waiting", other: "{{.count}} conversations waiting" },
  },
} as const

describe("createI18n", () => {
  it("按偏好选择初始语言，匹配不到时用默认语言", () => {
    expect(createI18n({ messages, defaultLanguage: "zh-CN", preferred: () => ["en-US", "zh-CN"] }).language()).toBe(
      "en",
    )
    expect(createI18n({ messages, defaultLanguage: "zh-CN", preferred: () => ["fr"] }).language()).toBe("zh-CN")
    expect(createI18n({ messages, defaultLanguage: "zh-CN", preferred: () => undefined }).language()).toBe("zh-CN")
  })

  it("翻译、复数、占位符，缺少时回退到默认语言和 ID", () => {
    const i18n = createI18n({ messages, defaultLanguage: "zh-CN", preferred: () => ["en"] })
    expect(i18n.t("inbox.empty")).toBe("No conversations")
    expect(i18n.t("inbox.waiting", { count: 1 })).toBe("1 conversation waiting")
    expect(i18n.t("inbox.waiting", { count: 3 })).toBe("3 conversations waiting")
    expect(i18n.t("inbox.only_zh")).toBe("只有中文")
    expect(i18n.t("missing.id" as never)).toBe("missing.id")
  })

  it("setLanguage 通知订阅者；不传时按偏好重新选择", () => {
    const i18n = createI18n({ messages, defaultLanguage: "zh-CN", preferred: () => ["en"] })
    let calls = 0
    const off = i18n.subscribe(() => calls++)
    i18n.setLanguage("zh-TW")
    expect(i18n.language()).toBe("zh-CN")
    expect(i18n.t("inbox.waiting", { count: 2 })).toBe("2 个会话等待回复")
    i18n.setLanguage("zh-CN")
    expect(calls).toBe(1)
    i18n.setLanguage(null)
    expect(i18n.language()).toBe("en")
    expect(calls).toBe(2)
    off()
    i18n.setLanguage("zh-CN")
    expect(calls).toBe(2)
    expect(i18n.headers()).toEqual({ "Accept-Language": "zh-CN" })
  })

  it("extend 加入的消息优先级较低，只补充应用已有的语言", () => {
    const i18n = createI18n({
      messages: { "zh-CN": { "tenon.search": "搜索（应用）" } },
      defaultLanguage: "zh-CN",
      preferred: () => ["ja", "en"],
    })
    expect(i18n.language()).toBe("zh-CN")
    i18n.extend({ "zh-CN": { "tenon.search": "搜索", "tenon.cancel": "取消" }, en: { "tenon.search": "Search" } })
    // 应用没有英文：偏好英文、要求英文都仍是简体中文，界面不会一半中文一半英文
    expect(i18n.language()).toBe("zh-CN")
    expect(i18n.languages()).toEqual(["zh-CN"])
    i18n.setLanguage("en")
    expect(i18n.language()).toBe("zh-CN")
    expect(i18n.t("tenon.search" as never)).toBe("搜索（应用）")
    expect(i18n.t("tenon.cancel" as never)).toBe("取消")
    expect(i18n.has("tenon.cancel")).toBe(true)
    expect(i18n.has("tenon.missing")).toBe(false)
  })
})

describe("matchLanguage", () => {
  it("先找相同的，再找同一主语言的", () => {
    expect(matchLanguage(["en-GB"], ["zh-CN", "en"])).toBe("en")
    expect(matchLanguage(["zh-HK"], ["en", "zh-TW", "zh-CN"])).toBe("zh-TW")
    expect(matchLanguage(["ZH-cn"], ["zh-CN"])).toBe("zh-CN")
    expect(matchLanguage(["fr", "en"], ["zh-CN", "en"])).toBe("en")
    expect(matchLanguage([undefined, "de"], ["en"])).toBeUndefined()
  })
})

describe("formatMessage", () => {
  it("缺少的占位符换成空字符串，没有对应的复数形式时用 other", () => {
    expect(formatMessage("你好，{{.name}}{{ .suffix }}", "zh-CN", { name: "张三" })).toBe("你好，张三")
    expect(formatMessage({ other: "{{.count}} 项" }, "en", { count: 1 })).toBe("1 项")
  })
})
