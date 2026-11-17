// 演示用的资源和设置：手写的声明（实际应用中由 tenon generate 生成在 resources.ts、settings.ts 中），
// 接口是内存中的假数据。

import type { OperationLike } from "@go-tenon/client"
import { type ResourceDef, resourceModel, type SettingsDef, settingsModel } from "@go-tenon/shell"
import { QueryClient } from "@tanstack/react-query"

export const queryClient = new QueryClient()

type Customer = {
  id: string
  name: string
  level: string
  tags: string[]
  active: boolean
  credit: number
  joined: string
  last_order_at?: string
}

const levels = ["普通", "银卡", "金卡", "钻石"]
let customers: Customer[] = Array.from({ length: 46 }, (_, i) => ({
  id: `c${i + 1}`,
  name:
    ["华东分公司", "北辰贸易", "青木设计", "远山物流", "星河零售", "海岸咨询", "南风餐饮", "北极星教育"][i % 8] +
    (i >= 8 ? ` ${Math.floor(i / 8) + 1} 号` : ""),
  level: levels[i % 4],
  tags: [["批发"], ["零售", "线上"], [], ["VIP"]][i % 4],
  active: i % 5 !== 3,
  credit: 5000 + ((i * 1733) % 50000),
  joined: `2025-${String((i % 12) + 1).padStart(2, "0")}-${String((i % 27) + 1).padStart(2, "0")}`,
  last_order_at: i % 3 === 0 ? undefined : `2026-11-${String((i % 16) + 1).padStart(2, "0")}T0${i % 9}:30:00Z`,
}))

const delay = <T,>(v: T) => new Promise<T>((r) => setTimeout(() => r(v), 150))

let shopValues: Record<string, unknown> = { name: "Acme 商城", currency: "CNY", invoice: true, notice: "" }

const client = {
  customer: {
    async list(params: Record<string, unknown>) {
      let rows = customers
      if (params.q) rows = rows.filter((c) => c.name.includes(String(params.q)))
      if (params.level) rows = rows.filter((c) => c.level === params.level)
      if (params.active !== undefined) rows = rows.filter((c) => c.active === params.active)
      const sort = params.sort as string | undefined
      if (sort) {
        const desc = sort.startsWith("-")
        const field = (desc ? sort.slice(1) : sort) as keyof Customer
        rows = [...rows].sort((a, b) => (String(a[field]) > String(b[field]) ? 1 : -1) * (desc ? -1 : 1))
      }
      const page = Number(params.page ?? 1)
      const per = Number(params.per_page ?? 20)
      return delay({ items: rows.slice((page - 1) * per, page * per), total: rows.length, page, per_page: per })
    },
    get: (id: string) => delay(customers.find((c) => c.id === id)),
    async create(body: Customer) {
      const c = { ...body, id: `c${customers.length + 1}`, joined: "2026-11-17" }
      customers = [c, ...customers]
      return delay(c)
    },
    async update(id: string, body: Partial<Customer>) {
      customers = customers.map((c) => (c.id === id ? { ...c, ...body } : c))
      return delay(customers.find((c) => c.id === id))
    },
    async delete(id: string) {
      customers = customers.filter((c) => c.id !== id)
      return delay(undefined)
    },
    async disable(id: string) {
      customers = customers.map((c) => (c.id === id ? { ...c, active: false } : c))
      return delay(undefined)
    },
  },
  shop: {
    settingsGet: () => delay(shopValues),
    settingsUpdate(body: Record<string, unknown>) {
      shopValues = body
      return delay(body)
    },
  },
}

const customerDef: ResourceDef = {
  name: "customer",
  key: "id",
  operations: {
    list: ["customer", "list"],
    get: ["customer", "get"],
    create: ["customer", "create"],
    update: ["customer", "update"],
    delete: ["customer", "delete"],
  },
  permissions: { list: [], get: [], create: [], update: [], delete: [] },
  columns: [
    { name: "name", type: "string", required: true },
    { name: "level", type: "string", enum: "Level", required: true, sortable: true },
    { name: "tags", type: "array", items: "string" },
    { name: "active", type: "boolean", required: true },
    { name: "credit", type: "integer", sortable: true },
    { name: "last_order_at", type: "string", format: "date-time", sortable: true },
  ],
  search: "q",
  filters: [
    { name: "level", type: "string", enum: "Level" },
    { name: "active", type: "boolean" },
  ],
  form: [
    { name: "name", type: "string", required: true },
    { name: "level", type: "string", enum: "Level", required: true },
    { name: "tags", type: "array", items: "string" },
    { name: "credit", type: "integer" },
    { name: "joined", type: "string", format: "date" },
    { name: "active", type: "boolean" },
  ],
  actions: [{ name: "disable", operation: ["customer", "disable"], permissions: [], confirm: true, danger: true }],
  labels: {
    "zh-CN": {
      title: "客户",
      fields: {
        name: "名称",
        level: "等级",
        tags: "标签",
        active: "启用",
        credit: "信用额度",
        last_order_at: "最近下单",
        joined: "加入日期",
      },
      actions: { disable: "停用" },
    },
  },
}

