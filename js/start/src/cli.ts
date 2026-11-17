#!/usr/bin/env bun
// tenon-start compile：构建前端，把 SSR 服务和全部静态文件编译成一个可执行文件，由 Go 嵌入（见 Go 的 ssr 包）。
//
//	bunx tenon-start compile --out ../../internal/web/ssr.bin --target bun-linux-arm64

import { mkdirSync, rmSync } from "node:fs"
import { join, resolve } from "node:path"
import { parseArgs } from "node:util"
import { Glob } from "bun"
import { defaultTarget, entrySource } from "./compile"

const usage = "usage: tenon-start compile --out <file> [--target bun-linux-x64|bun-linux-arm64] [--skip-build]"

async function run(cmd: string[]): Promise<void> {
  const p = Bun.spawn(cmd, { stdout: "inherit", stderr: "inherit" })
  if ((await p.exited) !== 0) throw new Error(`${cmd.join(" ")} failed`)
}

async function compile(args: string[]): Promise<void> {
  const { values } = parseArgs({
    args,
    options: { out: { type: "string" }, target: { type: "string" }, "skip-build": { type: "boolean" } },
  })
  if (!values.out) throw new Error(usage)
  const root = process.cwd()
  if (!values["skip-build"]) await run(["bunx", "--bun", "vite", "build"])
  const clientDir = join(root, "dist", "client")
  const files: string[] = []
  for await (const f of new Glob("**/*").scan({ cwd: clientDir, onlyFiles: true })) {
    if (!f.endsWith(".map")) files.push(f) // source map 等于源码，不嵌入
  }
  files.sort()
  const entryDir = join(root, "node_modules", ".tenon-start")
  rmSync(entryDir, { recursive: true, force: true })
  mkdirSync(entryDir, { recursive: true })
  const entry = join(entryDir, "entry.ts")
  await Bun.write(entry, entrySource(join(root, "dist", "server", "server.js"), files, clientDir, entryDir))
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

if (import.meta.main) {
  const [command, ...rest] = Bun.argv.slice(2)
  if (command !== "compile") {
    console.error(usage)
    process.exit(2)
  }
  compile(rest).catch((err) => {
    console.error(err instanceof Error ? err.message : err)
    process.exit(1)
  })
}
