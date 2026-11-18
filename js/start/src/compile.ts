// tenon-start compile 用到的纯函数，单独放在这里，测试不需要 Bun。

import { relative } from "node:path"

/**
 * 可执行文件的默认编译目标：Go 的程序是为 GOARCH 构建的（go tool tenon build 设置它），没有时是当前机器的架构。
 * Go 托管 SSR 进程只支持 Linux。
 */
export function defaultTarget(goarch = process.env.GOARCH, arch = process.arch): string {
  const a = goarch || arch
  return `bun-linux-${a === "arm64" ? "arm64" : "x64"}`
}

/**
 * 生成编译入口的源码，入口写在 entryDir 中：entries 是前端的名称到它的服务端入口（dist/server/server.js 的绝对路径），
 * srcDir 是这个包的 src 目录，serve 和 headers 按路径导入（入口所在的项目不一定能按包名解析到它们）。
 * 入口按环境变量 TENON_FRONTEND 动态导入其中一个前端，交给 serve：几个 SSR 前端编进同一个可执行文件，
 * 每个进程只加载并运行自己的那一个。
 */
export function entrySource(entries: Record<string, string>, entryDir: string, srcDir: string): string {
  const loaders = Object.keys(entries)
    .sort()
    .map(
      (name) => `  ${JSON.stringify(name)}: () => import(${JSON.stringify(relativeImport(entryDir, entries[name]))}),`,
    )
  return [
    `import { EnvFrontend } from ${JSON.stringify(relativeImport(entryDir, `${srcDir}/headers.ts`))}`,
    `import { type Handler, serve } from ${JSON.stringify(relativeImport(entryDir, `${srcDir}/serve.ts`))}`,
    "",
    "const frontends: Record<string, () => Promise<{ default: Handler }>> = {",
    ...loaders,
    "}",
    'const name = process.env[EnvFrontend] ?? ""',
    "const load = frontends[name]",
    'if (!load) throw new Error("tenon-start: unknown frontend " + JSON.stringify(name) + " in " + EnvFrontend)',
    "serve({ handler: (await load()).default })",
    "",
  ].join("\n")
}

function relativeImport(from: string, to: string): string {
  const r = relative(from, to)
  return r.startsWith(".") ? r : `./${r}`
}
