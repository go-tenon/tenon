import { describe, expect, it } from "vitest"
import { formatDuration } from "../../theme/format"
import { periodSearch, resolvePeriodDates } from "./period"

describe("periodSearch", () => {
  it("strips default preset and invalid params while keeping valid range and granularity", () => {
    const parse = periodSearch({ defaultPreset: "30d" })
    expect(parse({ preset: "30d" })).toEqual({})
    expect(parse({ preset: "7d", granularity: "day" })).toEqual({ preset: "7d", granularity: "day" })
    expect(parse({ from: "2026-11-01", to: "2026-11-10" })).toEqual({
      preset: "custom",
      from: "2026-11-01",
      to: "2026-11-10",
    })
    expect(parse({ from: "2026-11-15", to: "2026-11-10" })).toEqual({})
    expect(parse({ preset: "bogus", granularity: "quarter" })).toEqual({})
  })
})

describe("resolvePeriodDates", () => {
  it("computes presets in the requested IANA time zone", () => {
    // UTC 2026-11-22 16:30 = Asia/Shanghai 2026-11-23 00:30
    const now = new Date("2026-11-22T16:30:00Z")
    expect(resolvePeriodDates("today", "Asia/Shanghai", now)).toEqual({ from: "2026-11-23", to: "2026-11-23" })
    expect(resolvePeriodDates("today", "UTC", now)).toEqual({ from: "2026-11-22", to: "2026-11-22" })
    expect(resolvePeriodDates("7d", "Asia/Shanghai", now)).toEqual({ from: "2026-11-17", to: "2026-11-23" })
    expect(resolvePeriodDates("this_month", "Asia/Shanghai", now)).toEqual({ from: "2026-11-01", to: "2026-11-23" })
    expect(resolvePeriodDates("last_month", "Asia/Shanghai", now)).toEqual({ from: "2026-10-01", to: "2026-10-31" })
  })
})

describe("formatDuration", () => {
  it("formats seconds, milliseconds, and empty values", () => {
    expect(formatDuration(null)).toBe("—")
    expect(formatDuration(undefined)).toBe("—")
    expect(formatDuration(0)).toBe("0s")
    expect(formatDuration(45)).toBe("45s")
    expect(formatDuration(142)).toBe("2m 22s")
    expect(formatDuration(3600)).toBe("1h")
    expect(formatDuration(5480)).toBe("1h 31m")
    expect(formatDuration(90000)).toBe("1d 1h")
    expect(formatDuration(420, { unit: "ms" })).toBe("420 ms")
    expect(formatDuration(1250, { unit: "ms" })).toBe("1.3s")
  })
})
