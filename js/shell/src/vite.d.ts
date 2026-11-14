import type { Plugin } from "vite"

/** tenonVersion 的选项。 */
export interface VersionPluginOptions {
  /** 版本号，例如 git commit；默认每次构建随机生成。 */
  version?: string
  /** 写入 dist 的文件名，默认 version.json。 */
  fileName?: string
}

/**
 * 构建时在 dist 中写入 {"version": "…"}，并把版本号和文件的地址（带上 base）注入为
 * import.meta.env.TENON_BUILD_VERSION、TENON_VERSION_URL。只在构建时生效。
 */
export function tenonVersion(options?: VersionPluginOptions): Plugin
