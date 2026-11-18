// 主进程的日志和崩溃报告。日志写入系统的应用日志目录中的 main.log（macOS 是 ~/Library/Logs/<应用>，其他系统在
// userData 中），超过大小时换到 main.log.old；崩溃报告保存在本地，设置了上报地址时上传。

import { join } from "node:path"
import { format } from "node:util"
import { app, crashReporter } from "electron"
import { appendLog } from "./logfile"

/** logs 的选项。 */
export interface LogOptions {
  crashReportURL?: string // 崩溃报告的上传地址（Crashpad 格式，例如自建的 Sentry 或 BugSplat）；不设置时只保存在本地
  maxSize?: number // 日志文件的最大字节数，默认 5 MB
}

/** 把主进程的 console 输出同时写入日志文件，并开始收集崩溃报告。在 createHost 之后、start 之前调用。 */
export function logs(options: LogOptions = {}): void {
  crashReporter.start({
    submitURL: options.crashReportURL ?? "",
    uploadToServer: options.crashReportURL !== undefined,
    compress: true,
  })
  const max = options.maxSize ?? 5 * 1024 * 1024
  const file = () => join(app.getPath("logs"), "main.log")
  for (const level of ["log", "info", "warn", "error"] as const) {
    const original = console[level].bind(console)
    console[level] = (...args: unknown[]) => {
      original(...args)
      try {
        appendLog(file(), `${new Date().toISOString()} ${level.toUpperCase()} ${format(...args)}`, max)
      } catch (err) {
        original("@go-tenon/electron: writing the log file failed", err)
      }
    }
  }
}
