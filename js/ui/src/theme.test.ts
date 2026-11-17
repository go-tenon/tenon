import { generateColors } from "@mantine/colors-generator"
import { describe, expect, it } from "vitest"
import { createUiTheme, layoutMetrics, palette, primaryColors, themes } from "./theme"

describe("palette", () => {
  it("预设的名称：26 种，色板与官方生成器对同一颜色值的结果相同", () => {
    expect(Object.keys(primaryColors)).toHaveLength(26)
    for (const [name, hex] of Object.entries(primaryColors)) {
      const p = palette(name)
      expect(p).toHaveLength(10)
      expect(p).toEqual(generateColors(hex))
      expect(p).toContain(hex)
    }
  })

  it("颜色值直接交给官方生成器", () => {
    expect(palette("#0f766e")).toEqual(generateColors("#0f766e"))
  })

  it("不认识的名称报错并列出预设", () => {
    expect(() => palette("sky-bleu")).toThrow(/invalid color "sky-bleu".*sky-blue/)
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

  it("日期时间的格式随预设：运营后台是 MM/DD HH:mm，可以覆盖", () => {
    expect(layoutMetrics(themes.admin).datetimeFormat).toBe("MM/DD HH:mm")
    expect(layoutMetrics(themes.console).datetimeFormat).toBe("YYYY-MM-DD HH:mm")
    const t = createUiTheme({ preset: "admin", datetimeFormat: "YYYY/MM/DD HH:mm" })
    expect(layoutMetrics(t)).toMatchObject({ preset: "admin", datetimeFormat: "YYYY/MM/DD HH:mm", headerHeight: 52 })
  })

  it("运营后台默认用 pale-blue 的主色，项目的品牌色覆盖它，可以写预设的名称", () => {
    expect(themes.admin.colors?.brand).toEqual(palette("pale-blue"))
    expect(createUiTheme({ preset: "admin", primary: "sky-blue" }).colors?.brand).toEqual(palette("#099cff"))
  })

  it("由设计令牌生成：品牌色、圆角、字体覆盖预设", () => {
    const t = createUiTheme({ preset: "admin", primary: "#0f766e", radius: 8, fontFamily: "Inter" })
    expect(t.colors?.brand).toEqual(palette("#0f766e"))
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
