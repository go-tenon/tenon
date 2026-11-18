// 只运行一个实例：第二次启动时把已有实例的窗口带到前面，然后退出。

import { app } from "electron"
import type { Host } from "./main"

/**
 * 申请单实例锁，返回本进程是否是第一个实例；不是时应用已经开始退出，不要再调用 host.start。
 * 之后再启动时，已有实例的窗口被带到前面，那次启动的命令行参数交给 onSecondInstance（例如其中的深度链接）。
 */
export function singleInstance(host: Host, onSecondInstance?: (argv: string[]) => void): boolean {
  if (!app.requestSingleInstanceLock()) {
    app.quit()
    return false
  }
  app.on("second-instance", (_e, argv) => {
    const win = host.window()
    if (win) {
      if (win.isMinimized()) win.restore()
      win.show()
      win.focus()
    }
    onSecondInstance?.(argv)
  })
  return true
}
