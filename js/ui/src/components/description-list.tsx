// 描述列表：一组“名称 — 值”，例如详情页的基本信息、系统状态、请求的元数据。
// 卡片中名称在左、值靠右，行间一条淡线；标题没有底色，右边可以放状态（StatusDot）或按钮。
// 用 <dl> 渲染，读屏软件按名称和值朗读。

import { Box, Card, Group, Text } from "@mantine/core"
import type { ReactNode } from "react"

/** 描述列表中的一项。 */
export interface DescriptionItem {
  label: ReactNode
  /** 值；undefined、null、空字符串显示为“—”。 */
  value: ReactNode
  /** React 的 key，默认是 label（label 不是字符串时需要设置）。 */
  key?: string
  /** 值用等宽字体，例如 ID、名称、路径。 */
  mono?: boolean
}

/** DescriptionList 的选项。 */
export interface DescriptionListProps {
  items: DescriptionItem[]
  /** 标题，显示在卡片顶部。 */
  title?: ReactNode
  /** 标题下面的一行小字，例如数据的来源。 */
  description?: ReactNode
  /** 标题右边的内容，通常是 StatusDot 或按钮。 */
  extra?: ReactNode
  /** 名称也用等宽字体，例如元数据的键。 */
  monoLabels?: boolean
  /** 放在卡片中（带边框），默认 true；放进对话框、已有卡片中时用 false。 */
  withBorder?: boolean
}

const empty = (v: ReactNode) => v === undefined || v === null || v === ""

/**
 * 描述列表：
 *
 *	<DescriptionList
 *	  title="复制槽"
 *	  description="PostgreSQL"
 *	  extra={<StatusDot color="green">转发中</StatusDot>}
 *	  items={[
 *	    { label: "名称", value: slot.name, mono: true },
 *	    { label: "积压", value: "12.4 KiB" },
 *	  ]}
 *	/>
 */
export function DescriptionList({
  items,
  title,
  description,
  extra,
  monoLabels,
  withBorder = true,
}: DescriptionListProps): ReactNode {
  const content = (
    <>
      {(title || extra) && (
        <Group justify="space-between" align="flex-start" gap="sm" wrap="nowrap" className="tn-dl-header">
          <Box style={{ minWidth: 0 }}>
            {title && <Text fw={600}>{title}</Text>}
            {description && (
              <Text size="xs" c="dimmed">
                {description}
              </Text>
            )}
          </Box>
          {extra}
        </Group>
      )}
      <Box component="dl" className="tn-dl">
        {items.map((item) => (
          <div key={item.key ?? String(item.label)} className="tn-dl-row">
            <dt data-mono={monoLabels || undefined}>{item.label}</dt>
            <dd data-mono={item.mono || undefined}>{empty(item.value) ? "—" : item.value}</dd>
          </div>
        ))}
      </Box>
    </>
  )
  if (!withBorder) return <Box className="tn-dl-plain">{content}</Box>
  return (
    <Card padding={0} radius="md" className="tn-dl-card">
      {content}
    </Card>
  )
}
