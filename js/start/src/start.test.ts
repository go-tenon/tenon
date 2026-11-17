import { spawnSync } from "node:child_process"
import { fileURLToPath } from "node:url"
import { describe, expect, it } from "vitest"
import { defaultTarget, entrySource, normalizeBase } from "./compile"
import { forwardHeaders, socketPath, stripBase } from "./headers"

describe("headers", () => {
  it("只转交登录凭据、请求 ID、链路和语言", () => {
    const h = new Headers({
      Cookie: "__Host-session=t",
      "X-Request-ID": "r1",
      traceparent: "00-a-b-01",
      "Accept-Language": "zh-CN",
      "X-CSP-Nonce": "n",
      Host: "example.com",
    })
    expect(forwardHeaders(h)).toEqual({
      cookie: "__Host-session=t",
      "x-request-id": "r1",
      traceparent: "00-a-b-01",
      "accept-language": "zh-CN",
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

  it("挂载路径", () => {
    expect(normalizeBase()).toBe("/")
    expect(normalizeBase("admin")).toBe("/admin/")
    expect(normalizeBase("/admin/")).toBe("/admin/")
    expect(stripBase("/admin/assets/a.js", "/admin/")).toBe("/assets/a.js")
    expect(stripBase("/admin", "/admin/")).toBe("/")
    expect(stripBase("/assets/a.js", "/admin/")).toBeUndefined()
    expect(stripBase("/docs/", "/")).toBe("/docs/")
  })

  it("入口嵌入每个静态文件，路径以 / 开头", () => {
    const src = entrySource(
      "/app/dist/server/server.js",
      ["assets/a.js", "index.html"],
      "/app/dist/client",
      "/app/node_modules/.tenon-start",
    )
    expect(src).toContain('import { serve } from "@go-tenon/start/serve"')
    expect(src).toContain('import handler from "../../dist/server/server.js"')
    expect(src).toContain('import f0 from "../../dist/client/assets/a.js" with { type: "file" }')
    expect(src).toContain('"/index.html": f1,')
    expect(src).toContain('base: "/"')
  })
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
