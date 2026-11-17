// @vitest-environment happy-dom
// 导入、导出的按钮和对话框。
import { createSession, type OperationLike } from "@go-tenon/client"
import { AppRoot, createApp, type RouterContext } from "@go-tenon/shell"
import { createMemoryHistory, createRootRouteWithContext, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { ExportButton, ImportButton } from "@/components/tenon/transfer"

afterEach(() => cleanup())

async function mount(children: () => ReactNode) {
  const session = createSession({ load: async () => ({ account: "u1" }) })
  await session.signIn({ account: "u1" })
  const app = createApp({
    id: "registry-transfer",
    routeTree: createRootRouteWithContext<RouterContext>()({
      component: () => (
        <>
          {children()}
          <Outlet />
        </>
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
    const start = vi.fn(async () => ({ id: "imp1" }))
    await mount(() => (
      <ImportButton
        source={{ upload: async () => "file1", start, progress: p.progress, failures }}
        title="导入文章"
        onCompleted={onCompleted}
      />
    ))
    fireEvent.click(await screen.findByRole("button", { name: "导入文章" }))
    const input = (await screen.findByText("点击或拖动表格到这里")).closest("label")?.querySelector("input")
    await act(async () => {
      fireEvent.change(input as HTMLInputElement, { target: { files: [new File(["a"], "articles.csv")] } })
    })
    await screen.findByText("正在导入…")
    expect(start).toHaveBeenCalledWith("file1")
    expect(screen.getByText("4 / 10 行")).toBeTruthy()
    await act(async () => p.release())
    await screen.findByText("导入完成")
    expect(screen.getByText("成功 7 行，3 行没有导入")).toBeTruthy()
    expect(onCompleted).toHaveBeenCalledOnce()
    fireEvent.click(screen.getByRole("button", { name: "下载没有导入的行" }))
    await waitFor(() => expect(failures).toHaveBeenCalledWith("imp1"))
  })

  it("导入失败时显示后台操作的错误码，可以重新选择文件", async () => {
    await mount(() => (
      <ImportButton
        source={{
          upload: async () => "f",
          start: async () => ({ id: "imp2" }),
          progress: () =>
            async function* () {
              yield {
                event: "progress",
                data: {
                  id: "imp2",
                  status: "failed",
                  total: 0,
                  processed: 0,
                  failed: 0,
                  error: "sheet.missing_columns",
                },
              }
            },
        }}
      />
    ))
    fireEvent.click(await screen.findByRole("button", { name: "导入" }))
    const input = (await screen.findByText("点击或拖动表格到这里")).closest("label")?.querySelector("input")
    await act(async () => {
      fireEvent.change(input as HTMLInputElement, { target: { files: [new File(["a"], "a.csv")] } })
    })
    await screen.findByText("导入失败")
    expect(screen.getByText("处理失败（sheet.missing_columns）")).toBeTruthy()
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
    await mount(() => <ExportButton source={{ start: async () => ({ id: "exp1" }), progress: p.progress }} />)
    fireEvent.click(await screen.findByRole("button", { name: "导出" }))
    await screen.findByText("已处理 120 行")
    await act(async () => p.release())
    await screen.findByText("导出完成")
    const link = screen.getByRole("link", { name: "下载" })
    expect(link.getAttribute("href")).toBe("/storage/x.xlsx")
    expect(link.getAttribute("download")).toBe("articles.xlsx")
  })
})
