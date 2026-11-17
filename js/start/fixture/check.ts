// 集成测试（bun 运行，start.test.ts 调用它）：编译 fixture，在 unix socket 上运行，用假的 Go 接口检查约定。
import { join } from "node:path"

const dir = import.meta.dir
const id = `${process.pid}-${Date.now()}`
const ssrName = `@tenon-start-ssr-${id}`
const apiName = `@tenon-start-api-${id}`
const unix = (name: string) => `\0${name.slice(1)}`

function fail(what: string, detail?: unknown): never {
  console.error(`FAIL: ${what}`, detail === undefined ? "" : detail)
  process.exit(1)
}

// 假的 Go：内部接口返回收到的请求头。
const api = Bun.serve({
  unix: unix(apiName),
  fetch: (req) =>
    Response.json({
      path: new URL(req.url).pathname,
      cookie: req.headers.get("cookie") ?? "",
      requestId: req.headers.get("x-request-id") ?? "",
      traceparent: req.headers.get("traceparent") ?? "",
    }),
})

// 与 Go 相同：前端编译成 ssr.js，加上共用的运行时；启动运行时，ssr.js 放在文件描述符 3 上。
const cli = join(dir, "..", "src", "cli.ts")
const bundle = join(dir, "dist", "ssr.js")
const runtime = join(dir, "dist", "ssr-runtime.bin")
for (const args of [
  ["compile", "--out", bundle],
  ["runtime", "--out", runtime],
]) {
  const r = Bun.spawnSync(["bun", cli, ...args], { cwd: dir, stdout: "inherit", stderr: "inherit" })
  if (r.exitCode !== 0) fail(args[0])
}
const { openSync } = await import("node:fs")
const ssr = Bun.spawn([runtime], {
  env: { ...process.env, SSR_SOCKET: ssrName, API_SOCKET: apiName },
  stdio: ["ignore", "pipe", "inherit", openSync(bundle, "r")],
})
const page = (path: string, headers: Record<string, string> = {}) =>
  fetch(`http://ssr${path}`, { unix: unix(ssrName), headers } as RequestInit)

let ready = false
for (let i = 0; i < 100 && !ready; i++) {
  try {
    await page("/assets/none")
    ready = true
  } catch {
    await Bun.sleep(50)
  }
}
if (!ready) fail("ssr did not start")

const traceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
const res = await page("/articles/7", {
  Cookie: "__Host-session=tok",
  "X-Request-ID": "REQ-1",
  traceparent,
  "X-CSP-Nonce": "n1",
})
const html = await res.text()
if (res.status !== 200) fail("ssr status", res.status)
if (res.headers.get("X-Tenon-Route") !== "/articles/$id") fail("route header", res.headers.get("X-Tenon-Route"))
const echo = html.match(/<pre data-testid="echo">(.*?)<\/pre>/)?.[1]?.replaceAll("&quot;", '"')
const got = echo ? JSON.parse(echo) : undefined
if (
  got?.path !== "/api/articles/7" ||
  got.cookie !== "__Host-session=tok" ||
  got.requestId !== "REQ-1" ||
  got.traceparent !== traceparent
)
  fail("forwarded headers", got)
const inline = [...html.matchAll(/<script(?![^>]*\bsrc=)[^>]*>/g)].map((m) => m[0])
if (inline.length === 0 || inline.some((s) => !s.includes('nonce="n1"'))) fail("inline scripts without nonce", inline)

const home = await page("/", { "X-CSP-Nonce": "n2" })
const homeHtml = await home.text()
if (!homeHtml.includes("预渲染的首页") || homeHtml.includes("__tenon_csp_nonce__") || !homeHtml.includes('nonce="n2"'))
  fail("prerendered page", homeHtml.slice(0, 400))
if (home.headers.get("X-Tenon-Route")) fail("prerendered page reports a route")

const asset = homeHtml.match(/\/assets\/[^"]+\.js/)?.[0]
const assetRes = asset ? await page(asset) : undefined
if (assetRes?.status !== 200 || !assetRes.headers.get("Cache-Control")?.includes("immutable")) fail("asset", asset)

// 打印模板：Go 的 ssr.Host.Render 发来数据，返回完整的 HTML，样式内联。
const printed = await fetch("http://ssr/__tenon/print/invoice", {
  unix: unix(ssrName),
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ no: "INV-7", items: [{ name: "咨询服务", amount: 1200 }] }),
} as RequestInit)
const printedHtml = await printed.text()
if (
  printed.status !== 200 ||
  !printedHtml.startsWith('<!doctype html><html lang="zh-CN">') ||
  !printedHtml.includes("<title>发票 INV-7</title>") ||
  !printedHtml.includes("咨询服务") ||
  !printedHtml.includes("rgb(1, 2, 3)") ||
  !printedHtml.includes("@page")
)
  fail("print", printedHtml.slice(0, 600))
const missing = await fetch("http://ssr/__tenon/print/nope", {
  unix: unix(ssrName),
  method: "POST",
  body: "{}",
} as RequestInit)
if (missing.status !== 404) fail("unknown print template", missing.status)

ssr.kill("SIGTERM")
const code = await ssr.exited
if (code !== 0) fail("exit after SIGTERM", code)
api.stop(true)
console.log("ok")
