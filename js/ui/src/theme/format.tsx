// 显示值的格式：按主题中的设置（createUiTheme 的 datetimeFormat）格式化日期时间，按 TimeZoneProvider 的时区换算。

import { useMantineTheme } from "@mantine/core"
import dayjs from "dayjs"
import timezone from "dayjs/plugin/timezone"
import utc from "dayjs/plugin/utc"
import { createContext, type ReactNode, useCallback, useContext } from "react"
import { useText } from "../i18n/i18n"
import { layoutMetrics } from "./theme"

dayjs.extend(utc)
dayjs.extend(timezone)

const TimeZoneContext = createContext<string | undefined>(undefined)

/** TimeZoneProvider 的选项。 */
export interface TimeZoneProviderProps {
  /** IANA 时区名，例如 Asia/Shanghai。为空时按浏览器的时区。 */
  zone?: string
  children?: ReactNode
}

/**
 * 页面中显示和输入时间使用的时区，例如用户在偏好设置中选的时区：useFormatDateTime（表格、操作记录、通知等）、
 * 资源表单的日期时间输入框都按它换算。时区通常在登录后才读到，所以放在布局外面，而不是主题中：
 *
 *	<TimeZoneProvider zone={me.data?.time_zone}>
 *	  <RailLayout … />
 *	</TimeZoneProvider>
 *
 * 不在任何 TimeZoneProvider 中、或者 zone 为空时，按浏览器的时区。
 */
export function TimeZoneProvider({ zone, children }: TimeZoneProviderProps): ReactNode {
  return <TimeZoneContext.Provider value={zone || undefined}>{children}</TimeZoneContext.Provider>
}

/** 当前的时区（TimeZoneProvider 的 zone）；按浏览器的时区时为空。 */
export function useTimeZone(): string | undefined {
  return useContext(TimeZoneContext)
}

/** 把时刻 value 换到时区 zone 中，zone 为空时是浏览器的时区。返回 dayjs 对象，用它的 format 显示。 */
export function inTimeZone(value: string | Date, zone?: string): dayjs.Dayjs {
  const d = dayjs(value)
  return zone && d.isValid() ? d.tz(zone) : d
}

/** 把字节数格式化为易读的文件大小，例如 512 B、1.2 KB、3.4 MB。 */
export function formatFileSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B"
  const units = ["B", "KB", "MB", "GB", "TB"]
  let value = bytes
  let i = 0
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024
    i++
  }
  return i === 0 ? `${Math.round(value)} B` : `${Math.round(value * 10) / 10} ${units[i]}`
}

/** 时长格式化的选项。 */
export interface FormatDurationOptions {
  /** 输入值的单位：默认 "s"（秒），模型耗时等场景可传 "ms"（毫秒）。 */
  unit?: "s" | "ms"
}

/** 按给定单位文案把时长格式化为易读文字，例如 "2 分 30 秒"、"1.2 s"；空值返回 "—"。 */
export function formatDuration(
  value: number | null | undefined,
  options: FormatDurationOptions & {
    labels?: {
      ms: (n: number) => string
      second: (n: number | string) => string
      minute: (n: number) => string
      hour: (n: number) => string
      day: (n: number) => string
      separator: string
    }
  } = {},
): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "—"
  const unit = options.unit ?? "s"
  const labels = options.labels ?? {
    ms: (n) => `${n} ms`,
    second: (n) => `${n}s`,
    minute: (n) => `${n}m`,
    hour: (n) => `${n}h`,
    day: (n) => `${n}d`,
    separator: " ",
  }
  const v = Math.max(0, value)
  if (unit === "ms") {
    if (v < 1000) return labels.ms(Math.round(v))
    if (v < 60_000) return labels.second(Math.round((v / 1000) * 10) / 10)
    return formatDuration(v / 1000, { unit: "s", labels })
  }
  const total = Math.round(v)
  if (total < 60) return labels.second(total)
  const days = Math.floor(total / 86_400)
  const hours = Math.floor((total % 86_400) / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const seconds = total % 60
  const parts: string[] = []
  if (days > 0) {
    parts.push(labels.day(days))
    if (hours > 0) parts.push(labels.hour(hours))
  } else if (hours > 0) {
    parts.push(labels.hour(hours))
    if (minutes > 0) parts.push(labels.minute(minutes))
  } else {
    parts.push(labels.minute(minutes))
    if (seconds > 0) parts.push(labels.second(seconds))
  }
  return parts.join(labels.separator)
}

/** 按当前界面语言格式化时长（秒或毫秒），空值显示“—”。 */
export function useFormatDuration(): (value: number | null | undefined, options?: FormatDurationOptions) => string {
  const t = useText()
  return useCallback(
    (value: number | null | undefined, options?: FormatDurationOptions) =>
      formatDuration(value, {
        ...options,
        labels: {
          ms: (n) => t("tenon.duration.ms", { n }),
          second: (n) => t("tenon.duration.second", { n }),
          minute: (n) => t("tenon.duration.minute", { n }),
          hour: (n) => t("tenon.duration.hour", { n }),
          day: (n) => t("tenon.duration.day", { n }),
          separator: t("tenon.duration.separator"),
        },
      }),
    [t],
  )
}

/**
 * 按主题的 datetimeFormat（createUiTheme 的选项，默认 YYYY-MM-DD HH:mm）显示日期时间。后端返回的是带时区的 ISO 时间，
 * 显示为 TimeZoneProvider 的时区（没有时是浏览器的时区）中的时间；解析不了时原样返回。
 *
 *   const formatDateTime = useFormatDateTime()
 *   formatDateTime(order.created_at) // 11/03 10:12
 */
export function useFormatDateTime(): (value: string | Date) => string {
  const format = layoutMetrics(useMantineTheme()).datetimeFormat ?? "YYYY-MM-DD HH:mm"
  const zone = useTimeZone()
  return useCallback(
    (value: string | Date) => {
      const d = inTimeZone(value, zone)
      return d.isValid() ? d.format(format) : String(value)
    },
    [format, zone],
  )
}
