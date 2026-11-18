// 显示值的格式：按主题中的设置（createUiTheme 的 datetimeFormat）格式化日期时间。

import { useMantineTheme } from "@mantine/core"
import dayjs from "dayjs"
import { useCallback } from "react"
import { layoutMetrics } from "./theme"

/**
 * 按主题的 datetimeFormat（createUiTheme 的选项，默认 YYYY-MM-DD HH:mm）显示日期时间。后端返回的是带时区的 ISO 时间，显示为浏览器所在时区的时间；
 * 解析不了时原样返回。
 *
 *   const formatDateTime = useFormatDateTime()
 *   formatDateTime(order.created_at) // 11/03 10:12
 */
export function useFormatDateTime(): (value: string | Date) => string {
  const format = layoutMetrics(useMantineTheme()).datetimeFormat ?? "YYYY-MM-DD HH:mm"
  return useCallback(
    (value: string | Date) => {
      const d = dayjs(value)
      return d.isValid() ? d.format(format) : String(value)
    },
    [format],
  )
}
