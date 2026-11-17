// 表格：包装 mantine-datatable，应用不直接依赖它。分页和排序接 @go-tenon/shell 的列表模型（保存在地址中），
// 选中接 useSelection（可以跨页），列的显隐和顺序可以保存在浏览器中。密度随主题的预设。

import type { ListState, Selection } from "@go-tenon/shell"
import { ActionIcon, Box, Button, Checkbox, Group, Popover, Stack, Text, Tooltip, useMantineTheme } from "@mantine/core"
import { useLocalStorage } from "@mantine/hooks"
import { IconArrowDown, IconArrowUp, IconColumns3, IconInbox } from "@tabler/icons-react"
import { type DataTableColumn, type DataTableSortStatus, DataTable as MantineDataTable } from "mantine-datatable"
import type { ReactNode } from "react"
import { layoutMetrics } from "./theme"

/** 一列。 */
export interface Column<T> {
  /** 字段名，也是排序参数和列设置中的标识；可以用 a.b 读取嵌套的字段。 */
  accessor: string
  title?: ReactNode
  /** 可以按这一列排序：点击表头时把 accessor 交给列表的 sort（-accessor 表示倒序）。 */
  sortable?: boolean
  /** 单元格的内容；默认显示字段的值。 */
  render?: (row: T, index: number) => ReactNode
  width?: number | string
  textAlign?: "left" | "center" | "right"
  /** 不换行。 */
  noWrap?: boolean
  /** 超出时显示省略号。 */
  ellipsis?: boolean
  /** 默认隐藏，用户可以在列设置中打开（需要 storeKey）。 */
  hidden?: boolean
  /** 不能在列设置中隐藏，例如名称列、操作列。 */
  fixed?: boolean
}

/** 分页和排序：useList 的返回值中用到的部分。 */
export type TableList = Pick<
  ListState<Record<string, unknown>>,
  "page" | "perPage" | "sort" | "setPage" | "setPerPage" | "setSort"
>

/** DataTable 的选项。 */
export interface DataTableProps<T> {
  records: readonly T[]
  columns: Column<T>[]
  /** 一行的标识：字段名或函数，默认 id。选中和展开靠它区分行。 */
  rowKey?: string | ((row: T) => string)
  /** 读取中：显示加载的遮罩。 */
  loading?: boolean
  /** 总条数；没有时不显示分页（例如游标分页）。 */
  total?: number
  /** 分页和排序，通常是 useList(Route) 的返回值。 */
  list?: TableList
  /** 每页条数的选项，默认 10、20、50、100。 */
  perPageOptions?: number[]
  /**
   * 选中的行，useSelection 的返回值：显示勾选框。翻页时保留其他页的选中，
   * 所以 useSelection 的 resetOn 通常是 list.filters 而不是 list.params。
   */
  selection?: Selection<string>
  /** 有行被选中时显示在表格上方的批量操作，例如“批量删除”。 */
  bulkActions?: (keys: string[]) => ReactNode
  /** 点击行展开的内容。 */
  expand?: (row: T) => ReactNode
  /** 保存列的显隐和顺序的键，例如 "orders"：显示列设置按钮，设置保存在浏览器中。 */
  storeKey?: string
  /** 表格上方左侧的内容，例如筛选条件。 */
  toolbar?: ReactNode
  /** 没有数据时的说明，默认“暂无数据”。 */
  empty?: ReactNode
  onRowClick?: (row: T) => void
  /** 表格的最小高度（像素），没有数据时撑开空状态，默认 160。 */
  minHeight?: number
}

/** 把 sort 参数（-created）转换为表格的排序状态。 */
function sortStatus<T>(sort: string | undefined): DataTableSortStatus<T> | undefined {
  if (!sort) return undefined
  const desc = sort.startsWith("-")
  return { columnAccessor: desc ? sort.slice(1) : sort, direction: desc ? "desc" : "asc" }
}

function keyOf<T>(rowKey: DataTableProps<T>["rowKey"]): (row: T) => string {
  if (typeof rowKey === "function") return rowKey
  const field = rowKey ?? "id"
  return (row) => String((row as Record<string, unknown>)[field] ?? "")
}

