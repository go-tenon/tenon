// @vitest-environment happy-dom

import type { ErrorInfo } from "@go-tenon/client"
import { MantineProvider, TextInput } from "@mantine/core"
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { type ReactNode, useState } from "react"
import { afterEach, describe, expect, it } from "vitest"
import { applyServerErrors, FormError, useSchemaForm } from "./form"
import { polyfill } from "./testing"

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
  "zh-CN": { "validation.min_length": "至少 {{.Min}} 个字符", "validation.filled": "不能为空" },
}
const rules = { components, messages, language: "zh-CN" }

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

function ArticleForm({ server }: { server?: unknown }): ReactNode {
  const form = useSchemaForm({ schema, rules, initialValues: { title: "", items: [{ name: "" }] } })
  const [error, setError] = useState<ErrorInfo>()
  return (
    <MantineProvider env="test">
      <form onSubmit={form.onSubmit(() => setError(applyServerErrors(form, server)))}>
        <FormError error={error} />
        <TextInput label="标题" {...form.getInputProps("title")} />
        <TextInput label="名称" {...form.getInputProps("items.0.name")} />
        <button type="submit">保存</button>
      </form>
    </MantineProvider>
  )
}

describe("useSchemaForm", () => {
  it("提交前用生成的规则校验，提示与服务端相同", async () => {
    polyfill()
    render(<ArticleForm />)
    fireEvent.click(screen.getByText("保存"))
    expect(await screen.findByText("至少 2 个字符")).toBeTruthy()
    expect(screen.getByText("不能为空")).toBeTruthy()
  })

  it("applyServerErrors：字段错误显示在对应字段下面，其他错误交给 FormError", async () => {
    polyfill()
    const fill = () => {
      fireEvent.change(screen.getByLabelText("标题"), { target: { value: "好标题" } })
      fireEvent.change(screen.getByLabelText("名称"), { target: { value: "a" } })
      fireEvent.click(screen.getByText("保存"))
    }
    const { unmount } = render(<ArticleForm server={unprocessable} />)
    fill()
    expect(await screen.findByText("标题已被使用")).toBeTruthy()
    expect(screen.getByText("名称不能为空")).toBeTruthy()
    expect(screen.queryByRole("alert")).toBeNull()
    unmount()

    render(<ArticleForm server={conflict} />)
    fill()
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("slug 已被使用"))
  })
})
