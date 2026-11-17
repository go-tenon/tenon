// 新版本提示：构建插件（@go-tenon/start/vite 的 tenon 插件）在 dist 中写入 version.json 并注入版本号，
// 页面定期读取 version.json，版本号不同时提示用户刷新。提示的界面是 NewVersionNotice。

import { useCallback, useEffect, useState } from "react"

declare global {
  interface ImportMetaEnv {
    /**
     * Vite 的 base，例如 /admin/：前端挂载的路径。修饰符必须与 vite/client 中的声明相同（没有 readonly），
     * 否则引入 vite/client 类型的应用类型检查失败；version.test.ts 引入了它，检查两者兼容。
     */
    BASE_URL: string
    /** tenon() 插件注入的版本号；开发模式和没有使用插件时没有。 */
    readonly TENON_BUILD_VERSION?: string
    /** version.json 的地址，已经带上 Vite 的 base。 */
    readonly TENON_VERSION_URL?: string
  }
  interface ImportMeta {
    readonly env: ImportMetaEnv
  }
}

/** watchVersion 的选项。 */
export interface VersionOptions {
  /** 当前页面的版本号。 */
  current: string
  /** version.json 的地址。 */
  url: string
  /** 检查的间隔，默认 60 秒。页面不可见时不检查，重新可见时立即检查。 */
  interval?: number
  /** 发现新版本时调用一次，之后停止检查。 */
  onNewVersion: (version: string) => void
  fetch?: typeof fetch
}

/** 定期检查 version.json，返回停止的函数。读取失败（部署中、断网）时等下一次。 */
export function watchVersion(options: VersionOptions): () => void {
  const fetchFn = options.fetch ?? globalThis.fetch.bind(globalThis)
  let stopped = false
  let checking = false
  const check = async () => {
    if (stopped || checking || document.visibilityState === "hidden") return
    checking = true
    try {
      const res = await fetchFn(options.url, { cache: "no-store" })
      if (!res.ok) return
      const data = (await res.json()) as { version?: unknown }
      if (!stopped && typeof data.version === "string" && data.version !== "" && data.version !== options.current) {
        stop()
        options.onNewVersion(data.version)
      }
    } catch {
      // 等下一次。
    } finally {
      checking = false
    }
  }
  const timer = setInterval(check, options.interval ?? 60_000)
  const onVisible = () => void check()
  document.addEventListener("visibilitychange", onVisible)
  const stop = () => {
    stopped = true
    clearInterval(timer)
    document.removeEventListener("visibilitychange", onVisible)
  }
  void check()
  return stop
}

/** useNewVersion 的结果。 */
export interface NewVersion {
  /** 有新版本可用。 */
  available: boolean
  /** 刷新页面，载入新版本。 */
  reload: () => void
  /** 暂不刷新：关闭提示，本次打开页面期间不再提示。 */
  dismiss: () => void
}

/**
 * 检查新版本。版本号和地址默认取自 tenon() 插件注入的值；开发模式下没有这些值，不检查。
 */
export function useNewVersion(
  options: Partial<Pick<VersionOptions, "current" | "url" | "interval" | "fetch">> = {},
): NewVersion {
  const current = options.current ?? import.meta.env.TENON_BUILD_VERSION
  const url = options.url ?? import.meta.env.TENON_VERSION_URL
  const [available, setAvailable] = useState(false)
  useEffect(() => {
    if (!current || !url) return
    return watchVersion({
      current,
      url,
      interval: options.interval,
      fetch: options.fetch,
      onNewVersion: () => setAvailable(true),
    })
  }, [current, url, options.interval, options.fetch])
  const reload = useCallback(() => window.location.reload(), [])
  const dismiss = useCallback(() => setAvailable(false), [])
  return { available, reload, dismiss }
}
