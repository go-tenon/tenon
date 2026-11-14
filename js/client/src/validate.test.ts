import { describe, expect, it } from "vitest"
import { type Schema, validate } from "./validate"

const messages = {
  en: { "validation.minimum": "Must be at least {{.Min}}" },
  "zh-CN": { "validation.minimum": "不能小于 {{.Min}}", "validation.pattern_description": "必须是{{.Description}}" },
}
const codes = (schema: Schema, value: unknown) => validate(schema, value).map((e) => `${e.location} ${e.code}`)

describe("validate", () => {
  it("数值规则，提示中的数字与 Go 的 %v 相同", () => {
    const s = { type: "integer", minimum: 1, maximum: 10, multipleOf: 2 }
    expect(codes(s, 0)).toEqual(["body validation.minimum"])
    expect(codes(s, 11)).toEqual(["body validation.maximum", "body validation.multiple_of"])
    expect(codes(s, 2.5)).toEqual(["body validation.integer", "body validation.multiple_of"])
    expect(codes(s, "2")).toEqual(["body validation.integer"])
    expect(validate({ type: "number", minimum: 1.5 }, 1, { messages, language: "zh-CN" })[0].message).toBe(
      "不能小于 1.5",
    )
    expect(codes({ type: "number", exclusiveMinimum: 0, exclusiveMaximum: 1 }, 1)).toEqual([
      "body validation.exclusive_maximum",
    ])
  })

  it("字符串按 Unicode 码点计长度，检查 pattern 和 format", () => {
    expect(codes({ type: "string", maxLength: 2 }, "😀😀")).toEqual([])
    expect(codes({ type: "string", minLength: 3 }, "ab")).toEqual(["body validation.min_length"])
    const r = validate({ type: "string", pattern: "^[a-z]+$", patternDescription: "小写字母" }, "A", {
      messages,
      language: "zh-CN",
    })
    expect(r[0]).toMatchObject({ code: "validation.pattern_description", message: "必须是小写字母" })
    for (const [format, ok, bad] of [
      ["date-time", "2026-11-11T08:00:00Z", "2026-11-11 08:00:00"],
      ["date", "2026-02-28", "2026-02-30"],
      ["time", "08:00:00", "25:00:00"],
      ["email", "a@example.com", "a@"],
      ["uri", "https://example.com/x", "/relative"],
      ["uuid", "0193e7a2-7c1e-7b4a-9d2f-3c5a1e8b6f10", "123"],
    ]) {
      expect(codes({ type: "string", format }, ok), format).toEqual([])
      expect(codes({ type: "string", format }, bad), format).toHaveLength(1)
    }
  })

  it("数组：数量、去重和每一项", () => {
    const s = { type: "array", minItems: 1, maxItems: 3, uniqueItems: true, items: { type: "string", minLength: 1 } }
    expect(codes(s, [])).toEqual(["body validation.min_items"])
    expect(codes(s, ["a", "a", ""])).toEqual(["body validation.unique", "body[2] validation.filled"])
    expect(codes(s, "a")).toEqual(["body validation.type"])
  })

  it("对象：$ref、allOf、只读字段、null 和 additionalProperties", () => {
    const components = {
      Base: { type: "object", properties: { id: { type: "string", readOnly: true } }, required: ["id"] },
      Item: { allOf: [{ $ref: "#/components/schemas/Base" }], type: "object", properties: { n: { type: "integer" } } },
    }
    // 只读字段在请求中不是必填的。
    expect(validate({ $ref: "#/components/schemas/Item" }, { n: "x" }, { components }).map((e) => e.code)).toEqual([
      "validation.integer",
    ])
    const s = { type: "object", properties: { a: { type: "string" } }, additionalProperties: { type: "integer" } }
    expect(codes(s, { a: null, b: 1, c: "x" })).toEqual(["body.c validation.integer"])
  })

  it("语言回退：主语言，然后是第一种语言；没有模板时提示是错误码", () => {
    expect(validate({ type: "integer", minimum: 1 }, 0, { messages, language: "zh-TW" })[0].message).toBe("不能小于 1")
    expect(validate({ type: "integer", minimum: 1 }, 0, { messages, language: "ja" })[0].message).toBe(
      "Must be at least 1",
    )
    expect(validate({ type: "integer", minimum: 1 }, 0)[0].message).toBe("validation.minimum")
  })

  it("位置的前缀", () => {
    expect(
      validate(
        { type: "object", properties: { per_page: { type: "integer", maximum: 100 } } },
        { per_page: 500 },
        { location: "query" },
      )[0].location,
    ).toBe("query.per_page")
  })
})
