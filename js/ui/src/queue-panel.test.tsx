// @vitest-environment happy-dom

import { createRootRouteWithContext } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { formatBytes, type QueueApi, type QueueFailedJobData, QueuePanel } from "./queue-panel"
import type { RouterContext } from "./shell"
import { renderApp } from "./testing"

afterEach(cleanup)

function fakeApi(options: { admin?: boolean; slotExists?: boolean } = {}) {
  let failed: QueueFailedJobData[] = [
    {
      id: "j1",
      topic: "mail.send",
      attempts: 10,
      error: "smtp: connection refused",
      failed_at: "2026-11-17T09:00:00Z",
    },
    { id: "j2", topic: "mail.send", attempts: 10, error: "smtp: timeout", failed_at: "2026-11-17T08:00:00Z" },
    { id: "j3", topic: "report.daily", attempts: 3, error: "pdf: unavailable", failed_at: "2026-11-17T07:00:00Z" },
  ]
  const paused = new Set<string>(["report.daily"])
  const counts = () => {
    const m = new Map<string, number>()
    for (const j of failed) m.set(j.topic, (m.get(j.topic) ?? 0) + 1)
    return [...m].map(([topic, count]) => ({ topic, count }))
  }
  const api: QueueApi = {
    status: vi.fn(async () => ({
      slot: {
        name: "tenon_queue_abc",
        exists: options.slotExists ?? true,
        active: true,
        active_pid: 4821,
        wal_status: "reserved",
        lag: 12700,
      },
      jetstream: { stream: "jobs", exists: true, messages: 28, bytes: 7577, replicas: 1, deferred: 5 },
      topics: ["mail.send", "report.daily", "sms.send"].map((topic) => ({
        topic,
        state: paused.has(topic) ? "paused" : topic === "sms.send" ? "no_handler" : "ok",
        pending: 9,
        in_flight: 3,
        redelivered: 0,
        waiting: 1,
        failed: failed.filter((j) => j.topic === topic).length,
        paused_until: paused.has(topic) ? "2026-11-17T10:30:00Z" : undefined,
      })),
      failed: failed.length,
    })),
    failedList: vi.fn(async (input = {}) => {
      const rows = input.topic ? failed.filter((j) => j.topic === input.topic) : failed
      return { items: rows, total: rows.length, topics: counts() }
    }),
    failedGet: vi.fn(async (id: string) => ({
      ...(failed.find((j) => j.id === id) as QueueFailedJobData),
      error: "smtp: connection refused\n  at send (mail.go:42)",
      metadata: { request_id: "REQ-42" },
      payload: { to: "ming@example.com" },
    })),
    failedRetry: vi.fn(async (id: string) => {
      failed = failed.filter((j) => j.id !== id)
    }),
    failedDelete: vi.fn(async (id: string) => {
      failed = failed.filter((j) => j.id !== id)
    }),
    failedRetryAll: vi.fn(async (input = {}) => {
      const before = failed.length
      failed = failed.filter((j) => input.topic && j.topic !== input.topic)
      return { count: before - failed.length }
    }),
    failedDeleteAll: vi.fn(async () => ({ count: 0 })),
  }
  if (options.admin !== false) {
    api.topicPause = vi.fn(async (t: string) => void paused.add(t))
    api.topicResume = vi.fn(async (t: string) => void paused.delete(t))
  }
  return api
}

async function mount(api: QueueApi) {
  const root = createRootRouteWithContext<RouterContext>()({
    component: () => <QueuePanel api={api} refreshInterval={60_000} />,
  })
  const app = await renderApp(root)
  await screen.findByText("mail.send")
  return app
}

/** 切到一个页内标签。 */
async function openTab(name: string) {
  await act(async () => fireEvent.click(screen.getByRole("tab", { name: new RegExp(name) })))
}

const topicRow = (topic: string) =>
  within(screen.getByRole("tabpanel"))
    .getAllByText(topic)
    .map((e) => e.closest("tr") as HTMLElement)[0]

