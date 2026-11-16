// @vitest-environment happy-dom
// registry 的组件：用真实的 Radix、Router、@go-tenon/shell 和会话渲染。
import { createSession, type Session } from "@go-tenon/client"
import {
  AppRoot,
  createApp,
  Fill,
  listSearch,
  type RouterContext,
  requireAuth,
  useList,
  useSelection,
} from "@go-tenon/shell"
import { createMemoryHistory, createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import type { ColumnDef } from "@tanstack/react-table"
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest"
import { ColorModeProvider } from "@/components/tenon/color-mode"
import { CommandMenu } from "@/components/tenon/command-menu"
import { DataTable, ListToolbar, StatusBadge } from "@/components/tenon/data-table"
import { ConfirmProvider, useConfirm } from "@/components/tenon/dialogs"
import { Field, FormError, useApiForm } from "@/components/tenon/form"
import { LoginForm } from "@/components/tenon/login-form"
import { MixedLayout } from "@/components/tenon/mixed-layout"
import { AuthLayout } from "@/components/tenon/page-layouts"
import { SidebarLayout } from "@/components/tenon/sidebar-layout"
import { TopNavLayout } from "@/components/tenon/topnav-layout"
import { UserMenu } from "@/components/tenon/user-menu"
import { Input } from "@/components/ui/input"

beforeAll(() => {
  window.matchMedia ??= ((query: string) => ({
    matches: false,
    media: query,
    addEventListener() {},
    removeEventListener() {},
    onchange: null,
    dispatchEvent: () => false,
  })) as never
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as never
  Element.prototype.scrollIntoView ??= () => {}
  Element.prototype.hasPointerCapture ??= () => false
})

afterEach(() => {
  cleanup()
  localStorage.clear()
})

/** 打开 Radix 的菜单：触发器在按下指针时打开。 */
const openMenu = (el: HTMLElement) =>
  act(async () => {
    fireEvent.pointerDown(el, { button: 0, ctrlKey: false, pointerType: "mouse" })
  })

type Article = { id: string; title: string; status: "draft" | "published" }
const articles: Article[] = [
  { id: "a1", title: "你好", status: "published" },
  { id: "a2", title: "草稿一篇", status: "draft" },
]

let layout = () => <SidebarLayout title="控制台" />
const logout = vi.fn(async () => {})

function Root() {
  return (
    <ColorModeProvider>
      <ConfirmProvider>
        <Outlet />
      </ConfirmProvider>
    </ColorModeProvider>
  )
}

function Shell() {
  return (
    <>
      <Fill name="user">
        <UserMenu name="小明" email="ming@example.com" logout={logout} />
      </Fill>
      <Fill name="search">
        <CommandMenu />
      </Fill>
      {layout()}
    </>
  )
}

const query = {
  type: "object",
  properties: {
    page: { type: "integer", minimum: 1, default: 1 },
    per_page: { type: "integer", minimum: 1, maximum: 100, default: 1 },
    sort: { type: "string", enum: ["title", "-title"] },
    q: { type: "string" },
  },
}

const columns: ColumnDef<Article>[] = [
  { id: "title", accessorKey: "title", header: "标题" },
  {
    id: "status",
    header: "状态",
    enableSorting: false,
    cell: ({ row }) => (
      <StatusBadge tone={row.original.status === "published" ? "success" : "muted"}>
        {row.original.status === "published" ? "已发布" : "草稿"}
      </StatusBadge>
    ),
  },
]

const removed = vi.fn()

function ArticlesPage() {
  const list = useList(articlesRoute)
  const selection = useSelection<string>(list.params)
  const page = articles.slice((list.page - 1) * list.perPage, list.page * list.perPage)
  return (
    <DataTable
      columns={columns}
      data={page}
      total={articles.length}
      list={list as never}
      selection={selection}
      rowActions={(a) => [{ label: "删除", destructive: true, onSelect: () => removed(a.id) }]}
      toolbar={<ListToolbar list={list as never} />}
    />
  )
}

type Changes = { title: string }
const schema = { type: "object", required: ["title"], properties: { title: { type: "string", minLength: 2 } } }
const rules = { messages: { "zh-CN": { "validation.min_length": "至少 {{.Min}} 个字符" } }, language: "zh-CN" }
let submit = vi.fn(async (_: Changes) => {})

function EditorPage() {
  const { form, onSubmit, formError, pending } = useApiForm<Changes>({
    schema,
    rules,
    defaultValues: { title: "" },
    submit: (v) => submit(v),
  })
  const confirm = useConfirm()
  const [answer, setAnswer] = useStateAnswer()
  return (
    <form onSubmit={onSubmit}>
      <FormError error={formError} />
      <Field form={form} name="title" label="标题">
        {(p) => <Input {...p} />}
      </Field>
      <button type="submit" disabled={pending}>
        保存
      </button>
      <button
        type="button"
        onClick={async () => setAnswer(String(await confirm({ title: "确定删除？", destructive: true })))}
      >
        删除
      </button>
      <p data-testid="answer">{answer}</p>
    </form>
  )
}

import { useState } from "react"

function useStateAnswer() {
  return useState("")
}

const root = createRootRouteWithContext<RouterContext>()({ component: Root })
const appRoute = createRoute({ getParentRoute: () => root, id: "_app", beforeLoad: requireAuth, component: Shell })
const home = createRoute({
  getParentRoute: () => appRoute,
  path: "/",
  staticData: { title: "首页", icon: "home", order: -1 },
  component: () => <p>home page</p>,
})
const articlesRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/articles",
  staticData: { title: "文章", icon: "file-text" },
  validateSearch: listSearch(query),
  component: ArticlesPage,
})
const editor = createRoute({
  getParentRoute: () => appRoute,
  path: "/editor",
  staticData: { title: "编辑器" },
  component: EditorPage,
})
let loginFn = vi.fn(async () => ({ access_token: "a2", expires_at: "" }))
const login = createRoute({
  getParentRoute: () => root,
  path: "/login",
  validateSearch: (s: Record<string, unknown>): { redirect?: string } => ({
    redirect: typeof s.redirect === "string" ? s.redirect : undefined,
  }),
  component: function Login() {
    const { redirect } = login.useSearch()
    return (
      <AuthLayout title="登录">
        <LoginForm login={() => loginFn()} redirect={redirect} />
      </AuthLayout>
    )
  },
})
const routeTree = root.addChildren([appRoute.addChildren([home, articlesRoute, editor]), login])

