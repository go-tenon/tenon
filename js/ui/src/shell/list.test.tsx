// @vitest-environment happy-dom
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
  RouterProvider,
  useNavigate,
} from "@tanstack/react-router"
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react"
import { useState } from "react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { useUnsavedChanges } from "./form"
import { listSearch, useList, useSelection } from "./list"

// 与生成的 schemas.article.list.query 形状相同，加上排序。
const components = { ArticleStatus: { enum: ["draft", "published"], type: "string" } }
const query = {
  type: "object",
  properties: {
    page: { default: 1, minimum: 1, type: "integer" },
    per_page: { default: 20, maximum: 100, minimum: 1, type: "integer" },
    q: { type: "string" },
    sort: { enum: ["created", "-created", "title", "-title"], type: "string" },
    status: { $ref: "#/components/schemas/ArticleStatus" },
    tag: { items: { type: "string" }, type: ["array", "null"] },
  },
}
type Query = { page?: number; per_page?: number; q?: string; sort?: string; status?: string; tag?: string[] | null }

afterEach(cleanup)

describe("listSearch", () => {
  const parse = listSearch<Query>(query, { components })

  it("按规则转换类型，去掉不合法、不认识、空的和等于默认值的参数", () => {
    expect(
      parse({ page: "2", per_page: "20", q: "", status: "draft", tag: "a,b", other: "x", sort: "-created" }),
    ).toEqual({ page: 2, status: "draft", tag: ["a", "b"], sort: "-created" })
    expect(parse({ page: 0, per_page: 500, status: "bogus", sort: "rank", tag: [] })).toEqual({})
    // 路由默认把地址中的值解析为 JSON：数字和数组也能接受。
    expect(parse({ page: 3, per_page: 50, tag: ["x"] })).toEqual({ page: 3, per_page: 50, tag: ["x"] })
    expect(parse.defaults).toEqual({ page: 1, per_page: 20 })
  })
})

const listRoot = createRootRoute({ component: Outlet })
const articles = createRoute({
  getParentRoute: () => listRoot,
  path: "/articles",
  validateSearch: listSearch<Query>(query, { components }),
  component: Articles,
})
const listTree = listRoot.addChildren([articles])

function Articles() {
  const list = useList(articles)
  const selection = useSelection<string>(list.params)
  return (
    <div>
      <p data-testid="state">
        {JSON.stringify({ page: list.page, perPage: list.perPage, sort: list.sort, filters: list.filters })}
      </p>
      <p data-testid="table">{JSON.stringify(list.tableState)}</p>
      <p data-testid="selected">{[...selection.keys].join(",")}</p>
      <button type="button" onClick={() => selection.toggle("a1")}>
        select
      </button>
      <button type="button" onClick={() => list.setFilters({ q: "go" })}>
        search
      </button>
      <button type="button" onClick={() => list.setPage(2)}>
        page 2
      </button>
      <button type="button" onClick={() => list.setPerPage(20)}>
        per page 20
      </button>
      <button type="button" onClick={() => list.setSort({ field: "created", desc: true })}>
        sort
      </button>
      <button type="button" onClick={() => list.setFilters({ status: undefined })}>
        clear status
      </button>
      <button type="button" onClick={() => list.reset()}>
        reset
      </button>
    </div>
  )
}

function setup(initial: string) {
  const router = createRouter({ routeTree: listTree, history: createMemoryHistory({ initialEntries: [initial] }) })
  render(<RouterProvider router={router} />)
  const search = () => router.state.location.search as Record<string, unknown>
  const state = () => JSON.parse(screen.getByTestId("state").textContent ?? "{}")
  const click = (name: string) => act(async () => fireEvent.click(screen.getByText(name)))
  return { router, search, state, click }
}

