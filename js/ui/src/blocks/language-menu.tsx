// 语言菜单：顶栏的图标按钮，切换界面的语言。

import { ActionIcon, Menu } from "@mantine/core"
import { IconCheck, IconLanguage } from "@tabler/icons-react"
import type { ReactNode } from "react"
import { useText } from "../i18n/i18n"

/** 语言菜单中的一种语言。 */
export interface LanguageOption {
  /** 语言的代码，例如 zh-CN。 */
  value: string
  /** 显示的名称，用这种语言自己的写法，例如 English。 */
  label: string
}

/** LanguageMenu 的选项。 */
export interface LanguageMenuProps {
  languages: LanguageOption[]
  /** 当前的语言。 */
  value: string
  /** 用户选择了一种语言。换文案、记住选择、告诉后端由应用决定。 */
  onChange: (value: string) => void
}

/** 语言切换：顶栏的图标按钮，菜单中列出语言，当前的一项打勾。作为 FrameLayout 的 actions 放在用户菜单的左边。 */
export function LanguageMenu({ languages, value, onChange }: LanguageMenuProps): ReactNode {
  const t = useText()
  return (
    <Menu width={160} position="bottom-end">
      <Menu.Target>
        <ActionIcon size={32} aria-label={t("tenon.language.switch")}>
          <IconLanguage size={16} stroke={1.5} />
        </ActionIcon>
      </Menu.Target>
      <Menu.Dropdown>
        {languages.map((l) => (
          <Menu.Item
            key={l.value}
            aria-checked={l.value === value}
            rightSection={l.value === value ? <IconCheck size={14} stroke={2} /> : undefined}
            onClick={() => onChange(l.value)}
          >
            {l.label}
          </Menu.Item>
        ))}
      </Menu.Dropdown>
    </Menu>
  )
}
