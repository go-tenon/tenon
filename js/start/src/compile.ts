// tenon-start compile 用到的纯函数，单独放在这里，测试不需要 Bun。

import { join, relative } from "node:path"

/**
 * 默认的 Bun 编译目标：Go 的程序是为 GOARCH 构建的（go tool tenon build 设置它），没有时是当前机器的架构。
 * Go 托管 SSR 进程只支持 Linux。
 */
export function defaultTarget(goarch = process.env.GOARCH, arch = process.arch): string {
  const a = goarch || arch
  return `bun-linux-${a === "arm64" ? "arm64" : "x64"}`
}

/** 规范挂载路径：以 / 开头和结尾，例如 /admin/。 */
export function normalizeBase(base = "/"): string {
  const trimmed = base.replace(/^\/+|\/+$/g, "")
  return trimmed ? `/${trimmed}/` : "/"
}

/** 生成编译入口的源码：导入服务端入口，用 with { type: "file" } 嵌入 dist/client 中的每个文件。 */
export function entrySource(
  serverEntry: string,
  files: string[],
  clientDir: string,
  entryDir: string,
  base = "/",
): string {
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
  return `${lines.join("\n")}\n\nserve({ handler, base: ${JSON.stringify(normalizeBase(base))}, files: {\n${map.join("\n")}\n} })\n`
}

function relativeImport(from: string, to: string): string {
  const r = relative(from, to)
  return r.startsWith(".") ? r : `./${r}`
}
