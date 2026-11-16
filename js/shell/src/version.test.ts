// @vitest-environment happy-dom
/// <reference types="vite/client" />
import { mkdir, mkdtemp, readdir, readFile, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { build } from "vite"
import { afterEach, describe, expect, it, vi } from "vitest"
import { watchVersion } from "./version"
import { tenonVersion } from "./vite"

const json = (data: unknown, ok = true) => ({ ok, json: async () => data }) as Response

afterEach(() => vi.useRealTimers())

describe("watchVersion", () => {
  it("版本号不同时提示一次并停止；读取失败时等下一次", async () => {
    vi.useFakeTimers()
    const replies = [
      Promise.reject(new TypeError("offline")),
      json({}, false),
      json({ version: "v1" }),
      json({ version: "v2" }),
    ]
    const fetch = vi.fn(async () => replies.shift() ?? json({ version: "v3" }))
    const onNewVersion = vi.fn()
    watchVersion({ current: "v1", url: "/admin/version.json", interval: 1000, fetch, onNewVersion })
    for (let i = 0; i < 6; i++) await vi.advanceTimersByTimeAsync(1000)
    expect(onNewVersion).toHaveBeenCalledTimes(1)
    expect(onNewVersion).toHaveBeenCalledWith("v2")
    expect(fetch).toHaveBeenCalledTimes(4)
    expect(fetch).toHaveBeenCalledWith("/admin/version.json", { cache: "no-store" })
  })

  it("页面不可见时不检查，重新可见时立即检查", async () => {
    vi.useFakeTimers()
    let state: DocumentVisibilityState = "hidden"
    vi.spyOn(document, "visibilityState", "get").mockImplementation(() => state)
    const fetch = vi.fn(async () => json({ version: "v1" }))
    const stop = watchVersion({ current: "v1", url: "/version.json", interval: 1000, fetch, onNewVersion: () => {} })
    await vi.advanceTimersByTimeAsync(3000)
    expect(fetch).not.toHaveBeenCalled()
    state = "visible"
    document.dispatchEvent(new Event("visibilitychange"))
    await vi.advanceTimersByTimeAsync(0)
    expect(fetch).toHaveBeenCalledTimes(1)
    stop()
    await vi.advanceTimersByTimeAsync(3000)
    expect(fetch).toHaveBeenCalledTimes(1)
  })
})

describe("tenonVersion", () => {
  it("构建时写入 version.json，并注入版本号和带 base 的地址", async () => {
    const root = await mkdtemp(join(tmpdir(), "tenon-version-"))
    await writeFile(join(root, "index.html"), '<script type="module" src="/main.js"></script>')
    await writeFile(
      join(root, "main.js"),
      "console.log(import.meta.env.TENON_BUILD_VERSION, import.meta.env.TENON_VERSION_URL)\n",
    )
    await build({ root, base: "/admin/", logLevel: "silent", plugins: [tenonVersion({ version: "abc123" })] })
    expect(JSON.parse(await readFile(join(root, "dist", "version.json"), "utf8"))).toEqual({ version: "abc123" })
    const html = await readFile(join(root, "dist", "index.html"), "utf8")
    const script = html.match(/src="\/admin\/(assets\/[^"]+\.js)"/)?.[1]
    const code = await readFile(join(root, "dist", script ?? ""), "utf8")
    expect(code).toMatch(/["`]abc123["`]/)
    expect(code).toMatch(/["`]\/admin\/version\.json["`]/)
  })

  it("sourcemaps：生成 hidden source map，移出 dist，放到指定的目录", async () => {
    const root = await mkdtemp(join(tmpdir(), "tenon-sourcemaps-"))
    await writeFile(join(root, "index.html"), '<script type="module" src="/main.js"></script>')
    await writeFile(join(root, "main.js"), "export const answer = () => 42\nconsole.log(answer())\n")
    // 上一次构建留下的文件：构建前清空。
    await mkdir(join(root, "sourcemaps", "assets"), { recursive: true })
    await writeFile(join(root, "sourcemaps", "assets", "stale.js.map"), "{}")
    await build({ root, logLevel: "silent", plugins: [tenonVersion({ version: "v1", sourcemaps: "sourcemaps" })] })

    const list = async (dir: string) =>
      (await readdir(join(root, dir), { recursive: true })).map((f) => String(f)).sort()
    expect((await list("dist")).filter((f) => f.endsWith(".map"))).toEqual([])
    const maps = await list("sourcemaps")
    expect(maps).toHaveLength(2) // assets 目录和其中的一个 .map
    const map = maps.find((f) => f.endsWith(".js.map")) ?? ""
    expect(map).not.toContain("stale")
    expect(
      JSON.parse(await readFile(join(root, "sourcemaps", map), "utf8")).sources.some((s: string) =>
        s.includes("main.js"),
      ),
    ).toBe(true)
    // 脚本中没有 sourceMappingURL：浏览器不会请求 source map。
    const script = map.replace(/\.map$/, "")
    expect(await readFile(join(root, "dist", script), "utf8")).not.toContain("sourceMappingURL")
  })
})
