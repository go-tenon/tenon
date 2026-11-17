// 队列面板：queue/queueapi 的运维页面。概览（复制槽、JetStream、各 topic，定时刷新）、失败任务（按 topic 筛选、
// 分页、重试和删除）、失败任务的详情。数据来自应用生成的客户端分组 api.queue。

import {
  Alert,
  Badge,
  Button,
  Card,
  Code,
  Drawer,
  Group,
  Loader,
  ScrollArea,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Title,
  Tooltip,
} from "@mantine/core"
import {
  IconExternalLink,
  IconPlayerPause,
  IconPlayerPlay,
  IconRefresh,
  IconRotateClockwise,
  IconTrash,
} from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import dayjs from "dayjs"
import { type ReactNode, useState } from "react"
import { type Column, DataTable } from "./data-table"
import { confirm, notifyError, notifySuccess } from "./feedback"
import { Page } from "./page"

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
  links: { name: string; url: string }[]
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
  topics: { topic: string; count: number }[]
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
  /** 概览刷新的间隔（毫秒），默认 5000。 */
  refreshInterval?: number
}

const stateText: Record<string, { label: string; color: string; hint: string }> = {
  ok: { label: "正常", color: "teal", hint: "正常处理" },
  no_handler: { label: "没有处理函数", color: "orange", hint: "没有 Worker 为这个 topic 注册处理函数，任务一直等待" },
  paused: { label: "已暂停", color: "yellow", hint: "暂停了处理，到时间自动恢复" },
  stalled: { label: "停滞", color: "red", hint: "有任务在等待，却没有 Worker 在处理" },
  absent: { label: "—", color: "gray", hint: "没有 JetStream 中的状态，只有失败任务数" },
}

const time = (t?: string) => (t ? dayjs(t).format("MM-DD HH:mm:ss") : "—")

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

/**
 * 队列面板：
 *
 *	export const Route = createFileRoute("/_app/queue")({
 *	  staticData: { title: "队列", icon: "list", permission: "queue:manage" },
 *	  component: () => <QueuePanel api={api.queue} />,
 *	})
 */
export function QueuePanel({ api, title = "队列", refreshInterval = 5000 }: QueuePanelProps): ReactNode {
  const status = useQuery({
    queryKey: ["tenon", "queue", "status"],
    queryFn: () => api.status(),
    refetchInterval: refreshInterval,
  })
  const s = status.data
  const links = s?.links ?? []
  return (
    <Page
      title={title}
      description="复制槽、JetStream 和各 topic 的状态，每 5 秒刷新；失败任务可以重试或删除。"
      actions={
        <Group gap="xs">
          {links.map((l) => (
            <Button
              key={l.url}
              variant="default"
              component="a"
              href={l.url}
              target="_blank"
              rel="noreferrer"
              rightSection={<IconExternalLink size={14} />}
            >
              {l.name}
            </Button>
          ))}
          <Tooltip label="立即刷新">
            <Button variant="default" px="xs" aria-label="刷新" onClick={() => void status.refetch()}>
              {status.isFetching ? <Loader size={14} /> : <IconRefresh size={16} stroke={1.6} />}
            </Button>
          </Tooltip>
        </Group>
      }
    >
      {status.isError && !s ? (
        <Alert color="red" variant="light" title="读取队列状态失败" role="alert">
          {String((status.error as { detail?: string })?.detail ?? status.error)}
        </Alert>
      ) : !s ? (
        <Group justify="center" py="xl">
          <Loader size="sm" />
        </Group>
      ) : (
        <>
          <Overview status={s} />
          <Topics status={s} api={api} />
        </>
      )}
      <FailedJobs api={api} />
    </Page>
  )
}

function Metric({ label, value, hint }: { label: string; value: ReactNode; hint?: ReactNode }): ReactNode {
  return (
    <Stack gap={2}>
      <Text size="xs" c="dimmed">
        {label}
      </Text>
      <Text fw={500} style={{ fontVariantNumeric: "tabular-nums" }}>
        {value}
      </Text>
      {hint && (
        <Text size="xs" c="dimmed">
          {hint}
        </Text>
      )}
    </Stack>
  )
}

