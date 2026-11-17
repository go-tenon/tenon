// @vitest-environment happy-dom

import { createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { type Column, DataTable } from "./data-table"
import { type RouterContext, useList, useSelection } from "./shell"
import { renderApp } from "./testing"

afterEach(() => {
  cleanup()
  localStorage.clear()
})

type Row = { id: string; name: string; amount: number }
const all: Row[] = Array.from({ length: 25 }, (_, i) => ({
  id: `r${i + 1}`,
  name: `订单 ${i + 1}`,
  amount: (i + 1) * 10,
}))

const columns: Column<Row>[] = [
  { accessor: "name", title: "名称", sortable: true, fixed: true },
  { accessor: "amount", title: "金额", sortable: true, textAlign: "right" },
  { accessor: "note", title: "备注", hidden: true, render: () => "备注内容" },
]

function tree() {
  const root = createRootRouteWithContext<RouterContext>()({ component: Outlet })
  const page = createRoute({
    getParentRoute: () => root,
    path: "/",
    validateSearch: (s: Record<string, unknown>) => s,
    component: function Orders() {
      const list = useList(page as never)
      const selection = useSelection<string>()
      const desc = list.sort?.startsWith("-")
      const sorted = list.sort ? [...all].sort((a, b) => (desc ? b.amount - a.amount : a.amount - b.amount)) : all
      const records = sorted.slice((list.page - 1) * list.perPage, list.page * list.perPage)
      return (
        <DataTable
          records={records}
          total={all.length}
          list={list}
          columns={columns}
          selection={selection}
          storeKey="orders"
          bulkActions={(keys) => <span data-testid="bulk">{keys.join(",")}</span>}
          expand={(row) => <p>{`${row.name} 的明细`}</p>}
        />
      )
    },
  })
  return root.addChildren([page])
}

describe("DataTable", () => {
  it("分页显示中文，翻页和排序写进地址", async () => {
    const app = await renderApp(tree(), { path: "/?per_page=10" })
    expect(await screen.findByText("1–10，共 25 条")).toBeTruthy()
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "2" })))
    await screen.findByText("11–20，共 25 条")
    expect(app.router.state.location.search).toMatchObject({ page: 2, per_page: 10 })
    await act(async () => fireEvent.click(screen.getByText("金额")))
    await waitFor(() => expect(app.router.state.location.search).toMatchObject({ sort: "amount" }))
    expect(app.router.state.location.search).not.toHaveProperty("page")
    await act(async () => fireEvent.click(screen.getByText("金额")))
    await waitFor(() => expect(app.router.state.location.search).toMatchObject({ sort: "-amount" }))
    expect(screen.getAllByRole("row")[1].textContent).toContain("订单 25")
  })

  it("选中跨页保留，批量操作拿到全部选中的行", async () => {
    await renderApp(tree(), { path: "/?per_page=10" })
    await screen.findByText("订单 1")
    const check = (name: string) => within(screen.getByText(name).closest("tr") as HTMLElement).getByRole("checkbox")
    await act(async () => fireEvent.click(check("订单 1")))
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "2" })))
    await screen.findByText("订单 11")
    await act(async () => fireEvent.click(check("订单 12")))
    expect(screen.getByText("已选 2 项")).toBeTruthy()
    expect(screen.getByTestId("bulk").textContent).toBe("r1,r12")
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "取消选择" })))
    expect(screen.queryByText("已选 2 项")).toBeNull()
  })

  it("默认隐藏的列可以在列设置中打开，保存在浏览器中；点击行展开", async () => {
    await renderApp(tree())
    await screen.findByText("订单 1")
    expect(screen.queryByText("备注内容")).toBeNull()
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "列设置" })))
    await act(async () => fireEvent.click(screen.getByRole("checkbox", { name: "备注" })))
    expect((await screen.findAllByText("备注内容")).length).toBeGreaterThan(0)
    expect(JSON.stringify(localStorage)).toContain("orders")
    await act(async () => fireEvent.click(screen.getByText("订单 3")))
    expect(await screen.findByText("订单 3 的明细")).toBeTruthy()
  })
})
