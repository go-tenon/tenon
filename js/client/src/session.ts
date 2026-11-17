// 会话：记录当前的身份。浏览器的登录凭据是服务端写入的 HttpOnly 会话 cookie，随同源请求自动发送，
// 页面上的脚本既读不到也不需要管理它：这里没有 token，只有“当前是谁”。

import { isApiErrorLike } from "./errors"
import { type FetchLike, globalFetch } from "./fetch"

/** 当前的身份：账号，以及多租户应用中当前的租户。 */
export interface Identity {
  account: string
  tenant?: string
}

/** createSession 的选项。 */
export interface SessionOptions {
  /**
   * 读取当前的身份，通常调用应用的“当前用户”接口：已登录时返回身份，未登录时返回 null。
   * 打开页面时调用一次（restore），登录成功后没有给出身份时也调用。
   */
  load: () => Promise<Identity | null>
  /** 登录失效、需要重新登录时调用，例如跳到登录页。主动 signOut 不调用它。 */
  onSignedOut?: () => void
}

/** 会话。 */
export interface Session {
  /** 打开页面时调用：读取一次身份，返回是否已经登录。并发的调用共用同一次读取；已经有身份时不再读取。 */
  restore(): Promise<boolean>
  /** 登录接口成功后调用（cookie 已由服务端写入）：设置身份；不传时用 load 读取。 */
  signIn(identity?: Identity): Promise<void>
  /** 退出：清除身份。退出接口由应用自己调用，cookie 由它清除。 */
  signOut(): void
  /**
   * 受保护的请求返回 401 时调用：登录已经失效，清除身份并调用 onSignedOut。
   * 把会话交给生成的客户端（createClient({ session })），它按请求发出时的代次调用这里。
   */
  expired(): void
  /** 是否已经登录。 */
  signedIn(): boolean
  /** 当前的身份，没有登录时为 null。 */
  identity(): Identity | null
  /** 设置身份，例如切换租户。身份变化时代次加一并通知订阅者。 */
  setIdentity(identity: Identity | null): void
  /** 身份的代次：每次登录、退出、切换身份都加一。代次变化前发出的请求，结果不应再使用。 */
  generation(): number
  /** query key 的作用域：[账号, 租户]，交给 createQueries 的 scope。 */
  scope(): readonly unknown[]
  /**
   * 返回用于不经过生成客户端的请求的 fetch，例如 AI 对话的消息流：同源发送 cookie；收到 401 时，
   * 请求发出之后身份没有变过才调用 expired。默认包装全局的 fetch。
   */
  fetch<F extends FetchLike = FetchLike>(base?: F): F
  /** 订阅身份的变化（登录、退出、切换身份），返回取消订阅的函数。 */
  subscribe(listener: () => void): () => void
}

/** 创建会话。 */
export function createSession(options: SessionOptions): Session {
  const listeners = new Set<() => void>()
  let current: Identity | null = null
  let gen = 0
  let restoring: Promise<boolean> | null = null

  const set = (identity: Identity | null) => {
    const same =
      (current === null) === (identity === null) &&
      current?.account === identity?.account &&
      current?.tenant === identity?.tenant
    if (same) return
    current = identity ? { ...identity } : null
    gen++
    for (const l of [...listeners]) l()
  }

  const session: Session = {
    restore() {
      if (current !== null) return Promise.resolve(true) // 已经知道身份，例如刚登录
      const started = gen
      restoring ??= options.load().then(
        (identity) => {
          // 读取期间登录、退出或切换了身份：以那次变化为准，丢弃读到的结果。
          if (gen === started) set(identity)
          return current !== null
        },
        (err) => {
          restoring = null // 网络错误等：下次再试
          if (isApiErrorLike(err) && err.status === 401) {
            set(null)
            return false
          }
          throw err
        },
      )
      return restoring
    },
    async signIn(identity) {
      set(identity ?? (await options.load()))
    },
    signOut() {
      set(null)
    },
    expired() {
      if (current === null) return
      set(null)
      options.onSignedOut?.()
    },
    signedIn: () => current !== null,
    identity: () => current,
    setIdentity: set,
    generation: () => gen,
    scope: () => (current ? [current.account, current.tenant ?? ""] : []),
    fetch<F extends FetchLike = FetchLike>(base?: F): F {
      const send: FetchLike = base ?? globalFetch()
      const wrapped: FetchLike = async (url, init = {}) => {
        const issued = gen
        const res = await send(url, { ...init, credentials: init.credentials ?? "same-origin" })
        if (res.status === 401 && gen === issued) session.expired()
        return res
      }
      return wrapped as F
    },
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
  return session
}
