// 演示用的资源和设置：手写的声明（实际应用中由 tenon generate 生成在 resources.ts、settings.ts 中），
// 接口是内存中的假数据。

import type { OperationLike } from "@go-tenon/client"
import {
  type ActivityItem,
  type ResourceDef,
  resourceModel,
  type ScheduleApi,
  type ScheduleRunData,
  type ScheduleTaskData,
  type SettingsDef,
  settingsModel,
} from "@go-tenon/ui"
import { QueryClient } from "@tanstack/react-query"

export const queryClient = new QueryClient()

type CustomerGroup = {
  id: string
  name: string
}

const customerGroups: CustomerGroup[] = [
  { id: "g1", name: "华东大区" },
  { id: "g2", name: "华北大区" },
  { id: "g3", name: "华南大区" },
  { id: "g4", name: "海外渠道" },
]

type Customer = {
  id: string
  name: string
  group_id: string
  group_name: string
  level: string
  tags: string[]
  active: boolean
  credit: number
  joined: string
  last_order_at?: string
  api_key: string
  note: string // 备注：有空的、短的和很长的，演示文字列的截断和弹出全文
}

const levels = ["普通", "银卡", "金卡", "钻石"]
let customers: Customer[] = Array.from({ length: 46 }, (_, i) => {
  const group = customerGroups[i % customerGroups.length]
  return {
    id: `c${i + 1}`,
    name:
      ["华东分公司", "北辰贸易", "青木设计", "远山物流", "星河零售", "海岸咨询", "南风餐饮", "北极星教育"][i % 8] +
      (i >= 8 ? ` ${Math.floor(i / 8) + 1} 号` : ""),
    group_id: group.id,
    group_name: group.name,
    level: levels[i % 4],
    tags: [["批发"], ["零售", "线上"], [], ["VIP"]][i % 4],
    active: i % 5 !== 3,
    credit: 5000 + ((i * 1733) % 50000),
    joined: `2025-${String((i % 12) + 1).padStart(2, "0")}-${String((i % 27) + 1).padStart(2, "0")}`,
    last_order_at: i % 3 === 0 ? undefined : `2026-11-${String((i % 16) + 1).padStart(2, "0")}T0${i % 9}:30:00Z`,
    api_key: i % 4 === 2 ? "" : `sk-live-${(i * 7919).toString(36).padStart(6, "0")}${i}abcd`,
    note: [
      "",
      "月结客户",
      "每月 5 日前对账，发票抬头与合同一致；大宗订单需提前三个工作日通知仓库备货，节假日前后的订单按华东仓的发货时间表执行。",
      "联系人：王经理，电话见合同附件",
    ][i % 4],
  }
})

const delay = <T,>(v: T) => new Promise<T>((r) => setTimeout(() => r(v), 150))

let shopValues: Record<string, unknown> = { name: "Acme 商城", currency: "CNY", invoice: true, notice: "" }
let notifyValues: Record<string, unknown> = { order_email: true, refund_email: false, sender: "Acme 商城" }

