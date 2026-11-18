// 与框架功能配合的桌面能力：系统通知、未读角标、托盘、开机自启、深度链接。每个函数各管一项，由应用的 main.ts 组合。

import { existsSync, mkdirSync, rmSync, writeFileSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"
import type { HostNotification } from "@go-tenon/host"
import {
  app,
  type BrowserWindow,
  Menu,
  type MenuItemConstructorOptions,
  type NativeImage,
  Notification,
  Tray,
} from "electron"
import { autostartEntry, linkInArgv, linkPath } from "./links"
import type { Host } from "./main"

/** 把主窗口带到前面。 */
function focus(win: BrowserWindow | undefined): void {
  if (!win) return
  if (win.isMinimized()) win.restore()
  win.show()
  win.focus()
}

/**
 * 系统通知：页面调用 notify({ title, body, path })，用户点击通知时把窗口带到前面，并让页面跳到 path。
 * appId 与 electron-builder.yml 的 appId 相同：Windows 按它关联通知和应用，不设置时通知不显示。
 */
export function notifications(host: Host, options: { appId: string }): void {
  if (process.platform === "win32") app.setAppUserModelId(options.appId)
  host.handle("notify", async (n: HostNotification) => {
    if (typeof n?.title !== "string") throw new Error("notify: title is required")
    if (!Notification.isSupported()) return
    const notice = new Notification({ title: n.title, body: n.body ?? "" })
    notice.on("click", () => {
      focus(host.window())
      if (n.path) host.emit("navigate", n.path)
    })
    notice.show()
  })
}

/**
 * 未读角标：页面调用 badge(count)。macOS 的 Dock 和 Linux 的 Unity 启动器显示数字；Windows 任务栏没有数字角标，
 * 有未读时显示 windowsOverlay（一个 16×16 的小图标，例如红点），没有传入时不显示。
 */
export function badge(host: Host, options: { windowsOverlay?: NativeImage } = {}): void {
  host.handle("badge", async (count: number) => {
    const n = Math.max(0, Math.floor(Number(count) || 0))
    if (process.platform === "win32") {
      if (options.windowsOverlay)
        host.window()?.setOverlayIcon(n > 0 ? options.windowsOverlay : null, n > 0 ? `${n} 条未读` : "")
      return
    }
    app.setBadgeCount(n)
  })
}

/** tray 的选项。 */
export interface TrayOptions {
  title: string // 托盘提示和菜单中显示的应用名称
  icon: NativeImage // 托盘图标，例如 nativeImage.createFromPath(...)；macOS 上同目录的 @2x 文件用于高分屏
  menu?: MenuItemConstructorOptions[] // 加在“打开”和“退出”之间的菜单项
}

/**
 * 托盘：关闭窗口时隐藏到托盘，应用继续运行（实时连接、通知不断开），从托盘菜单“退出”才真正退出。
 * Linux 的托盘依赖桌面环境的 AppIndicator 支持，GNOME 默认没有，需要安装扩展；看不到托盘时再次打开应用即可回到窗口
 * （singleInstance）。返回创建好的 Tray（应用就绪之后）。
 */
export function tray(host: Host, options: TrayOptions): Promise<Tray> {
  let quitting = false
  app.on("before-quit", () => {
    quitting = true
  })
  host.whenWindow((win) => {
    win.on("close", (e) => {
      if (quitting) return
      e.preventDefault()
      win.hide()
    })
  })
  app.on("activate", () => focus(host.window())) // macOS：点击 Dock 图标
  return app.whenReady().then(() => {
    const t = new Tray(options.icon)
    t.setToolTip(options.title)
    t.setContextMenu(
      Menu.buildFromTemplate([
        { label: `打开${options.title}`, click: () => focus(host.window()) },
        ...(options.menu?.length ? [{ type: "separator" as const }, ...options.menu] : []),
        { type: "separator" },
        { label: "退出", click: () => app.quit() },
      ]),
    )
    t.on("click", () => focus(host.window()))
    return t
  })
}

/**
 * 开机自启：页面调用 autostart.get()、autostart.set(on)，例如在设置页中切换。macOS、Windows 用系统的登录项；
 * Linux 在 ~/.config/autostart（XDG_CONFIG_HOME）中写一个 .desktop 文件。
 */
export function autostart(host: Host, options: { name: string }): void {
  const linuxFile = () =>
    join(process.env.XDG_CONFIG_HOME || join(homedir(), ".config"), "autostart", `${options.name}.desktop`)
  host.handle("autostart.get", async () =>
    process.platform === "linux" ? existsSync(linuxFile()) : app.getLoginItemSettings().openAtLogin,
  )
  host.handle("autostart.set", async (enabled: boolean) => {
    if (process.platform !== "linux") {
      app.setLoginItemSettings({ openAtLogin: enabled === true })
      return
    }
    if (enabled !== true) {
      rmSync(linuxFile(), { force: true })
      return
    }
    mkdirSync(join(linuxFile(), ".."), { recursive: true })
    writeFileSync(linuxFile(), autostartEntry(options.name, process.env.APPIMAGE ?? process.execPath))
  })
}

/** deepLinks 的选项。 */
export interface DeepLinkOptions {
  scheme: string // 链接的协议，与 electron-builder.yml 的 protocols 相同，通常是客户端的名字
  base: string // 前端的挂载路径，只打开它之下的页面
}

/**
 * 深度链接：<scheme>://open/<路径> 打开或回到应用，并让页面跳到这个路径（与网页上的路径相同，邮件中只生成一种链接）。
 * 返回处理命令行参数的函数，交给 singleInstance 的 onSecondInstance：Windows、Linux 把链接作为新进程的参数。
 */
export function deepLinks(host: Host, options: DeepLinkOptions): (argv: string[]) => void {
  if (app.isPackaged) app.setAsDefaultProtocolClient(options.scheme)
  else if (process.argv[1]) app.setAsDefaultProtocolClient(options.scheme, process.execPath, [process.argv[1]]) // 开发时
  let pending: string | undefined
  let loaded = false
  const open = (url: string | undefined) => {
    const path = url && linkPath(url, options.scheme, options.base)
    if (!path) return
    focus(host.window())
    if (loaded) host.emit("navigate", path)
    else pending = path // 冷启动：页面加载完成后再跳
  }
  app.on("open-url", (e, url) => {
    e.preventDefault()
    open(url) // macOS
  })
  open(linkInArgv(process.argv, options.scheme))
  host.whenWindow((win) => {
    win.webContents.once("did-finish-load", () => {
      loaded = true
      if (pending) host.emit("navigate", pending)
      pending = undefined
    })
  })
  return (argv) => open(linkInArgv(argv, options.scheme))
}
