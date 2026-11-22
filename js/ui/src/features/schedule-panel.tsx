// 定时任务面板：schedule/scheduleapi 的运维页面，分成两个页内标签（地址中的 ?tab=）：
//   - 任务：每个任务的说明、表达式、下一次执行、上一次的结果，可以立即执行。
//   - 执行记录：按任务、结果筛选，分页；点失败的错误查看完整的错误。
// 有任务错过了触发时，标签上面显示提示。数据来自应用生成的客户端分组 api.schedule。
// 和其他列表一样按需读取：打开页面、切换标签和筛选、做完操作、切回浏览器窗口时读取，没有定时刷新。
import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Button,
  Code,
  Group,
  Menu,
  Modal,
  ScrollArea,
  SegmentedControl,
  Select,
  Stack,
  Text,
  Tooltip,
} from "@mantine/core"
import { IconDots } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { type ReactNode, useState } from "react"
import { CellLink, type Column, DataTable } from "../components/data-table/data-table"
import { confirm, notifyError, notifySuccess } from "../components/feedback"
import { Page } from "../components/page"
import { type UiMessageId, text as uiText, useLanguage, useText } from "../i18n/i18n"
import { useFormatDateTime } from "../theme/format"
import { describeScheduleExpr } from "./schedule-expr"

// ———————————————————————————— 接口 ————————————————————————————

/** 一次执行的结果。 */
export type ScheduleStatus = "succeeded" | "failed" | "skipped"

/** 任务列表中的一个任务：scheduleapi.ScheduleTask。 */
export interface ScheduleTaskData {
  name: string
  label?: string
  description: string
  expr: string
  next_run: string
  running: boolean
  last_status?: ScheduleStatus
  last_run_at?: string
  last_duration_ms?: number
  last_error?: string
  last_manual?: boolean
  missed: boolean
}

/** 执行记录中的一次执行：scheduleapi.ScheduleRun。 */
export interface ScheduleRunData {
  id: string
  task: string
  task_label?: string
  status: ScheduleStatus
  manual: boolean
  started_at: string
  duration_ms: number
  error: string
}

/** 定时任务的接口：tenon generate 为 schedule/scheduleapi 生成的客户端分组，即 api.schedule。 */
export interface ScheduleApi {
  list(): Promise<{ items: ScheduleTaskData[] }>
  run(name: string): Promise<unknown>
  runs(input?: {
    task?: string
    status?: ScheduleStatus
    page?: number
    per_page?: number
  }): Promise<{ items: ScheduleRunData[]; total: number }>
}

// ———————————————————————————— 面板 ————————————————————————————

/** SchedulePanel 的选项。 */
export interface SchedulePanelProps {
  /** 生成的客户端分组：api.schedule。 */
  api: ScheduleApi
  /** 页面的标题，默认“定时任务”（当前语言的）。 */
  title?: ReactNode
}

// 每种执行结果的显示文字的消息 ID 和颜色。
const statusText: Record<ScheduleStatus, { label: UiMessageId; color: string }> = {
  succeeded: { label: "tenon.schedule.succeeded", color: "green" },
  failed: { label: "tenon.schedule.failed", color: "red" },
  skipped: { label: "tenon.schedule.skipped", color: "gray" },
}

/** 跳过的原因（schedule.SkipReason）的说明的消息 ID，其他错误原样显示。 */
const skipText: Record<string, UiMessageId> = {
  overlap: "tenon.schedule.skip_overlap",
  lock_failed: "tenon.schedule.skip_lock_failed",
}

/** 执行记录中错误的显示：跳过的原因换成说明，其他错误原样返回。 */
function errorText(status: ScheduleStatus | undefined, error: string | undefined): string {
  if (!error) return ""
  return status === "skipped" && skipText[error] ? uiText(skipText[error]) : error
}

