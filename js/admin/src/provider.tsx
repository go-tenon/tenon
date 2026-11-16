// AdminProvider：antd 的全局配置（中文、设计令牌、暗色与紧凑算法）、antd 的 App（message、modal 的上下文）和图标。

import { useApp } from "@go-tenon/shell"
import { App as AntdApp, ConfigProvider, theme } from "antd"
import zhCN from "antd/locale/zh_CN"
import { createContext, type ReactNode, useContext, useEffect, useMemo, useState, useSyncExternalStore } from "react"
import { defaultIcons, type IconMap } from "./icons"

/** 设计令牌生成的 antd 令牌：项目主题包（@<项目>/theme）的 antd，见设计令牌一章。 */
export interface AntdTokens {
  brand: { light: object; dark: object }
  full: { light: object; dark: object }
  density: string
}

/** 颜色模式：跟随系统、亮色、暗色。 */
export type ColorMode = "system" | "light" | "dark"

interface AdminContextValue {
  icons: IconMap
  mode: ColorMode
  dark: boolean
  setMode(mode: ColorMode): void
}

const AdminContext = createContext<AdminContextValue | null>(null)

/** 当前的颜色模式和图标，在 AdminProvider 之内使用。 */
export function useAdmin(): AdminContextValue {
  const v = useContext(AdminContext)
  if (!v) throw new Error("useAdmin: not inside AdminProvider")
  return v
}

/** AdminProvider 的选项。 */
export interface AdminProviderProps {
  /** 项目主题包的 antd 令牌：import { antd } from "@<项目>/theme"。 */
  tokens?: AntdTokens
  /** 用哪一组令牌：brand（默认）只取品牌色和圆角，其余保持 antd 的紧凑风格；full 用全部令牌。 */
  use?: "brand" | "full"
  /** 是否紧凑，默认按令牌的 density；运营后台通常紧凑。 */
  compact?: boolean
  /** 路由元数据中 icon 的名字到图标的映射，合并到默认的图标之上。 */
  icons?: IconMap
  children: ReactNode
}

const darkQuery = "(prefers-color-scheme: dark)"

function useSystemDark(): boolean {
  return useSyncExternalStore(
    (onChange) => {
      const mql = window.matchMedia?.(darkQuery)
      mql?.addEventListener("change", onChange)
      return () => mql?.removeEventListener("change", onChange)
    },
    () => window.matchMedia?.(darkQuery).matches ?? false,
    () => false,
  )
}

/**
 * antd 的全局配置。放在根路由中，包住全部页面：
 *
 *   <AdminProvider tokens={antd}><Outlet /></AdminProvider>
 */
export function AdminProvider({ tokens, use = "brand", compact, icons, children }: AdminProviderProps): ReactNode {
  const app = useApp()
  const [mode, setModeState] = useState<ColorMode>(() => app.storage.get<ColorMode>("color-mode") ?? "system")
  const systemDark = useSystemDark()
  const dark = mode === "dark" || (mode === "system" && systemDark)
  const isCompact = compact ?? tokens?.density === "compact"
  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark)
    document.documentElement.style.colorScheme = dark ? "dark" : "light"
  }, [dark])
  const value = useMemo<AdminContextValue>(
    () => ({
      icons: { ...defaultIcons, ...icons },
      mode,
      dark,
      setMode(next) {
        app.storage.set("color-mode", next)
        setModeState(next)
      },
    }),
    [icons, mode, dark, app],
  )
  const group = tokens?.[use]
  const algorithm = [
    ...(dark ? [theme.darkAlgorithm] : [theme.defaultAlgorithm]),
    ...(isCompact ? [theme.compactAlgorithm] : []),
  ]
  return (
    <ConfigProvider locale={zhCN} theme={{ token: (dark ? group?.dark : group?.light) ?? {}, algorithm }}>
      <AntdApp>
        <AdminContext.Provider value={value}>{children}</AdminContext.Provider>
      </AntdApp>
    </ConfigProvider>
  )
}
