// @go-tenon/shell/vite：构建时写入 version.json 并注入版本号，供 useNewVersion 检查新版本。
// 用 JavaScript 编写：Vite 加载 vite.config.ts 时，node_modules 中的包由 Node 直接导入，Node 不处理其中的 TypeScript。
// 类型见 vite.d.ts。

import { randomUUID } from "node:crypto"

/**
 * 构建时在 dist 中写入 {"version": "…"}，并把版本号和文件的地址（带上 base）注入为
 * import.meta.env.TENON_BUILD_VERSION、TENON_VERSION_URL。只在构建时生效，开发模式下不检查新版本。
 * Go 的 httpx.SPA 对没有哈希的文件返回 Cache-Control: no-cache，version.json 不会被缓存。
 *
 * @param {import("./vite").VersionPluginOptions} [options]
 * @returns {import("vite").Plugin}
 */
export function tenonVersion(options = {}) {
  const version = options.version ?? randomUUID()
  const fileName = (options.fileName ?? "version.json").replace(/^\/+/, "")
  return {
    name: "tenon:version",
    apply: "build",
    config(config) {
      const base = config.base && config.base !== "./" ? config.base : "/"
      const url = `${base.endsWith("/") ? base : `${base}/`}${fileName}`
      return {
        define: {
          "import.meta.env.TENON_BUILD_VERSION": JSON.stringify(version),
          "import.meta.env.TENON_VERSION_URL": JSON.stringify(url),
        },
      }
    },
    generateBundle() {
      this.emitFile({ type: "asset", fileName, source: JSON.stringify({ version }) })
    },
  }
}
