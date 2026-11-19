// 显示值的格式：按主题中的设置（createUiTheme 的 datetimeFormat）格式化日期时间，按 TimeZoneProvider 的时区换算。

import { useMantineTheme } from "@mantine/core"
import dayjs from "dayjs"
import timezone from "dayjs/plugin/timezone"
import utc from "dayjs/plugin/utc"
import { createContext, type ReactNode, useCallback, useContext } from "react"
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
