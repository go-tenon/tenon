import { describe, expect, it } from "vitest"
import { describeError, fieldErrors, isApiErrorLike, parseLocation } from "./errors"

const apiError = (status: number, extra: Record<string, unknown> = {}) => ({ status, fields: [], ...extra })

describe("describeError", () => {
  it("按状态码和错误码分类", () => {
    const cases: [unknown, string][] = [
      [apiError(401, { code: "auth.unauthenticated" }), "unauthenticated"],
      [apiError(403, { code: "auth.forbidden" }), "forbidden"],
      [apiError(403, { code: "auth.confirm_required" }), "confirm_required"],
      [apiError(403, { code: "auth.two_factor_required" }), "two_factor_required"],
      [apiError(403, { code: "auth.method_required" }), "method_required"],
      [apiError(426, { code: "app.upgrade_required" }), "upgrade_required"],
      [apiError(429, { code: "rate_limit.exceeded", retryAfter: 30 }), "rate_limited"],
      [apiError(422, { fields: [{ location: "body.title", message: "不能为空" }] }), "invalid"],
      [apiError(422, { code: "article.slug_locked" }), "business"], // 业务规则不允许：先看 code
      [apiError(409, { code: "article.slug_taken" }), "business"],
      [apiError(404), "not_found"],
      [apiError(503), "server"],
      [new TypeError("Failed to fetch"), "network"],
      [Object.assign(new Error("aborted"), { name: "AbortError" }), "aborted"],
      ["oops", "unknown"],
    ]
    for (const [err, kind] of cases) expect(describeError(err).kind, JSON.stringify(err)).toBe(kind)
    expect(describeError(apiError(429, { retryAfter: 30 })).retryAfter).toBe(30)
  })

  it("带上公开参数，没有时是空对象", () => {
    const err = apiError(403, { code: "auth.method_required", params: { methods: ["acme_sso"] } })
    expect(describeError(err)).toMatchObject({ kind: "method_required", params: { methods: ["acme_sso"] } })
    expect(describeError(apiError(404)).params).toEqual({})
    expect(describeError(new TypeError("offline")).params).toEqual({})
  })

  it("按形状识别 ApiError", () => {
    expect(isApiErrorLike(apiError(500))).toBe(true)
    expect(isApiErrorLike({ status: 500 })).toBe(false)
    expect(isApiErrorLike(null)).toBe(false)
  })
})

describe("parseLocation", () => {
  it("拆开来源和字段路径", () => {
    expect(parseLocation("body.items[0].name")).toEqual({
      source: "body",
      segments: ["items", 0, "name"],
      path: "items.0.name",
    })
    expect(parseLocation("query.per_page")).toEqual({ source: "query", segments: ["per_page"], path: "per_page" })
    expect(parseLocation("body")).toEqual({ source: "body", segments: [], path: "" })
  })
})

describe("fieldErrors", () => {
  it("按字段路径分组，每个字段取第一条", () => {
    const err = apiError(422, {
      fields: [
        { location: "body.title", message: "不能为空" },
        { location: "body.title", message: "至少 3 个字符" },
        { location: "body.tags[1]", message: "不能为空" },
        { location: "query.per_page", message: "不能大于 100" },
      ],
    })
    expect(fieldErrors(err)).toEqual({ title: "不能为空", "tags.1": "不能为空" })
    expect(fieldErrors(err, "query")).toEqual({ per_page: "不能大于 100" })
    expect(fieldErrors(new Error("x"))).toEqual({})
  })
})
