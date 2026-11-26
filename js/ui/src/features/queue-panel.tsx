// 队列面板：queue/queueapi 的运维页面，分成三个页内标签（地址中的 ?tab=）：
//   - 任务类型：每个 topic 的状态和任务数，可以暂停、恢复。
//   - 失败任务：按任务类型筛选、分页，点错误查看完整的原因（对话框），单条或勾选多条重试、删除，也可以一次处理当前筛选的全部。
//   - 系统状态：数据库待转发队列（outbox）和 JetStream 的底层状态，给开发人员排查问题用。
// 待转发队列滞留或 JetStream 出问题时，标签上面显示醒目的提示。数据来自应用生成的客户端分组 api.queue。
// 和其他列表一样按需读取：打开页面、切换标签和筛选、做完操作、切回浏览器窗口时读取，没有定时刷新。
import {
  ActionIcon,
  Alert,
  Badge,
  Box,
  Button,
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
  Text,
  Tooltip,
} from "@mantine/core"
import { IconDots, IconPlayerPause, IconPlayerPlay, IconRotateClockwise, IconTrash } from "@tabler/icons-react"
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { type ReactNode, useState } from "react"
import { CellLink, type Column, DataTable } from "../components/data-table/data-table"
import { useSelection } from "../components/data-table/list"
import { DescriptionList } from "../components/description-list"
import { confirm, notifyError, notifySuccess } from "../components/feedback"
import { useSmoothTransition } from "../components/loading"
import { Page } from "../components/page"
import { StatusDot } from "../components/status-dot"
import { type UiMessageId, useText } from "../i18n/i18n"
import { inTimeZone, useFormatDateTime, useTimeZone } from "../theme/format"

// ———————————————————————————— 接口 ————————————————————————————

/** 概览：queueapi.QueueStatus。 */
export interface QueueStatusData {
  outbox: { pending: number; deferred: number; oldest_pending?: string }
  jetstream?: {
    error?: string
    stream: string
    exists: boolean
    messages: number
    bytes: number
    replicas: number
    leader?: string
  }
  topics: QueueTopicData[]
  failed: number
}

/** 概览中的一个 topic：queueapi.QueueTopic。 */
export interface QueueTopicData {
  topic: string
  label?: string
  description?: string
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
  topic_label?: string
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
  /** 页面的标题，默认“队列”（当前语言的）。 */
  title?: ReactNode
}