function Overview({ status }: { status: QueueStatusData }): ReactNode {
  const { slot, jetstream: js } = status
  const slotState = !slot.exists ? (
    <Badge color="red">不存在</Badge>
  ) : slot.active ? (
    <Badge color="teal" variant="dot">
      转发中
    </Badge>
  ) : (
    <Badge color="orange" variant="dot">
      没有 Worker
    </Badge>
  )
  const walColor = slot.wal_status === "lost" ? "red" : slot.wal_status === "unreserved" ? "orange" : undefined
  return (
    <SimpleGrid cols={{ base: 1, md: 3 }} spacing="md">
      <Card>
        <Group justify="space-between" mb="md">
          <Text fw={600}>复制槽</Text>
          {slotState}
        </Group>
        <SimpleGrid cols={2} spacing="sm">
          <Metric label="积压" value={formatBytes(slot.lag)} />
          <Metric
            label="WAL 状态"
            value={
              <Text span c={walColor} inherit>
                {slot.wal_status || "—"}
              </Text>
            }
          />
          <Metric label="进程" value={slot.active_pid || "—"} />
        </SimpleGrid>
        <Text size="xs" c="dimmed" ff="monospace" mt="sm" truncate title={slot.name}>
          {slot.name}
        </Text>
        {!slot.exists && (
          <Text size="xs" c="red" mt="sm">
            Worker 不会启动，运行 queue slot recreate 重建。
          </Text>
        )}
      </Card>
      <Card>
        <Group justify="space-between" mb="md">
          <Text fw={600}>JetStream</Text>
          {js &&
            !js.error &&
            (js.exists ? (
              <Badge variant="dot" color="teal">
                {js.stream}
              </Badge>
            ) : (
              <Badge color="orange">未创建</Badge>
            ))}
        </Group>
        {!js ? (
          <Text size="sm" c="dimmed">
            应用没有设置 queueapi.Options.Admin，只显示数据库一侧的状态。
          </Text>
        ) : js.error ? (
          <Alert color="red" variant="light" p="xs">
            <Text size="sm">读不到 JetStream：{js.error}</Text>
          </Alert>
        ) : (
          <SimpleGrid cols={2} spacing="sm">
            <Metric label="消息" value={js.messages.toLocaleString()} hint={formatBytes(js.bytes)} />
            <Metric label="定时投递" value={js.deferred.toLocaleString()} />
            <Metric label="副本" value={js.replicas} />
            <Metric label="Leader" value={js.leader ?? "—"} />
          </SimpleGrid>
        )}
      </Card>
      <Card>
        <Text fw={600} mb="md">
          失败任务
        </Text>
        <Text
          fz={32}
          fw={600}
          c={status.failed > 0 ? "red" : undefined}
          style={{ letterSpacing: "-0.02em", fontVariantNumeric: "tabular-nums" }}
        >
          {status.failed.toLocaleString()}
        </Text>
        <Text size="xs" c="dimmed">
          {status.failed > 0 ? "在下方按 topic 查看、重试或删除" : "没有失败的任务"}
        </Text>
      </Card>
    </SimpleGrid>
  )
}

