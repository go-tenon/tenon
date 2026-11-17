import { configDefaults, defineConfig } from "vitest/config"

// e2e/ 中是 Playwright 的测试，用 bun run e2e 运行。
export default defineConfig({
  test: { exclude: [...configDefaults.exclude, "e2e/**"] },
})