/** topic 的状态：显示文字的消息 ID、颜色，异常时说明的消息 ID（显示在状态下面）；没有 label 时显示“—”。 */
const stateText: Record<string, { label?: UiMessageId; color: string; hint?: UiMessageId }> = {
  ok: { label: "tenon.queue.state_ok", color: "green" },
  no_handler: {
    label: "tenon.queue.state_no_handler",
    color: "deep-orange",
    hint: "tenon.queue.state_no_handler_hint",
  },
  paused: { label: "tenon.queue.state_paused", color: "yellow" },
  stalled: { label: "tenon.queue.state_stalled", color: "red", hint: "tenon.queue.state_stalled_hint" },
  absent: { color: "gray" },
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
export function QueuePanel({ api, title }: QueuePanelProps): ReactNode {
  const tr = useText()
  const status = useQuery({ queryKey: statusKey, queryFn: () => api.status() })
  const s = status.data
  // 状态还没读到时各个标签显示读取中；读取失败时显示原因。
  const needStatus = (render: (s: QueueStatusData) => ReactNode) =>
    s ? (
      render(s)
    ) : status.isError ? (
      <Alert color="red" variant="light" title={tr("tenon.queue.status_failed")} role="alert">
        {String((status.error as { detail?: string })?.detail ?? status.error)}
      </Alert>
    ) : (
      <Group justify="center" py="xl">
        <Loader size="sm" />
      </Group>
    )
  return (
    <Page
      title={title ?? tr("tenon.queue.title")}
      description={tr("tenon.queue.description")}
      tabs={[
        {
          value: "topics",
          label: tr("tenon.queue.tab_topics"),
          content: needStatus((s) => <Topics status={s} api={api} />),
        },
        {
          value: "failed",
          label: tr("tenon.queue.tab_failed"),
          count: s?.failed,
          alert: true,
          content: (
            <FailedJobs
              api={api}
              counts={s?.topics
                .filter((t) => t.failed > 0)
                .map((t) => ({ topic: t.topic, label: t.label, count: t.failed }))}
            />
          ),
        },
        {
          value: "system",
          label: tr("tenon.queue.tab_system"),
          content: needStatus((s) => <SystemStatus status={s} />),
        },
      ]}
    >
      {s && <Problems status={s} />}
    </Page>
  )
}

// ———————————————————————————— 概览 ————————————————————————————

/** 待转发任务滞留超过该时长时，提示可能没有 Worker 在运行或 NATS 不可用。 */
const outboxStaleMs = 30_000

/** 报告 outbox 中最早一条待转发任务是否已经滞留。 */
function isOutboxStale(outbox: QueueStatusData["outbox"]): boolean {
  if (outbox.pending <= 0 || !outbox.oldest_pending) return false
  const ts = Date.parse(outbox.oldest_pending)
  return !Number.isNaN(ts) && Date.now() - ts > outboxStaleMs
}

/** Outbox 滞留或 JetStream 出问题时的提示：平时不显示。 */
function Problems({ status }: { status: QueueStatusData }): ReactNode {
  const { outbox, jetstream: js } = status
  const tr = useText()
  const problems: { color: string; title: string; text: string }[] = []
  if (isOutboxStale(outbox))
    problems.push({
      color: "deep-orange",
      title: tr("tenon.queue.problem_outbox_stale"),
      text: tr("tenon.queue.problem_outbox_stale_text", { count: outbox.pending }),
    })
  if (js?.error) problems.push({ color: "red", title: tr("tenon.queue.problem_stream_error"), text: js.error })
  else if (js && !js.exists)
    problems.push({
      color: "deep-orange",
      title: tr("tenon.queue.problem_stream_missing"),
      text: tr("tenon.queue.problem_stream_missing_text", { name: js.stream }),
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

/** 系统状态：数据库待转发队列（outbox）和 JetStream 的底层状态，给开发人员排查问题用。 */
function SystemStatus({ status }: { status: QueueStatusData }): ReactNode {
  const { outbox, jetstream: js } = status
  const formatDateTime = useFormatDateTime()
  const tr = useText()
  const stale = isOutboxStale(outbox)
  const outboxState = stale
    ? { label: tr("tenon.queue.outbox_stale"), color: "deep-orange" }
    : { label: tr("tenon.queue.ok"), color: "green" }
  let stream: ReactNode
  if (!js)
    stream = (
      <DescriptionList
        title={tr("tenon.queue.stream")}
        description="NATS JetStream"
        items={[{ label: tr("tenon.queue.status"), value: tr("tenon.queue.stream_no_admin") }]}
      />
    )
  else if (js.error)
    stream = (
      <DescriptionList
        title={tr("tenon.queue.stream")}
        description="NATS JetStream"
        extra={<StatusDot color="red">{tr("tenon.queue.load_failed")}</StatusDot>}
        items={[{ label: tr("tenon.queue.error"), value: js.error, mono: true }]}
      />
    )
  else
    stream = (
      <DescriptionList
        title={tr("tenon.queue.stream")}
        description="NATS JetStream"
        extra={
          js.exists ? (
            <StatusDot color="green">{tr("tenon.queue.ok")}</StatusDot>
          ) : (
            <StatusDot color="deep-orange">{tr("tenon.queue.not_created")}</StatusDot>
          )
        }
        items={[
          { label: tr("tenon.queue.name"), value: js.stream, mono: true },
          {
            label: tr("tenon.queue.messages"),
            value: tr("tenon.queue.messages_value", { count: js.messages, size: formatBytes(js.bytes) }),
          },
          { label: tr("tenon.queue.replicas"), value: js.replicas },
          { label: "Leader", value: js.leader, mono: true },
        ]}
      />
    )
  return (
    <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md" style={{ alignItems: "start" }}>
      <DescriptionList
        title={tr("tenon.queue.outbox")}
        description={tr("tenon.queue.outbox_description")}
        extra={<StatusDot color={outboxState.color}>{outboxState.label}</StatusDot>}
        items={[
          { label: tr("tenon.queue.outbox_pending"), value: outbox.pending },
          { label: tr("tenon.queue.deferred"), value: outbox.deferred },
          {
            label: tr("tenon.queue.oldest_pending"),
            value: outbox.oldest_pending ? formatDateTime(outbox.oldest_pending) : undefined,
          },
        ]}
      />
      {stream}
    </SimpleGrid>
  )
}

// ———————————————————————————— 任务类型 ————————————————————————————

function Topics({ status, api }: { status: QueueStatusData; api: QueueApi }): ReactNode {
  const queryClient = useQueryClient()
  const formatDateTime = useFormatDateTime()
  const zone = useTimeZone()
  const tr = useText()
  const toggle = useMutation({
    mutationFn: async ({ topic, pause }: { topic: string; label?: string; pause: boolean }) =>
      pause ? api.topicPause?.(topic) : api.topicResume?.(topic),
    onSuccess: (_, v) => {
      const display = v.label || v.topic
      notifySuccess(
        v.pause
          ? tr("tenon.queue.paused_topic", { topic: display })
          : tr("tenon.queue.resumed_topic", { topic: display }),
      )
      void queryClient.invalidateQueries({ queryKey: ["tenon", "queue"] })
    },
    onError: (err) => notifyError(err),
  })
  const canPause = !!api.topicPause && !!status.jetstream && !status.jetstream.error
  const columns: Column<QueueTopicData>[] = [
    {
      accessor: "topic",
      title: tr("tenon.queue.tab_topics"),
      render: (t) => (
        <Stack gap={2} align="flex-start">
          <TopicLabel topic={t.topic} label={t.label} fw={500} />
          {t.description && (
            <Text size="xs" c="dimmed">
              {t.description}
            </Text>
          )}
        </Stack>
      ),
    },
    {
      accessor: "state",
      title: tr("tenon.queue.status"),
      // 不换行：窄屏时状态和说明保持完整，表格左右滚动。
      noWrap: true,
      render: (t) => {
        const st = stateText[t.state]
        const label =
          t.state === "paused" && t.paused_until
            ? tr("tenon.queue.paused_until", { time: inTimeZone(t.paused_until, zone).format("HH:mm") })
            : st
              ? st.label
                ? tr(st.label)
                : "—"
              : t.state
        return (
          <Stack gap={2} align="flex-start">
            <Badge color={st?.color ?? "gray"} style={{ flexShrink: 0 }}>
              {label}
            </Badge>
            {st?.hint && (
              <Text size="xs" c="dimmed">
                {tr(st.hint)}
              </Text>
            )}
          </Stack>
        )
      },
    },
    { accessor: "pending", title: tr("tenon.queue.pending"), noWrap: true },
    { accessor: "in_flight", title: tr("tenon.queue.in_flight"), noWrap: true },
    { accessor: "redelivered", title: tr("tenon.queue.redelivered"), noWrap: true },
    { accessor: "waiting", title: tr("tenon.queue.idle_workers"), noWrap: true },
    {
      accessor: "failed",
      title: tr("tenon.queue.failed"),
      noWrap: true,
      render: (t) => (
        <Text span size="sm" c={t.failed > 0 ? "red" : undefined}>
          {t.failed}
        </Text>
      ),
    },
    {
      accessor: "last_delivered",
      title: tr("tenon.queue.last_delivered"),
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
              onClick={() => toggle.mutate({ topic: t.topic, label: t.label, pause: false })}
            >
              {tr("tenon.queue.resume")}
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
                  title: tr("tenon.queue.pause_confirm", { topic: t.label || t.topic }),
                  message: tr("tenon.queue.pause_message"),
                  confirmLabel: tr("tenon.queue.pause"),
                })
              )
                toggle.mutate({ topic: t.topic, label: t.label, pause: true })
            }}
          >
            {tr("tenon.queue.pause")}
          </Button>
        )
      },
    })
  }
  return (
    <DataTable
      records={status.topics}
      columns={columns}
      rowKey="topic"
      empty={tr("tenon.queue.no_topics")}
      minHeight={120}
    />
  )
}