async function setup(path: string, signedIn = true) {
  const session: Session = createSession({
    refresh: async () => {
      if (!signedIn) throw { status: 401, fields: [] }
      return { access_token: "a1", expires_at: "" }
    },
  })
  if (signedIn) await session.signIn({ access_token: "a1", expires_at: "" }, { account: "u1" })
  const app = createApp({
    id: "registry-test",
    routeTree,
    session,
    router: { history: createMemoryHistory({ initialEntries: [path] }) },
  })
  render(<AppRoot app={app} />)
  return { app, session, search: () => app.router.state.location.search as Record<string, unknown> }
}

/** 窄屏：matchMedia 的查询都匹配。 */
const narrowScreen = (query: string) =>
  ({ matches: true, media: query, addEventListener() {}, removeEventListener() {} }) as unknown as MediaQueryList

describe("布局", () => {
  it("侧栏布局：菜单来自路由，链接由路由跳转，收起状态保存在布局状态中", async () => {
    layout = () => <SidebarLayout title="控制台" />
    const { app } = await setup("/")
    await screen.findByText("home page")
    expect(screen.getAllByText("文章").length).toBeGreaterThan(0)
    await act(async () => fireEvent.click(screen.getAllByText("文章")[0].closest("a") as HTMLElement))
    await screen.findByText("你好")
    expect(app.router.state.location.pathname).toBe("/articles")
    await act(async () => fireEvent.click(screen.getByRole("button", { name: /Toggle Sidebar/i })))
    expect(JSON.parse(localStorage.getItem("tenon:registry-test:layout:u1") ?? "{}").collapsed).toBe(true)
  })

  it("侧栏布局的多标签", async () => {
    layout = () => <SidebarLayout title="控制台" tabs />
    const { app } = await setup("/")
    await screen.findByText("home page")
    await act(() => app.router.navigate({ href: "/editor" }))
    await screen.findByLabelText("标题")
    expect(screen.getAllByRole("tab").map((t) => t.textContent)).toEqual(["首页", "编辑器"])
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "关闭 编辑器" })))
    await waitFor(() => expect(screen.getAllByRole("tab").length).toBe(1))
  })

  it("顶部导航布局", async () => {
    layout = () => <TopNavLayout title="门户" />
    await setup("/")
    await screen.findByText("home page")
    expect(screen.getByText("门户")).toBeTruthy()
    expect(screen.getAllByText("文章")[0].closest("a")?.getAttribute("href")).toBe("/articles")
  })

  it("顶部导航布局：窄屏时 narrowNav=bottom 把第一级菜单放到底部标签栏", async () => {
    const narrow = vi.spyOn(window, "matchMedia").mockImplementation(narrowScreen)
    try {
      layout = () => <TopNavLayout title="门户" narrowNav="bottom" />
      const { app } = await setup("/")
      await screen.findByText("home page")
      const bottom = screen.getByRole("navigation", { name: "主导航" })
      const links = Array.from(bottom.querySelectorAll("a"))
      expect(links.map((a) => a.textContent)).toEqual(["首页", "文章", "编辑器"])
      expect(links[0].getAttribute("aria-current")).toBe("page")
      expect(screen.queryByRole("button", { name: "打开导航" })).toBeNull()
      await act(async () => fireEvent.click(links[1]))
      await screen.findByText("你好")
      expect(app.router.state.location.pathname).toBe("/articles")
    } finally {
      narrow.mockRestore()
    }
  })

  it("顶栏 + 侧栏布局：模块在顶栏，点击进入模块", async () => {
    layout = () => <MixedLayout title="控制台" />
    const { app } = await setup("/")
    await screen.findByText("home page")
    const header = document.querySelector("header") as HTMLElement
    const modules = Array.from(header.querySelectorAll("nav a"))
    expect(modules.map((a) => a.textContent)).toEqual(["首页", "文章", "编辑器"])
    expect(modules[0].getAttribute("aria-current")).toBe("page")
    await act(async () => fireEvent.click(modules[1]))
    await screen.findByText("你好")
    expect(app.router.state.location.pathname).toBe("/articles")
    await act(async () => fireEvent.click(screen.getByRole("button", { name: /Toggle Sidebar/i })))
    expect(JSON.parse(localStorage.getItem("tenon:registry-test:layout:u1") ?? "{}").collapsed).toBe(true)
  })
})

