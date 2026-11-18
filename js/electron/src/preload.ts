// @go-tenon/electron/preload 在页面中放好 tenonHost：从启动参数读出主进程注册的方法，每个方法调用主进程。
// 应用的 preload 先导入它，需要更多原生方法时再用 contextBridge 加自己的对象。

import type { HostEvents } from "@go-tenon/host"
import { contextBridge, type IpcRendererEvent, ipcRenderer } from "electron"
import { EVENT_PREFIX, type HostInit, INIT_ARG, METHOD_PREFIX } from "./ipc"

const arg = process.argv.find((a) => a.startsWith(INIT_ARG))
if (!arg) throw new Error("@go-tenon/electron: this window was not opened by createHost")
const init = JSON.parse(arg.slice(INIT_ARG.length)) as HostInit

const api: Record<string, unknown> = {
  contract: init.contract,
  kind: init.kind,
  platform: init.platform,
  appVersion: init.appVersion,
  on<E extends keyof HostEvents>(event: E, listener: (data: HostEvents[E]) => void): () => void {
    const channel = EVENT_PREFIX + event
    const handler = (_e: IpcRendererEvent, data: HostEvents[E]) => listener(data)
    ipcRenderer.on(channel, handler)
    return () => {
      ipcRenderer.removeListener(channel, handler)
    }
  },
}

// 方法名按点分隔放进嵌套的对象，例如 auth.signIn 成为 api.auth.signIn。
for (const name of init.methods) {
  const parts = name.split(".")
  let node = api
  for (const part of parts.slice(0, -1)) {
    node[part] ??= {}
    node = node[part] as Record<string, unknown>
  }
  node[parts[parts.length - 1]] = (...args: unknown[]) => ipcRenderer.invoke(METHOD_PREFIX + name, ...args)
}

contextBridge.exposeInMainWorld("tenonHost", api)
