// 指标卡片（StatCard）：用于仪表盘和报表页的 KPI 概览。
// 显示标题、可选口径说明（Tooltip）、等宽数字主值、辅助说明以及相对上一周期的环比变化。

import { ActionIcon, Badge, Box, Group, Paper, Skeleton, Stack, Text, Tooltip } from "@mantine/core"
import { IconArrowDownRight, IconArrowUpRight, IconInfoCircle, IconMinus } from "@tabler/icons-react"
import type { ReactNode } from "react"

/** 指标相对上一周期的变化。 */
export interface StatDelta {
  /** 变化量（正数表示上升，负数表示下降，0 表示持平；null/undefined 时不渲染）。 */
  value: number | null | undefined
  /** 展示的文字，例如 "+12.4%"、"-1 分 20 秒"；省略时按 value 自动格式化。 */
  label?: string
  /** 为 true 时表示指标越低越好（如响应时长、失败数）：下降显示 green，上升显示 deep-orange。 */
  invert?: boolean
}

/** StatCard 的选项。 */
export interface StatCardProps {
  /** 指标标题。 */
  label: ReactNode
  /** 指标口径说明，悬停在标题旁的信息图标上显示。 */
  description?: ReactNode
  /** 指标主值；加载中时显示骨架屏。 */
  value: ReactNode
  /** 主值下方的补充信息，例如“均值 4 分 · P90 12 分”或样本数。 */
  secondary?: ReactNode
  /** 相对上一周期的环比变化。 */
  delta?: StatDelta
  /** 右上角的可选图标。 */
  icon?: ReactNode
  /** 加载状态：主值与辅助行显示骨架屏。 */
  loading?: boolean
}

/** 指标卡片：用于展示单个关键统计指标及其环比趋势。 */
export function StatCard({ label, description, value, secondary, delta, icon, loading }: StatCardProps): ReactNode {
  const deltaBadge = renderDelta(delta)
  return (
    <Paper withBorder radius="md" p="md" className="tn-stat-card">
      <Stack gap={6}>
        <Group justify="space-between" align="center" gap="xs" wrap="nowrap">
          <Group gap={4} wrap="nowrap" style={{ minWidth: 0 }}>
            <Text size="xs" fw={500} c="dimmed" truncate>
              {label}
            </Text>
            {description && (
              <Tooltip label={description} multiline maw={260} withArrow>
                <ActionIcon variant="transparent" color="gray" size="xs" aria-label={String(label)}>
                  <IconInfoCircle size={14} stroke={1.6} />
                </ActionIcon>
              </Tooltip>
            )}
          </Group>
          {icon && <Box className="tn-stat-card-icon">{icon}</Box>}
        </Group>

        {loading ? (
          <Stack gap={6} pt={2}>
            <Skeleton height={28} width="55%" radius="sm" />
            <Skeleton height={14} width="75%" radius="sm" />
          </Stack>
        ) : (
          <>
            <Group align="baseline" gap="xs" wrap="wrap">
              <Text className="tn-stat-card-value">{value}</Text>
              {deltaBadge}
            </Group>
            {secondary && (
              <Text size="xs" c="dimmed" className="tn-stat-card-secondary">
                {secondary}
              </Text>
            )}
          </>
        )}
      </Stack>
    </Paper>
  )
}

/** 按变化方向和 invert 语义渲染环比徽章。 */
function renderDelta(delta: StatDelta | undefined): ReactNode {
  if (!delta || delta.value === null || delta.value === undefined || !Number.isFinite(delta.value)) return null
  const v = delta.value
  const label = delta.label ?? (v > 0 ? `+${v}` : String(v))
  if (v === 0) {
    return (
      <Badge size="sm" variant="light" color="gray" leftSection={<IconMinus size={12} stroke={2} />}>
        {label}
      </Badge>
    )
  }
  const positive = v > 0
  const good = delta.invert ? !positive : positive
  const color = good ? "green" : "deep-orange"
  const arrow = positive ? <IconArrowUpRight size={12} stroke={2} /> : <IconArrowDownRight size={12} stroke={2} />
  return (
    <Badge size="sm" variant="light" color={color} leftSection={arrow}>
      {label}
    </Badge>
  )
}
