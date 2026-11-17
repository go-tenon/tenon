// @vitest-environment happy-dom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { useState } from "react"
import { useForm } from "react-hook-form"
import { afterEach, describe, expect, it } from "vitest"
import { formValidator, resolver, serverErrors, setServerErrors } from "./form"

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

afterEach(cleanup)

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

type Values = { title: string; items: { name: string }[] }

function HookForm({ server }: { server?: unknown }) {
  const form = useForm<Values>({
    resolver: resolver<Values>(schema, options),
    defaultValues: { title: "", items: [{ name: "" }] },
  })
  const [formError, setFormError] = useState<string>()
  const { errors } = form.formState
  return (
    <form onSubmit={form.handleSubmit(() => setFormError(setServerErrors(server, form)?.message))}>
      <input aria-label="title" {...form.register("title")} />
      <span data-testid="title-error">{errors.title?.message}</span>
      <input aria-label="name" {...form.register("items.0.name")} />
      <span data-testid="name-error">{errors.items?.[0]?.name?.message}</span>
      <span data-testid="form-error">{formError}</span>
      <button type="submit">save</button>
    </form>
  )
}

describe("react-hook-form", () => {
  it("提交前用生成的规则校验，提示与服务端相同", async () => {
    render(<HookForm />)
    fireEvent.click(screen.getByText("save"))
    await waitFor(() => expect(screen.getByTestId("title-error").textContent).toBe("至少 2 个字符"))
    expect(screen.getByTestId("name-error").textContent).toBe("不能为空")
  })

  it("服务端的字段错误显示在对应字段下面，其他错误显示在表单上方", async () => {
    const { unmount } = render(<HookForm server={unprocessable} />)
    fireEvent.change(screen.getByLabelText("title"), { target: { value: "好标题" } })
    fireEvent.change(screen.getByLabelText("name"), { target: { value: "a" } })
    fireEvent.click(screen.getByText("save"))
    await waitFor(() => expect(screen.getByTestId("title-error").textContent).toBe("标题已被使用"))
    expect(screen.getByTestId("name-error").textContent).toBe("名称不能为空")
    expect(screen.getByTestId("form-error").textContent).toBe("")
    unmount()

    render(<HookForm server={conflict} />)
    fireEvent.change(screen.getByLabelText("title"), { target: { value: "好标题" } })
    fireEvent.change(screen.getByLabelText("name"), { target: { value: "a" } })
    fireEvent.click(screen.getByText("save"))
    await waitFor(() => expect(screen.getByTestId("form-error").textContent).toBe("slug 已被使用"))
  })
})