function Topics({ status, api }: { status: QueueStatusData; api: QueueApi }): ReactNode {
  const queryClient = useQueryClient()
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["tenon", "queue"] })
  const toggle = useMutation({
    mutationFn: async ({ topic, pause }: { topic: string; pause: boolean }) =>
      pause ? api.topicPause?.(topic) : api.topicResume?.(topic),
    onSuccess: (_, v) => {
      notifySuccess(v.pause ? `已暂停 ${v.topic} 一小时` : `已恢复 ${v.topic}`)
      void refresh()
    },
    onError: (err) => notifyError(err),
  })
  const canPause = !!api.topicPause && !!status.jetstream && !status.jetstream.error
  const columns: Column<QueueTopicData>[] = [
    {
      accessor: "topic",
      title: "Topic",
      render: (t) => (
        <Text size="sm" ff="monospace">
          {t.topic}
        </Text>
      ),
    },
    {
      accessor: "state",
      title: "状态",
      render: (t) => {
        const st = stateText[t.state] ?? { label: t.state, color: "gray", hint: "" }
        const label =
          t.state === "paused" && t.paused_until ? `暂停到 ${dayjs(t.paused_until).format("HH:mm")}` : st.label
        return (
          <Tooltip label={st.hint} disabled={!st.hint}>
            <Badge color={st.color} variant={t.state === "ok" ? "dot" : "light"}>
              {label}
            </Badge>
          </Tooltip>
        )
      },
    },
    { accessor: "pending", title: "等待", textAlign: "right" },
    { accessor: "in_flight", title: "处理中", textAlign: "right" },
    { accessor: "redelivered", title: "重新投递", textAlign: "right" },
    { accessor: "waiting", title: "拉取", textAlign: "right" },
    {
      accessor: "failed",
      title: "失败",
      textAlign: "right",
      render: (t) => (
        <Text span size="sm" c={t.failed > 0 ? "red" : undefined}>
          {t.failed}
        </Text>
      ),
    },
    {
      accessor: "last_delivered",
      title: "最近投递",
      noWrap: true,
      render: (t) => (
        <Text span size="sm" c="dimmed">
          {time(t.last_delivered)}
        </Text>
      ),
    },
  ]
  if (canPause) {
    columns.push({
      accessor: "_actions",
      title: "",
      textAlign: "right",
      width: 100,
      render: (t) =>
        t.state === "paused" ? (
          <Button
            size="compact-sm"
            variant="subtle"
            leftSection={<IconPlayerPlay size={14} />}
            loading={toggle.isPending && toggle.variables?.topic === t.topic}
            onClick={() => toggle.mutate({ topic: t.topic, pause: false })}
          >
            恢复
          </Button>
        ) : t.state === "ok" || t.state === "stalled" ? (
          <Button
            size="compact-sm"
            variant="subtle"
            color="gray"
            leftSection={<IconPlayerPause size={14} />}
            loading={toggle.isPending && toggle.variables?.topic === t.topic}
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
        ) : null,
    })
  }
  return (
    <Stack gap="xs">
      <Title order={4}>Topic</Title>
      <DataTable records={status.topics} columns={columns} rowKey="topic" empty="还没有 topic" minHeight={120} />
    </Stack>
  )
}

