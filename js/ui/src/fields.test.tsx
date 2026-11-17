// @vitest-environment happy-dom

import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { FieldValue } from "./fields"
import { UIProvider } from "./provider"
import { createUiTheme, themes } from "./theme"

afterEach(cleanup)

const field = { name: "created_at", label: "创建时间", kind: "datetime" } as never

describe("FieldValue", () => {
  it("日期时间按主题的格式显示：运营后台是 MM/DD HH:mm，控制台是 YYYY-MM-DD HH:mm，可以覆盖", () => {
    const show = (theme: Parameters<typeof UIProvider>[0]["theme"]) =>
      render(
        <UIProvider theme={theme} env="test">
          <FieldValue field={field} value="2026-11-03T10:12:00" />
        </UIProvider>,
      )
    show(themes.admin)
    expect(screen.getByText("11/03 10:12")).toBeTruthy()
    cleanup()
    show(themes.console)
    expect(screen.getByText("2026-11-03 10:12")).toBeTruthy()
    cleanup()
    show(createUiTheme({ preset: "admin", datetimeFormat: "YYYY/MM/DD HH:mm" }))
    expect(screen.getByText("2026/11/03 10:12")).toBeTruthy()
  })

  it("解析不了的值原样显示", () => {
    render(
      <UIProvider theme={themes.admin} env="test">
        <FieldValue field={field} value="不是时间" />
      </UIProvider>,
    )
    expect(screen.getByText("不是时间")).toBeTruthy()
  })
})
