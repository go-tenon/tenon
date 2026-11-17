// 表格：包装 mantine-datatable，应用不直接依赖它。分页和排序接 shell/ 中的列表模型（保存在地址中），
// 选中接 useSelection（可以跨页）。设置了 storeKey 时，用 mantine-datatable 自带的列设置：拖动表头调整顺序，
// 右键点击表头选择显示的列，拖动表头的边缘调整宽度，设置保存在浏览器中。密度随主题的预设。

import { Box, Button, Group, Stack, Text, useMantineTheme } from "@mantine/core"
import { IconInbox } from "@tabler/icons-react"
import {
  type DataTableColumn,
  type DataTableSortStatus,
  DataTable as MantineDataTable,
  useDataTableColumns,
} from "mantine-datatable"
import type { ReactNode } from "react"
import type { ListState, Selection } from "./shell"
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
  /** 默认隐藏；设置了 storeKey 时，用户可以右键点击表头打开。 */
  hidden?: boolean
  /** 不能隐藏、拖动和调整宽度，例如名称列、操作列。 */
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
  /** 保存列设置的键，例如 "orders"：可以拖动表头调整顺序、右键点击表头选择显示的列、拖动边缘调整宽度，保存在浏览器中。 */
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

  const mapped = (storeKey ? columns : columns.filter((c) => !c.hidden)).map(
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
        ...(storeKey && !c.fixed
          ? { toggleable: true, draggable: true, resizable: true, defaultToggle: !c.hidden }
          : {}),
      }) as DataTableColumn<T>,
  )
  const storeColumnsKey = storeKey ? `tenon:columns:${storeKey}` : undefined
  const { effectiveColumns } = useDataTableColumns<T>({ key: storeColumnsKey, columns: mapped })

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
      {(toolbar || (bulkActions && keys.length > 0)) && (
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
        columns={effectiveColumns}
        storeColumnsKey={storeColumnsKey}
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
