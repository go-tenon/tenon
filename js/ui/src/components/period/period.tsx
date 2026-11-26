// 时间窗口选择器与地址同步（PeriodPicker / usePeriod）：
// 与后端 period.Params 成对使用：把预设区间、自定义起止日期和分桶粒度同步到 URL search，
// 并按 TimeZoneProvider 的时区换算“今天 / 近 7 天 / 近 30 天 / 本月 / 上月”，刷新、分享链接和切换子报表时状态一致。

import { Group, SegmentedControl, Select } from "@mantine/core"
import { DatePickerInput } from "@mantine/dates"
import { IconCalendar } from "@tabler/icons-react"
import { useNavigate } from "@tanstack/react-router"
import { type ReactNode, useCallback, useMemo, useState } from "react"
import { useText } from "../../i18n/i18n"
import { inTimeZone, useTimeZone } from "../../theme/format"

/** 内置的时间窗口预设。 */
export type PeriodPreset = "today" | "7d" | "30d" | "this_month" | "last_month" | "custom"

/** 时间序列分桶粒度，与后端 period.Granularity 一致。 */
export type PeriodGranularity = "hour" | "day" | "week" | "month"

/** 保存在 URL search 中的时间窗口参数。 */
export interface PeriodSearchParams {
  preset?: PeriodPreset
  from?: string
  to?: string
  granularity?: PeriodGranularity
}

/** 传给后端 period.Params 接口的查询参数。 */
export interface PeriodQueryParams {
  from: string
  to: string
  tz?: string
  granularity?: PeriodGranularity
}

/** periodSearch 的选项。 */
export interface PeriodSearchOptions {
  /** 默认预设，省略时为 "30d"；等于默认预设时不写进地址。 */
  defaultPreset?: Exclude<PeriodPreset, "custom">
}

/** usePeriod 需要的路由对象（createFileRoute 返回的 Route）。 */
export interface PeriodRoute {
  useSearch(): Record<string, unknown>
}

/** usePeriod 的选项。 */
export interface UsePeriodOptions {
  /** 默认预设，省略时为 "30d"。 */
  defaultPreset?: Exclude<PeriodPreset, "custom">
  /** 当 TimeZoneProvider 未设置时区时使用的回退 IANA 时区（为空时使用浏览器时区）。 */
  defaultZone?: string
}

/** usePeriod 返回的时间窗口状态与操作。 */
export interface PeriodState {
  preset: PeriodPreset
  /** 起始日期 YYYY-MM-DD（含）。 */
  from: string
  /** 结束日期 YYYY-MM-DD（含）。 */
  to: string
  /** 当前生效的 IANA 时区名（可能为 undefined，表示交由服务端默认时区）。 */
  tz: string | undefined
  /** 显式选择的粒度，未选时为 undefined（由服务端按跨度自动选择）。 */
  granularity: PeriodGranularity | undefined
  /** 直接传给生成客户端（嵌入了 period.Params）的参数对象。 */
  params: PeriodQueryParams
  /** 切换到指定预设并清除自定义 from/to。 */
  setPreset(preset: Exclude<PeriodPreset, "custom">): void
  /** 设置自定义日期范围 [from, to]（YYYY-MM-DD）。 */
  setRange(from: string, to: string): void
  /** 设置或清除分桶粒度。 */
  setGranularity(granularity: PeriodGranularity | undefined): void
  /** 恢复到默认预设。 */
  reset(): void
}

const PRESETS: readonly PeriodPreset[] = ["today", "7d", "30d", "this_month", "last_month", "custom"]
const GRANULARITIES: readonly PeriodGranularity[] = ["hour", "day", "week", "month"]
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/

