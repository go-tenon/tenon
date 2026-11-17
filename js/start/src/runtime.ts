// 几个前端共用的 SSR 运行时：tenon-start runtime 把这个加载器连同 Bun 编译成一个可执行文件，Go 只嵌入一份。
// Go 的 ssr 包启动它时，把前端的代码（tenon-start compile 生成的 ssr.js）放在文件描述符 3 上；
// 加载器读出代码并导入它，代码中的 serve() 开始在 SSR_SOCKET 上提供页面。代码不落盘。

import { fstatSync, readSync } from "node:fs"

/** 前端代码所在的文件描述符，与 Go 的 ssr.BundleFD 相同。 */
const BundleFD = 3

// 从头读（pread）：重启后的进程拿到的是同一个文件描述，不能依赖当前的偏移。
const size = fstatSync(BundleFD).size
const code = Buffer.alloc(size)
for (let off = 0; off < size; ) {
  const n = readSync(BundleFD, code, off, size - off, off)
  if (n === 0) throw new Error(`tenon-start runtime: bundle truncated at ${off} of ${size} bytes`)
  off += n
}
// Bun 读取 import 的文件时会解析 /proc/self/fd 的链接，memfd 没有真实的路径，所以经 Blob URL 导入。
await import(URL.createObjectURL(new Blob([code], { type: "text/javascript" })))
