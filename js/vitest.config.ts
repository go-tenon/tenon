import { fileURLToPath } from "node:url"
import { defineConfig } from "vitest/config"

// @/ 是 shadcn 组件的路径别名，只有 registry 中的组件使用。
export default defineConfig({
  resolve: { alias: { "@": fileURLToPath(new URL("./registry/src", import.meta.url)) } },
})
