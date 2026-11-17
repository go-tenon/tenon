// @vitest-environment happy-dom

import { createRootRouteWithContext } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { type ScheduleApi, SchedulePanel, type ScheduleRunData } from "./schedule-panel"
import type { RouterContext } from "./shell"
import { renderApp } from "./testing"

afterEach(cleanup)

function fakeApi(options: { missed?: boolean } = {}) {
  const runs: ScheduleRunData[] = [
    {
      id: "r1",
      task: "report",
      status: "failed",
      manual: false,
      started_at: "2026-11-17T09:00:00Z",
      duration_ms: 4200,
      error: "pdf: unavailable\n  at render (report.go:42)",
    },
    {
      id: "r2",
      task: "cleanup",
      status: "succeeded",
      manual: true,
      started_at: "2026-11-17T08:00:00Z",
      duration_ms: 320,
      error: "",
    },
    {
      id: "r3",
      task: "report",
      status: "skipped",
      manual: false,
      started_at: "2026-11-17T07:00:00Z",
      duration_ms: 0,
      error: "overlap",
    },
  ]
  const api: ScheduleApi = {
    list: vi.fn(async () => ({
      items: [
        {
          name: "report",
          description: "生成日报",
          expr: "@hourly",
          next_run: "2026-11-17T10:00:00Z",
          running: false,
          last_status: "failed" as const,
          last_run_at: "2026-11-17T09:00:00Z",
          last_duration_ms: 4200,
          last_error: "pdf: unavailable",
          missed: !!options.missed,
        },
        {
          name: "cleanup",
          description: "",
          expr: "@daily",
          next_run: "2026-11-18T00:00:00Z",
          running: true,
          missed: false,
        },
        {
          name: "digest",
          description: "",
          expr: "0 9 * * 1",
          next_run: "2026-11-23T01:00:00Z",
          running: false,
          missed: false,
        },
      ],
    })),
    run: vi.fn(async () => undefined),
    runs: vi.fn(async (input = {}) => {
      const rows = runs.filter(
        (r) => (!input.task || r.task === input.task) && (!input.status || r.status === input.status),
      )
      return { items: rows, total: rows.length }
    }),
  }
  return api
}

async function mount(api: ScheduleApi) {
  const root = createRootRouteWithContext<RouterContext>()({ component: () => <SchedulePanel api={api} /> })
  const app = await renderApp(root)
  await screen.findByText("生成日报")
  return app
}

const row = (text: string) => within(screen.getByRole("tabpanel")).getAllByText(text)[0].closest("tr") as HTMLElement

describe("SchedulePanel", () => {
  it("任务列表显示表达式、上一次的结果和状态", async () => {
    await mount(fakeApi())
    expect(screen.queryByRole("alert")).toBeNull()
    expect(within(row("report")).getByText("@hourly")).toBeTruthy()
    expect(within(row("report")).getByText("失败")).toBeTruthy()
    expect(within(row("report")).getByText("pdf: unavailable")).toBeTruthy()
    expect(within(row("cleanup")).getByText("执行中")).toBeTruthy()
    expect((within(row("cleanup")).getByRole("button", { name: "立即执行" }) as HTMLButtonElement).disabled).toBe(true)
    expect(within(row("digest")).getByText("还没执行过")).toBeTruthy()
  })

  it("错过触发的任务在标签上面提示", async () => {
    await mount(fakeApi({ missed: true }))
    const alert = await screen.findByRole("alert")
    expect(alert.textContent).toContain("1 个任务错过了上一次执行")
    expect(within(row("report")).getByText("错过")).toBeTruthy()
  })

  it("立即执行先确认，交给后台后重新读取", async () => {
    const api = fakeApi()
    await mount(api)
    await act(async () => fireEvent.click(within(row("report")).getByRole("button", { name: "立即执行" })))
    await act(async () => fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "执行" })))
    await waitFor(() => expect(api.run).toHaveBeenCalledWith("report"))
    await waitFor(() => expect(api.list).toHaveBeenCalledTimes(2))
  })

  it("点任务名打开它的执行记录；按结果筛选；点失败的错误看完整的错误", async () => {
    const api = fakeApi()
    const app = await mount(api)
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "report" })))
    expect(app.router.state.location.search).toEqual({ tab: "runs" })
    await waitFor(() =>
      expect(api.runs).toHaveBeenLastCalledWith({ task: "report", status: undefined, page: 1, per_page: 20 }),
    )
    const panel = screen.getByRole("tabpanel")
    await within(panel).findByText("上一次还没结束") // 跳过的原因换成说明
    expect(within(panel.querySelector("tbody") as HTMLElement).queryByText("cleanup")).toBeNull()

    await act(async () => fireEvent.click(within(panel).getByRole("radio", { name: "失败" })))
    await waitFor(() =>
      expect(api.runs).toHaveBeenLastCalledWith({ task: "report", status: "failed", page: 1, per_page: 20 }),
    )
    await waitFor(() => expect(within(panel).queryByText("上一次还没结束")).toBeNull())

    await act(async () => fireEvent.click(within(panel).getByRole("button", { name: /pdf: unavailable/ })))
    const dialog = await screen.findByRole("dialog")
    expect(dialog.textContent).toContain("report.go:42")
    expect(dialog.textContent).toContain("4.2 秒")
  })
})
