#!/usr/bin/env bun
// tenon-start：前端的命令行工具。
//
// compile 把服务端渲染的前端编译成 Go 的 frontend 包托管的 SSR 可执行文件。go tool tenon build 在每个前端
// vite build 之后运行它，参数是每个 SSR 前端的名称和服务端入口：
//
//	bunx tenon-start compile --out apps/dist/ssr web=apps/web/dist/server/server.js
//
// routes 在前端的目录中生成路由树 src/routeTree.gen.ts，前端的 typecheck 先运行它：
//
//	tenon-start routes && tsc --noEmit

import { mkdirSync, rmSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"
import { defaultTarget, entrySource } from "./compile"

const usage = `usage:
  tenon-start compile --out <file> [--target bun-linux-x64|bun-linux-arm64] <name>=<dist/server/server.js>...
  tenon-start routes`

async function run(cmd: string[]): Promise<void> {
  const p = Bun.spawn(cmd, { stdout: "inherit", stderr: "inherit" })
  if ((await p.exited) !== 0) throw new Error(`${cmd.join(" ")} failed`)
}

/** 把几个前端的服务端入口和 Bun 编译成一个可执行文件，进程按 TENON_FRONTEND 运行其中一个。 */
async function compile(args: string[]): Promise<void> {
  const { values, positionals } = parseArgs({
    args,
    options: { out: { type: "string" }, target: { type: "string" } },
    allowPositionals: true,
  })
  if (!values.out || positionals.length === 0) throw new Error(usage)
  const entries: Record<string, string> = {}
  for (const p of positionals) {
    const [name, entry] = p.split("=", 2)
    if (!name || !entry) throw new Error(usage)
    entries[name] = resolve(entry)
  }
  const entryDir = join(process.cwd(), "node_modules", ".tenon-start")
  rmSync(entryDir, { recursive: true, force: true })
  mkdirSync(entryDir, { recursive: true })
  const entry = join(entryDir, "entry.ts")
  await Bun.write(entry, entrySource(entries, entryDir, import.meta.dir))
  await run([
    "bun",
    "build",
    "--compile",
    "--minify",
    "--sourcemap=none",
    `--target=${values.target ?? defaultTarget()}`,
    entry,
    "--outfile",
    resolve(values.out),
  ])
}

/**
 * 按当前目录的 vite.config 生成路由树：创建一个不监听端口的 Vite 开发服务器，TanStack Start 的插件在启动时生成
 * src/routeTree.gen.ts，然后关闭。生成器与 vite dev、vite build 是同一个，结果相同，包括末尾 Start 的 Register
 * 类型声明；@tanstack/router-cli 的 tsr generate 不带这段声明，与 vite dev 生成的文件来回变化。
 */
async function routes(args: string[]): Promise<void> {
  if (args.length > 0) throw new Error(usage)
  const { createServer } = await import("vite")
  const server = await createServer({
    server: { middlewareMode: true, hmr: false, ws: false },
    appType: "custom",
    logLevel: "warn",
  })
  await server.close()
}

const commands: Record<string, (args: string[]) => Promise<void>> = { compile, routes }

if (import.meta.main) {
  const [command, ...rest] = Bun.argv.slice(2)
  const fn = command ? commands[command] : undefined
  if (!fn) {
    console.error(usage)
    process.exit(2)
  }
  fn(rest).catch((err) => {
    console.error(err instanceof Error ? err.message : err)
    process.exit(1)
  })
}
