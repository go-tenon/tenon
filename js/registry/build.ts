// 由 registry.json 生成 shadcn registry 的条目 r/<名称>.json：文件的内容内联在其中，`shadcn add` 直接读取它们。
// 修改 src/components/tenon 中的组件后运行 bun registry/build.ts；registry.test.ts 检查 r/ 是否最新。

import { readFileSync, writeFileSync } from "node:fs"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

/** registry.json 中的一个条目：files 是 src/components/tenon 中的文件名。 */
export interface ManifestItem {
  name: string
  type: string
  title: string
  description?: string
  registryDependencies?: string[]
  dependencies?: string[]
  files: string[]
}

export const tenonDir = "src/components/tenon"

/** 读取 registry.json。 */
export function manifest(root: string): { items: ManifestItem[] } {
  return JSON.parse(readFileSync(join(root, "registry.json"), "utf8"))
}

/** 生成全部条目：文件名到内容。 */
export function build(root: string): Map<string, string> {
  const out = new Map<string, string>()
  for (const item of manifest(root).items) {
    const json = {
      $schema: "https://ui.shadcn.com/schema/registry-item.json",
      name: item.name,
      type: item.type,
      title: item.title,
      ...(item.description ? { description: item.description } : {}),
      ...(item.dependencies ? { dependencies: item.dependencies } : {}),
      ...(item.registryDependencies ? { registryDependencies: item.registryDependencies } : {}),
      files: item.files.map((f) => ({
        path: `${tenonDir}/${f}`,
        type: "registry:component",
        target: `~/${tenonDir}/${f}`,
        content: readFileSync(join(root, tenonDir, f), "utf8"),
      })),
    }
    out.set(`${item.name}.json`, `${JSON.stringify(json, null, 2)}\n`)
  }
  return out
}

const self = fileURLToPath(import.meta.url)
if (process.argv[1] === self) {
  const root = dirname(self)
  for (const [name, content] of build(root)) writeFileSync(join(root, "r", name), content)
}