/** 校验并解析地址中的时间窗口参数（用于路由的 validateSearch，也可与其他查询参数组合）。 */
export function periodSearch(
  options: PeriodSearchOptions = {},
): (search: Record<string, unknown>) => PeriodSearchParams {
  const defaultPreset = options.defaultPreset ?? "30d"
  return (search: Record<string, unknown>): PeriodSearchParams => {
    const out: PeriodSearchParams = {}
    const rawPreset = typeof search.preset === "string" ? (search.preset as PeriodPreset) : undefined
    const rawFrom = typeof search.from === "string" && DATE_RE.test(search.from) ? search.from : undefined
    const rawTo = typeof search.to === "string" && DATE_RE.test(search.to) ? search.to : undefined
    const rawGran = typeof search.granularity === "string" ? (search.granularity as PeriodGranularity) : undefined

    if (rawPreset && PRESETS.includes(rawPreset)) {
      if (rawPreset === "custom") {
        if (rawFrom && rawTo && rawFrom <= rawTo) {
          out.preset = "custom"
          out.from = rawFrom
          out.to = rawTo
        }
      } else if (rawPreset !== defaultPreset) {
        out.preset = rawPreset
      }
    } else if (rawFrom && rawTo && rawFrom <= rawTo) {
      out.preset = "custom"
      out.from = rawFrom
      out.to = rawTo
    }

    if (rawGran && GRANULARITIES.includes(rawGran)) {
      out.granularity = rawGran
    }
    return out
  }
}

/** 按指定时区把预设换算为 [from, to]（YYYY-MM-DD 闭区间）。 */
export function resolvePeriodDates(
  preset: Exclude<PeriodPreset, "custom">,
  zone?: string,
  now: Date = new Date(),
): { from: string; to: string } {
  const today = inTimeZone(now, zone)
  const fmt = (d: typeof today) => d.format("YYYY-MM-DD")
  switch (preset) {
    case "today":
      return { from: fmt(today), to: fmt(today) }
    case "7d":
      return { from: fmt(today.subtract(6, "day")), to: fmt(today) }
    case "30d":
      return { from: fmt(today.subtract(29, "day")), to: fmt(today) }
    case "this_month":
      return { from: fmt(today.startOf("month")), to: fmt(today) }
    case "last_month": {
      const prev = today.subtract(1, "month")
      return { from: fmt(prev.startOf("month")), to: fmt(prev.endOf("month")) }
    }
  }
}

/** 返回浏览器当前时区的 IANA 名称，无法读取时返回 undefined。 */
function browserTimeZone(): string | undefined {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || undefined
  } catch {
    return undefined
  }
}

/**
 * 读取并同步地址中的时间窗口状态，保留路由上其他查询参数（如 team_id、channel_id）。
 * 返回的 period.params 可直接交给嵌入了 period.Params 的生成客户端接口。
 */
export function usePeriod(route: PeriodRoute, options: UsePeriodOptions = {}): PeriodState {
  const defaultPreset = options.defaultPreset ?? "30d"
  const search = route.useSearch() as PeriodSearchParams
  const navigate = useNavigate()
  const contextZone = useTimeZone()
  const tz = contextZone || options.defaultZone || browserTimeZone()

  const preset: PeriodPreset =
    search.preset && PRESETS.includes(search.preset)
      ? search.preset
      : search.from && search.to
        ? "custom"
        : defaultPreset

  const { from, to } = useMemo(() => {
    if (preset === "custom" && search.from && search.to && search.from <= search.to) {
      return { from: search.from, to: search.to }
    }
    return resolvePeriodDates(preset === "custom" ? defaultPreset : preset, tz)
  }, [preset, search.from, search.to, defaultPreset, tz])

  const granularity = search.granularity && GRANULARITIES.includes(search.granularity) ? search.granularity : undefined

  const update = useCallback(
    (patch: Partial<PeriodSearchParams>) => {
      void navigate({
        to: ".",
        search: ((prev: Record<string, unknown>) => {
          const next: Record<string, unknown> = { ...prev }
          for (const [k, v] of Object.entries(patch)) {
            if (v === undefined) delete next[k]
            else next[k] = v
          }
          return next
        }) as never,
        replace: true,
      })
    },
    [navigate],
  )

  const setPreset = useCallback(
    (next: Exclude<PeriodPreset, "custom">) => {
      update({ preset: next === defaultPreset ? undefined : next, from: undefined, to: undefined })
    },
    [defaultPreset, update],
  )

  const setRange = useCallback(
    (nextFrom: string, nextTo: string) => {
      if (!DATE_RE.test(nextFrom) || !DATE_RE.test(nextTo) || nextFrom > nextTo) return
      update({ preset: "custom", from: nextFrom, to: nextTo })
    },
    [update],
  )

  const setGranularity = useCallback(
    (next: PeriodGranularity | undefined) => {
      update({ granularity: next })
    },
    [update],
  )

  const reset = useCallback(() => {
    update({ preset: undefined, from: undefined, to: undefined, granularity: undefined })
  }, [update])

  const params = useMemo<PeriodQueryParams>(() => {
    const out: PeriodQueryParams = { from, to }
    if (tz) out.tz = tz
    if (granularity) out.granularity = granularity
    return out
  }, [from, to, tz, granularity])

  return { preset, from, to, tz, granularity, params, setPreset, setRange, setGranularity, reset }
}

