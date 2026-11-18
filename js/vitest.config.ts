import { defineConfig } from "vitest/config"

// 前端包中 client、start、electron 有测试（electron 只测不依赖 Electron 的部分）；ui 不写测试，改动在 playground 中看效果。
export default defineConfig({})
