import { describe, expect, it } from "vitest"
import { canonicalizeViewConfig, sameViewConfig } from "./saved-view"

describe("saved-view", () => {
  it("忽略对象键顺序与 undefined 字段", () => {
    const a = { sort: "waiting", priority: "high", tag: undefined, nested: { b: 2, a: 1, c: undefined } }
    const b = { nested: { a: 1, b: 2 }, priority: "high", sort: "waiting" }
    expect(sameViewConfig(a, b)).toBe(true)
    expect(canonicalizeViewConfig(a)).toEqual({ nested: { a: 1, b: 2 }, priority: "high", sort: "waiting" })
  })

  it("保留 null、false、0、空字符串和数组顺序语义", () => {
    expect(sameViewConfig({ waiting: false }, { waiting: undefined })).toBe(false)
    expect(sameViewConfig({ waiting: null }, { waiting: false })).toBe(false)
    expect(sameViewConfig({ count: 0 }, {})).toBe(false)
    expect(sameViewConfig({ q: "" }, {})).toBe(false)
    expect(sameViewConfig({ tags: ["a", "b"] }, { tags: ["b", "a"] })).toBe(false)
    expect(sameViewConfig({ tags: ["a", "b"] }, { tags: ["a", "b"] })).toBe(true)
  })
})
