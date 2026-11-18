// 构建版本：tenon 插件（vite.js）的一部分，构建时写入 version.json 并注入版本号，供 @go-tenon/ui 的 useNewVersion
// 检查新版本。用 JavaScript 编写，原因同 vite.js。类型见 version.d.ts。

import { randomUUID } from "node:crypto"
import { copyFile, mkdir, readdir, rm, unlink } from "node:fs/promises"
import { dirname, join, relative, resolve } from "node:path"

/**
 * 默认的版本号：环境变量 TENON_BUILD_VERSION，没有时随机生成一个并写入它。
 * 一次构建中 vite.config 可能被加载好几次（TanStack Start 为不同的环境各加载一次），每次都创建这个插件；
 * 版本号在进程中只生成一次，注入页面的和写进 version.json 的才是同一个。
 *
 * @returns {string}
 */
function buildVersion() {
  process.env.TENON_BUILD_VERSION ||= randomUUID()
  return process.env.TENON_BUILD_VERSION
}

/**
 * 构建时在 dist 中写入 {"version": "…"}，并把版本号和文件的地址（带上 base）注入为
 * import.meta.env.TENON_BUILD_VERSION、TENON_VERSION_URL。只在构建时生效，开发模式下不检查新版本。
 * 用 TanStack Start 时只作用于浏览器的构建（client 环境），version.json 写在 dist/client 中；
 * 由 Go 返回时，没有哈希的文件带 Cache-Control: no-cache，version.json 不会被缓存。
 *
 * @param {import("./version").VersionPluginOptions} [options]
 * @returns {import("vite").Plugin}
 */
export function versionPlugin(options = {}) {
  const version = options.version ?? buildVersion()
  const fileName = "version.json"
  /** @type {string | undefined} */
  let mapsDir
  return {
    name: "tenon:version",
    apply: "build",
    // 服务端的构建（ssr 环境）不需要 version.json，它的 source map 也不公开。
    applyToEnvironment: (environment) => environment.name === "client",
    config(config) {
      const base = config.base && config.base !== "./" ? config.base : "/"
      const url = `${base.endsWith("/") ? base : `${base}/`}${fileName}`
      return {
        define: {
          "import.meta.env.TENON_BUILD_VERSION": JSON.stringify(version),
          "import.meta.env.TENON_VERSION_URL": JSON.stringify(url),
        },
        // 生成 source map，但脚本中不写 sourceMappingURL：浏览器不会去请求它们。
        ...(options.sourcemaps ? { build: { sourcemap: "hidden" } } : {}),
      }
    },
    configResolved(config) {
      if (options.sourcemaps) mapsDir = resolve(config.root, options.sourcemaps)
    },
    generateBundle() {
      this.emitFile({ type: "asset", fileName, source: JSON.stringify({ version }) })
    },
    // 构建结束后把 .map 移出 dist：它们不嵌入二进制、不公开，由 CI 上传给错误追踪服务。
    async closeBundle() {
      if (!mapsDir) return
      const { root, build } = this.environment.config
      const outDir = resolve(root, build.outDir)
      await rm(mapsDir, { recursive: true, force: true })
      for (const entry of await readdir(outDir, { recursive: true, withFileTypes: true })) {
        if (!entry.isFile() || !entry.name.endsWith(".map")) continue
        const from = join(entry.parentPath, entry.name)
        const to = join(mapsDir, relative(outDir, from))
        await mkdir(dirname(to), { recursive: true })
        await copyFile(from, to)
        await unlink(from)
      }
    },
  }
}
