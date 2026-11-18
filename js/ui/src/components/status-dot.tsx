// 状态点：带浅色光圈的圆点和文字，例如描述列表、卡片标题右边的“正常”“转发中”。

import { Box, Group, type MantineColor, Text } from "@mantine/core"
import type { ReactNode } from "react"

/** StatusDot 的选项。 */
export interface StatusDotProps {
  /** 颜色：正常用 green，需要注意用 deep-orange，出错用 red，没有状态用 gray。 */
  color: MantineColor
  children: ReactNode
}

/** 状态：一个带浅色光圈的圆点和文字，例如描述列表、卡片标题右边的“转发中”“正常”。 */
export function StatusDot({ color, children }: StatusDotProps): ReactNode {
  return (
    <Group gap={6} wrap="nowrap" className="tn-status-dot" style={{ flexShrink: 0 }}>
      <Box
        component="span"
        aria-hidden
        className="tn-status-dot-mark"
        style={{
          background: `var(--mantine-color-${color}-filled)`,
          boxShadow: `0 0 0 3px var(--mantine-color-${color}-light)`,
        }}
      />
      <Text span size="sm" fw={500} c={`var(--mantine-color-${color}-text)`}>
        {children}
      </Text>
    </Group>
  )
}
