// CoreUIProvider：只安装 Mantine 主题和图标，不含通知和弹窗，适合官网、文档、嵌入组件等轻量前端。

import {
  localStorageColorSchemeManager,
  type MantineColorScheme,
  MantineProvider,
  type MantineThemeOverride,
} from "@mantine/core"
import { type ReactNode, useMemo } from "react"
import { type IconComponent, IconsProvider } from "./icons"
import { themes } from "./theme"

/** CoreUIProvider 的选项。 */
export interface CoreUIProviderProps {
  /** 主题：themes 中的一套，或者 createUiTheme 生成的主题。默认是 comfortable 预设。 */
  theme?: MantineThemeOverride
  /** 默认的配色：auto 跟随系统。用户切换后的选择保存在浏览器中。 */
  defaultColorScheme?: MantineColorScheme
  /** 保存配色的 localStorage 键，通常是 app.storage.key("color-scheme")。 */
  colorSchemeKey?: string
  /** 增加或替换图标名称，见 defaultIcons。 */
  icons?: Record<string, IconComponent>
  /** test 关闭过渡动画和 portal，用于应用的组件测试。 */
  env?: "default" | "test"
  children: ReactNode
}

/** 安装主题和图标，配合 @go-tenon/ui/base.css 和 @go-tenon/ui/core 使用。 */
export function CoreUIProvider({
  theme = themes.comfortable,
  defaultColorScheme = "auto",
  colorSchemeKey,
  icons,
  env,
  children,
}: CoreUIProviderProps) {
  const colorSchemeManager = useMemo(
    () => (colorSchemeKey ? localStorageColorSchemeManager({ key: colorSchemeKey }) : undefined),
    [colorSchemeKey],
  )
  return (
    <MantineProvider
      theme={theme}
      defaultColorScheme={defaultColorScheme}
      colorSchemeManager={colorSchemeManager}
      env={env}
    >
      <IconsProvider icons={icons}>{children}</IconsProvider>
    </MantineProvider>
  )
}