/** 耗时的显示，例如 320 ms、4.2 秒、3 分 12 秒。 */
function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms} ms`
  if (ms < 60_000) return uiText("tenon.schedule.seconds", { value: (ms / 1000).toFixed(1) })
  const s = Math.round(ms / 1000)
  return uiText("tenon.schedule.minutes_seconds", { minutes: Math.floor(s / 60), seconds: s % 60 })
}

/** 执行结果的标签，手动执行的另加“手动”。 */
function StatusBadge({ status, manual }: { status: ScheduleStatus; manual?: boolean }): ReactNode {
  const t = useText()
  const st = statusText[status]
  return (
    <Group gap={4} wrap="nowrap">
      <Badge color={st?.color ?? "gray"}>{st ? t(st.label) : status}</Badge>
      {manual && (
        <Badge color="gray" variant="outline">
          {t("tenon.schedule.manual")}
        </Badge>
      )}
    </Group>
  )
}

/** 优先返回任务的显示名称，未配置时回退到内部技术名。 */
function taskLabel(t: { name: string; label?: string }): string {
  return t.label || t.name
}

const tasksKey = ["tenon", "schedule", "tasks"]

/**
 * 定时任务面板：
 *
 *	export const Route = createFileRoute("/_app/schedules")({
 *	  staticData: { title: "定时任务", icon: "clock", permission: "jobs:manage" },
 *	  component: () => <SchedulePanel api={api.schedule} />,
 *	})
 */
export function SchedulePanel({ api, title }: SchedulePanelProps): ReactNode {
  const tr = useText()
  const tasks = useQuery({ queryKey: tasksKey, queryFn: () => api.list() })
  const navigate = useNavigate()
  const [task, setTask] = useState<string>()
  // 从任务列表打开某个任务的执行记录。
  const showRuns = (name: string) => {
    setTask(name)
    void navigate({
      to: ".",
      search: ((prev: Record<string, unknown>) => ({ ...prev, tab: "runs" })) as never,
      replace: true,
    })
  }
  const items = tasks.data?.items
  const missed = items?.filter((t) => t.missed) ?? []
  return (
    <Page
      title={title ?? tr("tenon.schedule.title")}
      description={tr("tenon.schedule.description")}
      tabs={[
        {
          value: "tasks",
          label: tr("tenon.schedule.tab_tasks"),
          content: <Tasks api={api} query={tasks} showRuns={showRuns} />,
        },
        {
          value: "runs",
          label: tr("tenon.schedule.tab_runs"),
          content: <Runs api={api} tasks={items ?? []} task={task} setTask={setTask} />,
        },
      ]}
    >
      {missed.length > 0 && (
        <Alert
          color="deep-orange"
          variant="light"
          title={tr("tenon.schedule.missed_title", { count: missed.length })}
          role="alert"
        >
          {tr("tenon.schedule.missed_text", {
            names: missed.map(taskLabel).join(tr("tenon.transfer.list_separator")),
          })}
        </Alert>
      )}
    </Page>
  )
}

// ———————————————————————————— 任务 ————————————————————————————

function Tasks({
  api,
  query,
  showRuns,
}: {
  api: ScheduleApi
  query: ReturnType<typeof useQuery<{ items: ScheduleTaskData[] }>>
  showRuns: (name: string) => void
}): ReactNode {
  const queryClient = useQueryClient()
  const formatDateTime = useFormatDateTime()
  const language = useLanguage()
  const tr = useText()
  const runNow = useMutation({
    mutationFn: ({ name }: { name: string; label: string }) => api.run(name),
    onSuccess: (_, v) => {
      notifySuccess(tr("tenon.schedule.queued", { name: v.label }))
      void queryClient.invalidateQueries({ queryKey: ["tenon", "schedule"] })
    },
    onError: (err) => notifyError(err),
  })
  const columns: Column<ScheduleTaskData>[] = [
    {
      accessor: "name",
      title: tr("tenon.schedule.tab_tasks"),
      render: (t) => {
        const display = taskLabel(t)
        const hasLabel = Boolean(t.label && t.label !== t.name)
        const link = (
          <Anchor
            component="button"
            size="sm"
            fw={hasLabel ? 500 : undefined}
            ff={hasLabel ? undefined : "monospace"}
            ta="left"
            onClick={(e) => {
              e.stopPropagation()
              showRuns(t.name)
            }}
          >
            {display}
          </Anchor>
        )
        return (
          <Stack gap={2} align="flex-start">
            {hasLabel ? (
              <Tooltip label={t.name} openDelay={200} withArrow>
                {link}
              </Tooltip>
            ) : (
              link
            )}
            {t.description && (
              <Text size="xs" c="dimmed">
                {t.description}
              </Text>
            )}
          </Stack>
        )
      },
    },
    {
      accessor: "expr",
      title: tr("tenon.schedule.expr"),
      noWrap: true,
      render: (t) => {
        const described = describeScheduleExpr(t.expr, language, tr)
        if (!described) return <Code>{t.expr}</Code>
        return (
          <Tooltip label={t.expr} openDelay={200} withArrow>
            <Text span size="sm">
              {described}
            </Text>
          </Tooltip>
        )
      },
    },
    {
      accessor: "next_run",
      title: tr("tenon.schedule.next"),
      noWrap: true,
      render: (t) => (
        <Text span size="sm">
          {t.next_run && !t.next_run.startsWith("0001") ? formatDateTime(t.next_run) : "—"}
        </Text>
      ),
    },
    {
      accessor: "last_status",
      title: tr("tenon.schedule.last"),
      noWrap: true,
      render: (t) => {
        if (t.running) return <Badge color="bright-blue">{tr("tenon.schedule.running")}</Badge>
        if (t.missed) return <Badge color="deep-orange">{tr("tenon.schedule.missed")}</Badge>
        if (!t.last_status) return <Text c="dimmed">{tr("tenon.schedule.never_run")}</Text>
        return (
          <Stack gap={2} align="flex-start">
            <StatusBadge status={t.last_status} manual={t.last_manual} />
            <Text size="xs" c="dimmed">
              {t.last_run_at && formatDateTime(t.last_run_at)} · {formatDuration(t.last_duration_ms ?? 0)}
            </Text>
          </Stack>
        )
      },
    },
    {
      accessor: "last_error",
      title: tr("tenon.schedule.last_error"),
      ellipsis: true,
      render: (t) => (
        <Text span size="sm" c={t.last_status === "failed" ? "red" : "dimmed"}>
          {errorText(t.last_status, t.last_error) || "—"}
        </Text>
      ),
    },
    {
      accessor: "_actions",
      title: "",
      textAlign: "right",
      noWrap: true,
      render: (t) => (
        <Group gap={2} justify="flex-end" wrap="nowrap" onClick={(e) => e.stopPropagation()}>
          <Button
            size="compact-sm"
            variant="subtle"
            loading={runNow.isPending && runNow.variables?.name === t.name}
            disabled={t.running}
            onClick={async () => {
              const label = taskLabel(t)
              if (
                await confirm({
                  title: tr("tenon.schedule.run_confirm", { name: label }),
                  message: tr("tenon.schedule.run_message"),
                  confirmLabel: tr("tenon.schedule.run"),
                })
              )
                runNow.mutate({ name: t.name, label })
            }}
          >
            {tr("tenon.schedule.run_now")}
          </Button>
          <Menu position="bottom-end" width={140}>
            <Menu.Target>
              <ActionIcon aria-label={tr("tenon.resource.more")}>
                <IconDots size={16} stroke={1.6} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item onClick={() => showRuns(t.name)}>{tr("tenon.schedule.tab_runs")}</Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </Group>
      ),
    },
  ]
  if (query.isError)
    return (
      <Alert color="red" variant="light" title={tr("tenon.schedule.load_failed")} role="alert">
        {String((query.error as { detail?: string })?.detail ?? query.error)}
      </Alert>
    )
  return (
    <DataTable
      records={query.data?.items ?? []}
      columns={columns}
      rowKey="name"
      loading={query.isPending}
      empty={tr("tenon.schedule.no_tasks")}
      minHeight={120}
    />
  )
}

// ———————————————————————————— 执行记录 ————————————————————————————

function Runs({
  api,
  tasks,
  task,
  setTask,
}: {
  api: ScheduleApi
  tasks: ScheduleTaskData[]
  task?: string
  setTask: (task: string | undefined) => void
}): ReactNode {
  const [status, setStatus] = useState<ScheduleStatus>()
  const [page, setPage] = useState(1)
  const [perPage, setPerPage] = useState(20)
  const [open, setOpen] = useState<ScheduleRunData>()
  const formatDateTime = useFormatDateTime()
  const tr = useText()
  const list = useQuery({
    queryKey: ["tenon", "schedule", "runs", task, status, page, perPage],
    queryFn: () => api.runs({ task, status, page, per_page: perPage }),
    // 换筛选、翻页时总是向服务器读最新的，读取中显示加载状态；不用缓存的旧数据。
    gcTime: 0,
  })
  // 从任务列表带着任务打开时回到第一页。
  const [shownTask, setShownTask] = useState(task)
  if (shownTask !== task) {
    setShownTask(task)
    setPage(1)
  }

  const labelOf = (name: string, explicit?: string) => explicit || tasks.find((t) => t.name === name)?.label || name

  const columns: Column<ScheduleRunData>[] = [
    {
      accessor: "started_at",
      title: tr("tenon.schedule.started_at"),
      noWrap: true,
      width: 120,
      render: (r) => (
        <Text span size="sm">
          {formatDateTime(r.started_at)}
        </Text>
      ),
    },
    {
      accessor: "task",
      title: tr("tenon.schedule.tab_tasks"),
      noWrap: true,
      render: (r) => {
        const label = labelOf(r.task, r.task_label)
        if (label === r.task) {
          return (
            <Text size="sm" ff="monospace">
              {r.task}
            </Text>
          )
        }
        return (
          <Tooltip label={r.task} openDelay={200} withArrow>
            <Text span size="sm">
              {label}
            </Text>
          </Tooltip>
        )
      },
    },
    {
      accessor: "status",
      title: tr("tenon.schedule.result"),
      noWrap: true,
      render: (r) => <StatusBadge status={r.status} manual={r.manual} />,
    },
    {
      accessor: "duration_ms",
      title: tr("tenon.schedule.duration"),
      noWrap: true,
      render: (r) => (r.status === "skipped" ? "—" : formatDuration(r.duration_ms)),
    },
    {
      accessor: "error",
      title: tr("tenon.queue.error"),
      ellipsis: true,
      // 失败的错误可能很长、带堆栈：点它在对话框中看完整的；跳过的原因很短，直接显示。
      render: (r) =>
        r.status === "failed" && r.error ? (
          <CellLink onClick={() => setOpen(r)}>{r.error}</CellLink>
        ) : (
          <Text span size="sm" c="dimmed">
            {errorText(r.status, r.error) || "—"}
          </Text>
        ),
    },
  ]

  const taskFilter = (
    <Select
      value={task ?? ""}
      onChange={(v) => setTask(v || undefined)}
      data={[
        { value: "", label: tr("tenon.schedule.all_tasks") },
        ...tasks.map((t) => ({ value: t.name, label: taskLabel(t) })),
      ]}
      allowDeselect={false}
      w={220}
      aria-label={tr("tenon.schedule.filter_task")}
    />
  )
  const statusFilter = (
    <ScrollArea type="never" offsetScrollbars={false}>
      <SegmentedControl
        value={status ?? ""}
        onChange={(v) => {
          setStatus((v || undefined) as ScheduleStatus | undefined)
          setPage(1)
        }}
        data={[
          { value: "", label: tr("tenon.schedule.all") },
          { value: "failed", label: tr("tenon.schedule.failed") },
          { value: "succeeded", label: tr("tenon.schedule.succeeded") },
          { value: "skipped", label: tr("tenon.schedule.skipped") },
        ]}
        aria-label={tr("tenon.schedule.filter_result")}
      />
    </ScrollArea>
  )

  return (
    <>
      <DataTable
        toolbar={
          <Group gap="xs">
            {taskFilter}
            {statusFilter}
          </Group>
        }
        records={list.data?.items ?? []}
        total={list.data?.total}
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
        empty={task || status ? tr("tenon.schedule.no_matching_runs") : tr("tenon.schedule.no_runs")}
      />
      <Modal
        opened={!!open}
        onClose={() => setOpen(undefined)}
        title={
          open
            ? (() => {
                const label = labelOf(open.task, open.task_label)
                return label !== open.task ? (
                  <Tooltip label={open.task} openDelay={200} withArrow>
                    <Text span inherit>
                      {label}
                    </Text>
                  </Tooltip>
                ) : (
                  open.task
                )
              })()
            : undefined
        }
        size="lg"
      >
        {open && (
          <Stack gap="sm">
            <Group gap="md">
              <StatusBadge status={open.status} manual={open.manual} />
              <Text size="sm" c="dimmed">
                {formatDateTime(open.started_at)}
                {open.status !== "skipped" && ` · ${formatDuration(open.duration_ms)}`}
              </Text>
            </Group>
            {open.error ? (
              <Code block style={{ whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
                {errorText(open.status, open.error)}
              </Code>
            ) : (
              <Text size="sm" c="dimmed">
                {tr("tenon.schedule.no_error")}
              </Text>
            )}
          </Stack>
        )}
      </Modal>
    </>
  )
}
