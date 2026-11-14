// 会话：按客户端协议保存和刷新登录凭据，并记录当前的身份。一个会话可以连接多个 API 的客户端。

import { isApiErrorLike } from "./errors"

/** 登录、刷新和确认身份接口的响应体（apitoken.Session）。 */
export interface SessionBody {
  access_token: string
  /** 不为空时替换已保存的 refresh token；为空或不出现时继续使用已保存的。 */
  refresh_token?: string
  expires_at: string
}

/** 保存 refresh token 的存储，例如 App 的安全存储。浏览器使用 cookie，不传存储。 */
export interface TokenStorage {
  get(): string | null | undefined | Promise<string | null | undefined>
  set(token: string): void | Promise<void>
  remove(): void | Promise<void>
}

/** 当前的身份：账号，以及多租户应用中当前的租户。 */
export interface Identity {
  account: string
  tenant?: string
}

/** 生成的客户端中会话需要的部分。 */
export interface TokenHolder {
  setAccessToken(token: string): void
}

/** createSession 的选项。 */
export interface SessionOptions {
  /**
   * 调用应用的刷新接口，返回它的响应体。传入存储时 refreshToken 是保存的 refresh token，放在请求体中；
   * 浏览器使用 cookie，refreshToken 是 undefined，请求体为空，浏览器带上 cookie。
   */
  refresh: (refreshToken: string | undefined) => Promise<SessionBody>
  /** 保存 refresh token 的存储。不传时 refresh token 在 cookie 中，由浏览器保存。 */
  storage?: TokenStorage
  /** 登录失效、需要重新登录时调用，例如跳到登录页。主动 signOut 不调用它。 */
  onSignedOut?: () => void
}

/** 会话。 */
export interface Session {
  /** 交给生成客户端的 createClient({ auth })：受保护的接口返回 401 时用它刷新。 */
  readonly auth: { refresh: () => Promise<string | null> }
  /** 连接一个客户端：它立即得到当前的 access token，之后登录、刷新、退出时随之更新。返回断开的函数。 */
  connect(client: TokenHolder): () => void
  /** 打开页面或启动 App 时调用：刷新一次，返回是否已经登录。并发的调用共用同一次刷新。 */
  restore(): Promise<boolean>
  /** 登录接口成功后调用：保存凭据，把 access token 交给所有连接的客户端，设置身份。 */
  signIn(body: SessionBody, identity?: Identity): Promise<void>
  /** 退出：清除凭据和身份。登出接口由应用自己调用，cookie 由它清除。 */
  signOut(): Promise<void>
  /** 当前的身份，没有登录时为 null。 */
  identity(): Identity | null
  /** 设置身份，例如登录后读到当前用户、切换租户。身份变化时代次加一并通知订阅者。 */
  setIdentity(identity: Identity | null): void
  /** 身份的代次：每次登录、退出、切换身份都加一。代次变化前发出的请求，结果不应再使用。 */
  generation(): number
  /** query key 的作用域：[账号, 租户]，交给 createQueries 的 scope。 */
  scope(): readonly unknown[]
  /** 当前的 access token，例如交给实时频道的 SDK。 */
  accessToken(): string
  /** 订阅身份的变化（登录、退出、切换身份），返回取消订阅的函数。 */
  subscribe(listener: () => void): () => void
}

/** 创建会话。 */
export function createSession(options: SessionOptions): Session {
  const clients = new Set<TokenHolder>()
  const listeners = new Set<() => void>()
  let token = ""
  let current: Identity | null = null
  let gen = 0
  let refreshing: Promise<string | null> | null = null

  const setToken = (t: string) => {
    token = t
    for (const c of clients) c.setAccessToken(t)
  }
  const changed = () => {
    gen++
    for (const l of [...listeners]) l()
  }
  const save = async (body: SessionBody) => {
    if (options.storage && body.refresh_token) await options.storage.set(body.refresh_token)
    setToken(body.access_token)
  }
  const clear = async () => {
    await options.storage?.remove()
    setToken("")
    if (current !== null) {
      current = null
      changed()
    }
  }

  const refresh = (): Promise<string | null> => {
    refreshing ??= (async () => {
      let saved: string | undefined
      if (options.storage) {
        saved = (await options.storage.get()) ?? undefined
        if (!saved) return null
      }
      try {
        const body = await options.refresh(saved)
        await save(body)
        return body.access_token
      } catch (err) {
        // 刷新被拒绝（401）表示这次登录已经失效；网络错误等保留凭据，下次再试。
        if (isApiErrorLike(err) && err.status === 401) {
          const had = token !== "" || current !== null
          await clear()
          if (had) options.onSignedOut?.()
        }
        return null
      }
    })().finally(() => {
      refreshing = null
    })
    return refreshing
  }

  return {
    auth: { refresh },
    connect(client) {
      clients.add(client)
      client.setAccessToken(token)
      return () => clients.delete(client)
    },
    restore: async () => (await refresh()) !== null,
    async signIn(body, identity) {
      await save(body)
      if (identity) this.setIdentity(identity)
      else changed()
    },
    async signOut() {
      await options.storage?.remove()
      setToken("")
      current = null
      changed()
    },
    identity: () => current,
    setIdentity(identity) {
      const same =
        current?.account === identity?.account &&
        current?.tenant === identity?.tenant &&
        (current === null) === (identity === null)
      if (same) return
      current = identity ? { ...identity } : null
      changed()
    },
    generation: () => gen,
    scope: () => (current ? [current.account, current.tenant ?? ""] : []),
    accessToken: () => token,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
}