const client = {
  group: {
    list: () => delay({ items: customerGroups, total: customerGroups.length, page: 1, per_page: 20 }),
  },
  customer: {
    async list(params: Record<string, unknown>) {
      let rows = customers
      if (params.name) rows = rows.filter((c) => c.name.includes(String(params.name)))
      if (params.group_id) rows = rows.filter((c) => c.group_id === params.group_id)
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
      const group = customerGroups.find((g) => g.id === body.group_id) ?? customerGroups[0]
      const c = {
        ...body,
        id: `c${customers.length + 1}`,
        group_id: group.id,
        group_name: group.name,
        joined: "2026-11-17",
      }
      customers = [c, ...customers]
      return delay(c)
    },
    async update(id: string, body: Partial<Customer>) {
      const group = body.group_id ? customerGroups.find((g) => g.id === body.group_id) : undefined
      customers = customers.map((c) =>
        c.id === id ? { ...c, ...body, ...(group ? { group_name: group.name } : {}) } : c,
      )
      return delay(customers.find((c) => c.id === id))
    },
    async delete(id: string) {
      customers = customers.filter((c) => c.id !== id)
      return delay(undefined)
    },
    async enable(id: string) {
      customers = customers.map((c) => (c.id === id ? { ...c, active: true } : c))
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
  notify: {
    settingsGet: () => delay(notifyValues),
    settingsUpdate(body: Record<string, unknown>) {
      notifyValues = body
      return delay(body)
    },
  },
}

const groupDef: ResourceDef = {
  name: "group",
  key: "id",
  operations: { list: ["group", "list"] },
  permissions: { list: [] },
  columns: [{ name: "name", type: "string", required: true }],
  filters: [],
  createForm: [],
  updateForm: [],
  actions: [],
  labels: { "zh-CN": { title: "分组", fields: { name: "名称" }, actions: {} } },
}

// 客户的新建和修改填写同样的字段。
const customerForm = [
  { name: "name", type: "string", required: true },
  { name: "group_id", type: "string", ref: "group", required: true },
  { name: "level", type: "string", enum: "Level", required: true },
  { name: "tags", type: "array", items: "string" },
  { name: "credit", type: "integer" },
  { name: "joined", type: "string", format: "date" },
  { name: "active", type: "boolean" },
  { name: "api_key", type: "string", format: "password" },
] as const

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
    { name: "group_name", type: "string" },
    { name: "note", type: "string" },
    { name: "level", type: "string", enum: "Level", required: true, sortable: true },
    { name: "tags", type: "array", items: "string" },
    { name: "active", type: "boolean", required: true },
    { name: "credit", type: "integer", sortable: true },
    { name: "last_order_at", type: "string", format: "date-time", sortable: true },
    { name: "api_key", type: "string", format: "password" },
  ],
  filters: [
    { name: "name", type: "string" },
    { name: "group_id", type: "string", ref: "group" },
    { name: "level", type: "string", enum: "Level" },
    { name: "active", type: "boolean" },
  ],
  createForm: customerForm,
  updateForm: customerForm,
  // 停用只在启用的客户上显示，启用只在停用的客户上显示。
  actions: [
    {
      name: "disable",
      operation: ["customer", "disable"],
      permissions: [],
      confirm: true,
      danger: true,
      when: { active: ["true"] },
    },
    {
      name: "enable",
      operation: ["customer", "enable"],
      permissions: [],
      confirm: true,
      danger: false,
      when: { active: ["false"] },
    },
  ],
  // 只有停用的客户能删除：启用的客户行上没有“删除”
  deleteWhen: { active: ["false"] },
  labels: {
    "zh-CN": {
      title: "客户",
      fields: {
        name: "名称",
        group_id: "分组",
        group_name: "分组",
        note: "备注",
        level: "等级",
        tags: "标签",
        active: "启用",
        credit: "信用额度",
        last_order_at: "最近下单",
        joined: "加入日期",
        api_key: "接口密钥",
      },
      actions: { disable: "停用", enable: "启用" },
    },
  },
}