describe("QueuePanel", () => {
  it("分成任务类型、失败任务、系统状态三个页内标签；当前标签记在地址中", async () => {
    const app = await mount(fakeApi())
    expect(screen.getByRole("tab", { name: "任务类型" }).getAttribute("aria-selected")).toBe("true")
    expect(screen.getByRole("tab", { name: /失败任务/ }).textContent).toContain("3")
    expect(screen.queryByRole("alert")).toBeNull()
    expect(screen.queryByText("tenon_queue_abc")).toBeNull() // 没打开过的标签不渲染
    expect(within(topicRow("sms.send")).getByText("没有处理程序")).toBeTruthy()
    expect(within(topicRow("sms.send")).getByText(/没有 Worker 处理这类任务/)).toBeTruthy()

    await openTab("系统状态")
    expect(app.router.state.location.search).toEqual({ tab: "system" })
    expect(screen.getByText("tenon_queue_abc")).toBeTruthy()
    expect(screen.getByText("12.4 KiB")).toBeTruthy()
    expect(screen.getByText("4821")).toBeTruthy()

    await openTab("任务类型")
    expect(app.router.state.location.search).toEqual({})
  })

  it("复制槽不存在时显示醒目的提示", async () => {
    await mount(fakeApi({ slotExists: false }))
    const alert = await screen.findByRole("alert")
    expect(within(alert).getByText("任务转发已停止")).toBeTruthy()
    expect(alert.textContent).toContain("queue slot recreate")
  })

  it("按状态显示暂停和恢复；暂停先确认", async () => {
    const api = fakeApi()
    await mount(api)
    expect(within(topicRow("sms.send")).queryByRole("button")).toBeNull()
    await act(async () => fireEvent.click(within(topicRow("report.daily")).getByRole("button", { name: "恢复" })))
    await waitFor(() => expect(api.topicResume).toHaveBeenCalledWith("report.daily"))
    await act(async () => fireEvent.click(within(topicRow("mail.send")).getByRole("button", { name: "暂停" })))
    await screen.findByText("暂停 mail.send 一小时？")
    await act(async () => fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "暂停" })))
    await waitFor(() => expect(api.topicPause).toHaveBeenCalledWith("mail.send"))
  })

  it("没有设置 Admin 时没有暂停和恢复", async () => {
    await mount(fakeApi({ admin: false }))
    expect(within(topicRow("mail.send")).queryByRole("button", { name: "暂停" })).toBeNull()
  })

  it("失败任务按任务类型筛选；单条重试先确认；按钮写明一次处理的条数", async () => {
    const api = fakeApi()
    await mount(api)
    await openTab("失败任务")
    await act(async () => fireEvent.click(await screen.findByRole("radio", { name: /report\.daily/ })))
    await waitFor(() =>
      expect(api.failedList).toHaveBeenLastCalledWith({ topic: "report.daily", page: 1, per_page: 20 }),
    )
    await waitFor(() => expect(screen.queryByText("smtp: timeout")).toBeNull())
    expect(screen.getByRole("button", { name: "重试这 1 个" })).toBeTruthy()

    await act(async () => fireEvent.click(screen.getByRole("radio", { name: /全部/ })))
    const row = (await screen.findByText("smtp: timeout")).closest("tr") as HTMLElement
    await act(async () => fireEvent.click(within(row).getByRole("button", { name: "重试" })))
    await screen.findByText("重试这个任务？")
    await act(async () => fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "重试" })))
    await waitFor(() => expect(api.failedRetry).toHaveBeenCalledWith("j2"))

    await act(async () => fireEvent.click(await screen.findByRole("button", { name: "重试全部 2 个" })))
    await screen.findByText("重试全部 2 个失败任务？")
    await act(async () => fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "重试" })))
    expect(await screen.findByText("已重新发布 2 个")).toBeTruthy()
    expect(api.failedRetryAll).toHaveBeenCalledWith({ topic: undefined })
  })

  it("切回看过的筛选时先显示缓存的数据，同时重新请求服务器；失败列表定时刷新", async () => {
    const api = fakeApi()
    const root = createRootRouteWithContext<RouterContext>()({
      component: () => <QueuePanel api={api} refreshInterval={200} />,
    })
    await renderApp(root, { path: "/?tab=failed" })
    await screen.findByText("smtp: timeout")
    const calls = () => (api.failedList as ReturnType<typeof vi.fn>).mock.calls.map((c) => c[0]?.topic ?? "全部")
    await act(async () => fireEvent.click(await screen.findByRole("radio", { name: /report\.daily/ })))
    await waitFor(() => expect(screen.queryByText("smtp: timeout")).toBeNull())
    const before = calls().filter((t) => t === "全部").length
    await act(async () => fireEvent.click(screen.getByRole("radio", { name: /全部/ })))
    expect(screen.getByText("smtp: timeout")).toBeTruthy() // 缓存的数据立即显示
    await waitFor(() => expect(calls().filter((t) => t === "全部").length).toBeGreaterThan(before)) // 同时重新请求
    const n = calls().length
    await waitFor(() => expect(calls().length).toBeGreaterThan(n), { timeout: 1000 }) // 定时刷新
  })

  it("勾选几条一起删除：先确认，逐条调用接口", async () => {
    const api = fakeApi()
    await mount(api)
    await openTab("失败任务")
    for (const text of ["smtp: timeout", "pdf: unavailable"]) {
      const row = (await screen.findByText(text)).closest("tr") as HTMLElement
      await act(async () => fireEvent.click(within(row).getByRole("checkbox")))
    }
    expect(screen.getByText("已选 2 项")).toBeTruthy()
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "删除" })))
    await screen.findByText("删除选中的 2 个任务？")
    await act(async () => fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "删除" })))
    await waitFor(() => expect(api.failedDelete).toHaveBeenCalledTimes(2))
    expect(api.failedDelete).toHaveBeenCalledWith("j2")
    expect(api.failedDelete).toHaveBeenCalledWith("j3")
  })

  it("点击一条失败任务，在对话框中显示完整的错误、元数据和任务内容", async () => {
    const api = fakeApi()
    await mount(api)
    await openTab("失败任务")
    await act(async () => fireEvent.click(await screen.findByText("pdf: unavailable")))
    const dialog = await screen.findByRole("dialog")
    expect(await within(dialog).findByText(/at send \(mail\.go:42\)/)).toBeTruthy()
    expect(within(dialog).getByText("REQ-42")).toBeTruthy()
    expect(within(dialog).getByText(/ming@example\.com/)).toBeTruthy()
    expect(within(dialog).getByText("任务内容")).toBeTruthy()
    expect(api.failedGet).toHaveBeenCalledWith("j3")
  })
})

describe("formatBytes", () => {
  it("按 1024 换算", () => {
    expect(formatBytes(512)).toBe("512 B")
    expect(formatBytes(12700)).toBe("12.4 KiB")
    expect(formatBytes(3 * 1024 * 1024)).toBe("3.0 MiB")
  })
})
