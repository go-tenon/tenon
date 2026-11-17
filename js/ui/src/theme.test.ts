import { describe, expect, it } from "vitest"
import { createUiTheme, layoutMetrics, palette, themes } from "./theme"

// 相对亮度，用来检查色板从浅到深单调。
function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => Number.parseInt(hex.slice(i, i + 2), 16) / 255)
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

describe("palette", () => {
  it.each(["#4f5bd5", "#0f766e", "#e11d48", "#f59e0b", "#111827", "#a5b4fc"])(
    "%s：10 级，第 6 级是输入的颜色，从浅到深",
    (hex) => {
      const p = palette(hex)
      expect(p).toHaveLength(10)
      expect(p[6]).toBe(hex)
      for (const c of p) expect(c).toMatch(/^#[0-9a-f]{6}$/)
      for (let i = 1; i < 10; i++) expect(luminance(p[i])).toBeLessThanOrEqual(luminance(p[i - 1]) + 1e-9)
    },
  )

  it("不合法的颜色报错", () => {
    expect(() => palette("blue")).toThrow(/invalid color/)
  })
})

describe("createUiTheme", () => {
  it("三套预设的尺寸不同：后台最紧凑，官网最宽松", () => {
    const admin = layoutMetrics(themes.admin)
    const console = layoutMetrics(themes.console)
    const site = layoutMetrics(themes.site)
    expect(admin.headerHeight).toBeLessThan(console.headerHeight)
    expect(console.headerHeight).toBeLessThan(site.headerHeight)
    expect(admin.navbarWidth).toBeLessThan(console.navbarWidth)
    expect([admin.controlSize, console.controlSize, site.controlSize]).toEqual(["sm", "sm", "md"])
  })

  it("运营后台默认用深蓝灰的主色，项目的品牌色覆盖它", () => {
    expect(themes.admin.colors?.brand?.[6]).toBe("#34507f")
    expect(createUiTheme({ preset: "admin", primary: "#0f766e" }).colors?.brand?.[6]).toBe("#0f766e")
  })

  it("由设计令牌生成：品牌色、圆角、字体覆盖预设", () => {
    const t = createUiTheme({ preset: "admin", primary: "#0f766e", radius: 8, fontFamily: "Inter" })
    expect(t.colors?.brand?.[6]).toBe("#0f766e")
    expect(t.radius?.md).toBe("8px")
    expect(t.radius?.xl).toBe("16px")
    expect(t.fontFamily).toBe("Inter")
    expect(layoutMetrics(t).preset).toBe("admin")
    expect(t.primaryColor).toBe("brand")
  })

  it("落地页的风格写在布局尺寸中，默认没有；不认识的风格报错", () => {
    expect(layoutMetrics(themes.site).landing).toBeUndefined()
    const t = createUiTheme({ preset: "site", landing: "ink", primary: "#0f766e" })
    expect(layoutMetrics(t)).toMatchObject({ preset: "site", landing: "ink", headerHeight: 64 })
    expect(() => createUiTheme({ preset: "site", landing: "neon" as never })).toThrow(/unknown landing/)
  })
})
