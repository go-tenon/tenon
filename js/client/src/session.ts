// 会话：按客户端协议保存和刷新登录凭据，并记录当前的身份。一个会话可以连接多个 API 的客户端。

import { isApiErrorLike } from "./errors"
import { type FetchLike, globalFetch, headerRecord } from "./fetch"

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
  /**
   * 交给生成客户端的 createClient({ auth })：受保护的接口返回 401 时用它刷新。
   * generation 与 Session.generation 相同：请求发出之后身份变了，客户端不再刷新重试，免得用别人的身份重发。
   */
  readonly auth: { refresh: () => Promise<string | null>; generation: () => number }
  /** 连接一个客户端：它立即得到当前的 access token，之后登录、刷新、退出时随之更新。返回断开的函数。 */
  connect(client: TokenHolder): () => void
  /** 打开页面或启动 App 时调用：刷新一次，返回是否已经登录。并发的调用共用同一次刷新。 */
  restore(): Promise<boolean>
  /**
   * 登录接口成功后调用：保存凭据，把 access token 交给所有连接的客户端，设置身份。
   * 这之前开始、还没有完成的刷新，结果被丢弃。
   */
  signIn(body: SessionBody, identity?: Identity): Promise<void>
  /** 退出：清除凭据和身份。登出接口由应用自己调用，cookie 由它清除。这之前开始、还没有完成的刷新，结果被丢弃。 */
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
  /**
   * 返回带认证的 fetch，用于不经过生成客户端的请求，例如 AI 对话的消息流、对象存储之外的上传接口：
   * 请求带上 Authorization: Bearer；收到 401 时刷新一次（与其他请求共用同一次刷新），再重试。默认包装全局的 fetch。
   * 请求发出之后身份变了（登录、退出、切换身份）时不重试，返回原来的 401。
   */
  fetch<F extends FetchLike = FetchLike>(base?: F): F
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
  // 凭据的代次：登录、退出、刷新被拒绝时加一。刷新完成时代次变了，说明凭据已被替换，结果作废。
  // 切换租户等只改身份、不换凭据，不影响进行中的刷新。
  let epoch = 0
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
  // replaced 让进行中的刷新作废：新的刷新不再共用它。
  const replaced = () => {
    epoch++
    refreshing = null
  }
  const clear = async () => {
    replaced()
    await options.storage?.remove()
    setToken("")
    if (current !== null) {
      current = null
      changed()
    }
  }

  const refresh = (): Promise<string | null> => {
    if (refreshing) return refreshing
    const started = epoch
    const stale = () => epoch !== started
    const p: Promise<string | null> = (async () => {
      let saved: string | undefined
      if (options.storage) {
        saved = (await options.storage.get()) ?? undefined
        if (!saved || stale()) return null
      }
      try {
        const body = await options.refresh(saved)
        // 等待期间退出或换了账号登录：这是旧凭据的结果，保存它会把旧账号的 token 写回去。
        if (stale()) return null
        await save(body)
        return body.access_token
      } catch (err) {
        // 刷新被拒绝（401）表示这次登录已经失效；网络错误等保留凭据，下次再试。凭据已被替换时与它无关。
        if (isApiErrorLike(err) && err.status === 401 && !stale()) {
          const had = token !== "" || current !== null
          await clear()
          if (had) options.onSignedOut?.()
        }
        return null
      }
    })().finally(() => {
      if (refreshing === p) refreshing = null
    })
    refreshing = p
    return p
  }

  return {
    auth: { refresh, generation: () => gen },
    connect(client) {
      clients.add(client)
      client.setAccessToken(token)
      return () => clients.delete(client)
    },
    restore: async () => (await refresh()) !== null,
    async signIn(body, identity) {
      replaced()
      await save(body)
      if (identity) this.setIdentity(identity)
      else changed()
    },
    async signOut() {
      replaced()
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
    fetch<F extends FetchLike = FetchLike>(base?: F): F {
      const send: FetchLike = base ?? globalFetch()
      const wrapped: FetchLike = async (url, init = {}) => {
        const attempt = (t: string) => {
          const headers = headerRecord(init.headers)
          if (t) headers.Authorization = `Bearer ${t}`
          return send(url, { ...init, headers })
        }
        const used = token
        const sent = gen
        const res = await attempt(used)
        // 身份变了：这是上一个身份的请求，不能用现在的凭据重发。
        if (res.status !== 401 || gen !== sent) return res
        // 等待期间其他请求可能已经换了 token：直接用新的重试。
        const next = token !== used ? token : await refresh()
        return next && gen === sent ? attempt(next) : res
      }
      return wrapped as F
    },
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
}