/**
 * 表格：
 *
 *   const list = useList(Route)
 *   const orders = useQuery(queries.order.list(list.params))
 *   <DataTable records={orders.data?.items ?? []} total={orders.data?.total} loading={orders.isPending} list={list} columns={columns} />
 */
export function DataTable<T>(props: DataTableProps<T>): ReactNode {
  const { records, columns, list, selection, storeKey, toolbar, bulkActions } = props
  const m = layoutMetrics(useMantineTheme())
  const dense = m.preset === "admin"
  const key = keyOf(props.rowKey)

  const [prefs, setPrefs] = useColumnPrefs(storeKey)
  const visible = arrange(columns, prefs).filter((c) =>
    prefs.hidden ? !prefs.hidden.includes(c.accessor) || c.fixed : !c.hidden,
  )
  const effective = visible.map(
    (c) =>
      ({
        accessor: c.accessor,
        title: c.title,
        sortable: c.sortable,
        render: c.render ? (row: T, index: number) => c.render?.(row, index) : undefined,
        width: c.width,
        textAlign: c.textAlign,
        // ellipsis 和 noWrap 只能二选一。
        ...(c.ellipsis ? { ellipsis: true } : { noWrap: c.noWrap }),
      }) as DataTableColumn<T>,
  )

  const selected = selection ? records.filter((r) => selection.has(key(r))) : undefined
  const selectionProps = selection
    ? {
        selectedRecords: selected,
        onSelectedRecordsChange: (rows: T[]) => {
          // 当前页之外的选中保持不变。
          const here = new Set(records.map(key))
          const keep = [...selection.keys].filter((k) => !here.has(k))
          selection.set([...keep, ...rows.map(key)])
        },
      }
    : {}

  const paginationProps =
    list && props.total !== undefined
      ? {
          page: list.page,
          onPageChange: list.setPage,
          totalRecords: props.total,
          recordsPerPage: list.perPage,
          recordsPerPageOptions: props.perPageOptions ?? [10, 20, 50, 100],
          onRecordsPerPageChange: list.setPerPage,
          recordsPerPageLabel: "每页",
          paginationText: ({ from, to, totalRecords }: { from: number; to: number; totalRecords: number }) =>
            `${from}–${to}，共 ${totalRecords} 条`,
          paginationSize: m.controlSize,
          loadingText: "加载中…",
        }
      : {}

  const sortProps = list
    ? {
        sortStatus: sortStatus<T>(list.sort) ?? { columnAccessor: "", direction: "asc" as const },
        onSortStatusChange: (s: DataTableSortStatus<T>) =>
          list.setSort(
            s.columnAccessor ? `${s.direction === "desc" ? "-" : ""}${String(s.columnAccessor)}` : undefined,
          ),
      }
    : {}

  const keys = selection ? [...selection.keys] : []
  return (
    <Stack gap="sm">
      {(toolbar || storeKey || (bulkActions && keys.length > 0)) && (
        <Group justify="space-between" gap="sm" wrap="nowrap" align="flex-end">
          <Box style={{ flex: 1, minWidth: 0 }}>
            {bulkActions && keys.length > 0 ? (
              <Group gap="sm">
                <Text size="sm" fw={500}>
                  已选 {keys.length} 项
                </Text>
                {bulkActions(keys)}
                <Button variant="subtle" color="gray" size={m.controlSize} onClick={() => selection?.clear()}>
                  取消选择
                </Button>
              </Group>
            ) : (
              toolbar
            )}
          </Box>
          {storeKey && <ColumnsMenu columns={columns} prefs={prefs} onChange={setPrefs} />}
        </Group>
      )}
      <MantineDataTable<T>
        className="tn-datatable"
        withTableBorder
        borderRadius="md"
        borderColor="var(--tn-border)"
        rowBorderColor="var(--tn-border)"
        highlightOnHover
        verticalAlign="center"
        verticalSpacing={dense ? 6 : "sm"}
        horizontalSpacing={dense ? "sm" : "md"}
        fz="sm"
        minHeight={records.length === 0 ? (props.minHeight ?? 160) : undefined}
        records={records as T[]}
        columns={effective}
        idAccessor={key as never}
        fetching={props.loading}
        loaderBackgroundBlur={1}
        noRecordsText={typeof props.empty === "string" ? props.empty : "暂无数据"}
        noRecordsIcon={
          <Box c="dimmed" mb={6}>
            <IconInbox size={28} stroke={1.4} />
          </Box>
        }
        onRowClick={props.onRowClick ? ({ record }) => props.onRowClick?.(record) : undefined}
        rowExpansion={
          props.expand ? { allowMultiple: true, content: ({ record }) => props.expand?.(record) } : undefined
        }
        {...(selectionProps as object)}
        {...(sortProps as object)}
        {...(paginationProps as object)}
      />
    </Stack>
  )
}

