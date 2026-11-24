/// <reference types="node" />
// 本次页面请求的上下文，只在服务端使用：SSR 调用 Go 接口时从这里取出要转交的请求头，
// 同时为 @go-tenon/client 的会话和国际化单例提供按请求隔离的状态存储。

import { AsyncLocalStorage } from "node:async_hooks"

interface RequestContext {
  headers: Headers
  state: Map<object, unknown>
}

const store = new AsyncLocalStorage<RequestContext>()

// 与 @go-tenon/client/src/request-store.ts 约定的全局 Symbol，避免两包之间引入运行时耦合。
const requestStateStoreSymbol = Symbol.for("tenon.client.requestStateStore")
;(globalThis as Record<symbol, unknown>)[requestStateStoreSymbol] = () => store.getStore()?.state

/** 在 fn 运行期间记住页面请求的请求头并开启独立的请求级客户端状态。 */
export function withIncoming<T>(headers: Headers, fn: () => T): T {
  return store.run({ headers, state: new Map<object, unknown>() }, fn)
}

/** 当前页面请求的请求头；不在页面请求中时为 undefined。 */
export function incoming(): Headers | undefined {
  return store.getStore()?.headers
}
