// @vitest-environment happy-dom

import { TextInput } from "@mantine/core"
import { createRootRouteWithContext } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { Page } from "./page"
import type { RouterContext } from "./shell"
import { renderApp } from "./testing"

afterEach(cleanup)

function tree() {
  return createRootRouteWithContext<RouterContext>()({
    component: () => (
      <Page
        title="订单"
        tabs={[
          { value: "list", label: "列表", content: <TextInput aria-label="备注" /> },
          { value: "stats", label: "统计", count: 7, content: <p>统计内容</p> },
        ]}
      >
        <p>页面提示</p>
      </Page>
    ),
  })
}

describe("Page 的页内标签", () => {
  it("当前标签记在地址中，第一个标签不写；切走再回来，打开过的标签保留状态", async () => {
    const app = await renderApp(tree())
    expect(await screen.findByText("页面提示")).toBeTruthy() // children 在标签上面
    expect(screen.getByRole("tab", { name: /统计/ }).textContent).toContain("7")
    expect(screen.queryByText("统计内容")).toBeNull() // 没打开过的标签不渲染
    fireEvent.change(screen.getByLabelText("备注"), { target: { value: "写了一半" } })

    await act(async () => fireEvent.click(screen.getByRole("tab", { name: /统计/ })))
    expect(app.router.state.location.search).toEqual({ tab: "stats" })
    expect(screen.getByText("统计内容")).toBeTruthy()

    await act(async () => fireEvent.click(screen.getByRole("tab", { name: "列表" })))
    expect(app.router.state.location.search).toEqual({})
    expect((screen.getByLabelText("备注") as HTMLInputElement).value).toBe("写了一半")
  })

  it("地址中的 tab 打开对应的标签；不认识的值打开第一个", async () => {
    await renderApp(tree(), { path: "/?tab=stats" })
    expect((await screen.findByRole("tab", { name: /统计/ })).getAttribute("aria-selected")).toBe("true")
    cleanup()
    await renderApp(tree(), { path: "/?tab=nope" })
    expect((await screen.findByRole("tab", { name: "列表" })).getAttribute("aria-selected")).toBe("true")
  })
})
