// 记住主窗口的位置、大小和是否最大化，下次打开时恢复；保存的位置在当前的显示器上看不到时用默认位置。

import { readFileSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { app, screen } from "electron"
import { parseState, visible } from "./bounds"
import type { Host } from "./main"

/** 记住主窗口的状态，保存在 file 中，默认是 userData 中的 window-state.json。 */
export function windowState(host: Host, file?: string): void {
  host.whenWindow((win) => {
    const path = file ?? join(app.getPath("userData"), "window-state.json")
    let saved: ReturnType<typeof parseState>
    try {
      saved = parseState(readFileSync(path, "utf8"))
    } catch {
      saved = undefined // 第一次打开，还没有保存过
    }
    if (
      saved &&
      visible(
        saved.bounds,
        screen.getAllDisplays().map((d) => d.workArea),
      )
    )
      win.setBounds(saved.bounds)
    if (saved?.maximized) win.once("ready-to-show", () => win.maximize())

    const write = () => {
      if (win.isDestroyed()) return
      writeFileSync(path, JSON.stringify({ bounds: win.getNormalBounds(), maximized: win.isMaximized() }))
    }
    let timer: ReturnType<typeof setTimeout> | undefined
    const later = () => {
      clearTimeout(timer)
      timer = setTimeout(write, 500)
    }
    win.on("resize", later)
    win.on("move", later)
    win.on("close", () => {
      clearTimeout(timer)
      write()
    })
  })
}