// ———————————————————————————— 失败任务 ————————————————————————————

function FailedJobs({
  api,
  counts,
}: {
  api: QueueApi
  /** 各任务类型的失败条数，来自定时刷新的概览：筛选项按它显示。 */
  counts?: { topic: string; label?: string; count: number }[]
}): ReactNode {
  const [topic, setTopic] = useState<string>()
  const [page, setPage] = useState(1)
  const [perPage, setPerPage] = useState(20)
  const [open, setOpen] = useState<string>()
  const queryClient = useQueryClient()
  const formatDateTime = useFormatDateTime()
  const tr = useText()
  const list = useQuery({
    queryKey: ["tenon", "queue", "failed", topic, page, perPage],
    queryFn: () => api.failedList({ topic, page, per_page: perPage }),
    // 换筛选、翻页时总是向服务器读最新的，读取中暂留旧行并展示统一最短时长的加载态。
    gcTime: 0,
    placeholderData: keepPreviousData,
  })
  const transition = useSmoothTransition(list.data, `${topic ?? ""}|${page}|${perPage}`, list.isFetching)
  const selection = useSelection<string>([topic, page, perPage])
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["tenon", "queue"] })
  const data = transition.data
  // 筛选项和条数只看概览：只列出有失败任务的类型。选中的类型没有失败的任务了（例如刚重试完），回到“全部”。
  const topics = counts ?? []
  if (topic && counts && !counts.some((t) => t.topic === topic)) {
    setTopic(undefined)
    setPage(1)
  }
  const all = topics.reduce((n, t) => n + t.count, 0)
  const count = topic ? (topics.find((t) => t.topic === topic)?.count ?? 0) : all
  const labelOf = (name: string, explicit?: string) => explicit || topics.find((t) => t.topic === name)?.label || name

  const single = async (action: "retry" | "delete", id: string) => {
    const retry = action === "retry"
    if (
      !(await confirm({
        title: retry ? tr("tenon.queue.retry_one") : tr("tenon.queue.delete_one"),
        message: retry ? tr("tenon.queue.retry_message") : tr("tenon.resource.delete_message"),
        confirmLabel: retry ? tr("tenon.queue.retry") : tr("tenon.common.delete"),
        danger: !retry,
      }))
    )
      return false
    try {
      await (retry ? api.failedRetry(id) : api.failedDelete(id))
      notifySuccess(retry ? tr("tenon.queue.republished") : tr("tenon.queue.deleted"))
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
        title: tr(retry ? "tenon.queue.retry_selected" : "tenon.queue.delete_selected", { count: ids.length }),
        message: retry ? tr("tenon.queue.retry_message") : tr("tenon.resource.delete_message"),
        confirmLabel: retry ? tr("tenon.queue.retry") : tr("tenon.common.delete"),
        danger: !retry,
      }))
    )
      return
    const results = await Promise.allSettled(ids.map((id) => (retry ? api.failedRetry(id) : api.failedDelete(id))))
    const failed = results.filter((r) => r.status === "rejected")
    const done = ids.length - failed.length
    if (done > 0)
      notifySuccess(tr(retry ? "tenon.queue.republished_count" : "tenon.queue.deleted_count", { count: done }))
    if (failed.length > 0)
      notifyError(
        (failed[0] as PromiseRejectedResult).reason,
        tr("tenon.queue.not_done_count", { count: failed.length }),
      )
    selection.clear()
    await refresh()
  }
  // 当前筛选的全部：接口一次处理。
  const everything = async (action: "retry" | "delete") => {
    const retry = action === "retry"
    const topicLabel = topic ? labelOf(topic) : undefined
    if (
      !(await confirm({
        title: retry
          ? tr(topic ? "tenon.queue.retry_topic_all" : "tenon.queue.retry_all", { topic: topicLabel, count })
          : tr(topic ? "tenon.queue.delete_topic_all" : "tenon.queue.delete_all", { topic: topicLabel, count }),
        message: retry ? tr("tenon.queue.retry_all_message") : tr("tenon.resource.delete_message"),
        confirmLabel: retry ? tr("tenon.queue.retry") : tr("tenon.common.delete"),
        danger: !retry,
      }))
    )
      return
    try {
      const r = await (retry ? api.failedRetryAll({ topic }) : api.failedDeleteAll({ topic }))
      notifySuccess(tr(retry ? "tenon.queue.republished_count" : "tenon.queue.deleted_count", { count: r.count }))
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
      title: tr("tenon.queue.tab_topics"),
      noWrap: true,
      render: (j) => <TopicLabel topic={j.topic} label={j.topic_label || labelOf(j.topic)} />,
    },
    {
      accessor: "error",
      title: tr("tenon.queue.error"),
      ellipsis: true,
      render: (j) => <CellLink onClick={() => setOpen(j.id)}>{j.error}</CellLink>,
    },
    { accessor: "attempts", title: tr("tenon.queue.attempts"), noWrap: true, width: 90 },
    {
      accessor: "failed_at",
      title: tr("tenon.queue.failed_at"),
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
            {tr("tenon.queue.retry")}
          </Button>
          <Menu position="bottom-end" width={140}>
            <Menu.Target>
              <ActionIcon aria-label={tr("tenon.resource.more")}>
                <IconDots size={16} stroke={1.6} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item color="red" onClick={() => void single("delete", j.id)}>
                {tr("tenon.common.delete")}
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </Group>
      ),
    },
  ]

  // 按任务类型筛选：类型不多时并排，多了改成下拉选择。
  const options = [
    { value: "", label: tr("tenon.queue.filter_all", { count: all }) },
    ...topics.map((t) => ({ value: t.topic, label: `${t.label || t.topic} ${t.count}` })),
  ]
  const choose = (v: string | null) => {
    setTopic(v || undefined)
    setPage(1)
  }
  const filter =
    options.length <= 5 ? (
      <ScrollArea type="never" offsetScrollbars={false}>
        <SegmentedControl
          value={topic ?? ""}
          onChange={choose}
          data={options}
          aria-label={tr("tenon.queue.filter_label")}
        />
      </ScrollArea>
    ) : (
      <Select
        value={topic ?? ""}
        onChange={choose}
        data={options}
        allowDeselect={false}
        w={220}
        aria-label={tr("tenon.queue.filter_label")}
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
                {tr(topic ? "tenon.queue.retry_these" : "tenon.queue.retry_every", { count })}
              </Button>
              <Menu position="bottom-end" width={180}>
                <Menu.Target>
                  <ActionIcon
                    variant="default"
                    size={32}
                    aria-label={tr("tenon.queue.more_bulk")}
                    disabled={count === 0}
                  >
                    <IconDots size={16} stroke={1.6} />
                  </ActionIcon>
                </Menu.Target>
                <Menu.Dropdown>
                  <Menu.Item color="red" onClick={() => void everything("delete")}>
                    {tr(topic ? "tenon.queue.delete_these" : "tenon.queue.delete_every", { count })}
                  </Menu.Item>
                </Menu.Dropdown>
              </Menu>
            </Group>
          </Group>
        }
        records={data?.items ?? []}
        total={data?.total}
        loading={(list.isPending && !data) || transition.busy}
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
              {tr("tenon.queue.retry")}
            </Button>
            <Button size="compact-sm" variant="default" color="red" onClick={() => void selected("delete", ids)}>
              {tr("tenon.common.delete")}
            </Button>
          </>
        )}
        empty={topic ? tr("tenon.queue.no_failed_topic") : tr("tenon.queue.no_failed")}
      />
      <FailedJobDetail
        api={api}
        id={open}
        labelOf={labelOf}
        onClose={() => setOpen(undefined)}
        onAction={(a, id) => single(a, id)}
      />
    </>
  )
}

