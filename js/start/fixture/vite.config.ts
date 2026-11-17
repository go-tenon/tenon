import { defineConfig } from "vite"
import { tenon } from "../src/vite"

export default defineConfig({ plugins: [tenon({ prerender: ["/"] })] })
