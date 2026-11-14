import { describe, expect, it } from "vitest"
import { safeRedirect } from "./redirect"

describe("safeRedirect", () => {
  it("只接受同源的相对路径", () => {
    expect(safeRedirect("/articles?page=2#top")).toBe("/articles?page=2#top")
    expect(safeRedirect("/a/../b")).toBe("/b")
    for (const bad of [
      "//evil.com",
      "/\\evil.com",
      "https://evil.com",
      "evil",
      "javascript:alert(1)",
      "",
      undefined,
      42,
    ]) {
      expect(safeRedirect(bad, "/home"), String(bad)).toBe("/home")
    }
  })
})
