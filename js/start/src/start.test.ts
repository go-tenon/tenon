import { spawnSync } from "node:child_process"
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { fileURLToPath } from "node:url"
import { describe, expect, it, vi } from "vitest"
import { defaultTarget, entrySource } from "./compile"
import { forwardHeaders, HeaderNonce, PrerenderNonce, socketPath } from "./headers"
import * as vite from "./vite.js"

describe("headers", () => {
  it("只转交登录凭据、请求 ID、链路、语言和代理主机", () => {
    const h = new Headers({
      Cookie: "__Host-session=t",
      "X-Request-ID": "r1",
      traceparent: "00-a-b-01",
      "Accept-Language": "zh-CN",
      "X-CSP-Nonce": "n",
      Host: "acme.example.com",
    })
    expect(forwardHeaders(h)).toEqual({
      cookie: "__Host-session=t",
      "x-request-id": "r1",
      traceparent: "00-a-b-01",
      "accept-language": "zh-CN",
      "x-forwarded-host": "acme.example.com",
    })
  })

  it("抽象 socket 的 @ 换成 \\0，路径原样", () => {
    expect(socketPath("@tenon-ssr")).toBe("\0tenon-ssr")
    expect(socketPath("/run/ssr.sock")).toBe("/run/ssr.sock")
  })
})

describe("compile", () => {
  it("编译目标跟随 GOARCH，没有时是机器的架构", () => {
    expect(defaultTarget("arm64", "x64")).toBe("bun-linux-arm64")
    expect(defaultTarget("amd64", "arm64")).toBe("bun-linux-x64")
    expect(defaultTarget(undefined, "arm64")).toBe("bun-linux-arm64")
  })

  it("入口按 TENON_FRONTEND 动态导入其中一个前端的服务端入口", () => {
    const src = entrySource(
      { web: "/app/apps/web/dist/server/server.js", admin: "/app/apps/admin/dist/server/server.js" },
      "/app/apps/web/node_modules/.tenon-start",
      "/app/tenon/js/start/src",
    )
    expect(src).toContain('import { type Handler, serve } from "../../../../tenon/js/start/src/serve.ts"')
    expect(src).toContain(
      '  "admin": () => import("../../../admin/dist/server/server.js"),\n  "web": () => import("../../dist/server/server.js"),',
    )
    expect(src).toContain("const name = process.env[EnvFrontend]")
  })
})

describe("vite", () => {
  it("常量与 headers.ts 相同", () => {
    expect(vite.HeaderNonce).toBe(HeaderNonce)
    expect(vite.PrerenderNonce).toBe(PrerenderNonce)
  })

  it("从 package.json 的 tenon 字段读取挂载路径和是否服务端渲染", () => {
    const dir = mkdtempSync(join(tmpdir(), "tenon-start-"))
    const write = (tenon: unknown) => writeFileSync(join(dir, "package.json"), JSON.stringify({ tenon }))
    write({ base: "/admin/" })
    expect(vite.frontendConfig(dir)).toEqual({ base: "/admin/", ssr: false })
    write({ base: "/", ssr: true })
    expect(vite.frontendConfig(dir)).toEqual({ base: "/", ssr: true })
    // 为宿主构建时（TENON_RENDER=csr）不论 tenon.ssr 都只在浏览器中渲染。
    vi.stubEnv("TENON_RENDER", "csr")
    expect(vite.frontendConfig(dir)).toEqual({ base: "/", ssr: false })
    vi.unstubAllEnvs()
    write({ base: "/admin" })
    expect(() => vite.frontendConfig(dir)).toThrow("tenon.base must start and end with /")
  })

  it("只在浏览器中渲染时打开 SPA 模式，页面外壳带占位 nonce", () => {
    const headers = { [HeaderNonce]: PrerenderNonce }
    expect(vite.startOptions(true, [])).toEqual({})
    expect(vite.startOptions(false, [])).toEqual({ spa: { enabled: true, prerender: { headers } } })
    const spa = vite.startOptions(false, ["/docs"])
    // 页面外壳的地址 / 和 /docs 下的页面都要预渲染，其余不预渲染。
    const filter = spa.prerender?.filter as (page: { path: string }) => boolean
    expect(["/", "/docs", "/docs/intro", "/blog"].map((path) => filter({ path }))).toEqual([true, true, true, false])
    expect(spa.prerender?.headers).toEqual(headers)
    const ssr = vite.startOptions(true, ["/"]).prerender?.filter as (page: { path: string }) => boolean
    expect(["/", "/docs"].map((path) => ssr({ path }))).toEqual([true, false])
    // 只在浏览器中渲染的前端同样可以预渲染首页 /：外壳改用独立查询参数，避免与首页在预渲染队列中冲突。
    const staticSite = vite.startOptions(false, ["/", "/docs"])
    expect(staticSite.spa).toEqual({ enabled: true, maskPath: "/?__tenon_shell=1", prerender: { headers } })
    const staticFilter = staticSite.prerender?.filter as (page: { path: string }) => boolean
    expect(["/", "/?__tenon_shell=1", "/docs", "/docs/intro", "/blog"].map((path) => staticFilter({ path }))).toEqual([
      true,
      true,
      true,
      true,
      false,
    ])
  })

  // vite dev 在 Node 中运行，由 Node 直接导入 @go-tenon/start/vite：它不能是 TypeScript。
  it("Node 可以直接导入", () => {
    const file = fileURLToPath(new URL("./vite.js", import.meta.url))
    const r = spawnSync(
      "node",
      ["--input-type=module", "-e", `const m = await import(${JSON.stringify(file)}); console.log(typeof m.tenon)`],
      { encoding: "utf8" },
    )
    expect(r.stderr).toBe("")
    expect(r.stdout.trim()).toBe("function")
  })
})

// tenon-start routes 用 Start 的 Vite 插件生成路由树：有全部页面，末尾有 Start 的 Register 类型声明（tsr generate 没有）。
describe("routes", () => {
  it("生成与 vite dev 相同的路由树", () => {
    const dir = fileURLToPath(new URL("../fixture", import.meta.url))
    const file = join(dir, "src", "routeTree.gen.ts")
    rmSync(file, { force: true })
    const cli = fileURLToPath(new URL("./cli.ts", import.meta.url))
    const r = spawnSync("bun", [cli, "routes"], { cwd: dir, encoding: "utf8", timeout: 60_000 })
    expect(r.status, r.stderr).toBe(0)
    const tree = readFileSync(file, "utf8")
    expect(tree).toContain("'/articles/$id'")
    expect(tree).toContain("declare module '@tanstack/react-start'")
    expect(tree).toContain("router: Awaited<ReturnType<typeof getRouter>>")
  }, 90_000)
})

// 构建并编译 fixture，在 unix socket 上运行，用假的 Go 接口检查：转交请求头、路由模板、nonce、预渲染、退出。
describe("integration", () => {
  it("编译出的 SSR 服务按约定工作", () => {
    const dir = fileURLToPath(new URL("../fixture", import.meta.url))
    const r = spawnSync("bun", ["check.ts"], { cwd: dir, encoding: "utf8", timeout: 240_000 })
    expect(r.stderr.split("\n").filter((l) => l.startsWith("FAIL"))).toEqual([])
    expect(r.stdout.trim().split("\n").pop()).toBe("ok")
  }, 300_000)
})
