// 日志文件：一行一条，超过大小时把当前文件换成 .old，只保留一份旧文件。

import { appendFileSync, mkdirSync, renameSync, statSync } from "node:fs"
import { dirname } from "node:path"

/** 把 line 追加到 file；file 超过 maxSize 字节时先换成 <file>.old（覆盖已有的）。 */
export function appendLog(file: string, line: string, maxSize: number): void {
  mkdirSync(dirname(file), { recursive: true })
  try {
    if (statSync(file).size > maxSize) renameSync(file, `${file}.old`)
  } catch {
    // 文件还不存在
  }
  appendFileSync(file, `${line}\n`)
}
