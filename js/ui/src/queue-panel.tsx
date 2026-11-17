// 队列面板：queue/queueapi 的运维页面，分成三个页内标签（地址中的 ?tab=）：
//   - 任务类型：每个 topic 的状态和任务数，可以暂停、恢复。
//   - 失败任务：按任务类型筛选、分页，查看原因（对话框），单条或勾选多条重试、删除，也可以一次处理当前筛选的全部。
//   - 系统状态：复制槽和 JetStream 的底层状态，给开发人员排查问题用。
// 复制槽、JetStream 出问题时，标签上面显示醒目的提示。数据来自应用生成的客户端分组 api.queue。
// 和其他列表一样按需读取：打开页面、切换标签和筛选、做完操作、切回浏览器窗口时读取，没有定时刷新。
import {
  ActionIcon,
  Alert,
  Badge,
  Box,
  Button,
  Card,
  Code,
  Group,
  Loader,
  Menu,
  Modal,
  ScrollArea,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
} from "@mantine/core"
import { IconDots, IconPlayerPause, IconPlayerPlay, IconRotateClockwise, IconTrash } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import dayjs from "dayjs"
import { type ReactNode, useState } from "react"
import { type Column, DataTable } from "./data-table"
import { confirm, notifyError, notifySuccess } from "./feedback"
import { useFormatDateTime } from "./fields"
import { Page } from "./page"
import { useSelection } from "./shell"

// ———————————————————————————— 接口 ————————————————————————————

/** 概览：queueapi.QueueStatus。 */
export interface QueueStatusData {
  slot: { name: string; exists: boolean; active: boolean; active_pid: number; wal_status: string; lag: number }
  jetstream?: {
    error?: string
    stream: string
    exists: boolean
    messages: number
    bytes: number
    replicas: number
    leader?: string
    deferred: number
  }
  topics: QueueTopicData[]
  failed: number
}

/** 概览中的一个 topic：queueapi.QueueTopic。 */
export interface QueueTopicData {
  topic: string
  state: string
  pending: number
  in_flight: number
  redelivered: number
  waiting: number
  failed: number
  last_delivered?: string
  paused_until?: string
}

/** 列表中的一条失败任务：queueapi.QueueFailedJob。 */
export interface QueueFailedJobData {
  id: string
  topic: string
  attempts: number
  error: string
  failed_at: string
}

/** 失败任务的详情：queueapi.QueueFailedJobDetail。 */
export interface QueueFailedJobDetailData extends QueueFailedJobData {
  metadata: { [key: string]: string }
  payload?: unknown
}

/** 一页失败任务：queueapi.QueueFailedList。 */
export interface QueueFailedListData {
  items: QueueFailedJobData[]
  total: number
}

/**
 * 队列运维的接口：tenon generate 为 queue/queueapi 生成的客户端分组，即 api.queue。
 * 没有设置 queueapi.Options.Admin 时没有暂停和恢复。
 */
export interface QueueApi {
  status(): Promise<QueueStatusData>
  failedList(input?: { topic?: string; page?: number; per_page?: number }): Promise<QueueFailedListData>
  failedGet(id: string): Promise<QueueFailedJobDetailData>
  failedRetry(id: string): Promise<unknown>
  failedDelete(id: string): Promise<unknown>
  failedRetryAll(input?: { topic?: string }): Promise<{ count: number }>
  failedDeleteAll(input?: { topic?: string }): Promise<{ count: number }>
  topicPause?(topic: string): Promise<unknown>
  topicResume?(topic: string): Promise<unknown>
}

// ———————————————————————————— 面板 ————————————————————————————

/** QueuePanel 的选项。 */
export interface QueuePanelProps {
  /** 生成的客户端分组：api.queue。 */
  api: QueueApi
  /** 页面的标题，默认“队列”。 */
  title?: ReactNode
}

/** topic 的状态：显示的文字、颜色，异常时的说明（显示在状态下面）。 */
const stateText: Record<string, { label: string; color: string; hint?: string }> = {
  ok: { label: "正常", color: "teal" },
  no_handler: { label: "没有处理程序", color: "orange", hint: "没有 Worker 处理这类任务，任务会一直等待" },
  paused: { label: "已暂停", color: "yellow" },
  stalled: { label: "停滞", color: "red", hint: "有任务在等待，却没有 Worker 在处理" },
  absent: { label: "—", color: "gray" },
}

