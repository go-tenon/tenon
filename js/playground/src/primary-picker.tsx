// 主题色选择：playground 右下角的按钮，点开后列出颜色生成器的 26 种预设，也可以输入任意颜色，用来查看各种品牌色的效果。

import { type PrimaryColor, primaryColors } from "@go-tenon/ui"
import {
  ActionIcon,
  Affix,
  ColorInput,
  ColorSwatch,
  Group,
  Popover,
  SimpleGrid,
  Stack,
  Text,
  UnstyledButton,
} from "@mantine/core"
import { IconCheck, IconPalette } from "@tabler/icons-react"
import type { ReactNode } from "react"

const storageKey = "playground-primary"

// 没有选择过时用列表中的第一种颜色。
const first = Object.keys(primaryColors)[0]

/** 浏览器中保存的主题色；没有保存过时是列表中的第一种颜色。 */
export function savedPrimary(): string {
  try {
    return localStorage.getItem(storageKey) ?? first
  } catch {
    return first
  }
}

function save(value: string) {
  try {
    localStorage.setItem(storageKey, value)
  } catch {
    // 浏览器禁用存储时只在本次打开期间生效。
  }
}

/** PrimaryPicker 的选项。 */
export interface PrimaryPickerProps {
  /** 当前的主题色：预设的名称或颜色值。 */
  value: string
  onChange: (value: string) => void
}

/** 右下角的主题色按钮，选择后立即切换主题并保存在浏览器中。 */
export function PrimaryPicker({ value, onChange }: PrimaryPickerProps): ReactNode {
  const choose = (next: string) => {
    save(next)
    onChange(next)
  }
  const custom = value in primaryColors ? "" : value
  return (
    <Affix position={{ bottom: 20, right: 20 }} zIndex={150}>
      <Popover position="top-end" width={340} shadow="lg" withinPortal>
        <Popover.Target>
          <ActionIcon
            size={44}
            radius="xl"
            variant="filled"
            color="brand"
            aria-label="选择主题色"
            style={{ boxShadow: "var(--mantine-shadow-md)" }}
          >
            <IconPalette size={22} stroke={1.6} />
          </ActionIcon>
        </Popover.Target>
        <Popover.Dropdown>
          <Stack gap="sm">
            <Text fw={600} size="sm">
              主题色
            </Text>
            <SimpleGrid cols={2} spacing={4} verticalSpacing={2}>
              {(Object.entries(primaryColors) as [PrimaryColor, string][]).map(([name, hex]) => (
                <UnstyledButton
                  key={name}
                  onClick={() => choose(name)}
                  px={6}
                  py={4}
                  style={{
                    borderRadius: "var(--mantine-radius-sm)",
                    background: name === value ? "var(--mantine-color-default-hover)" : undefined,
                  }}
                >
                  <Group gap={8} wrap="nowrap">
                    <ColorSwatch color={hex} size={18}>
                      {name === value && <IconCheck size={12} color="white" />}
                    </ColorSwatch>
                    <Text size="xs" ff="monospace">
                      {name}
                    </Text>
                  </Group>
                </UnstyledButton>
              ))}
            </SimpleGrid>
            <ColorInput
              size="xs"
              label="自定义"
              placeholder="#0f766e"
              format="hex"
              key={custom}
              defaultValue={custom}
              onChangeEnd={(v) => /^#[0-9a-f]{6}$/i.test(v) && choose(v)}
              popoverProps={{ withinPortal: false }}
            />
          </Stack>
        </Popover.Dropdown>
      </Popover>
    </Affix>
  )
}
