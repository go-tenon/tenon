// 将 schedule 包支持的定时表达式格式化为当前语言的自然描述（例如“每分钟”“每天 00:00”“在02:00”）：
// - 标准 5/6 段 cron 表达式交给 cronstrue 翻译；
// - @hourly、@daily、@every 等 Go 风格简写按内置多语言文案格式化；
// - 无法识别的表达式返回 undefined，由界面回退显示原始表达式。

import cronstrue from "cronstrue/i18n"
import type { UiMessageId } from "../i18n/i18n"

const shortcuts: Record<string, UiMessageId> = {
  "@everysecond": "tenon.schedule.every_second",
  "@hourly": "tenon.schedule.every_hour",
  "@daily": "tenon.schedule.every_day",
  "@midnight": "tenon.schedule.every_day",
  "@weekly": "tenon.schedule.every_week",
  "@monthly": "tenon.schedule.every_month",
  "@yearly": "tenon.schedule.every_year",
  "@annually": "tenon.schedule.every_year",
}

const durationPart = /(\d+)([hms])/g

/** 把界面语言标签映射为 cronstrue 的 locale。 */
function cronLocale(language: string): string {
  const lower = language.toLowerCase()
  if (lower.startsWith("zh-tw") || lower.startsWith("zh-hk") || lower.startsWith("zh-hant")) return "zh_TW"
  if (lower.startsWith("zh")) return "zh_CN"
  if (lower.startsWith("en")) return "en"
  return language.replace("-", "_")
}

/** 解析 @every 后的 Go duration（如 15s、5m、1h30m），返回可读间隔；格式不合法时返回 undefined。 */
function formatEvery(
  raw: string,
  t: (id: UiMessageId, data?: Record<string, string | number>) => string,
): string | undefined {
  const s = raw.trim()
  if (!s) return undefined
  let matched = 0
  const parts: string[] = []
  for (const m of s.matchAll(durationPart)) {
    matched += m[0].length
    const n = Number(m[1])
    if (n <= 0) return undefined
    const unit = m[2]
    if (unit === "h") parts.push(t("tenon.schedule.unit_hours", { count: n }))
    else if (unit === "m") parts.push(t("tenon.schedule.unit_minutes", { count: n }))
    else parts.push(t("tenon.schedule.unit_seconds", { count: n }))
  }
  if (matched !== s.length || parts.length === 0) return undefined
  return t("tenon.schedule.every_interval", { interval: parts.join(" ") })
}

/** 把定时任务表达式格式化为当前语言的自然描述，无法解析时返回 undefined。 */
export function describeScheduleExpr(
  expr: string,
  language: string,
  t: (id: UiMessageId, data?: Record<string, string | number>) => string,
): string | undefined {
  const trimmed = expr.trim()
  if (!trimmed) return undefined
  const lower = trimmed.toLowerCase()
  const shortcut = shortcuts[lower]
  if (shortcut) return t(shortcut)
  if (lower.startsWith("@every ")) return formatEvery(trimmed.slice(7), t)
  if (lower.startsWith("@")) return undefined
  try {
    return cronstrue.toString(trimmed, {
      locale: cronLocale(language),
      use24HourTimeFormat: true,
      throwExceptionOnParseError: true,
    })
  } catch {
    return undefined
  }
}
