// 侧栏属性检查器（Inspector）：用于详情侧栏展示与就地编辑对象属性。
// 容器挂上 data-tn-density="inspector"，内部输入控件统一为 28px 高与 13px 字号（sm），
// 与相邻纯文本值保持完全一致的字号和基线，切换阅读态与编辑态无字号跳动。

import { Box, Collapse, Group, Stack, Text, UnstyledButton } from "@mantine/core"
import { IconChevronRight } from "@tabler/icons-react"
import type { CSSProperties, ReactNode } from "react"
import { useRailSectionState } from "../app/layout"

/** Inspector 的选项。 */
export interface InspectorProps {
  /** 左侧字段名列宽（像素），默认 84，写入 --tn-inspector-label-width。 */
  labelWidth?: number
  className?: string
  style?: CSSProperties
  children: ReactNode
}

/** 侧栏属性检查器容器：承载若干 InspectorSection，并统一内部字段与编辑控件的密度。 */
export function Inspector({ labelWidth, className, style, children }: InspectorProps): ReactNode {
  const vars: CSSProperties | undefined =
    labelWidth !== undefined ? ({ "--tn-inspector-label-width": `${labelWidth}px`, ...style } as CSSProperties) : style
  return (
    <Box className={className ? `tn-inspector ${className}` : "tn-inspector"} data-tn-density="inspector" style={vars}>
      {children}
    </Box>
  )
}

/** InspectorSection 的选项。 */
export interface InspectorSectionProps {
  /** 分区标题，以 sm（13px）加粗显示，与字段名（xs 12px）明确区分层级。 */
  title: ReactNode
  /** 标题旁的计数徽标或辅助信息。 */
  extra?: ReactNode
  /** 是否允许点击标题折叠分区，默认 false。 */
  collapsible?: boolean
  /** 允许折叠时是否默认展开，默认 true。 */
  defaultOpened?: boolean
  /** 可选持久化键：传入时按账号记住折叠状态。 */
  storeKey?: string
  /** 是否在顶部显示分隔线，默认 true（容器内首个分区自动隐藏顶分隔线）。 */
  withDivider?: boolean
  className?: string
  children: ReactNode
}

/** 检查器分区：顶部细分隔线、清晰的分节标题（支持可选折叠）与纵向排列的字段内容。 */
export function InspectorSection({
  title,
  extra,
  collapsible = false,
  defaultOpened = true,
  storeKey,
  withDivider = true,
  className,
  children,
}: InspectorSectionProps): ReactNode {
  const [opened, setOpened] = useRailSectionState(collapsible ? storeKey : undefined, defaultOpened)
  return (
    <Box
      className={className ? `tn-inspector-section ${className}` : "tn-inspector-section"}
      data-no-divider={!withDivider || undefined}
    >
      <div className="tn-inspector-section-header">
        {collapsible ? (
          <UnstyledButton
            className="tn-inspector-section-toggle"
            data-opened={opened || undefined}
            aria-expanded={opened}
            onClick={() => setOpened(!opened)}
          >
            <IconChevronRight size={13} stroke={2} className="tn-inspector-chevron" />
            <Text size="sm" fw={600} truncate>
              {title}
            </Text>
          </UnstyledButton>
        ) : (
          <Text size="sm" fw={600} truncate className="tn-inspector-section-title">
            {title}
          </Text>
        )}
        {extra && <div className="tn-inspector-section-extra">{extra}</div>}
      </div>
      {collapsible ? (
        <Collapse expanded={opened}>
          <Stack gap={8} className="tn-inspector-section-body">
            {children}
          </Stack>
        </Collapse>
      ) : (
        <Stack gap={8} className="tn-inspector-section-body">
          {children}
        </Stack>
      )}
    </Box>
  )
}

/** InspectorField 的选项。 */
export interface InspectorFieldProps {
  /** 左侧弱化的字段名称。 */
  label: ReactNode
  /** 垂直对齐方式：单行字段居中（center），多行字段或标签组顶对齐（start）。默认 center。 */
  align?: "center" | "start"
  className?: string
  children: ReactNode
}

/** 检查器中的单个属性行：左侧固定宽度的字段名（xs 12px）与右侧自适应宽度的内容值或编辑控件（sm 13px）。 */
export function InspectorField({ label, align = "center", className, children }: InspectorFieldProps): ReactNode {
  return (
    <Group
      gap="xs"
      wrap="nowrap"
      align={align === "start" ? "flex-start" : "center"}
      className={className ? `tn-inspector-field ${className}` : "tn-inspector-field"}
    >
      <Text size="xs" c="dimmed" className="tn-inspector-field-label" lh={1.35}>
        {label}
      </Text>
      <Box className="tn-inspector-field-value">{children}</Box>
    </Group>
  )
}
