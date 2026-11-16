import type { Plugin } from "vite"

/** tenonVersion 的选项。 */
export interface VersionPluginOptions {
  /** 版本号，例如 git commit；默认每次构建随机生成。 */
  version?: string
  /** 写入 dist 的文件名，默认 version.json。 */
  fileName?: string
  /**
   * 生成 source map 并放到这个目录（相对于前端目录，例如 "sourcemaps"），不留在 dist 中：
   * 它们不嵌入二进制、不公开，由 CI 连同 version.json 中的版本号上传给错误追踪服务。不设置时不生成。
   * 构建前清空这个目录。
   */
  sourcemaps?: string
}

/**
 * 构建时在 dist 中写入 {"version": "…"}，并把版本号和文件的地址（带上 base）注入为
 * import.meta.env.TENON_BUILD_VERSION、TENON_VERSION_URL。只在构建时生效。
 * 设置 sourcemaps 时生成 hidden source map 并移出 dist。
 */
export function tenonVersion(options?: VersionPluginOptions): Plugin