describe("顶栏的部件", () => {
  it("用户菜单：切换颜色模式；退出", async () => {
    layout = () => <SidebarLayout title="控制台" />
    const { app } = await setup("/")
    await screen.findByText("home page")
    await openMenu(screen.getByTestId("user-menu"))
    await act(async () => fireEvent.click(await screen.findByText("暗色")))
    expect(document.documentElement.classList.contains("dark")).toBe(true)
    expect(localStorage.getItem("tenon:registry-test:color-mode")).toBe('"dark"')
    await openMenu(screen.getByTestId("user-menu"))
    await act(async () => fireEvent.click(await screen.findByText("退出登录")))
    await waitFor(() => expect(app.router.state.location.pathname).toBe("/login"))
    expect(logout).toHaveBeenCalled()
  })

  it("⌘K 打开命令面板，选择页面后跳转", async () => {
    layout = () => <SidebarLayout title="控制台" />
    const { app } = await setup("/")
    await screen.findByText("home page")
    await act(async () => fireEvent.keyDown(window, { key: "k", metaKey: true }))
    const input = await screen.findByPlaceholderText("搜索页面…")
    fireEvent.change(input, { target: { value: "编辑" } })
    await act(async () => fireEvent.click(await screen.findByRole("option", { name: /编辑器/ })))
    await waitFor(() => expect(app.router.state.location.pathname).toBe("/editor"))
  })
})