function FailedJobs({ api }: { api: QueueApi }): ReactNode {
  const [topic, setTopic] = useState<string>()
  const [page, setPage] = useState(1)
  const [perPage, setPerPage] = useState(20)
  const [open, setOpen] = useState<string>()
  const queryClient = useQueryClient()
  const list = useQuery({
    queryKey: ["tenon", "queue", "failed", topic, page, perPage],
    queryFn: () => api.failedList({ topic, page, per_page: perPage }),
  })
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["tenon", "queue"] })
  const data = list.data
  const total = data?.topics.reduce((n, t) => n + t.count, 0) ?? 0

  const single = async (action: "retry" | "delete", id: string) => {
    const retry = action === "retry"
    if (
      !(await confirm({
        title: retry ? "重试这条失败任务？" : "删除这条失败任务？",
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
  const bulk = async (action: "retry" | "delete") => {
    const retry = action === "retry"
    const scope = topic ? `${topic} 的` : "全部"
    if (
      !(await confirm({
        title: retry ? `重试${scope}失败任务？` : `删除${scope}失败任务？`,
        message: retry ? "只处理现在已经失败的任务，重试后又失败的不会再放回。" : "删除后不能恢复。",
        confirmLabel: retry ? "全部重试" : "全部删除",
        danger: !retry,
      }))
    )
      return
    try {
      const r = await (retry ? api.failedRetryAll({ topic }) : api.failedDeleteAll({ topic }))
      notifySuccess(retry ? `已重新发布 ${r.count} 条` : `已删除 ${r.count} 条`)
      setPage(1)
      await refresh()
    } catch (err) {
      notifyError(err)
    }
  }

  const columns: Column<QueueFailedJobData>[] = [
    {
      accessor: "topic",
      title: "Topic",
      noWrap: true,
      render: (j) => (
        <Text size="sm" ff="monospace">
          {j.topic}
        </Text>
      ),
    },
    {
      accessor: "error",
      title: "错误",
      ellipsis: true,
      render: (j) => (
        <Text size="sm" truncate title={j.error}>
          {j.error}
        </Text>
      ),
    },
    { accessor: "attempts", title: "次数", textAlign: "right", width: 70 },
    {
      accessor: "failed_at",
      title: "失败时间",
      noWrap: true,
      width: 140,
      render: (j) => (
        <Text span size="sm" c="dimmed">
          {time(j.failed_at)}
        </Text>
      ),
    },
    {
      accessor: "_actions",
      title: "",
      textAlign: "right",
      width: 100,
      render: (j) => (
        <Group gap={2} justify="flex-end" wrap="nowrap" onClick={(e) => e.stopPropagation()}>
          <Tooltip label="重试">
            <Button
              size="compact-sm"
              variant="subtle"
              px={6}
              aria-label="重试"
              onClick={() => void single("retry", j.id)}
            >
              <IconRotateClockwise size={16} stroke={1.6} />
            </Button>
          </Tooltip>
          <Tooltip label="删除">
            <Button
              size="compact-sm"
              variant="subtle"
              color="red"
              px={6}
              aria-label="删除"
              onClick={() => void single("delete", j.id)}
            >
              <IconTrash size={16} stroke={1.6} />
            </Button>
          </Tooltip>
        </Group>
      ),
    },
  ]
  const chips = [{ topic: undefined as string | undefined, count: total }, ...(data?.topics ?? [])]
  return (
    <Stack gap="xs">
      <Group justify="space-between">
        <Title order={4}>失败任务</Title>
        <Group gap="xs">
          <Button
            variant="default"
            leftSection={<IconRotateClockwise size={16} />}
            disabled={!data?.total}
            onClick={() => void bulk("retry")}
          >
            全部重试
          </Button>
          <Button
            variant="default"
            color="red"
            leftSection={<IconTrash size={16} />}
            disabled={!data?.total}
            onClick={() => void bulk("delete")}
          >
            全部删除
          </Button>
        </Group>
      </Group>
      <DataTable
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
        empty={topic ? "这个 topic 没有失败的任务" : "没有失败的任务"}
        onRowClick={(j) => setOpen(j.id)}
        toolbar={
          <Group gap={6} role="tablist" aria-label="按 topic 筛选">
            {chips.map((c) => {
              const active = c.topic === topic
              return (
                <Button
                  key={c.topic ?? ""}
                  role="tab"
                  aria-selected={active}
                  size="compact-sm"
                  radius="xl"
                  variant={active ? "light" : "subtle"}
                  color={active ? undefined : "gray"}
                  onClick={() => {
                    setTopic(c.topic)
                    setPage(1)
                  }}
                  rightSection={
                    <Text span size="xs" c="dimmed">
                      {c.count}
                    </Text>
                  }
                >
                  {c.topic ?? "全部"}
                </Button>
              )
            })}
          </Group>
        }
      />
      <FailedJobDrawer api={api} id={open} onClose={() => setOpen(undefined)} onAction={(a, id) => single(a, id)} />
    </Stack>
  )
}

function FailedJobDrawer({
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
  const job = useQuery({
    queryKey: ["tenon", "queue", "failed-job", id],
    queryFn: () => api.failedGet(id as string),
    enabled: !!id,
  })
  const j = job.data
  const meta = Object.entries(j?.metadata ?? {})
  return (
    <Drawer opened={!!id} onClose={onClose} position="right" size="xl" title="失败任务">
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
          <SimpleGrid cols={3} spacing="sm">
            <Metric
              label="Topic"
              value={
                <Text span ff="monospace" inherit>
                  {j.topic}
                </Text>
              }
            />
            <Metric label="执行次数" value={j.attempts} />
            <Metric label="失败时间" value={dayjs(j.failed_at).format("YYYY-MM-DD HH:mm:ss")} />
          </SimpleGrid>
          <Group gap="xs">
            <Button
              leftSection={<IconRotateClockwise size={16} />}
              onClick={async () => (await onAction("retry", j.id)) && onClose()}
            >
              重试
            </Button>
            <Button
              variant="default"
              color="red"
              leftSection={<IconTrash size={16} />}
              onClick={async () => (await onAction("delete", j.id)) && onClose()}
            >
              删除
            </Button>
          </Group>
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
          <Section title="Payload">
            <ScrollArea.Autosize mah={360}>
              <Code block>{JSON.stringify(j.payload ?? null, null, 2)}</Code>
            </ScrollArea.Autosize>
          </Section>
        </Stack>
      )}
    </Drawer>
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
