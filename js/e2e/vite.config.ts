import { fileURLToPath } from "node:url"
import { defineConfig } from "vite"

const src = (pkg: string) => fileURLToPath(new URL(`../${pkg}/src/index.ts`, import.meta.url))

// E2E 用的应用：只用 @go-tenon/shell，标签栏是最简单的按钮，测试的是 shell 本身而不是某一套外观。
// e2e 不是 workspace 中的包（每个有 package.json 的目录都会复制到项目的 .tenon/js），框架的包按路径引用。
export default defineConfig({
  root: import.meta.dirname,
  resolve: { alias: { "@go-tenon/shell": src("shell"), "@go-tenon/client": src("client") } },
  server: { strictPort: true },
})
