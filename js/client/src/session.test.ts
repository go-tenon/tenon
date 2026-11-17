import { describe, expect, it, vi } from "vitest"
import { clearOnIdentityChange } from "./queries"
import { createSession, type SessionBody, type TokenStorage } from "./session"

const body = (access: string, refresh?: string): SessionBody => ({
  access_token: access,
  refresh_token: refresh,
  expires_at: "2026-11-11T08:10:00Z",
})
const unauthorized = { status: 401, fields: [], code: "auth.unauthenticated" }

function memoryStorage(initial?: string): TokenStorage & { value?: string } {
  const s: TokenStorage & { value?: string } = {
    value: initial,
    get: () => s.value,
    set: (t) => {
      s.value = t
    },
    remove: () => {
      s.value = undefined
    },
  }
  return s
}

describe("createSession", () => {
  it("登录后所有连接的客户端得到 access token，存储中保存 refresh token", async () => {
    const storage = memoryStorage()
    const session = createSession({ refresh: vi.fn(), storage })
    const a = { setAccessToken: vi.fn() }
    const b = { setAccessToken: vi.fn() }
    session.connect(a)
    await session.signIn(body("a1", "r1"), { account: "42" })
    session.connect(b)
    expect(a.setAccessToken).toHaveBeenLastCalledWith("a1")
    expect(b.setAccessToken).toHaveBeenLastCalledWith("a1")
    expect(storage.value).toBe("r1")
    expect(session.identity()).toEqual({ account: "42" })
    expect(session.scope()).toEqual(["42", ""])
  })

  it("刷新时传入保存的 refresh token；返回空的 refresh_token 时保留已保存的", async () => {
    const storage = memoryStorage("r1")
    const refresh = vi.fn().mockResolvedValueOnce(body("a2", "r2")).mockResolvedValueOnce(body("a3"))
    const session = createSession({ refresh, storage })
    expect(await session.restore()).toBe(true)
    expect(refresh).toHaveBeenLastCalledWith("r1")
    expect(storage.value).toBe("r2")
    expect(await session.auth.refresh()).toBe("a3")
    expect(refresh).toHaveBeenLastCalledWith("r2")
    expect(storage.value).toBe("r2")
    expect(session.accessToken()).toBe("a3")
  })

  it("并发的刷新只调用一次", async () => {
    let resolve!: (b: SessionBody) => void
    const refresh = vi.fn(() => new Promise<SessionBody>((r) => (resolve = r)))
    const session = createSession({ refresh })
    const all = Promise.all([session.auth.refresh(), session.auth.refresh(), session.restore()])
    resolve(body("a1"))
    expect(await all).toEqual(["a1", "a1", true])
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it("cookie 方式：refresh 收到 undefined", async () => {
    const refresh = vi.fn().mockResolvedValue(body("a1"))
    await createSession({ refresh }).restore()
    expect(refresh).toHaveBeenCalledWith(undefined)
  })

  it("刷新返回 401 时清除凭据和身份并通知；网络错误时保留", async () => {
    const storage = memoryStorage("r1")
    const onSignedOut = vi.fn()
    const refresh = vi.fn().mockRejectedValueOnce(new TypeError("offline")).mockRejectedValueOnce(unauthorized)
    const session = createSession({ refresh, storage, onSignedOut })
    await session.signIn(body("a1"), { account: "42" })
    expect(await session.auth.refresh()).toBeNull()
    expect(storage.value).toBe("r1")
    expect(onSignedOut).not.toHaveBeenCalled()
    expect(await session.auth.refresh()).toBeNull()
    expect(storage.value).toBeUndefined()
    expect(session.identity()).toBeNull()
    expect(session.accessToken()).toBe("")
    expect(onSignedOut).toHaveBeenCalledTimes(1)
  })

  // 刷新等待期间退出或换了账号：旧凭据的刷新结果不能写回。
  function pendingRefresh() {
    const calls: { resolve: (b: SessionBody) => void; reject: (e: unknown) => void }[] = []
    const refresh = vi.fn(() => new Promise<SessionBody>((resolve, reject) => calls.push({ resolve, reject })))
    return { refresh, calls }
  }

  it("刷新期间退出：不写回旧的 token", async () => {
    const storage = memoryStorage("r1")
    const { refresh, calls } = pendingRefresh()
    const session = createSession({ refresh, storage })
    await session.signIn(body("a1"), { account: "alice" })
    const pending = session.auth.refresh()
    await vi.waitFor(() => expect(calls).toHaveLength(1))
    await session.signOut()
    calls[0].resolve(body("a2", "r2"))
    expect(await pending).toBeNull()
    expect(session.accessToken()).toBe("")
    expect(storage.value).toBeUndefined()
  })

  it("刷新期间换了账号登录：结果丢弃；之后的刷新重新调用，旧刷新被拒绝也不退出新账号", async () => {
    const storage = memoryStorage("r1")
    const onSignedOut = vi.fn()
    const { refresh, calls } = pendingRefresh()
    const session = createSession({ refresh, storage, onSignedOut })
    await session.signIn(body("alice", "r-alice"), { account: "alice" })
    const first = session.auth.refresh()
    const second = session.auth.refresh()
    await vi.waitFor(() => expect(calls).toHaveLength(1))
    await session.signIn(body("bob", "r-bob"), { account: "bob" })
    const third = session.auth.refresh()
    await vi.waitFor(() => expect(calls).toHaveLength(2))
    expect(refresh).toHaveBeenLastCalledWith("r-bob")
    calls[0].resolve(body("alice2", "r-alice2"))
    expect(await first).toBeNull()
    expect(await second).toBeNull()
    expect(session.accessToken()).toBe("bob")
    expect(storage.value).toBe("r-bob")
    calls[1].resolve(body("bob2"))
    expect(await third).toBe("bob2")

    const late = session.auth.refresh()
    await vi.waitFor(() => expect(calls).toHaveLength(3))
    await session.signIn(body("carol", "r-carol"), { account: "carol" })
    calls[2].reject(unauthorized)
    expect(await late).toBeNull()
    expect(session.accessToken()).toBe("carol")
    expect(session.identity()).toEqual({ account: "carol" })
    expect(onSignedOut).not.toHaveBeenCalled()
  })

  // 异步的存储（例如 App 的安全存储）：set 要等 release 才完成。
  function slowStorage(initial?: string) {
    const s = memoryStorage(initial)
    const pending: (() => void)[] = []
    const slow: TokenStorage & { value?: string } = {
      get: () => s.value,
      set: (t) =>
        new Promise<void>((resolve) =>
          pending.push(() => {
            s.value = t
            resolve()
          }),
        ),
      remove: () => {
        s.value = undefined
      },
    }
    Object.defineProperty(slow, "value", { get: () => s.value })
    // release 完成最早的一次写入。
    const release = async () => {
      await vi.waitFor(() => expect(pending.length).toBeGreaterThan(0))
      pending.shift()?.()
    }
    return { storage: slow, release }
  }

  it("刷新的结果正在写入存储时换了账号登录：旧结果不覆盖新账号的凭据", async () => {
    const { storage, release } = slowStorage("r-alice")
    const refresh = vi.fn().mockResolvedValue(body("alice2", "r-alice2"))
    const session = createSession({ refresh, storage })
    const pending = session.auth.refresh()
    await vi.waitFor(() => expect(refresh).toHaveBeenCalled())
    const bob = session.signIn(body("bob", "r-bob"), { account: "bob" })
    await release() // Alice 的刷新写入存储
    await release() // Bob 的登录写入存储
    await bob
    expect(await pending).toBeNull()
    expect(storage.value).toBe("r-bob")
    expect(session.accessToken()).toBe("bob")
    expect(session.identity()).toEqual({ account: "bob" })
  })

  it("登录正在写入存储时退出：以退出为准；这时开始的刷新等写入完成再读取", async () => {
    const { storage, release } = slowStorage()
    const refresh = vi.fn().mockResolvedValue(body("bob2"))
    const session = createSession({ refresh, storage })
    const alice = session.signIn(body("alice", "r-alice"), { account: "alice" })
    await Promise.all([alice, session.signOut()]) // 登录的写入还没轮到就被退出取代，不再执行
    expect(session.accessToken()).toBe("")
    expect(session.identity()).toBeNull()
    expect(storage.value).toBeUndefined()

    const bob = session.signIn(body("bob", "r-bob"), { account: "bob" })
    const restored = session.restore()
    await release()
    await bob
    expect(await restored).toBe(true)
    expect(refresh).toHaveBeenCalledWith("r-bob")
  })

  it("刷新期间切换租户：凭据没有换，结果照常保存", async () => {
    const { refresh, calls } = pendingRefresh()
    const session = createSession({ refresh })
    await session.signIn(body("a1"), { account: "42", tenant: "acme" })
    const pending = session.auth.refresh()
    await vi.waitFor(() => expect(calls).toHaveLength(1))
    session.setIdentity({ account: "42", tenant: "beta" })
    calls[0].resolve(body("a2"))
    expect(await pending).toBe("a2")
    expect(session.accessToken()).toBe("a2")
  })

  it("没有保存的 refresh token 时不调用刷新接口", async () => {
    const refresh = vi.fn()
    expect(await createSession({ refresh, storage: memoryStorage() }).restore()).toBe(false)
    expect(refresh).not.toHaveBeenCalled()
  })

  it("身份变化时代次加一，通知订阅者并清空查询缓存", async () => {
    const session = createSession({ refresh: vi.fn() })
    const queryClient = { clear: vi.fn() }
    clearOnIdentityChange(session, queryClient)
    await session.signIn(body("a1"), { account: "42", tenant: "acme" })
    expect(session.scope()).toEqual(["42", "acme"])
    session.setIdentity({ account: "42", tenant: "acme" }) // 没有变化
    session.setIdentity({ account: "42", tenant: "beta" })
    await session.signOut()
    expect(session.generation()).toBe(3)
    expect(session.auth.generation()).toBe(3)
    expect(queryClient.clear).toHaveBeenCalledTimes(3)
    expect(session.scope()).toEqual([])
  })
})
