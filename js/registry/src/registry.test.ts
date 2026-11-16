// registry 的条目：r/ 是最新的；每个条目带上了它用到的 shadcn 组件和 tenon 文件。
import { existsSync, readdirSync, readFileSync } from "node:fs"
import { join } from "node:path"
import { describe, expect, it } from "vitest"
import { build, manifest, tenonDir } from "../build"

const root = join(import.meta.dirname, "..")

// 项目模板（console）已经带着的包，条目不需要再写进 dependencies。
const provided = new Set([
  "react",
  "lucide-react",
  "radix-ui",
  "@go-tenon/client",
  "@go-tenon/shell",
  "@tanstack/react-query",
  "@tanstack/react-router",
])

describe("registry", () => {
  it("r/ 与 src/components/tenon 一致（修改后运行 bun registry/build.ts）", () => {
    const built = build(root)
    expect(readdirSync(join(root, "r")).sort()).toEqual([...built.keys()].sort())
    for (const [name, content] of built) {
      expect(readFileSync(join(root, "r", name), "utf8"), name).toBe(content)
    }
  })

  it("每个条目带上了它导入的 shadcn 组件和 tenon 文件", () => {
    const tenonFiles = readdirSync(join(root, tenonDir)).filter((f) => f.endsWith(".tsx"))
    const listed = new Set(manifest(root).items.flatMap((i) => i.files))
    expect(tenonFiles.filter((f) => !listed.has(f))).toEqual([]) // 每个组件都在某个条目中
    for (const item of manifest(root).items) {
      const ui = new Set(item.registryDependencies ?? [])
      const files = new Set(item.files)
      for (const f of item.files) {
        const src = readFileSync(join(root, tenonDir, f), "utf8")
        for (const [, name] of src.matchAll(/from "@\/components\/ui\/([\w-]+)"/g)) {
          expect(ui.has(name), `${item.name}: ${f} imports ui/${name}`).toBe(true)
          expect(existsSync(join(root, "src/components/ui", `${name}.tsx`))).toBe(true)
        }
        for (const [, name] of src.matchAll(/from "@\/components\/tenon\/([\w-]+)"/g)) {
          expect(files.has(`${name}.tsx`), `${item.name}: ${f} imports tenon/${name}`).toBe(true)
        }
        for (const [, name] of src.matchAll(/from "@\/hooks\/([\w-]+)"/g)) {
          expect(ui.has(name), `${item.name}: ${f} imports hooks/${name}`).toBe(true)
          expect(existsSync(join(root, "src/hooks", `${name}.ts`))).toBe(true)
        }
        // 用到的 npm 包都写在 dependencies 中，项目模板已经带着的除外。
        for (const [, pkg] of src.matchAll(/from "((?:@[\w-]+\/)?[\w-]+)/g)) {
          if (provided.has(pkg)) continue
          expect(
            item.dependencies?.some((d) => d.startsWith(`${pkg}@`)),
            `${item.name}: ${f} imports ${pkg}`,
          ).toBe(true)
        }
      }
    }
  })
})
