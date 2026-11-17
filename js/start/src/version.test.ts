import { mkdir, mkdtemp, readdir, readFile, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { build } from "vite"
import { describe, expect, it } from "vitest"
import { versionPlugin } from "./version.js"

describe("versionPlugin", () => {
  it("构建时写入 version.json，并注入版本号和带 base 的地址", async () => {
    const root = await mkdtemp(join(tmpdir(), "tenon-version-"))
    await writeFile(join(root, "index.html"), '<script type="module" src="/main.js"></script>')
    await writeFile(
      join(root, "main.js"),
      "console.log(import.meta.env.TENON_BUILD_VERSION, import.meta.env.TENON_VERSION_URL)\n",
    )
    await build({ root, base: "/admin/", logLevel: "silent", plugins: [versionPlugin({ version: "abc123" })] })
    expect(JSON.parse(await readFile(join(root, "dist", "version.json"), "utf8"))).toEqual({ version: "abc123" })
    const html = await readFile(join(root, "dist", "index.html"), "utf8")
    const script = html.match(/src="\/admin\/(assets\/[^"]+\.js)"/)?.[1]
    const code = await readFile(join(root, "dist", script ?? ""), "utf8")
    expect(code).toMatch(/["`]abc123["`]/)
    expect(code).toMatch(/["`]\/admin\/version\.json["`]/)
  })

  it("默认的版本号在一个构建进程中只生成一次：vite.config 被加载多次时，页面和 version.json 中相同", async () => {
    delete process.env.TENON_BUILD_VERSION
    const versions: string[] = []
    for (const plugin of [versionPlugin(), versionPlugin()]) {
      const root = await mkdtemp(join(tmpdir(), "tenon-version-once-"))
      await writeFile(join(root, "index.html"), '<script type="module" src="/main.js"></script>')
      await writeFile(join(root, "main.js"), "console.log(import.meta.env.TENON_BUILD_VERSION)\n")
      await build({ root, logLevel: "silent", plugins: [plugin] })
      versions.push(JSON.parse(await readFile(join(root, "dist", "version.json"), "utf8")).version)
    }
    expect(versions[0]).toBeTruthy()
    expect(versions[1]).toBe(versions[0])
    expect(process.env.TENON_BUILD_VERSION).toBe(versions[0])
    delete process.env.TENON_BUILD_VERSION
  })

  it("sourcemaps：生成 hidden source map，移出 dist，放到指定的目录", async () => {
    const root = await mkdtemp(join(tmpdir(), "tenon-sourcemaps-"))
    await writeFile(join(root, "index.html"), '<script type="module" src="/main.js"></script>')
    await writeFile(join(root, "main.js"), "export const answer = () => 42\nconsole.log(answer())\n")
    // 上一次构建留下的文件：构建前清空。
    await mkdir(join(root, "sourcemaps", "assets"), { recursive: true })
    await writeFile(join(root, "sourcemaps", "assets", "stale.js.map"), "{}")
    await build({ root, logLevel: "silent", plugins: [versionPlugin({ version: "v1", sourcemaps: "sourcemaps" })] })

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
