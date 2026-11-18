// 主进程与 preload 之间的约定：preload 从启动参数读出 HostInit，为其中的每个方法生成一个调用主进程的函数。
// 页面只看到 tenonHost，看不到这里的通道名。

import type { HostPlatform } from "@go-tenon/host"

/** 主进程通过 webPreferences.additionalArguments 交给 preload 的参数的前缀，后面是 HostInit 的 JSON。 */
export const INIT_ARG = "--tenon-host="

/** 方法的 IPC 通道前缀，后面是方法名，例如 tenon:auth.signIn。 */
export const METHOD_PREFIX = "tenon:"

/** 事件的 IPC 通道前缀，后面是事件名，例如 tenon-event:navigate。 */
export const EVENT_PREFIX = "tenon-event:"

/** preload 生成 tenonHost 所需的信息。 */
export interface HostInit {
  contract: number
  kind: string
  platform: HostPlatform
  appVersion: string
  methods: string[] // 主进程注册了的方法，按点分隔的路径放进 tenonHost，例如 auth.signIn、update.install
}
