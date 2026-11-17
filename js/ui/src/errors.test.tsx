// @vitest-environment happy-dom

import { createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { RouteError } from "./errors"
import { ForbiddenError, type RouterContext } from "./shell"
import { renderApp } from "./testing"

afterEach(cleanup)

function tree(load: () => unknown) {
  const root = createRootRouteWithContext<RouterContext>()({ component: Outlet })
  const page = createRoute({
    getParentRoute: () => root,
    path: "/",
    loader: async () => load(),
    errorComponent: RouteError,
    component: () => <p>页面内容</p>,
  })
  return root.addChildren([page])
}

describe("RouteError", () => {
  it("没有权限显示 403", async () => {
    await renderApp(
      tree(() => {
        throw new ForbiddenError("users:manage")
      }),
    )
    expect(await screen.findByText("没有访问权限")).toBeTruthy()
    expect(screen.getByText("403")).toBeTruthy()
  })

  it("接口返回 404 显示页面不存在和服务端的说明", async () => {
    await renderApp(
      tree(() => {
        throw { status: 404, code: "article.not_found", detail: "文章不存在", fields: [] }
      }),
    )
    expect(await screen.findByText("页面不存在")).toBeTruthy()
    expect(screen.getByText("文章不存在")).toBeTruthy()
  })

  it("服务端错误显示请求 ID，重试后恢复", async () => {
    let fail = true
    await renderApp(
      tree(() => {
        if (fail) throw { status: 503, detail: "unavailable", fields: [], requestId: "REQ-1" }
        return null
      }),
    )
    expect(await screen.findByText("出了点问题")).toBeTruthy()
    expect(screen.getByText(/REQ-1/)).toBeTruthy()
    fail = false
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "重试" })))
    expect(await screen.findByText("页面内容")).toBeTruthy()
  })
})
