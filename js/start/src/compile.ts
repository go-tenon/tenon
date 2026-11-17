// tenon-start compile 用到的纯函数，单独放在这里，测试不需要 Bun。

import { relative } from "node:path"

/**
 * 运行时的默认编译目标：Go 的程序是为 GOARCH 构建的（go tool tenon build 设置它），没有时是当前机器的架构。
 * Go 托管 SSR 进程只支持 Linux。前端的 ssr.js 与架构无关。
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

/**
 * 生成编译入口的源码：导入服务端入口，把 dist/client 中的每个文件以 base64 内联（files 是路径到内容的映射）。
 * 编译出的 ssr.js 是一个文件，不依赖任何外部文件，由共用的运行时加载（见 tenon-start runtime）。
 */
export function entrySource(serverEntry: string, files: Record<string, string>, entryDir: string, base = "/"): string {
  const map = Object.keys(files)
    .sort()
    .map((f) => `  ${JSON.stringify(f.startsWith("/") ? f : `/${f}`)}: ${JSON.stringify(files[f])},`)
  return [
    'import { serve } from "@go-tenon/start/serve"',
    `import handler from ${JSON.stringify(relativeImport(entryDir, serverEntry))}`,
    "",
    `serve({ handler, base: ${JSON.stringify(normalizeBase(base))}, files: {`,
    ...map,
    "} })",
    "",
  ].join("\n")
}

function relativeImport(from: string, to: string): string {
  const r = relative(from, to)
  return r.startsWith(".") ? r : `./${r}`
}
