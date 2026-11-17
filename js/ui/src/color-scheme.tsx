// 配色：页面最早期设置配色的脚本，以及亮色和暗色之间的切换按钮。用户的选择保存在浏览器中。

import {
  ActionIcon,
  type MantineColorScheme,
  Tooltip,
  useComputedColorScheme,
  useMantineColorScheme,
} from "@mantine/core"
import { IconMoon, IconSun } from "@tabler/icons-react"
import { ScriptOnce } from "@tanstack/react-router"
import type { ReactNode } from "react"

/**
 * 在页面显示之前按用户保存的选择（没有时按 defaultColorScheme）设置配色，避免先闪一下亮色。放在根路由的 <head> 中，
 * 代替 Mantine 的 ColorSchemeScript：脚本只写进服务端渲染的 HTML，执行后移除自己，带上路由的 nonce；
 * 在浏览器中渲染时什么也不输出。只在浏览器中渲染的页面（defaultSsr: false）也就不会出现
 * “Encountered a script tag while rendering React component” 的警告。
 */
export function ColorSchemeScript({
  defaultColorScheme = "auto",
}: {
  defaultColorScheme?: MantineColorScheme
}): ReactNode {
  const fallback = JSON.stringify(defaultColorScheme)
  return (
    <ScriptOnce>{`try{var s=window.localStorage.getItem("mantine-color-scheme-value");var c=s==="light"||s==="dark"||s==="auto"?s:${fallback};document.documentElement.setAttribute("data-mantine-color-scheme",c!=="auto"?c:window.matchMedia("(prefers-color-scheme: dark)").matches?"dark":"light")}catch(e){}`}</ScriptOnce>
  )
}

/** 顶栏的配色切换按钮。 */
export function ColorSchemeToggle(): ReactNode {
  const { setColorScheme } = useMantineColorScheme()
  const computed = useComputedColorScheme("light", { getInitialValueInEffect: true })
  const dark = computed === "dark"
  const label = dark ? "切换到亮色" : "切换到暗色"
  return (
    <Tooltip label={label}>
      <ActionIcon aria-label={label} onClick={() => setColorScheme(dark ? "light" : "dark")} size="lg">
        {dark ? <IconSun size={18} stroke={1.6} /> : <IconMoon size={18} stroke={1.6} />}
      </ActionIcon>
    </Tooltip>
  )
}