/** 字节数的显示，例如 12.4 KiB。 */
export function formatBytes(n: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"]
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`
}

const statusKey = ["tenon", "queue", "status"]

/**
 * 队列面板：
 *
 *	export const Route = createFileRoute("/_app/queue")({
 *	  staticData: { title: "队列", icon: "list", permission: "queue:manage" },
 *	  component: () => <QueuePanel api={api.queue} />,
 *	})
 */
export function QueuePanel({ api, title = "队列" }: QueuePanelProps): ReactNode {
  const status = useQuery({ queryKey: statusKey, queryFn: () => api.status() })
  const s = status.data
  // 状态还没读到时各个标签显示读取中；读取失败时显示原因。
  const needStatus = (render: (s: QueueStatusData) => ReactNode) =>
    s ? (
      render(s)
    ) : status.isError ? (
      <Alert color="red" variant="light" title="读取队列状态失败" role="alert">
        {String((status.error as { detail?: string })?.detail ?? status.error)}
      </Alert>
    ) : (
      <Group justify="center" py="xl">
        <Loader size="sm" />
      </Group>
    )
  return (
    <Page
      title={title}
      description="后台任务的处理情况。失败的任务可以查看原因、重试或删除。"
      tabs={[
        { value: "topics", label: "任务类型", content: needStatus((s) => <Topics status={s} api={api} />) },
        {
          value: "failed",
          label: "失败任务",
          count: s?.failed,
          alert: true,
          content: (
            <FailedJobs
              api={api}
              counts={s?.topics.filter((t) => t.failed > 0).map((t) => ({ topic: t.topic, count: t.failed }))}
            />
          ),
        },
        { value: "system", label: "系统状态", content: needStatus((s) => <SystemStatus status={s} />) },
      ]}
    >
      {s && <Problems status={s} />}
    </Page>
  )
}

// ———————————————————————————— 概览 ————————————————————————————

/** 复制槽、JetStream 出问题时的提示：平时不显示。 */
function Problems({ status }: { status: QueueStatusData }): ReactNode {
  const { slot, jetstream: js } = status
  const problems: { color: string; title: string; text: string }[] = []
  if (!slot.exists)
    problems.push({
      color: "red",
      title: "任务转发已停止",
      text: `复制槽 ${slot.name} 不存在，Worker 不会启动。运行 queue slot recreate 重建。`,
    })
  else if (slot.wal_status === "lost")
    problems.push({
      color: "red",
      title: "任务转发已失效",
      text: "数据库已经丢弃了还没转发的任务记录（WAL 状态 lost）。运行 queue slot recreate 重建复制槽。",
    })
  else if (!slot.active)
    problems.push({
      color: "orange",
      title: "没有 Worker 在转发任务",
      text: "新发布的任务会积压在数据库中，直到 Worker 启动。",
    })
  else if (slot.wal_status === "unreserved")
    problems.push({
      color: "orange",
      title: "任务转发积压过多",
      text: `积压 ${formatBytes(slot.lag)}，再多数据库可能丢弃还没转发的任务记录。`,
    })
  if (js?.error) problems.push({ color: "red", title: "读不到任务流", text: js.error })
  else if (js && !js.exists)
    problems.push({
      color: "orange",
      title: "任务流还没有创建",
      text: `JetStream 中没有 ${js.stream}，Worker 启动时创建。`,
    })
  if (problems.length === 0) return null
  return (
    <Stack gap="xs">
      {problems.map((p) => (
        <Alert key={p.title} color={p.color} variant="light" title={p.title} role="alert">
          {p.text}
        </Alert>
      ))}
    </Stack>
  )
}

/** 系统状态：复制槽和 JetStream 的底层状态，给开发人员排查问题用。 */
function SystemStatus({ status }: { status: QueueStatusData }): ReactNode {
  const { slot, jetstream: js } = status
  const rows: [string, ReactNode][] = [
    ["复制槽", <Code key="slot">{slot.name}</Code>],
    ["复制槽状态", !slot.exists ? "不存在" : slot.active ? "转发中" : "没有 Worker"],
    ["WAL 状态", slot.wal_status || "—"],
    ["积压", formatBytes(slot.lag)],
    ["进程", slot.active_pid || "—"],
  ]
  if (js && !js.error)
    rows.push(
      ["任务流", <Code key="stream">{js.stream}</Code>],
      ["消息", `${js.messages}（${formatBytes(js.bytes)}）`],
      ["定时投递", js.deferred],
      ["副本", js.replicas],
      ["Leader", js.leader ?? "—"],
    )
  else if (!js) rows.push(["任务流", "应用没有设置 queueapi.Options.Admin，只显示数据库一侧的状态"])
  return (
    <Card>
      <Table fz="sm" verticalSpacing={8} withRowBorders={false}>
        <Table.Tbody>
          {rows.map(([k, v]) => (
            <Table.Tr key={k}>
              <Table.Td w={120} c="dimmed">
                {k}
              </Table.Td>
              <Table.Td>{v}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Card>
  )
}

// ———————————————————————————— 任务类型 ————————————————————————————

function Topics({ status, api }: { status: QueueStatusData; api: QueueApi }): ReactNode {
  const queryClient = useQueryClient()
  const formatDateTime = useFormatDateTime()
  const toggle = useMutation({
    mutationFn: async ({ topic, pause }: { topic: string; pause: boolean }) =>
      pause ? api.topicPause?.(topic) : api.topicResume?.(topic),
    onSuccess: (_, v) => {
      notifySuccess(v.pause ? `已暂停 ${v.topic} 一小时` : `已恢复 ${v.topic}`)
      void queryClient.invalidateQueries({ queryKey: ["tenon", "queue"] })
    },
    onError: (err) => notifyError(err),
  })
  const canPause = !!api.topicPause && !!status.jetstream && !status.jetstream.error
  const columns: Column<QueueTopicData>[] = [
    {
      accessor: "topic",
      title: "任务类型",
      noWrap: true,
      render: (t) => (
        <Text size="sm" ff="monospace">
          {t.topic}
        </Text>
      ),
    },
    {
      accessor: "state",
      title: "状态",
      // 不换行：窄屏时状态和说明保持完整，表格左右滚动。
      noWrap: true,
      render: (t) => {
        const st = stateText[t.state] ?? { label: t.state, color: "gray" }
        const label =
          t.state === "paused" && t.paused_until ? `暂停到 ${dayjs(t.paused_until).format("HH:mm")}` : st.label
        return (
          <Stack gap={2} align="flex-start">
            <Badge color={st.color} style={{ flexShrink: 0 }}>
              {label}
            </Badge>
            {st.hint && (
              <Text size="xs" c="dimmed">
                {st.hint}
              </Text>
            )}
          </Stack>
        )
      },
    },
    { accessor: "pending", title: "等待中", noWrap: true },
    { accessor: "in_flight", title: "处理中", noWrap: true },
    { accessor: "redelivered", title: "重新投递", noWrap: true },
    { accessor: "waiting", title: "空闲 Worker", noWrap: true },
    {
      accessor: "failed",
      title: "失败",
      noWrap: true,
      render: (t) => (
        <Text span size="sm" c={t.failed > 0 ? "red" : undefined}>
          {t.failed}
        </Text>
      ),
    },
    {
      accessor: "last_delivered",
      title: "最近处理",
      noWrap: true,
      render: (t) => (
        <Text span size="sm" c="dimmed">
          {t.last_delivered ? formatDateTime(t.last_delivered) : "—"}
        </Text>
      ),
    },
  ]
  if (canPause) {
    columns.push({
      accessor: "_actions",
      title: "",
      textAlign: "right",
      noWrap: true,
      width: 90,
      render: (t) => {
        const busy = toggle.isPending && toggle.variables?.topic === t.topic
        if (t.state === "paused")
          return (
            <Button
              size="compact-sm"
              variant="subtle"
              leftSection={<IconPlayerPlay size={14} />}
              loading={busy}
              onClick={() => toggle.mutate({ topic: t.topic, pause: false })}
            >
              恢复
            </Button>
          )
        if (t.state !== "ok" && t.state !== "stalled") return null
        return (
          <Button
            size="compact-sm"
            variant="subtle"
            leftSection={<IconPlayerPause size={14} />}
            loading={busy}
            onClick={async () => {
              if (
                await confirm({
                  title: `暂停 ${t.topic} 一小时？`,
                  message: "任务照常发布和保存，Worker 不再领取新的任务，处理中的任务不受影响。一小时后自动恢复。",
                  confirmLabel: "暂停",
                })
              )
                toggle.mutate({ topic: t.topic, pause: true })
            }}
          >
            暂停
          </Button>
        )
      },
    })
  }
  return <DataTable records={status.topics} columns={columns} rowKey="topic" empty="还没有任务" minHeight={120} />
}

// ———————————————————————————— 失败任务 ————————————————————————————

function FailedJobs({
  api,
  counts,
}: {
  api: QueueApi
  /** 各任务类型的失败条数，来自定时刷新的概览：筛选项按它显示。 */
  counts?: { topic: string; count: number }[]
}): ReactNode {
  const [topic, setTopic] = useState<string>()
  const [page, setPage] = useState(1)
  const [perPage, setPerPage] = useState(20)
  const [open, setOpen] = useState<string>()
  const queryClient = useQueryClient()
  const formatDateTime = useFormatDateTime()
  const list = useQuery({
    queryKey: ["tenon", "queue", "failed", topic, page, perPage],
    queryFn: () => api.failedList({ topic, page, per_page: perPage }),
    // 换筛选、翻页时总是向服务器读最新的，读取中显示加载状态；不用缓存的旧数据。
    gcTime: 0,
  })
  const selection = useSelection<string>([topic, page, perPage])
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["tenon", "queue"] })
  const data = list.data
  // 筛选项和条数只看概览：只列出有失败任务的类型。选中的类型没有失败的任务了（例如刚重试完），回到“全部”。
  const topics = counts ?? []
  if (topic && counts && !counts.some((t) => t.topic === topic)) {
    setTopic(undefined)
    setPage(1)
  }
  const all = topics.reduce((n, t) => n + t.count, 0)
  const count = topic ? (topics.find((t) => t.topic === topic)?.count ?? 0) : all

  const single = async (action: "retry" | "delete", id: string) => {
    const retry = action === "retry"
    if (
      !(await confirm({
        title: retry ? "重试这个任务？" : "删除这个任务？",
        message: retry ? "任务重新发布，执行次数从 0 开始计算。" : "删除后不能恢复。",
        confirmLabel: retry ? "重试" : "删除",
        danger: !retry,
      }))
    )
      return false
    try {
      await (retry ? api.failedRetry(id) : api.failedDelete(id))
      notifySuccess(retry ? "已重新发布" : "已删除")
      await refresh()
      return true
    } catch (err) {
      notifyError(err)
      return false
    }
  }
  // 勾选的几条：接口没有批量的版本，逐条处理。
  const selected = async (action: "retry" | "delete", ids: string[]) => {
    const retry = action === "retry"
    if (
      !(await confirm({
        title: retry ? `重试选中的 ${ids.length} 个任务？` : `删除选中的 ${ids.length} 个任务？`,
        message: retry ? "任务重新发布，执行次数从 0 开始计算。" : "删除后不能恢复。",
        confirmLabel: retry ? "重试" : "删除",
        danger: !retry,
      }))
    )
      return
    const results = await Promise.allSettled(ids.map((id) => (retry ? api.failedRetry(id) : api.failedDelete(id))))
    const failed = results.filter((r) => r.status === "rejected")
    const done = ids.length - failed.length
    if (done > 0) notifySuccess(retry ? `已重新发布 ${done} 个` : `已删除 ${done} 个`)
    if (failed.length > 0) notifyError((failed[0] as PromiseRejectedResult).reason, `${failed.length} 个没有成功`)
    selection.clear()
    await refresh()
  }
  // 当前筛选的全部：接口一次处理。
  const everything = async (action: "retry" | "delete") => {
    const retry = action === "retry"
    const scope = topic ? `${topic} 的 ${count} 个失败任务` : `全部 ${count} 个失败任务`
    if (
      !(await confirm({
        title: retry ? `重试${scope}？` : `删除${scope}？`,
        message: retry ? "只处理现在已经失败的任务，重试后又失败的不会再放回。" : "删除后不能恢复。",
        confirmLabel: retry ? "重试" : "删除",
        danger: !retry,
      }))
    )
      return
    try {
      const r = await (retry ? api.failedRetryAll({ topic }) : api.failedDeleteAll({ topic }))
      notifySuccess(retry ? `已重新发布 ${r.count} 个` : `已删除 ${r.count} 个`)
      setTopic(undefined) // 这一类（或全部）已经处理完，回到“全部”
      setPage(1)
      await refresh()
    } catch (err) {
      notifyError(err)
    }
  }

  const columns: Column<QueueFailedJobData>[] = [
    {
      accessor: "topic",
      title: "任务类型",
      noWrap: true,
      render: (j) => (
        <Text size="sm" ff="monospace">
          {j.topic}
        </Text>
      ),
    },
    { accessor: "error", title: "错误", ellipsis: true },
    { accessor: "attempts", title: "执行次数", noWrap: true, width: 90 },
    {
      accessor: "failed_at",
      title: "失败时间",
      noWrap: true,
      width: 120,
      render: (j) => (
        <Text span size="sm" c="dimmed">
          {formatDateTime(j.failed_at)}
        </Text>
      ),
    },
    {
      accessor: "_actions",
      title: "",
      textAlign: "right",
      noWrap: true,
      width: 110,
      render: (j) => (
        <Group gap={2} justify="flex-end" wrap="nowrap" onClick={(e) => e.stopPropagation()}>
          <Button size="compact-sm" variant="subtle" onClick={() => void single("retry", j.id)}>
            重试
          </Button>
          <Menu position="bottom-end" width={140}>
            <Menu.Target>
              <ActionIcon aria-label="更多操作">
                <IconDots size={16} stroke={1.6} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item onClick={() => setOpen(j.id)}>查看详情</Menu.Item>
              <Menu.Divider />
              <Menu.Item
                color="red"
                leftSection={<IconTrash size={14} stroke={1.6} />}
                onClick={() => void single("delete", j.id)}
              >
                删除
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </Group>
      ),
    },
  ]

  // 按任务类型筛选：类型不多时并排，多了改成下拉选择。
  const options = [
    { value: "", label: `全部 ${all}` },
    ...topics.map((t) => ({ value: t.topic, label: `${t.topic} ${t.count}` })),
  ]
  const choose = (v: string | null) => {
    setTopic(v || undefined)
    setPage(1)
  }
  const filter =
    options.length <= 5 ? (
      <ScrollArea type="never" offsetScrollbars={false}>
        <SegmentedControl value={topic ?? ""} onChange={choose} data={options} aria-label="按任务类型筛选" />
      </ScrollArea>
    ) : (
      <Select
        value={topic ?? ""}
        onChange={choose}
        data={options}
        allowDeselect={false}
        w={220}
        aria-label="按任务类型筛选"
      />
    )

  return (
    <>
      <DataTable
        toolbar={
          <Group justify="space-between" gap="xs">
            {filter}
            <Group gap="xs" wrap="nowrap">
              <Button
                variant="default"
                leftSection={<IconRotateClockwise size={16} />}
                disabled={count === 0}
                onClick={() => void everything("retry")}
              >
                {topic ? `重试这 ${count} 个` : `重试全部 ${count} 个`}
              </Button>
              <Menu position="bottom-end" width={180}>
                <Menu.Target>
                  <ActionIcon variant="default" size={32} aria-label="更多批量操作" disabled={count === 0}>
                    <IconDots size={16} stroke={1.6} />
                  </ActionIcon>
                </Menu.Target>
                <Menu.Dropdown>
                  <Menu.Item
                    color="red"
                    leftSection={<IconTrash size={14} stroke={1.6} />}
                    onClick={() => void everything("delete")}
                  >
                    {topic ? `删除这 ${count} 个` : `删除全部 ${count} 个`}
                  </Menu.Item>
                </Menu.Dropdown>
              </Menu>
            </Group>
          </Group>
        }
        records={data?.items ?? []}
        total={data?.total}
        loading={list.isPending}
        list={{
          page,
          perPage,
          setPage,
          setPerPage: (n) => {
            setPerPage(n)
            setPage(1)
          },
          sort: undefined,
          setSort: () => {},
        }}
        columns={columns}
        selection={selection}
        bulkActions={(ids) => (
          <>
            <Button size="compact-sm" variant="default" onClick={() => void selected("retry", ids)}>
              重试
            </Button>
            <Button size="compact-sm" variant="default" color="red" onClick={() => void selected("delete", ids)}>
              删除
            </Button>
          </>
        )}
        empty={topic ? "这类任务没有失败的" : "没有失败的任务"}
        onRowClick={(j) => setOpen(j.id)}
      />
      <FailedJobDetail api={api} id={open} onClose={() => setOpen(undefined)} onAction={(a, id) => single(a, id)} />
    </>
  )
}

/** 失败任务的详情（对话框）：错误、元数据和任务内容。 */
function FailedJobDetail({
  api,
  id,
  onClose,
  onAction,
}: {
  api: QueueApi
  id?: string
  onClose: () => void
  onAction: (action: "retry" | "delete", id: string) => Promise<boolean>
}): ReactNode {
  const formatDateTime = useFormatDateTime()
  const job = useQuery({
    queryKey: ["tenon", "queue", "failed-job", id],
    queryFn: () => api.failedGet(id as string),
    enabled: !!id,
  })
  const j = job.data
  const meta = Object.entries(j?.metadata ?? {})
  return (
    <Modal opened={!!id} onClose={onClose} size="xl" title="失败任务">
      {/* 打开时把焦点放在内容上，不让关闭按钮带着焦点框。 */}
      <Box data-autofocus tabIndex={-1} style={{ outline: "none" }}>
        {job.isPending ? (
          <Group justify="center" py="xl">
            <Loader size="sm" />
          </Group>
        ) : !j ? (
          <Alert color="red" variant="light" role="alert">
            读取失败任务失败，它可能已经被重试或删除。
          </Alert>
        ) : (
          <Stack gap="lg">
            <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="sm">
              <Field label="任务类型">
                <Text span ff="monospace" inherit>
                  {j.topic}
                </Text>
              </Field>
              <Field label="执行次数">{j.attempts}</Field>
              <Field label="失败时间">{formatDateTime(j.failed_at)}</Field>
            </SimpleGrid>
            <Section title="错误">
              <ScrollArea.Autosize mah={280}>
                <Code block style={{ whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
                  {j.error}
                </Code>
              </ScrollArea.Autosize>
            </Section>
            <Section title="元数据">
              {meta.length === 0 ? (
                <Text size="sm" c="dimmed">
                  没有元数据
                </Text>
              ) : (
                <Table withTableBorder fz="sm" verticalSpacing={6}>
                  <Table.Tbody>
                    {meta.map(([k, v]) => (
                      <Table.Tr key={k}>
                        <Table.Td w={160} c="dimmed" ff="monospace">
                          {k}
                        </Table.Td>
                        <Table.Td ff="monospace" style={{ wordBreak: "break-all" }}>
                          {v}
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              )}
            </Section>
            <Section title="任务内容">
              <ScrollArea.Autosize mah={360}>
                <Code block>{JSON.stringify(j.payload ?? null, null, 2)}</Code>
              </ScrollArea.Autosize>
            </Section>
            <Group justify="flex-end" gap="xs">
              <Button
                variant="default"
                color="red"
                leftSection={<IconTrash size={16} />}
                onClick={async () => (await onAction("delete", j.id)) && onClose()}
              >
                删除
              </Button>
              <Button
                leftSection={<IconRotateClockwise size={16} />}
                onClick={async () => (await onAction("retry", j.id)) && onClose()}
              >
                重试
              </Button>
            </Group>
          </Stack>
        )}
      </Box>
    </Modal>
  )
}

function Field({ label, children }: { label: string; children: ReactNode }): ReactNode {
  return (
    <Stack gap={2}>
      <Text size="xs" c="dimmed">
        {label}
      </Text>
      <Text fw={500} style={{ fontVariantNumeric: "tabular-nums" }}>
        {children}
      </Text>
    </Stack>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }): ReactNode {
  return (
    <Stack gap={6}>
      <Text size="sm" fw={600}>
        {title}
      </Text>
      {children}
    </Stack>
  )
}
