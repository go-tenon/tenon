// @go-tenon/electron/update：用 electron-updater 自动更新。后台检查和下载，下载完成后告诉页面（update 事件），
// 退出时安装；服务端要求升级（426 app.upgrade_required，见 X-App-Version）时弹出对话框，立即下载安装。
// 更新源由 electron-builder.yml 的 publish 决定。只在打包后的应用中检查，开发时什么也不做。

import type { UpdateStatus } from "@go-tenon/host"
import { app, dialog } from "electron"
import { autoUpdater } from "electron-updater"
import type { Host } from "./main"

/** autoUpdate 的选项。 */
export interface AutoUpdateOptions {
  interval?: number // 两次检查的间隔（毫秒），默认 4 小时
}

/** 自动更新，并让页面可以调用 update.check、update.install。在 start 之前调用。 */
export function autoUpdate(host: Host, options: AutoUpdateOptions = {}): void {
  let status: UpdateStatus = { state: "none" }
  const set = (s: UpdateStatus) => {
    status = s
    host.emit("update", s)
  }
  autoUpdater.autoDownload = true
  autoUpdater.autoInstallOnAppQuit = true
  autoUpdater.on("update-available", (info) => set({ state: "downloading", version: info.version }))
  autoUpdater.on("update-not-available", () => set({ state: "none" }))
  autoUpdater.on("update-downloaded", (info) => set({ state: "ready", version: info.version }))
  autoUpdater.on("error", (err) => console.error("@go-tenon/electron: update failed", err))

  const check = async (): Promise<UpdateStatus> => {
    if (app.isPackaged) await autoUpdater.checkForUpdates()
    return status
  }
  /** 下载（还没有下载时）并安装更新，然后重启。没有新版本时抛出错误。 */
  const install = async (): Promise<void> => {
    if (status.state !== "ready") {
      const result = app.isPackaged ? await autoUpdater.checkForUpdates() : null
      if (!result?.isUpdateAvailable) throw new Error("no update is available")
      await result.downloadPromise
    }
    autoUpdater.quitAndInstall()
  }
  host.handle("update.check", check)
  host.handle("update.install", install)

  let forcing = false
  host.onResponse((res) => {
    if (res.status !== 426 || forcing) return
    forcing = true
    void (async () => {
      const win = host.window()
      const opts = {
        type: "info" as const,
        message: "需要更新",
        detail: "当前版本过低，更新到新版本后才能继续使用。",
        buttons: ["立即更新", "退出"],
        defaultId: 0,
        cancelId: 1,
      }
      const { response } = win ? await dialog.showMessageBox(win, opts) : await dialog.showMessageBox(opts)
      if (response === 1) return app.quit()
      try {
        await install()
      } catch (err) {
        await dialog.showMessageBox({ type: "error", message: "更新失败", detail: String(err) })
        forcing = false
      }
    })()
  })

  void app.whenReady().then(() => {
    if (!app.isPackaged) return
    const tick = () => void check().catch(() => {}) // 失败已经由 error 事件记录
    tick()
    setInterval(tick, options.interval ?? 4 * 60 * 60 * 1000)
  })
}
