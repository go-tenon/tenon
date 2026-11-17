#!/usr/bin/env bun
// tenon-start：把前端编译成 Go 的 ssr 包托管的 SSR 服务。
//
//	bunx tenon-start compile --out ssr.js --base /admin/     # 每个前端：服务端入口和全部静态文件，一个 JS 文件
//	bunx tenon-start runtime --out ssr-runtime.bin           # 整个程序一份：Bun 和加载器，几个前端共用

import { mkdirSync, rmSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"
import { Glob } from "bun"
import { defaultTarget, entrySource } from "./compile"

const usage = `usage:
  tenon-start compile --out <ssr.js> [--base /admin/] [--skip-build]
  tenon-start runtime --out <file> [--target bun-linux-x64|bun-linux-arm64]`

async function run(cmd: string[]): Promise<void> {
  const p = Bun.spawn(cmd, { stdout: "inherit", stderr: "inherit" })
  if ((await p.exited) !== 0) throw new Error(`${cmd.join(" ")} failed`)
}

async function compile(args: string[]): Promise<void> {
  const { values } = parseArgs({
    args,
    options: { out: { type: "string" }, base: { type: "string" }, "skip-build": { type: "boolean" } },
  })
  if (!values.out) throw new Error(usage)
  const root = process.cwd()
  if (!values["skip-build"]) await run(["bunx", "--bun", "vite", "build"])
  const clientDir = join(root, "dist", "client")
  const files: Record<string, string> = {}
  for await (const f of new Glob("**/*").scan({ cwd: clientDir, onlyFiles: true })) {
    if (f.endsWith(".map")) continue // source map 等于源码，不嵌入
    files[f] = Buffer.from(await Bun.file(join(clientDir, f)).arrayBuffer()).toString("base64")
  }
  const entryDir = join(root, "node_modules", ".tenon-start")
  rmSync(entryDir, { recursive: true, force: true })
  mkdirSync(entryDir, { recursive: true })
  const entry = join(entryDir, "entry.ts")
  await Bun.write(entry, entrySource(join(root, "dist", "server", "server.js"), files, entryDir, values.base))
  // 打包成一个文件（动态导入也内联）：共用的运行时从内存中加载它。
  await run([
    "bun",
    "build",
    "--target=bun",
    "--format=esm",
    "--minify",
    "--sourcemap=none",
    entry,
    "--outfile",
    resolve(values.out),
  ])
}

async function runtime(args: string[]): Promise<void> {
  const { values } = parseArgs({ args, options: { out: { type: "string" }, target: { type: "string" } } })
  if (!values.out) throw new Error(usage)
  const loader = join(import.meta.dir, "runtime.ts")
  await run([
    "bun",
    "build",
    "--compile",
    "--minify",
    "--sourcemap=none",
    `--target=${values.target ?? defaultTarget()}`,
    loader,
    "--outfile",
    resolve(values.out),
  ])
}

const commands: Record<string, (args: string[]) => Promise<void>> = { compile, runtime }

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