describe("资源表格", () => {
  it("分页、排序、搜索保存在地址中；选中项；行操作", async () => {
    layout = () => <SidebarLayout title="控制台" />
    const { search } = await setup("/articles")
    await screen.findByText("你好")
    expect(screen.getByText("已发布")).toBeTruthy()
    expect(screen.getByText("第 1 / 2 页")).toBeTruthy()
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "下一页" })))
    await screen.findByText("草稿一篇")
    expect(search()).toEqual({ page: 2 })
    await act(async () => fireEvent.click(screen.getByRole("button", { name: /标题/ })))
    await waitFor(() => expect(search()).toEqual({ sort: "title" }))

    await act(async () => fireEvent.click(screen.getByRole("checkbox", { name: "选择" })))
    expect(screen.getByText(/已选 1 条/)).toBeTruthy()

    await openMenu(screen.getByRole("button", { name: "操作" }))
    await act(async () => fireEvent.click(await screen.findByText("删除")))
    expect(removed).toHaveBeenCalledWith("a1")

    fireEvent.change(screen.getByPlaceholderText("搜索…"), { target: { value: "你好" } })
    await waitFor(() => expect(search()).toEqual({ sort: "title", q: "你好" }), { timeout: 2000 })
  })
})

describe("表单与对话框", () => {
  it("前端按生成的规则校验；服务端的字段错误在输入框下面，其他错误在上方", async () => {
    layout = () => <SidebarLayout title="控制台" />
    await setup("/editor")
    fireEvent.change(await screen.findByLabelText("标题"), { target: { value: "x" } })
    await act(async () => fireEvent.submit(screen.getByText("保存").closest("form") as HTMLFormElement))
    await screen.findByText("至少 2 个字符")

    submit = vi.fn(async () => {
      throw { status: 422, fields: [{ location: "body.title", message: "标题已被使用" }] }
    })
    fireEvent.change(screen.getByLabelText("标题"), { target: { value: "好标题" } })
    await act(async () => fireEvent.submit(screen.getByText("保存").closest("form") as HTMLFormElement))
    await screen.findByText("标题已被使用")

    submit = vi.fn(async () => {
      throw { status: 409, code: "article.slug_taken", detail: "slug 已被使用", fields: [] }
    })
    await act(async () => fireEvent.submit(screen.getByText("保存").closest("form") as HTMLFormElement))
    await screen.findByText("slug 已被使用")
  })

  it("useConfirm 返回用户的选择", async () => {
    layout = () => <SidebarLayout title="控制台" />
    await setup("/editor")
    await act(async () => fireEvent.click(await screen.findByText("删除")))
    await act(async () => fireEvent.click(await screen.findByRole("button", { name: "确定" })))
    await waitFor(() => expect(screen.getByTestId("answer").textContent).toBe("true"))
    await act(async () => fireEvent.click(screen.getByText("删除")))
    await act(async () => fireEvent.click(await screen.findByRole("button", { name: "取消" })))
    await waitFor(() => expect(screen.getByTestId("answer").textContent).toBe("false"))
  })

  it("登录：回到原来的页面；接口的错误显示在表单上方", async () => {
    loginFn = vi.fn(async () => {
      throw { status: 401, code: "auth.invalid_credentials", detail: "邮箱或密码不对", fields: [] }
    })
    const { app, session } = await setup("/articles", false)
    fireEvent.change(await screen.findByLabelText("邮箱"), { target: { value: "ming@example.com" } })
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "x" } })
    await act(async () => fireEvent.submit(screen.getByLabelText("邮箱").closest("form") as HTMLFormElement))
    await screen.findByText("邮箱或密码不对")
    loginFn = vi.fn(async () => ({ access_token: "a2", expires_at: "" }))
    await act(async () => fireEvent.submit(screen.getByLabelText("邮箱").closest("form") as HTMLFormElement))
    await screen.findByText("你好")
    expect(app.router.state.location.pathname).toBe("/articles")
    expect(session.accessToken()).toBe("a2")
  })
})