/** PeriodPicker 的选项。 */
export interface PeriodPickerProps {
  /** usePeriod 返回的状态对象。 */
  period: PeriodState
  /** 显示的预设按钮，默认 ["today", "7d", "30d", "this_month", "last_month"]。 */
  presets?: readonly Exclude<PeriodPreset, "custom">[]
  /** 是否显示分桶粒度选择器，默认 false。 */
  showGranularity?: boolean
  /** 可选的分桶粒度列表，默认 ["day", "week", "month"]。 */
  granularities?: readonly PeriodGranularity[]
  /** 控件尺寸，默认 "xs"。 */
  size?: "xs" | "sm"
}

const DEFAULT_PRESETS: readonly Exclude<PeriodPreset, "custom">[] = ["today", "7d", "30d", "this_month", "last_month"]
const DEFAULT_GRANULARITIES: readonly PeriodGranularity[] = ["day", "week", "month"]

/**
 * 时间窗口选择器：快捷预设（今天 / 近 7 天 / 近 30 天 / 本月 / 上月）+ 自定义日期区间选择，
 * 可选分桶粒度切换，直接对接 usePeriod。
 */
export function PeriodPicker({
  period,
  presets = DEFAULT_PRESETS,
  showGranularity = false,
  granularities = DEFAULT_GRANULARITIES,
  size = "xs",
}: PeriodPickerProps): ReactNode {
  const t = useText()
  const [draft, setDraft] = useState<[string | null, string | null] | null>(null)

  const presetLabel = (p: Exclude<PeriodPreset, "custom">) => {
    switch (p) {
      case "today":
        return t("tenon.period.today")
      case "7d":
        return t("tenon.period.7d")
      case "30d":
        return t("tenon.period.30d")
      case "this_month":
        return t("tenon.period.this_month")
      case "last_month":
        return t("tenon.period.last_month")
    }
  }

  const granLabel = (g: PeriodGranularity) => {
    switch (g) {
      case "hour":
        return t("tenon.period.hour")
      case "day":
        return t("tenon.period.day")
      case "week":
        return t("tenon.period.week")
      case "month":
        return t("tenon.period.month")
    }
  }

  const rangeValue: [string | null, string | null] = draft ?? [period.from, period.to]

  return (
    <Group gap="xs" wrap="wrap" className="tn-period-picker">
      <div className="tn-period-presets">
        <SegmentedControl
          size={size}
          value={period.preset === "custom" ? "" : period.preset}
          onChange={(v) => {
            setDraft(null)
            if (v) period.setPreset(v as Exclude<PeriodPreset, "custom">)
          }}
          data={presets.map((p) => ({ value: p, label: presetLabel(p) }))}
        />
      </div>
      <DatePickerInput
        className="tn-period-range"
        type="range"
        size={size}
        valueFormat="YYYY-MM-DD"
        placeholder={t("tenon.period.range_placeholder")}
        leftSection={<IconCalendar size={14} stroke={1.6} />}
        value={rangeValue}
        onChange={(next) => {
          const [s, e] = next
          if (s && e) {
            setDraft(null)
            period.setRange(s, e)
          } else {
            setDraft([s, e])
          }
        }}
        allowSingleDateInRange
      />
      {showGranularity && (
        <Select
          className="tn-period-granularity"
          size={size}
          clearable
          placeholder={t("tenon.period.granularity")}
          value={period.granularity ?? null}
          onChange={(v) => period.setGranularity((v as PeriodGranularity | null) ?? undefined)}
          data={granularities.map((g) => ({ value: g, label: granLabel(g) }))}
        />
      )}
    </Group>
  )
}
