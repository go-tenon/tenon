import { describe, expect, it } from "vitest"
import { siteLinkActive } from "./site-layout"

describe("siteLinkActive", () => {
  it("根路径严格匹配，子路径按前缀匹配", () => {
    expect(siteLinkActive("/", { to: "/" })).toBe(true)
    expect(siteLinkActive("/docs", { to: "/" })).toBe(false)
    expect(siteLinkActive("/docs", { to: "/docs" })).toBe(true)
    expect(siteLinkActive("/docs/quickstart", { to: "/docs" })).toBe(true)
    expect(siteLinkActive("/docs-other", { to: "/docs" })).toBe(false)
  })

  it("设置 activePrefix 时按该前缀匹配任意子文档页", () => {
    const link = { to: "/docs/quickstart", activePrefix: "/docs" }
    expect(siteLinkActive("/docs", link)).toBe(true)
    expect(siteLinkActive("/docs/", link)).toBe(true)
    expect(siteLinkActive("/docs/quickstart", link)).toBe(true)
    expect(siteLinkActive("/docs/protocols", link)).toBe(true)
    expect(siteLinkActive("/blog", link)).toBe(false)
  })

  it("外链链接不匹配选中状态", () => {
    expect(siteLinkActive("/docs", {})).toBe(false)
  })
})