/** 保存在浏览器中的列设置：隐藏的列和顺序；没有保存过时为空，按列的定义显示。 */
interface ColumnPrefs {
  hidden?: string[]
  order?: string[]
}

function useColumnPrefs(key: string | undefined): [ColumnPrefs, (p: ColumnPrefs) => void] {
  const [prefs, setPrefs] = useLocalStorage<ColumnPrefs>({
    key: `tenon:columns:${key ?? ""}`,
    defaultValue: {},
    getInitialValueInEffect: false,
  })
  return key ? [prefs, setPrefs] : [{}, () => {}]
}

/** 按保存的顺序排列列：没有保存过的列（例如新增的列）排在原来的位置之后。 */
function arrange<T>(columns: Column<T>[], prefs: ColumnPrefs): Column<T>[] {
  if (!prefs.order) return columns
  const rank = new Map(prefs.order.map((a, i) => [a, i]))
  return [...columns].sort((a, b) => (rank.get(a.accessor) ?? 1e6) - (rank.get(b.accessor) ?? 1e6))
}

/** 列设置：勾选显示的列，上下移动调整顺序，恢复默认。固定的列不能隐藏和移动。 */
function ColumnsMenu<T>({
  columns,
  prefs,
  onChange,
}: {
  columns: Column<T>[]
  prefs: ColumnPrefs
  onChange: (p: ColumnPrefs) => void
}): ReactNode {
  const ordered = arrange(columns, prefs).filter((c) => !c.fixed)
  const hidden = prefs.hidden ?? columns.filter((c) => c.hidden).map((c) => c.accessor)
  const move = (index: number, delta: number) => {
    const next = ordered.map((c) => c.accessor)
    const [item] = next.splice(index, 1)
    next.splice(index + delta, 0, item)
    onChange({ ...prefs, hidden, order: next })
  }
  return (
    <Popover position="bottom-end" width={240}>
      <Popover.Target>
        <Tooltip label="列设置">
          <ActionIcon variant="default" size="lg" aria-label="列设置">
            <IconColumns3 size={16} stroke={1.6} />
          </ActionIcon>
        </Tooltip>
      </Popover.Target>
      <Popover.Dropdown>
        <Text size="xs" c="dimmed" mb="xs">
          显示的列
        </Text>
        <Stack gap={4}>
          {ordered.map((c, i) => (
            <Group key={c.accessor} gap={4} wrap="nowrap" justify="space-between">
              <Checkbox
                label={c.title ?? c.accessor}
                checked={!hidden.includes(c.accessor)}
                onChange={(e) =>
                  onChange({
                    ...prefs,
                    hidden: e.currentTarget.checked ? hidden.filter((h) => h !== c.accessor) : [...hidden, c.accessor],
                  })
                }
              />
              <Group gap={0} wrap="nowrap">
                <ActionIcon
                  size="sm"
                  aria-label={`上移 ${String(c.title ?? c.accessor)}`}
                  disabled={i === 0}
                  onClick={() => move(i, -1)}
                >
                  <IconArrowUp size={14} />
                </ActionIcon>
                <ActionIcon
                  size="sm"
                  aria-label={`下移 ${String(c.title ?? c.accessor)}`}
                  disabled={i === ordered.length - 1}
                  onClick={() => move(i, 1)}
                >
                  <IconArrowDown size={14} />
                </ActionIcon>
              </Group>
            </Group>
          ))}
        </Stack>
        <Button variant="subtle" size="xs" mt="sm" px={0} onClick={() => onChange({})}>
          恢复默认
        </Button>
      </Popover.Dropdown>
    </Popover>
  )
}
