// UIProvider：安装 Mantine 的主题、通知、对话框和图标。放在 AppRoot 之外（或 Start 的根文档中）。

import { type MantineColorScheme, MantineProvider, type MantineThemeOverride } from "@mantine/core"
import { ModalsProvider } from "@mantine/modals"
import { Notifications } from "@mantine/notifications"
import type { ReactNode } from "react"
import { type IconComponent, IconsProvider } from "./icons"
import { themes } from "./theme"

/** UIProvider 的选项。 */
export interface UIProviderProps {
  /** 主题：themes 中的一套，或者 createUiTheme 生成的主题。默认是 console 预设。 */
  theme?: MantineThemeOverride
  /** 默认的配色：auto 跟随系统。用户切换后的选择保存在浏览器中。 */
  defaultColorScheme?: MantineColorScheme
  /** 增加或替换图标名称，见 defaultIcons。 */
  icons?: Record<string, IconComponent>
  /** test 关闭过渡动画和 portal，用于组件测试。 */
  env?: "default" | "test"
  children: ReactNode
}

/**
 * 安装主题、通知和图标：
 *
 *   import "@go-tenon/ui/styles.css"
 *   <UIProvider theme={themes.admin}><AppRoot app={app} /></UIProvider>
 */
export function UIProvider({
  theme = themes.console,
  defaultColorScheme = "auto",
  icons,
  env,
  children,
}: UIProviderProps) {
  return (
    <MantineProvider theme={theme} defaultColorScheme={defaultColorScheme} env={env}>
      <Notifications position="top-right" limit={4} />
      <ModalsProvider labels={{ confirm: "确定", cancel: "取消" }}>
        <IconsProvider icons={icons}>{children}</IconsProvider>
      </ModalsProvider>
    </MantineProvider>
  )
}
