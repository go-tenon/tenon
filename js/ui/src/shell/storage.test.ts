import { describe, expect, it } from "vitest"
import { appStorage, type StorageLike } from "./storage"

function memory(): StorageLike & { data: Map<string, string> } {
  const data = new Map<string, string>()
  return {
    data,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => void data.set(k, v),
    removeItem: (k) => void data.delete(k),
  }
}

describe("appStorage", () => {
  it("按应用加前缀，值以 JSON 保存", () => {
    const backend = memory()
    const admin = appStorage("admin", backend)
    const web = appStorage("web", backend)
    admin.set("sider", { collapsed: true })
    expect(backend.data.get("tenon:admin:sider")).toBe('{"collapsed":true}')
    expect(admin.get("sider")).toEqual({ collapsed: true })
    expect(web.get("sider")).toBeUndefined()
    admin.remove("sider")
    expect(admin.get("sider")).toBeUndefined()
  })

  it("存储不可用或内容损坏时不抛出", () => {
    const broken: StorageLike = {
      getItem: () => {
        throw new Error("denied")
      },
      setItem: () => {
        throw new Error("quota")
      },
      removeItem: () => {
        throw new Error("denied")
      },
    }
    const s = appStorage("admin", broken)
    expect(() => s.set("x", 1)).not.toThrow()
    expect(s.get("x")).toBeUndefined()
    const bad = memory()
    bad.data.set("tenon:admin:x", "{")
    expect(appStorage("admin", bad).get("x")).toBeUndefined()
  })
})
