import { fileURLToPath } from "node:url"
import { configDefaults, defineConfig } from "vitest/config"

// @/ 是 shadcn 组件的路径别名，只有 registry 中的组件使用。e2e/ 中是 Playwright 的测试，用 bun run e2e 运行。
export default defineConfig({
  test: { exclude: [...configDefaults.exclude, "e2e/**"] },
  resolve: { alias: { "@": fileURLToPath(new URL("./registry/src", import.meta.url)) } },
})