const schemas = {
  customer: {
    list: {
      query: {
        type: "object",
        properties: {
          q: { type: "string" },
          level: { type: "string", enum: levels },
          active: { type: "boolean" },
          sort: { type: "string" },
          page: { type: "integer", minimum: 1, default: 1 },
          per_page: { type: "integer", minimum: 1, maximum: 100, default: 20 },
        },
      },
    },
    create: {
      body: {
        type: "object",
        required: ["name", "level"],
        properties: {
          name: { type: "string", minLength: 1, maxLength: 40 },
          level: { type: "string", enum: levels },
          tags: { type: "array", items: { type: "string" } },
          credit: { type: "integer", minimum: 0 },
          joined: { type: "string", format: "date" },
          active: { type: "boolean" },
        },
      },
    },
  },
  shop: {
    settingsUpdate: {
      body: {
        type: "object",
        required: ["name"],
        properties: {
          name: { type: "string", minLength: 1 },
          currency: { type: "string", enum: ["CNY", "USD", "EUR"] },
          invoice: { type: "boolean" },
          notice: { type: "string", maxLength: 200 },
        },
      },
    },
  },
}

const kit = {
  client,
  queries: {},
  schemas,
  enums: { Level: { values: levels, labels: { "zh-CN": {} } } },
  messages: {
    "zh-CN": {
      "validation.required": "必填",
      "validation.filled": "不能为空",
      "validation.min_length": "至少 {min} 个字",
      "validation.minimum": "不能小于 {min}",
    },
  },
  language: "zh-CN",
}

export const customersModel = resourceModel(customerDef, kit, queryClient)

const shopDef: SettingsDef = {
  name: "shop",
  operations: { get: ["shop", "settingsGet"], update: ["shop", "settingsUpdate"] },
  permissions: { get: [], update: [] },
  form: [
    { name: "name", type: "string", required: true },
    { name: "currency", type: "string", values: ["CNY", "USD", "EUR"] },
    { name: "invoice", type: "boolean" },
    { name: "notice", type: "string" },
  ],
  labels: {
    "zh-CN": {
      title: "店铺设置",
      fields: { name: "店铺名称", currency: "结算币种", invoice: "开具发票", notice: "公告" },
    },
  },
}

export const shopSettings = settingsModel(shopDef, kit, queryClient)

/** 演示的导入：上传和处理都是假的，3 秒内完成，有 2 行没有导入。 */
export const customerImport = {
  title: "导入客户",
  upload: async (_: File, o: { onProgress?: (r: number) => void }) => {
    for (const r of [0.3, 0.7, 1]) {
      o.onProgress?.(r)
      await delay(null)
    }
    return "file1"
  },
  start: async () => ({ id: `imp${Date.now()}` }),
  progress: (id: string) =>
    async function* (): AsyncIterable<{ event: string; data: OperationLike }> {
      for (let i = 0; i <= 10; i++) {
        yield {
          event: "progress",
          data: {
            id,
            status: i === 10 ? "completed" : "running",
            total: 120,
            processed: i * 12,
            failed: i === 10 ? 2 : 0,
          },
        }
        await new Promise((r) => setTimeout(r, 300))
      }
    },
  failures: async () => {},
}

/** 演示的导出。 */
export const customerExport = {
  title: "导出客户",
  start: async () => ({ id: `exp${Date.now()}` }),
  progress: (id: string) =>
    async function* (): AsyncIterable<{ event: string; data: OperationLike }> {
      for (let i = 0; i <= 5; i++) {
        yield {
          event: "progress",
          data: {
            id,
            status: i === 5 ? "completed" : "running",
            total: 0,
            processed: i * 40,
            failed: 0,
            ...(i === 5 ? { url: "#", filename: "customers.xlsx" } : {}),
          } as OperationLike,
        }
        await new Promise((r) => setTimeout(r, 300))
      }
    },
}

