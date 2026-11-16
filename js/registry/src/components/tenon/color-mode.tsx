// 颜色模式：跟随系统、亮色、暗色。暗色时给 <html> 加上 dark，与 shadcn、设计令牌（tokens.css 的 .dark）一致。
// 选择保存在应用的本地存储中。

import { useApp } from "@go-tenon/shell"
import { createContext, type ReactNode, useContext, useEffect, useMemo, useState, useSyncExternalStore } from "react"

export type ColorMode = "system" | "light" | "dark"

interface ColorModeValue {
  mode: ColorMode
  dark: boolean
  setMode(mode: ColorMode): void
}

const ColorModeContext = createContext<ColorModeValue | null>(null)

const darkQuery = "(prefers-color-scheme: dark)"

/** 颜色模式，放在根路由中，包住全部页面。 */
export function ColorModeProvider({ children }: { children: ReactNode }): ReactNode {
  const app = useApp()
  const [mode, setModeState] = useState<ColorMode>(() => app.storage.get<ColorMode>("color-mode") ?? "system")
  const systemDark = useSyncExternalStore(
    (onChange) => {
      const mql = window.matchMedia?.(darkQuery)
      mql?.addEventListener("change", onChange)
      return () => mql?.removeEventListener("change", onChange)
    },
    () => window.matchMedia?.(darkQuery).matches ?? false,
    () => false,
  )
  const dark = mode === "dark" || (mode === "system" && systemDark)
  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark)
    document.documentElement.style.colorScheme = dark ? "dark" : "light"
  }, [dark])
  const value = useMemo<ColorModeValue>(
    () => ({
      mode,
      dark,
      setMode(next) {
        app.storage.set("color-mode", next)
        setModeState(next)
      },
    }),
    [mode, dark, app],
  )
  return <ColorModeContext.Provider value={value}>{children}</ColorModeContext.Provider>
}

/** 当前的颜色模式；不在 ColorModeProvider 之内时是跟随系统、不能切换。 */
export function useColorMode(): ColorModeValue {
  return useContext(ColorModeContext) ?? { mode: "system", dark: false, setMode() {} }
}
