// tenon-start compile 用到的纯函数，单独放在这里，测试不需要 Bun。

import { join, relative } from "node:path"

/** 当前机器对应的 Bun 编译目标。 */
export function defaultTarget(arch = process.arch): string {
  return `bun-linux-${arch === "arm64" ? "arm64" : "x64"}`
}

/** 生成编译入口的源码：导入服务端入口，用 with { type: "file" } 嵌入 dist/client 中的每个文件。 */
export function entrySource(serverEntry: string, files: string[], clientDir: string, entryDir: string): string {
  const lines = [
    'import { serve } from "@go-tenon/start/serve"',
    `import handler from ${JSON.stringify(relativeImport(entryDir, serverEntry))}`,
  ]
  const map: string[] = []
  files.forEach((f, i) => {
    lines.push(
      `import f${i} from ${JSON.stringify(relativeImport(entryDir, join(clientDir, f)))} with { type: "file" }`,
    )
    map.push(`  ${JSON.stringify(`/${f}`)}: f${i},`)
  })
  return `${lines.join("\n")}\n\nserve({ handler, files: {\n${map.join("\n")}\n} })\n`
}

function relativeImport(from: string, to: string): string {
  const r = relative(from, to)
  return r.startsWith(".") ? r : `./${r}`
}