/** 失败任务的详情（对话框）：错误、元数据和任务内容。 */
function FailedJobDetail({
  api,
  id,
  labelOf,
  onClose,
  onAction,
}: {
  api: QueueApi
  id?: string
  labelOf: (name: string, explicit?: string) => string
  onClose: () => void
  onAction: (action: "retry" | "delete", id: string) => Promise<boolean>
}): ReactNode {
  const formatDateTime = useFormatDateTime()
  const tr = useText()
  const job = useQuery({
    queryKey: ["tenon", "queue", "failed-job", id],
    queryFn: () => api.failedGet(id as string),
    enabled: !!id,
  })
  const j = job.data
  const meta = Object.entries(j?.metadata ?? {})
  return (
    <Modal opened={!!id} onClose={onClose} size="xl" title={tr("tenon.queue.failed_job")}>
      {/* 打开时把焦点放在内容上，不让关闭按钮带着焦点框。 */}
      <Box data-autofocus tabIndex={-1} style={{ outline: "none" }}>
        {job.isPending ? (
          <Group justify="center" py="xl">
            <Loader size="sm" />
          </Group>
        ) : !j ? (
          <Alert color="red" variant="light" role="alert">
            {tr("tenon.queue.failed_job_missing")}
          </Alert>
        ) : (
          <Stack gap="lg">
            <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="sm">
              <Field label={tr("tenon.queue.tab_topics")}>
                <TopicLabel topic={j.topic} label={labelOf(j.topic, j.topic_label)} inherit />
              </Field>
              <Field label={tr("tenon.queue.attempts")}>{j.attempts}</Field>
              <Field label={tr("tenon.queue.failed_at")}>{formatDateTime(j.failed_at)}</Field>
            </SimpleGrid>
            <Section title={tr("tenon.queue.error")}>
              <ScrollArea.Autosize mah={280}>
                <Code block style={{ whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
                  {j.error}
                </Code>
              </ScrollArea.Autosize>
            </Section>
            <Section title={tr("tenon.queue.metadata")}>
              {meta.length === 0 ? (
                <Text size="sm" c="dimmed">
                  {tr("tenon.queue.no_metadata")}
                </Text>
              ) : (
                <DescriptionList items={meta.map(([k, v]) => ({ label: k, value: v, mono: true }))} monoLabels />
              )}
            </Section>
            <Section title={tr("tenon.queue.payload")}>
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
                {tr("tenon.common.delete")}
              </Button>
              <Button
                leftSection={<IconRotateClockwise size={16} />}
                onClick={async () => (await onAction("retry", j.id)) && onClose()}
              >
                {tr("tenon.queue.retry")}
              </Button>
            </Group>
          </Stack>
        )}
      </Box>
    </Modal>
  )
}

/** 优先展示显示名称，悬停时提示内部 topic 技术名；未配置显示名称时用等宽字体展示 topic。 */
function TopicLabel({
  topic,
  label,
  fw,
  inherit,
}: {
  topic: string
  label?: string
  fw?: number
  inherit?: boolean
}): ReactNode {
  if (!label || label === topic) {
    return (
      <Text span size={inherit ? undefined : "sm"} ff="monospace" fw={fw} inherit={inherit}>
        {topic}
      </Text>
    )
  }
  return (
    <Tooltip label={topic} openDelay={200} withArrow>
      <Text span size={inherit ? undefined : "sm"} fw={fw} inherit={inherit}>
        {label}
      </Text>
    </Tooltip>
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
