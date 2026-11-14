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
    expect(queryClient.clear).toHaveBeenCalledTimes(3)
    expect(session.scope()).toEqual([])
  })
})