describe("useList", () => {
  it("从地址读出列表的状态，修改时写回地址", async () => {
    const { router, search, state, click } = setup("/articles?page=3&per_page=50&status=draft&sort=title")
    await screen.findByTestId("state")
    expect(state()).toEqual({ page: 3, perPage: 50, sort: "title", filters: { status: "draft" } })
    expect(JSON.parse(screen.getByTestId("table").textContent ?? "")).toEqual({
      pageIndex: 2,
      pageSize: 50,
      sorting: [{ id: "title", desc: false }],
    })

    await click("search") // 修改筛选：回到第一页
    expect(search()).toEqual({ per_page: 50, status: "draft", sort: "title", q: "go" })
    await click("page 2") // 翻页：保留其他条件
    expect(search()).toEqual({ per_page: 50, status: "draft", sort: "title", q: "go", page: 2 })
    await click("per page 20") // 默认值不写进地址
    expect(search()).toEqual({ status: "draft", sort: "title", q: "go" })
    await click("sort")
    expect(search().sort).toBe("-created")
    await click("clear status")
    expect(search()).toEqual({ sort: "-created", q: "go" })
    await click("reset")
    expect(search()).toEqual({})
    expect(state()).toEqual({ page: 1, perPage: 20, filters: {} })

    await act(async () => router.history.back()) // 浏览器后退回到上一个列表
    await vi.waitFor(() => expect(search()).toEqual({ sort: "-created", q: "go" }))
  })

  it("查询条件变化时清空选中项", async () => {
    const { click } = setup("/articles")
    await screen.findByTestId("state")
    await click("select")
    expect(screen.getByTestId("selected").textContent).toBe("a1")
    await click("page 2")
    expect(screen.getByTestId("selected").textContent).toBe("")
  })
})

// confirmLeave 是当前测试的询问函数。
let confirmLeave: () => boolean = () => true

const editorRoot = createRootRoute({ component: Outlet })
const home = createRoute({ getParentRoute: () => editorRoot, path: "/", component: () => <p>home page</p> })
const edit = createRoute({ getParentRoute: () => editorRoot, path: "/edit", component: Editor })
const editorTree = editorRoot.addChildren([home, edit])

function Editor() {
  const [dirty, setDirty] = useState(false)
  const unsaved = useUnsavedChanges(dirty, { confirm: () => confirmLeave() })
  const navigate = useNavigate()
  return (
    <div>
      <button type="button" onClick={() => setDirty(true)}>
        type
      </button>
      <button
        type="button"
        onClick={() => {
          unsaved.release()
          void navigate({ to: "/" })
        }}
      >
        saved
      </button>
      <Link to="/">leave</Link>
    </div>
  )
}

describe("useUnsavedChanges", () => {
  function setupEditor(confirm: () => boolean) {
    confirmLeave = confirm
    const router = createRouter({ routeTree: editorTree, history: createMemoryHistory({ initialEntries: ["/edit"] }) })
    render(<RouterProvider router={router} />)
    return router
  }

  it("没有修改时直接离开", async () => {
    const confirm = vi.fn(() => false)
    setupEditor(confirm)
    await act(async () => fireEvent.click(await screen.findByText("leave")))
    expect(await screen.findByText("home page")).toBeTruthy()
    expect(confirm).not.toHaveBeenCalled()
  })

  it("有修改时先询问：取消时留下，确定时离开", async () => {
    const answers = [false, true]
    const confirm = vi.fn(() => answers.shift() ?? true)
    const router = setupEditor(confirm)
    await act(async () => fireEvent.click(await screen.findByText("type")))
    await act(async () => fireEvent.click(screen.getByText("leave")))
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(router.state.location.pathname).toBe("/edit")
    await act(async () => fireEvent.click(screen.getByText("leave")))
    expect(await screen.findByText("home page")).toBeTruthy()
  })

  it("保存成功后调用 release，跳转时不再询问", async () => {
    const confirm = vi.fn(() => false)
    setupEditor(confirm)
    await act(async () => fireEvent.click(await screen.findByText("type")))
    await act(async () => fireEvent.click(screen.getByText("saved")))
    expect(await screen.findByText("home page")).toBeTruthy()
    expect(confirm).not.toHaveBeenCalled()
  })
})
