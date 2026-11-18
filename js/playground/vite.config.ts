import { fileURLToPath } from "node:url"
import { defineConfig } from "vite"

const path = (p: string) => fileURLToPath(new URL(`../${p}`, import.meta.url))

// @go-tenon/ui 的演示：每种布局、登录页和样式预设各一个页面，用来检查设计、截图。bun run playground 打开。
// playground 不是 workspace 中的包，框架的包按路径引用。
export default defineConfig({
  root: import.meta.dirname,
  resolve: {
    alias: {
      "@go-tenon/ui/styles.css": path("ui/src/styles.css"),
      "@go-tenon/ui": path("ui/src/index.ts"),
      "@go-tenon/client": path("client/src/index.ts"),
    },
  },
  server: { strictPort: true },
})
