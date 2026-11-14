// @vitest-environment happy-dom
import { mkdtemp, readFile, writeFile } from "node:fs/promises"
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
})
