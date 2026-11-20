// 配色切换：亮色和暗色之间切换，用户的选择保存在浏览器中。

import { ActionIcon, Tooltip, useComputedColorScheme, useMantineColorScheme } from "@mantine/core"
import { IconMoon, IconSun } from "@tabler/icons-react"
import type { ReactNode } from "react"
import { useText } from "../i18n/i18n"

/** 顶栏的配色切换按钮。 */
export function ColorSchemeToggle(): ReactNode {
  const { setColorScheme } = useMantineColorScheme()
  const computed = useComputedColorScheme("light", { getInitialValueInEffect: true })
  const dark = computed === "dark"
  const t = useText()
  const label = dark ? t("tenon.color_scheme.to_light") : t("tenon.color_scheme.to_dark")
  return (
    <Tooltip label={label}>
      <ActionIcon aria-label={label} onClick={() => setColorScheme(dark ? "light" : "dark")} size="lg">
        {dark ? <IconSun size={18} stroke={1.6} /> : <IconMoon size={18} stroke={1.6} />}
      </ActionIcon>
    </Tooltip>
  )
}
