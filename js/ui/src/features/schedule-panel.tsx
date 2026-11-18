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
} from "@mantine/core"
import { IconDots } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { type ReactNode, useState } from "react"
import { CellLink, type Column, DataTable } from "../components/data-table/data-table"
import { confirm, notifyError, notifySuccess } from "../components/feedback"
import { Page } from "../components/page"
import { useFormatDateTime } from "../theme/format"

// ———————————————————————————— 接口 ————————————————————————————

/** 一次执行的结果。 */
export type ScheduleStatus = "succeeded" | "failed" | "skipped"

/** 任务列表中的一个任务：scheduleapi.ScheduleTask。 */
export interface ScheduleTaskData {
  name: string
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
  /** 页面的标题，默认“定时任务”。 */
  title?: ReactNode
}

const statusText: Record<ScheduleStatus, { label: string; color: string }> = {
  succeeded: { label: "成功", color: "green" },
  failed: { label: "失败", color: "red" },
  skipped: { label: "跳过", color: "gray" },
}

/** 跳过的原因（schedule.SkipReason）的说明，其他错误原样显示。 */
const skipText: Record<string, string> = {
  overlap: "上一次还没结束",
  lock_failed: "锁存储出错，无法确认由哪个实例执行",
}

function errorText(status: ScheduleStatus | undefined, error: string | undefined): string {
  if (!error) return ""
  return status === "skipped" ? (skipText[error] ?? error) : error
}

/** 耗时的显示，例如 320 ms、4.2 秒、3 分 12 秒。 */
function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} 秒`
  const s = Math.round(ms / 1000)
  return `${Math.floor(s / 60)} 分 ${s % 60} 秒`
}

function StatusBadge({ status, manual }: { status: ScheduleStatus; manual?: boolean }): ReactNode {
  const st = statusText[status] ?? { label: status, color: "gray" }
  return (
    <Group gap={4} wrap="nowrap">
      <Badge color={st.color}>{st.label}</Badge>
      {manual && (
        <Badge color="gray" variant="outline">
          手动
        </Badge>
      )}
    </Group>
  )
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
export function SchedulePanel({ api, title = "定时任务" }: SchedulePanelProps): ReactNode {
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
      title={title}
      description="按表达式在后台定时执行的任务。可以立即执行一次，执行记录保留一段时间。"
      tabs={[
        {
          value: "tasks",
          label: "任务",
          content: <Tasks api={api} query={tasks} showRuns={showRuns} />,
        },
        {
          value: "runs",
          label: "执行记录",
          content: <Runs api={api} tasks={items ?? []} task={task} setTask={setTask} />,
        },
      ]}
    >
      {missed.length > 0 && (
        <Alert color="deep-orange" variant="light" title={`${missed.length} 个任务错过了上一次执行`} role="alert">
          {missed.map((t) => t.name).join("、")}
          ：应当执行的时间已经过去，却没有执行记录，通常是调度器停止或正在部署。下一次会按时执行，需要补上时点“立即执行”。
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
  const runNow = useMutation({
    mutationFn: (name: string) => api.run(name),
    onSuccess: (_, name) => {
      notifySuccess(`已交给后台执行 ${name}，结果见执行记录`)
      void queryClient.invalidateQueries({ queryKey: ["tenon", "schedule"] })
    },
    onError: (err) => notifyError(err),
  })
  const columns: Column<ScheduleTaskData>[] = [
    {
      accessor: "name",
      title: "任务",
      render: (t) => (
        <Stack gap={2}>
          <Anchor
            component="button"
            size="sm"
            ff="monospace"
            ta="left"
            onClick={(e) => {
              e.stopPropagation()
              showRuns(t.name)
            }}
          >
            {t.name}
          </Anchor>
          {t.description && (
            <Text size="xs" c="dimmed">
              {t.description}
            </Text>
          )}
        </Stack>
      ),
    },
    { accessor: "expr", title: "表达式", noWrap: true, render: (t) => <Code>{t.expr}</Code> },
    {
      accessor: "next_run",
      title: "下一次",
      noWrap: true,
      render: (t) => (
        <Text span size="sm">
          {t.next_run && !t.next_run.startsWith("0001") ? formatDateTime(t.next_run) : "—"}
        </Text>
      ),
    },
    {
      accessor: "last_status",
      title: "上一次",
      noWrap: true,
      render: (t) => {
        if (t.running) return <Badge color="bright-blue">执行中</Badge>
        if (t.missed) return <Badge color="deep-orange">错过</Badge>
        if (!t.last_status) return <Text c="dimmed">还没执行过</Text>
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
      title: "上一次的错误",
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
            loading={runNow.isPending && runNow.variables === t.name}
            disabled={t.running}
            onClick={async () => {
              if (
                await confirm({
                  title: `立即执行 ${t.name}？`,
                  message: "在后台执行一次，不影响按表达式的下一次执行。正在执行时这次不执行。",
                  confirmLabel: "执行",
                })
              )
                runNow.mutate(t.name)
            }}
          >
            立即执行
          </Button>
          <Menu position="bottom-end" width={140}>
            <Menu.Target>
              <ActionIcon aria-label="更多操作">
                <IconDots size={16} stroke={1.6} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item onClick={() => showRuns(t.name)}>执行记录</Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </Group>
      ),
    },
  ]
  if (query.isError)
    return (
      <Alert color="red" variant="light" title="读取定时任务失败" role="alert">
        {String((query.error as { detail?: string })?.detail ?? query.error)}
      </Alert>
    )
  return (
    <DataTable
      records={query.data?.items ?? []}
      columns={columns}
      rowKey="name"
      loading={query.isPending}
      empty="还没有定时任务"
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

  const columns: Column<ScheduleRunData>[] = [
    {
      accessor: "started_at",
      title: "开始时间",
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
      title: "任务",
      noWrap: true,
      render: (r) => (
        <Text size="sm" ff="monospace">
          {r.task}
        </Text>
      ),
    },
    {
      accessor: "status",
      title: "结果",
      noWrap: true,
      render: (r) => <StatusBadge status={r.status} manual={r.manual} />,
    },
    {
      accessor: "duration_ms",
      title: "耗时",
      noWrap: true,
      render: (r) => (r.status === "skipped" ? "—" : formatDuration(r.duration_ms)),
    },
    {
      accessor: "error",
      title: "错误",
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
      data={[{ value: "", label: "全部任务" }, ...tasks.map((t) => ({ value: t.name, label: t.name }))]}
      allowDeselect={false}
      w={220}
      aria-label="按任务筛选"
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
          { value: "", label: "全部" },
          { value: "failed", label: "失败" },
          { value: "succeeded", label: "成功" },
          { value: "skipped", label: "跳过" },
        ]}
        aria-label="按结果筛选"
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
        empty={task || status ? "没有符合条件的执行记录" : "还没有执行记录"}
      />
      <Modal opened={!!open} onClose={() => setOpen(undefined)} title={open?.task} size="lg">
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
                没有错误
              </Text>
            )}
          </Stack>
        )}
      </Modal>
    </>
  )
}