/** 演示的队列运维接口：api.queue 的形状。 */
export const queueApi = (() => {
  let failed = Array.from({ length: 23 }, (_, i) => ({
    id: `job-${i + 1}`,
    topic: ["mail.send", "mail.send", "report.daily", "webhook.deliver"][i % 4],
    attempts: [10, 10, 3, 8][i % 4],
    error: [
      "smtp: dial tcp 10.0.3.12:587: connect: connection refused",
      "smtp: 421 4.7.0 Try again later, closing connection",
      "pdf: render report: gotenberg returned 503",
      "webhook: POST https://hooks.example.com/orders: context deadline exceeded",
    ][i % 4],
    failed_at: new Date(Date.now() - i * 37 * 60_000).toISOString(),
  }))
  const paused = new Set(["report.daily"])
  return {
    status: async () =>
      delay({
        slot: {
          name: "tenon_queue_3f2a9c1e5b7d4e60",
          exists: true,
          active: true,
          active_pid: 48213,
          wal_status: "reserved",
          lag: 12_700,
        },
        jetstream: { stream: "jobs", exists: true, messages: 28, bytes: 7_577, replicas: 1, deferred: 5 },
        topics: ["mail.send", "report.daily", "sms.send", "webhook.deliver"].map((topic, i) => ({
          topic,
          state: paused.has(topic)
            ? "paused"
            : topic === "sms.send"
              ? "no_handler"
              : topic === "webhook.deliver"
                ? "stalled"
                : "ok",
          pending: [9, 7, 4, 12][i],
          in_flight: [3, 0, 0, 0][i],
          redelivered: [0, 0, 0, 2][i],
          waiting: [2, 0, 0, 0][i],
          failed: failed.filter((j) => j.topic === topic).length,
          last_delivered: i === 0 ? new Date().toISOString() : undefined,
          paused_until: paused.has(topic) ? new Date(Date.now() + 40 * 60_000).toISOString() : undefined,
        })),
        failed: failed.length,
        links: [{ name: "Grafana", url: "https://grafana.example.com" }],
      }),
    failedList: async (input: { topic?: string; page?: number; per_page?: number } = {}) => {
      const rows = input.topic ? failed.filter((j) => j.topic === input.topic) : failed
      const page = input.page ?? 1
      const per = input.per_page ?? 20
      const counts = new Map<string, number>()
      for (const j of failed) counts.set(j.topic, (counts.get(j.topic) ?? 0) + 1)
      return delay({
        items: rows.slice((page - 1) * per, page * per),
        total: rows.length,
        topics: [...counts].map(([topic, count]) => ({ topic, count })).sort((a, b) => b.count - a.count),
      })
    },
    failedGet: async (id: string) => {
      const j = failed.find((x) => x.id === id) ?? failed[0]
      return delay({
        ...j,
        error: `${j.error}\n\ngoroutine 81 [running]:\nmain.(*Mailer).Send(...)\n\t/app/internal/mail/mail.go:142\nqueue.(*Worker).handle(...)\n\t/app/vendor/tenon/queue/worker.go:388`,
        metadata: {
          request_id: "01JD5Q8M2V9ZJ1H3",
          traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
        },
        payload: { to: "ming@example.com", template: "order_shipped", data: { order: 10482 } },
      })
    },
    failedRetry: async (id: string) => {
      failed = failed.filter((j) => j.id !== id)
      return delay(undefined)
    },
    failedDelete: async (id: string) => {
      failed = failed.filter((j) => j.id !== id)
      return delay(undefined)
    },
    failedRetryAll: async (input: { topic?: string } = {}) => {
      const before = failed.length
      failed = input.topic ? failed.filter((j) => j.topic !== input.topic) : []
      return delay({ count: before - failed.length })
    },
    failedDeleteAll: async (input: { topic?: string } = {}) => {
      const before = failed.length
      failed = input.topic ? failed.filter((j) => j.topic !== input.topic) : []
      return delay({ count: before - failed.length })
    },
    topicPause: async (topic: string) => delay(void paused.add(topic)),
    topicResume: async (topic: string) => delay(void paused.delete(topic)),
  }
})()
