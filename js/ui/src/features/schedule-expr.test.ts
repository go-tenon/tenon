import { createI18n } from "@go-tenon/client"
import { describe, expect, it } from "vitest"
import { type UiMessageId, uiMessages } from "../i18n/i18n"
import { describeScheduleExpr } from "./schedule-expr"

function makeT(language: "zh-CN" | "en") {
  const i18n = createI18n({ messages: uiMessages, defaultLanguage: language, preferred: () => [language] })
  return (id: UiMessageId, data?: Record<string, string | number>) => i18n.t(id, data)
}

describe("describeScheduleExpr", () => {
  it("translates standard 5/6-field cron expressions into Chinese and English", () => {
    const zh = makeT("zh-CN")
    const en = makeT("en")
    expect(describeScheduleExpr("* * * * *", "zh-CN", zh)).toBe("每分钟")
    expect(describeScheduleExpr("*/5 * * * *", "zh-CN", zh)).toBe("每隔 5 分钟")
    expect(describeScheduleExpr("0 2 * * *", "zh-CN", zh)).toBe("在02:00")
    expect(describeScheduleExpr("*/10 * * * * *", "zh-CN", zh)).toBe("每隔 10 秒")

    expect(describeScheduleExpr("* * * * *", "en", en)).toBe("Every minute")
    expect(describeScheduleExpr("*/5 * * * *", "en", en)).toBe("Every 5 minutes")
    expect(describeScheduleExpr("0 2 * * *", "en", en)).toBe("At 02:00")
  })

  it("translates Go schedule shortcuts and @every intervals", () => {
    const zh = makeT("zh-CN")
    const en = makeT("en")
    expect(describeScheduleExpr("@hourly", "zh-CN", zh)).toBe("每小时")
    expect(describeScheduleExpr("@daily", "zh-CN", zh)).toBe("每天 00:00")
    expect(describeScheduleExpr("@weekly", "zh-CN", zh)).toBe("每周日 00:00")
    expect(describeScheduleExpr("@every 15s", "zh-CN", zh)).toBe("每 15 秒")
    expect(describeScheduleExpr("@every 1h30m", "zh-CN", zh)).toBe("每 1 小时 30 分钟")

    expect(describeScheduleExpr("@hourly", "en", en)).toBe("Every hour")
    expect(describeScheduleExpr("@daily", "en", en)).toBe("Daily at 00:00")
    expect(describeScheduleExpr("@every 15s", "en", en)).toBe("Every 15 seconds")
  })

  it("returns undefined for unknown or invalid expressions", () => {
    const zh = makeT("zh-CN")
    expect(describeScheduleExpr("", "zh-CN", zh)).toBeUndefined()
    expect(describeScheduleExpr("@unknown", "zh-CN", zh)).toBeUndefined()
    expect(describeScheduleExpr("@every nope", "zh-CN", zh)).toBeUndefined()
    expect(describeScheduleExpr("not a cron", "zh-CN", zh)).toBeUndefined()
  })
})
