import { describe, expect, it } from "vitest"
import { formValidator, serverErrors } from "./form"

// 与生成的 schemas.ts、messages.ts 形状相同：命名类型在 components 中，提示按语言。
const components = {
  Item: { type: "object", required: ["name"], properties: { name: { type: "string", minLength: 1 } } },
}
const schema = {
  type: "object",
  required: ["title", "items"],
  properties: {
    title: { type: "string", minLength: 2, maxLength: 10 },
    items: { type: "array", items: { $ref: "#/components/schemas/Item" } },
  },
}
const messages = {
  "zh-CN": {
    "validation.min_length": "至少 {{.Min}} 个字符",
    "validation.max_length": "最多 {{.Max}} 个字符",
    "validation.filled": "不能为空",
  },
}
const options = { components, messages, language: "zh-CN" }

// 服务端的 422：字段错误的位置是 body.<路径>。
const unprocessable = {
  status: 422,
  detail: "提交的内容有误",
  fields: [
    { location: "body.title", message: "标题已被使用", code: "validation.custom" },
    { location: "body.items[0].name", message: "名称不能为空", code: "validation.required" },
  ],
}
const conflict = { status: 409, code: "article.slug_taken", detail: "slug 已被使用", fields: [] }

describe("formValidator", () => {
  it("按字段路径返回第一条提示，嵌套和数组字段用点连接", () => {
    const check = formValidator(schema, options)
    expect(check({ title: "x", items: [{ name: "" }] })).toEqual({
      title: "至少 2 个字符",
      "items.0.name": "不能为空",
    })
    expect(check({ title: "好标题", items: [{ name: "a" }] })).toEqual({})
  })
})

describe("serverErrors", () => {
  it("字段错误对应到字段；其他错误作为整个表单的错误", () => {
    expect(serverErrors(unprocessable)).toEqual({ fields: { title: "标题已被使用", "items.0.name": "名称不能为空" } })
    expect(serverErrors(conflict).form).toMatchObject({
      kind: "business",
      code: "article.slug_taken",
      message: "slug 已被使用",
    })
    expect(serverErrors(new TypeError("Failed to fetch")).form).toMatchObject({ kind: "network" })
  })
})