const schemas = {
  customer: {
    list: {
      query: {
        type: "object",
        properties: {
          name: { type: "string" },
          group_id: { type: "string" },
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
        required: ["name", "group_id", "level"],
        properties: {
          name: { type: "string", minLength: 1, maxLength: 40 },
          group_id: { type: "string", minLength: 1 },
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
          notice: { type: "string", format: "multiline", maxLength: 200 },
        },
      },
    },
  },
  notify: {
    settingsUpdate: {
      body: {
        type: "object",
        required: ["sender"],
        properties: {
          order_email: { type: "boolean" },
          refund_email: { type: "boolean" },
          sender: { type: "string", minLength: 1, maxLength: 40 },
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
  resources: { group: groupDef, customer: customerDef },
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
    { name: "notice", type: "string", format: "multiline" },
  ],
  labels: {
    "zh-CN": {
      title: "店铺设置",
      fields: { name: "店铺名称", currency: "结算币种", invoice: "开具发票", notice: "公告" },
      descriptions: { invoice: "下单时可以填写发票抬头", notice: "显示在店铺首页的顶部，可以换行" },
    },
  },
}

export const shopSettings = settingsModel(shopDef, kit, queryClient)

const notifyDef: SettingsDef = {
  name: "notify",
  operations: { get: ["notify", "settingsGet"], update: ["notify", "settingsUpdate"] },
  permissions: { get: [], update: [] },
  form: [
    { name: "order_email", type: "boolean" },
    { name: "refund_email", type: "boolean" },
    { name: "sender", type: "string", required: true },
  ],
  labels: {
    "zh-CN": {
      title: "通知设置",
      fields: { order_email: "新订单发邮件", refund_email: "退款发邮件", sender: "发件人名称" },
    },
  },
}

export const notifySettings = settingsModel(notifyDef, kit, queryClient)

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
  const topicMeta: Record<string, { label: string; description: string }> = {
    "mail.send": { label: "发送邮件", description: "向客户发送订单通知与验证邮件" },
    "report.daily": { label: "每日报表", description: "汇总前一天各渠道的订单与成交数据" },
    "sms.send": { label: "发送短信", description: "发送登录验证码和物流提醒短信" },
    "webhook.deliver": { label: "Webhook 投递", description: "向订阅方回调地址推送订单状态变更事件" },
  }
  let failed = Array.from({ length: 23 }, (_, i) => {
    const topic = ["mail.send", "mail.send", "report.daily", "webhook.deliver"][i % 4]
    return {
      id: `job-${i + 1}`,
      topic,
      topic_label: topicMeta[topic]?.label,
      attempts: [10, 10, 3, 8][i % 4],
      error: [
        "smtp: dial tcp 10.0.3.12:587: connect: connection refused",
        "smtp: 421 4.7.0 Try again later, closing connection",
        "pdf: render report: gotenberg returned 503",
        "webhook: POST https://hooks.example.com/orders: context deadline exceeded",
      ][i % 4],
      failed_at: new Date(Date.now() - i * 37 * 60_000).toISOString(),
    }
  })
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
          label: topicMeta[topic]?.label,
          description: topicMeta[topic]?.description,
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
      }),
    failedList: async (input: { topic?: string; page?: number; per_page?: number } = {}) => {
      const rows = input.topic ? failed.filter((j) => j.topic === input.topic) : failed
      const page = input.page ?? 1
      const per = input.per_page ?? 20
      return delay({
        items: rows.slice((page - 1) * per, page * per),
        total: rows.length,
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

/** 演示的定时任务接口：api.schedule 的形状。 */
export const scheduleApi = (() => {
  const now = Date.now()
  const at = (minutes: number) => new Date(now + minutes * 60_000).toISOString()
  const tasks: ScheduleTaskData[] = [
    {
      name: "articles:publish-scheduled",
      label: "定时发布文章",
      description: "发布到了预定时间的文章",
      expr: "* * * * *",
      next_run: at(1),
      running: false,
      last_status: "succeeded",
      last_run_at: at(-1),
      last_duration_ms: 84,
      missed: false,
    },
    {
      name: "reports:daily",
      label: "生成每日报表",
      description: "生成前一天的报表",
      expr: "0 2 * * *",
      next_run: at(60 * 9),
      running: false,
      last_status: "failed",
      last_run_at: at(-60 * 15),
      last_duration_ms: 42_300,
      last_error: "pdf: render timeout after 40s",
      missed: false,
    },
    {
      name: "locks:cleanup",
      label: "清理过期锁",
      description: "删除过期的锁",
      expr: "@hourly",
      next_run: at(23),
      running: true,
      last_status: "succeeded",
      last_run_at: at(-37),
      last_duration_ms: 1_204,
      missed: false,
    },
    {
      name: "queue:prune-failed",
      label: "清理队列失败任务",
      description: "删除 30 天前的失败任务",
      expr: "@daily",
      next_run: at(60 * 7),
      running: false,
      last_status: "succeeded",
      last_run_at: at(-60 * 41),
      last_duration_ms: 310,
      missed: true,
    },
  ]
  const runs: ScheduleRunData[] = Array.from({ length: 64 }, (_, i) => {
    const t = tasks[i % tasks.length]
    const failed = t.name === "reports:daily" && i % 3 === 1
    const skipped = t.name === "locks:cleanup" && i % 5 === 2
    return {
      id: `run-${i + 1}`,
      task: t.name,
      task_label: t.label,
      status: failed ? "failed" : skipped ? "skipped" : "succeeded",
      manual: i % 11 === 3,
      started_at: at(-i * 37),
      duration_ms: skipped ? 0 : (t.last_duration_ms ?? 100) + (i % 7) * 13,
      error: failed
        ? "pdf: render timeout after 40s\n    at reports.render (reports/daily.go:88)"
        : skipped
          ? "overlap"
          : "",
    }
  })
  const api: ScheduleApi = {
    async list() {
      await new Promise((r) => setTimeout(r, 300))
      return { items: tasks }
    },
    async run() {
      await new Promise((r) => setTimeout(r, 300))
    },
    async runs(input = {}) {
      await new Promise((r) => setTimeout(r, 300))
      const rows = runs.filter(
        (r) => (!input.task || r.task === input.task) && (!input.status || r.status === input.status),
      )
      const page = input.page ?? 1
      const perPage = input.per_page ?? 20
      return { items: rows.slice((page - 1) * perPage, page * perPage), total: rows.length }
    },
  }
  return api
})()

/** 演示的操作日志：包含人工操作、系统操作、代为操作与可跳转对象，支持按操作人筛选和游标翻页。 */
export const activityApi = (() => {
  const now = Date.now()
  const at = (minutes: number) => new Date(now - minutes * 60_000).toISOString()
  const actors = ["韩梅梅", "李雷", "王明", undefined] as const
  const templates: ((i: number) => { text: string; href?: string })[] = [
    (i) => ({ text: `发布了订单 #${10482 - i} 的发货通知`, href: "/frame/trade/orders" }),
    (i) => ({ text: `修改了客户「${customers[i % customers.length].name}」的信用额度`, href: "/frame/customers" }),
    () => ({ text: "更新了店铺设置中的结算币种与公告", href: "/frame/settings" }),
    (i) => ({ text: `导出了 11 月订单明细（共 ${120 + (i % 5) * 30} 条）` }),
    (i) => ({ text: `停用了客户「${customers[(i + 3) % customers.length].name}」`, href: "/frame/customers" }),
    () => ({ text: "定时清理了 180 天前的过期会话" }),
  ]
  const items: ActivityItem[] = Array.from({ length: 48 }, (_, i) => {
    const actor = i % 6 === 5 ? undefined : actors[i % 3]
    const tpl = templates[i % templates.length](i)
    return {
      id: `act-${String(48 - i).padStart(3, "0")}`,
      text: tpl.text,
      actor,
      impersonator: actor && actor !== "韩梅梅" && i % 9 === 2 ? "韩梅梅" : undefined,
      created_at: at(i * 43 + 5),
      href: tpl.href,
    }
  })
  return {
    actors: ["韩梅梅", "李雷", "王明"],
    async list(input: { actor?: string; before?: string; limit?: number } = {}): Promise<ActivityItem[]> {
      let rows = items
      if (input.actor === "__system__") rows = rows.filter((a) => !a.actor)
      else if (input.actor) rows = rows.filter((a) => a.actor === input.actor)
      if (input.before) {
        const idx = rows.findIndex((a) => a.id === input.before)
        rows = idx >= 0 ? rows.slice(idx + 1) : []
      }
      return delay(rows.slice(0, input.limit ?? 20))
    },
  }
})()
