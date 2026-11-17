/// <reference types="node" />
// 本次页面请求的请求头，只在服务端使用：SSR 调用 Go 接口时从这里取出要转交的请求头。

import { AsyncLocalStorage } from "node:async_hooks"

const store = new AsyncLocalStorage<Headers>()

/** 在 fn 运行期间记住页面请求的请求头。 */
export function withIncoming<T>(headers: Headers, fn: () => T): T {
  return store.run(headers, fn)
}

/** 当前页面请求的请求头；不在页面请求中时为 undefined。 */
export function incoming(): Headers | undefined {
  return store.getStore()
}
