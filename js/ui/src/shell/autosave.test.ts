import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { createAutoSaver } from "./autosave"

type V = { title: string }

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

/** 一个可以控制何时完成的保存。 */
function deferredSave() {
  const calls: { values: V; resolve: (ok: boolean) => void }[] = []
  const save = vi.fn((values: V) => new Promise<boolean>((resolve) => calls.push({ values, resolve })))
  return { save, calls }
}

describe("createAutoSaver", () => {
  it("停下 delay 之后保存最新的值；与已经保存的值相同时不保存", async () => {
    const save = vi.fn(async () => true)
    const s = createAutoSaver<V>({ title: "a" }, { save, delay: 500 })
    s.change({ title: "ab" })
    await vi.advanceTimersByTimeAsync(300)
    s.change({ title: "abc" })
    await vi.advanceTimersByTimeAsync(300)
    expect(save).not.toHaveBeenCalled()
    expect(s.status()).toBe("pending")
    await vi.advanceTimersByTimeAsync(200)
    expect(save).toHaveBeenCalledTimes(1)
    expect(save).toHaveBeenLastCalledWith({ title: "abc" })
    expect(s.status()).toBe("saved")

    s.change({ title: "abc" }) // 与已保存的相同
    await vi.advanceTimersByTimeAsync(500)
    expect(save).toHaveBeenCalledTimes(1)
  })

  it("校验不通过时不保存", async () => {
    const save = vi.fn(async () => true)
    const s = createAutoSaver<V>({ title: "a" }, { save, validate: (v) => v.title !== "", delay: 10 })
    s.change({ title: "" })
    await vi.advanceTimersByTimeAsync(10)
    expect(save).not.toHaveBeenCalled()
    expect(s.status()).toBe("idle")
  })

  it("保存一次只进行一个，期间的变化在完成后再保存一次", async () => {
    const { save, calls } = deferredSave()
    const s = createAutoSaver<V>({ title: "" }, { save, delay: 10 })
    s.change({ title: "1" })
    await vi.advanceTimersByTimeAsync(10)
    expect(calls).toHaveLength(1)
    s.change({ title: "2" })
    s.change({ title: "3" })
    await vi.advanceTimersByTimeAsync(10)
    expect(calls).toHaveLength(1) // 还在保存第一次
    calls[0].resolve(true)
    await vi.advanceTimersByTimeAsync(0)
    expect(calls).toHaveLength(2)
    expect(calls[1].values).toEqual({ title: "3" })
    calls[1].resolve(true)
    await vi.advanceTimersByTimeAsync(0)
    expect(s.status()).toBe("saved")
  })

  it("保存失败时下次变化重新提交", async () => {
    const onError = vi.fn()
    let fail = true
    const save = vi.fn(async () => {
      if (fail) throw new Error("网络错误")
      return true
    })
    const s = createAutoSaver<V>({ title: "" }, { save, onError, delay: 10 })
    s.change({ title: "x" })
    await vi.advanceTimersByTimeAsync(10)
    expect(s.status()).toBe("error")
    expect(onError).toHaveBeenCalledOnce()
    fail = false
    s.change({ title: "x" }) // 值没变，但上次没有保存成功
    await vi.advanceTimersByTimeAsync(10)
    expect(save).toHaveBeenCalledTimes(2)
    expect(s.status()).toBe("saved")
  })

  it("停止时立即保存还在等待的变化；discard 之后不保存", async () => {
    const save = vi.fn(async () => true)
    const a = createAutoSaver<V>({ title: "" }, { save, delay: 1000 })
    a.change({ title: "留下" })
    await a.stop()
    expect(save).toHaveBeenCalledWith({ title: "留下" })
    a.change({ title: "停止之后" })
    await vi.advanceTimersByTimeAsync(1000)
    expect(save).toHaveBeenCalledTimes(1)

    const b = createAutoSaver<V>({ title: "" }, { save, delay: 1000 })
    b.change({ title: "放弃" })
    b.discard()
    await b.stop()
    expect(save).toHaveBeenCalledTimes(1)
  })

  it("停止之后 resume 恢复接收变化", async () => {
    const save = vi.fn(async () => true)
    const s = createAutoSaver<V>({ title: "" }, { save, delay: 10 })
    s.change({ title: "隐藏前" })
    await s.stop()
    s.resume()
    s.change({ title: "重新显示" })
    await vi.advanceTimersByTimeAsync(10)
    expect(save).toHaveBeenLastCalledWith({ title: "重新显示" })
    expect(save).toHaveBeenCalledTimes(2)
  })

  it("markSaved 之后与它相同的值不保存；flush(true) 即使相同也保存", async () => {
    const save = vi.fn(async () => true)
    const s = createAutoSaver<V>({ title: "" }, { save, delay: 10 })
    s.markSaved({ title: "服务端" })
    s.change({ title: "服务端" })
    await vi.advanceTimersByTimeAsync(10)
    expect(save).not.toHaveBeenCalled()
    await s.flush(true)
    expect(save).toHaveBeenCalledWith({ title: "服务端" })
  })

  it("状态变化时通知订阅者", async () => {
    const s = createAutoSaver<V>({ title: "" }, { save: async () => true, delay: 10 })
    const seen: string[] = []
    s.subscribe(() => seen.push(s.status()))
    s.change({ title: "x" })
    await vi.advanceTimersByTimeAsync(10)
    expect(seen).toEqual(["pending", "saving", "saved"])
  })
})
