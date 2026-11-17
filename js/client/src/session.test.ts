import { describe, expect, it, vi } from "vitest"
import type { FetchLike, ResponseLike } from "./fetch"
import { clearOnIdentityChange } from "./queries"
import { createSession } from "./session"

const unauthorized = { status: 401, fields: [], code: "auth.unauthenticated" }

function response(status: number): ResponseLike {
  return { ok: status < 300, status, headers: { get: () => null }, json: async () => ({}), text: async () => "" }
}

describe("createSession", () => {
  it("打开页面时读取一次身份；并发的调用共用同一次", async () => {
    const load = vi.fn(async () => ({ account: "42" }))
    const session = createSession({ load })
    expect(await Promise.all([session.restore(), session.restore()])).toEqual([true, true])
    expect(await session.restore()).toBe(true)
    expect(load).toHaveBeenCalledTimes(1)
    expect(session.identity()).toEqual({ account: "42" })
    expect(session.scope()).toEqual(["42", ""])
  })

  it("未登录时 load 返回 null 或 401", async () => {
    expect(await createSession({ load: async () => null }).restore()).toBe(false)
    const session = createSession({ load: async () => Promise.reject(unauthorized) })
    expect(await session.restore()).toBe(false)
    expect(session.signedIn()).toBe(false)
  })

  it("网络错误时抛出，下次再读", async () => {
    const load = vi.fn().mockRejectedValueOnce(new TypeError("offline")).mockResolvedValueOnce({ account: "42" })
    const session = createSession({ load })
    await expect(session.restore()).rejects.toThrow("offline")
    expect(await session.restore()).toBe(true)
  })

  it("读取期间身份变了：以那次变化为准", async () => {
    let resolve!: (i: { account: string } | null) => void
    const session = createSession({ load: () => new Promise((r) => (resolve = r)) })
    const restoring = session.restore()
    await session.signIn({ account: "42", tenant: "acme" })
    resolve({ account: "42" })
    expect(await restoring).toBe(true)
    expect(session.identity()).toEqual({ account: "42", tenant: "acme" })
  })

  it("登录给出身份时不再读取；没有给出时读取", async () => {
    const load = vi.fn(async () => ({ account: "7" }))
    const session = createSession({ load })
    await session.signIn({ account: "42" })
    expect(load).not.toHaveBeenCalled()
    session.signOut()
    await session.signIn()
    expect(session.identity()).toEqual({ account: "7" })
  })

  it("401 时清除身份并通知一次；主动退出不通知", async () => {
    const onSignedOut = vi.fn()
    const session = createSession({ load: async () => null, onSignedOut })
    await session.signIn({ account: "42" })
    session.expired()
    session.expired()
    expect(session.signedIn()).toBe(false)
    expect(onSignedOut).toHaveBeenCalledTimes(1)
    await session.signIn({ account: "42" })
    session.signOut()
    expect(onSignedOut).toHaveBeenCalledTimes(1)
  })

  it("fetch 同源发送 cookie，不带 Authorization；401 时登录失效", async () => {
    const onSignedOut = vi.fn()
    const session = createSession({ load: async () => null, onSignedOut })
    await session.signIn({ account: "42" })
    const calls: unknown[] = []
    const base: FetchLike = async (_, init) => {
      calls.push(init)
      return response(401)
    }
    const res = await session.fetch(base)("/api/x", { method: "POST" })
    expect(res.status).toBe(401)
    expect(calls).toEqual([{ credentials: "same-origin", method: "POST" }])
    expect(onSignedOut).toHaveBeenCalledTimes(1)
  })

  it("fetch：请求发出之后身份变了，401 不让新的会话失效", async () => {
    const session = createSession({ load: async () => null })
    await session.signIn({ account: "alice" })
    let release!: () => void
    const base: FetchLike = () => new Promise((r) => (release = () => r(response(401))))
    const pending = session.fetch(base)("/api/x")
    session.signOut()
    await session.signIn({ account: "bob" })
    release()
    expect((await pending).status).toBe(401)
    expect(session.identity()).toEqual({ account: "bob" })
  })

  it("身份变化时代次加一，通知订阅者并清空查询缓存", async () => {
    const session = createSession({ load: async () => null })
    const queryClient = { clear: vi.fn() }
    clearOnIdentityChange(session, queryClient)
    await session.signIn({ account: "42", tenant: "acme" })
    expect(session.scope()).toEqual(["42", "acme"])
    session.setIdentity({ account: "42", tenant: "acme" }) // 没有变化
    session.setIdentity({ account: "42", tenant: "beta" })
    session.signOut()
    expect(session.generation()).toBe(3)
    expect(queryClient.clear).toHaveBeenCalledTimes(3)
    expect(session.scope()).toEqual([])
  })
})
