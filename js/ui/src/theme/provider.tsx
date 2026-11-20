// UIProvider：安装 Mantine 的主题、通知、对话框和图标。放在 AppRoot 之外（或 Start 的根文档中）。

import {
  localStorageColorSchemeManager,
  type MantineColorScheme,
  MantineProvider,
  type MantineThemeOverride,
} from "@mantine/core"
import { ModalsProvider } from "@mantine/modals"
import { Notifications } from "@mantine/notifications"
import { type ReactNode, useMemo } from "react"
import { useText } from "../i18n/i18n"
import { type IconComponent, IconsProvider } from "./icons"
import { themes } from "./theme"

/** UIProvider 的选项。 */
export interface UIProviderProps {
  /** 主题：themes 中的一套，或者 createUiTheme 生成的主题。默认是 comfortable 预设。 */
  theme?: MantineThemeOverride
  /** 默认的配色：auto 跟随系统。用户切换后的选择保存在浏览器中。 */
  defaultColorScheme?: MantineColorScheme
  /**
   * 保存配色的 localStorage 键，通常是 app.storage.key("color-scheme")，同一个域名下的每个前端各记各的配色。
   * 根文档中的 ColorSchemeScript 用同一个键（localStorageKey）。不传时用 Mantine 的默认键，同域名的前端共用。
   */
  colorSchemeKey?: string
  /** 增加或替换图标名称，见 defaultIcons。 */
  icons?: Record<string, IconComponent>
  /** test 关闭过渡动画和 portal，用于应用的组件测试。 */
  env?: "default" | "test"
  children: ReactNode
}

/**
 * 安装主题、通知和图标：
 *
 *   import "@go-tenon/ui/styles.css"
 *   <UIProvider theme={createUiTheme({ preset: "compact" })} colorSchemeKey={app.storage.key("color-scheme")}>
 *     <AppRoot app={app} />
 *   </UIProvider>
 */
export function UIProvider({
  theme = themes.comfortable,
  defaultColorScheme = "auto",
  colorSchemeKey,
  icons,
  env,
  children,
}: UIProviderProps) {
  const colorSchemeManager = useMemo(
    () => (colorSchemeKey ? localStorageColorSchemeManager({ key: colorSchemeKey }) : undefined),
    [colorSchemeKey],
  )
  const t = useText()
  return (
    <MantineProvider
      theme={theme}
      defaultColorScheme={defaultColorScheme}
      colorSchemeManager={colorSchemeManager}
      env={env}
    >
      <Notifications position="top-right" limit={4} />
      <ModalsProvider labels={{ confirm: t("tenon.common.confirm"), cancel: t("tenon.common.cancel") }}>
        <IconsProvider icons={icons}>{children}</IconsProvider>
      </ModalsProvider>
    </MantineProvider>
  )
}
