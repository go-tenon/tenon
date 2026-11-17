// @vitest-environment happy-dom
// 导入、导出的按钮和对话框。
import { createSession, type OperationLike } from "@go-tenon/client"
import { AppRoot, createApp, type RouterContext } from "@go-tenon/shell"
import { createMemoryHistory, createRootRouteWithContext, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest"
import { AdminProvider } from "./provider"
import { ExportButton, ImportButton } from "./transfer"

beforeAll(() => {
  window.matchMedia ??= ((query: string) => ({
    matches: false,
    media: query,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
  })) as never
})

afterEach(() => cleanup())

async function mount(children: () => ReactNode) {
  const session = createSession({ load: async () => ({ account: "u1" }) })
  await session.signIn({ account: "u1" })
  const app = createApp({
    id: "admin-transfer",
    routeTree: createRootRouteWithContext<RouterContext>()({
      component: () => (
        <AdminProvider>
          {children()}
          <Outlet />
        </AdminProvider>
      ),
    }),
    session,
    router: { history: createMemoryHistory({ initialEntries: ["/"] }) },
  })
  render(<AppRoot app={app} />)
}

/** 进度：先推送 first，等 release 之后推送 last 并结束。 */
function controlled(first: Partial<OperationLike>, last: Partial<OperationLike>) {
  let release: () => void = () => {}
  const gate = new Promise<void>((r) => {
    release = r
  })
  const base = { id: "op", status: "running", total: 0, processed: 0, failed: 0 }
  const progress = () =>
    async function* () {
      yield { event: "progress", data: { ...base, ...first } }
      await gate
      yield { event: "progress", data: { ...base, ...last } }
    }
  return { progress, release: () => release() }
}

describe("ImportButton", () => {
  it("选择表格后上传、导入，显示进度和结果，可以下载没有导入的行；完成后调用 onCompleted", async () => {
    const p = controlled({ total: 10, processed: 4 }, { status: "completed", total: 10, processed: 10, failed: 3 })
    const failures = vi.fn(async () => {})
    const onCompleted = vi.fn()
    const upload = vi.fn(async () => "file1")
    const start = vi.fn(async () => ({ id: "imp1" }))
    await mount(() => (
      <ImportButton
        source={{ upload, start, progress: p.progress, failures }}
        title="导入文章"
        onCompleted={onCompleted}
      />
    ))
    fireEvent.click(await screen.findByRole("button", { name: /导入文章/ }))
    const input = (await screen.findByText("点击或拖动表格到这里")).closest(".ant-upload")?.querySelector("input")
    const file = new File(["title\n你好"], "articles.csv", { type: "text/csv" })
    await act(async () => {
      fireEvent.change(input as HTMLInputElement, { target: { files: [file] } })
    })
    await screen.findByText("正在导入…")
    expect(start).toHaveBeenCalledWith("file1")
    expect(screen.getByText("40%")).toBeTruthy()
    await act(async () => p.release())
    await screen.findByText("导入完成")
    expect(screen.getByText("成功 7 行，3 行没有导入")).toBeTruthy()
    expect(onCompleted).toHaveBeenCalledOnce()
    fireEvent.click(screen.getByRole("button", { name: /下载没有导入的行/ }))
    await waitFor(() => expect(failures).toHaveBeenCalledWith("imp1"))
  })

  it("上传失败时显示原因，可以重新选择文件", async () => {
    const upload = vi.fn(async () => {
      throw { status: 413, fields: [], detail: "文件太大" }
    })
    await mount(() => (
      <ImportButton source={{ upload, start: async () => ({ id: "x" }), progress: () => async function* () {} }} />
    ))
    fireEvent.click(await screen.findByRole("button", { name: /导\s*入/ }))
    const input = (await screen.findByText("点击或拖动表格到这里")).closest(".ant-upload")?.querySelector("input")
    await act(async () => {
      fireEvent.change(input as HTMLInputElement, { target: { files: [new File(["a"], "a.csv")] } })
    })
    await screen.findByText("导入失败")
    fireEvent.click(screen.getByRole("button", { name: "重新选择文件" }))
    await screen.findByText("点击或拖动表格到这里")
  })
})

describe("ExportButton", () => {
  it("点击后导出，显示进度，完成后下载", async () => {
    const p = controlled({ processed: 120 }, {
      status: "completed",
      processed: 300,
      url: "/storage/x.xlsx",
      filename: "articles.xlsx",
    } as Partial<OperationLike>)
    const start = vi.fn(async () => ({ id: "exp1" }))
    await mount(() => <ExportButton source={{ start, progress: p.progress }} title="导出文章" />)
    fireEvent.click(await screen.findByRole("button", { name: /导出文章/ }))
    await screen.findByText("已处理 120 行") // 不知道总数时显示行数
    await act(async () => p.release())
    await screen.findByText("导出完成")
    const link = screen.getByRole("link", { name: /下\s*载/ })
    expect(link.getAttribute("href")).toBe("/storage/x.xlsx")
    expect(link.getAttribute("download")).toBe("articles.xlsx")
  })
})
