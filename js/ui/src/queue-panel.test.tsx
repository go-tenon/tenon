// @vitest-environment happy-dom
import type { RouterContext } from "@go-tenon/shell"
import { createRootRouteWithContext } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { formatBytes, type QueueApi, type QueueFailedJobData, QueuePanel } from "./queue-panel"
import { renderApp } from "./testing"

afterEach(cleanup)

function fakeApi(options: { admin?: boolean } = {}) {
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
        exists: true,
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
      links: [{ name: "Grafana", url: "https://grafana.example.com/d/queue" }],
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
  await renderApp(root)
  await screen.findByText("tenon_queue_abc")
}

const topicRow = (topic: string) =>
  within(screen.getByRole("heading", { name: "Topic" }).parentElement as HTMLElement)
    .getAllByText(topic)
    .map((e) => e.closest("tr") as HTMLElement)[0]

describe("QueuePanel", () => {
  it("概览：复制槽、JetStream、失败任务数和外部链接", async () => {
    await mount(fakeApi())
    expect(screen.getByText("转发中")).toBeTruthy()
    expect(screen.getByText("12.4 KiB")).toBeTruthy()
    expect(screen.getByText("4821")).toBeTruthy()
    expect(screen.getByText("定时投递")).toBeTruthy()
    expect(screen.getByRole("link", { name: /Grafana/ }).getAttribute("href")).toBe(
      "https://grafana.example.com/d/queue",
    )
    expect(within(topicRow("sms.send")).getByText("没有处理函数")).toBeTruthy()
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

  it("失败任务按 topic 筛选；单条重试先确认；全部重试提示条数", async () => {
    const api = fakeApi()
    await mount(api)
    const tabs = screen.getByRole("tablist", { name: "按 topic 筛选" })
    await act(async () => fireEvent.click(within(tabs).getByRole("tab", { name: /report\.daily/ })))
    await waitFor(() =>
      expect(api.failedList).toHaveBeenLastCalledWith({ topic: "report.daily", page: 1, per_page: 20 }),
    )
    await waitFor(() => expect(screen.queryByText("smtp: timeout")).toBeNull())

    await act(async () => fireEvent.click(within(tabs).getByRole("tab", { name: /全部/ })))
    const row = (await screen.findByText("smtp: timeout")).closest("tr") as HTMLElement
    await act(async () => fireEvent.click(within(row).getByRole("button", { name: "重试" })))
    await screen.findByText("重试这条失败任务？")
    await act(async () => fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "重试" })))
    await waitFor(() => expect(api.failedRetry).toHaveBeenCalledWith("j2"))

    await act(async () => fireEvent.click(screen.getByRole("button", { name: "全部重试" })))
    await screen.findByText("重试全部失败任务？")
    await act(async () => fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "全部重试" })))
    expect(await screen.findByText("已重新发布 2 条")).toBeTruthy()
    expect(api.failedRetryAll).toHaveBeenCalledWith({ topic: undefined })
  })

  it("点击一条失败任务显示完整的错误、元数据和 payload", async () => {
    const api = fakeApi()
    await mount(api)
    await act(async () => fireEvent.click(await screen.findByText("pdf: unavailable")))
    const drawer = await screen.findByRole("dialog")
    expect(await within(drawer).findByText(/at send \(mail\.go:42\)/)).toBeTruthy()
    expect(within(drawer).getByText("REQ-42")).toBeTruthy()
    expect(within(drawer).getByText(/ming@example\.com/)).toBeTruthy()
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
