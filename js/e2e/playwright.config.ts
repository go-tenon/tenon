import { defineConfig, devices } from "@playwright/test"

const port = Number(process.env.E2E_PORT || 4310)
const baseURL = `http://127.0.0.1:${port}`

// 在真实的浏览器中检查 @go-tenon/shell 的多标签与保活：浏览器的前进后退、地址栏、隐藏标签暂停 effect、
// 刷新后恢复标签栏。标签栏的各项操作由 shell/src/tabs.test.tsx 在 happy-dom 中检查。
export default defineConfig({
  testDir: "./tests",
  outputDir: "./test-results",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL,
    trace: "on-first-retry",
    ...devices["Desktop Chrome"],
  },
  webServer: {
    command: `bunx vite --config vite.config.ts --host 127.0.0.1 --port ${port}`,
    cwd: import.meta.dirname,
    url: baseURL,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
})
