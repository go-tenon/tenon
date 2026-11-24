// SSR 请求级状态存储：让模块级单例（createSession、createI18n）在服务端渲染的并发请求之间互不共享可变状态。
// 本模块不依赖 Node 或 DOM：服务端框架（例如 @go-tenon/start）通过全局 Symbol 注册基于 AsyncLocalStorage 的存储获取函数；
// 浏览器或普通脚本中未注册存储，自动退回普通闭包状态。

/** 全局注册表键，允许不同入口或副本共享同一请求级状态存储。 */
export const RequestStateStoreSymbol = Symbol.for("tenon.client.requestStateStore")

/** 获取当前请求状态 Map 的函数类型；不在 SSR 请求上下文中时返回 undefined。 */
export type RequestStateGetter = () => Map<object, unknown> | undefined

type GlobalWithRequestStore = typeof globalThis & {
  [RequestStateStoreSymbol]?: RequestStateGetter
}

/** 注册当前运行时的请求级状态存储获取函数，由 SSR 服务端中间件调用。 */
export function registerRequestStateStore(getter: RequestStateGetter | undefined): void {
  const g = globalThis as GlobalWithRequestStore
  if (getter) {
    g[RequestStateStoreSymbol] = getter
  } else {
    delete g[RequestStateStoreSymbol]
  }
}

/**
 * 读取当前 SSR 请求下与 key 关联的状态对象；首次访问时用 init 创建并写入本次请求的 Map。
 * 不在 SSR 请求中（例如浏览器环境）时返回 undefined。
 */
export function getRequestState<T>(key: object, init: () => T): T | undefined {
  const getter = (globalThis as GlobalWithRequestStore)[RequestStateStoreSymbol]
  const map = getter?.()
  if (!map) return undefined
  if (map.has(key)) return map.get(key) as T
  const value = init()
  map.set(key, value)
  return value
}
