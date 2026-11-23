// 表格：包装 mantine-datatable，应用不直接依赖它。分页和排序接 list.ts 中的列表状态（保存在地址中），
// 选中接 useSelection（可以跨页）。设置了 storeKey 时，用 mantine-datatable 自带的列设置：拖动表头的边缘调整宽度，
// 在表格右上方的“列设置”菜单（或右键点击表头）中选择显示的列、恢复默认，设置保存在浏览器中。列的顺序就是代码中的顺序。
// 密度随主题的预设。

import {
  ActionIcon,
  Anchor,
  Box,
  Button,
  Checkbox,
  Divider,
  Group,
  Popover,
  Stack,
  Text,
  Tooltip,
  useMantineTheme,
} from "@mantine/core"
import { IconChevronRight, IconInbox, IconSettings } from "@tabler/icons-react"
import {
  type DataTableColumn,
  type DataTableSortStatus,
  DataTable as MantineDataTable,
  useDataTableColumns,
} from "mantine-datatable"
import { type ReactNode, type RefObject, useEffect, useLayoutEffect, useRef, useState } from "react"
import { useText } from "../../i18n/i18n"
import { layoutMetrics } from "../../theme/theme"
import type { ListState, Selection } from "./list"

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
  /** 默认隐藏；设置了 storeKey 时，用户可以在“列设置”菜单中打开。 */
  hidden?: boolean
  /** 不能隐藏和调整宽度，例如名称列、操作列。 */
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
   * 所以 useSelection 的 resetOn 通常是 list.filters：筛选条件变化时清空选中。
   */
  selection?: Selection<string>
  /** 有行被选中时显示在表格上方的批量操作，例如“批量删除”。 */
  bulkActions?: (keys: string[]) => ReactNode
  /** 展开的内容：每行开头一个箭头按钮，点它展开、收起。点击行的其他地方没有反应，文字可以照常选中、复制。 */
  expand?: (row: T) => ReactNode
  /**
   * 保存列设置的键，例如 "orders"：可以拖动表头的边缘调整宽度，在表格右上方的“列设置”菜单
   * （或右键点击表头）中选择显示的列、恢复默认，保存在浏览器中。
   */
  storeKey?: string
  /** 表格上方左侧的内容，例如筛选条件。 */
  toolbar?: ReactNode
  /** 没有数据时的说明，默认“暂无数据”（当前语言的）。 */
  empty?: ReactNode
  /** 表格的最小高度（像素），没有数据时撑开空状态，默认 160。 */
  minHeight?: number
}

/** 把 sort 参数（-created）转换为表格的排序状态。 */
function sortStatus<T>(sort: string | undefined): DataTableSortStatus<T> | undefined {
  if (!sort) return undefined
  const desc = sort.startsWith("-")
  return { columnAccessor: desc ? sort.slice(1) : sort, direction: desc ? "desc" : "asc" }
}

/** 列标识的短哈希（djb2，base36），用在列设置的键中。 */
function columnsHash(accessors: string[]): string {
  let h = 5381
  for (const ch of accessors.join(",")) h = (h * 33 + (ch.codePointAt(0) ?? 0)) >>> 0
  return h.toString(36)
}

/** 把 rowKey 选项转换为取行标识的函数。 */
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
  const t = useText()
  const m = layoutMetrics(useMantineTheme())
  const dense = m.preset === "compact"
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
        ...(storeKey && !c.fixed ? { toggleable: true, resizable: true, defaultToggle: !c.hidden } : {}),
      }) as DataTableColumn<T>,
  )
  // 展开的行只由每行开头的箭头按钮切换，点击行不展开（行里的文字要能选中、复制）。
  // 箭头这一列也交给 useDataTableColumns：带列设置时，不在其中的列会让表格和列设置互相更新、反复渲染。
  const [expanded, setExpanded] = useState<string[]>([])
  if (props.expand)
    mapped.unshift({
      accessor: "_expand",
      title: "",
      width: 40,
      render: (row: T) => {
        const k = key(row)
        const open = expanded.includes(k)
        return (
          <ActionIcon
            size="sm"
            variant="subtle"
            color="gray"
            aria-label={open ? t("tenon.common.collapse") : t("tenon.common.expand")}
            aria-expanded={open}
            onClick={() => setExpanded((cur) => (cur.includes(k) ? cur.filter((x) => x !== k) : [...cur, k]))}
          >
            <IconChevronRight
              size={16}
              stroke={1.6}
              style={{ transform: open ? "rotate(90deg)" : undefined, transition: "transform 120ms ease" }}
            />
          </ActionIcon>
        )
      },
    } as DataTableColumn<T>)
  // 列设置的键带上全部列的标识。mantine-datatable 总会保存列的顺序，保存的顺序中没有的列排到最后（新加的列会跑到操作列后面），
  // 所以列增减、改名后换一个键，按代码中的顺序重新开始，并删除旧键的设置。
  const storePrefix = storeKey ? `tenon:columns:${storeKey}:` : undefined
  const storeColumnsKey = storePrefix ? storePrefix + columnsHash(mapped.map((c) => String(c.accessor))) : undefined
  const columnSettings = useDataTableColumns<T>({ key: storeColumnsKey, columns: mapped })
  const { effectiveColumns } = columnSettings
  useEffect(() => {
    if (!storePrefix || !storeColumnsKey) return
    for (const k of Object.keys(localStorage)) {
      if (k.startsWith(storePrefix) && !k.startsWith(`${storeColumnsKey}-`)) localStorage.removeItem(k)
    }
  }, [storePrefix, storeColumnsKey])

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
          recordsPerPageLabel: t("tenon.table.per_page"),
          paginationText: ({ from, to, totalRecords }: { from: number; to: number; totalRecords: number }) =>
            t("tenon.table.range", { from, to, total: totalRecords }),
          paginationSize: m.controlSize,
          loadingText: t("tenon.common.loading"),
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
  // 不在读取中时记下表格的高度。
  const table = useRef<HTMLTableElement>(null) as RefObject<HTMLTableElement>
  const height = useRef(0)
  useLayoutEffect(() => {
    if (!props.loading && table.current) height.current = table.current.offsetHeight
  })

  return (
    <Stack gap="sm">
      {(toolbar || (bulkActions && keys.length > 0) || storeKey) && (
        <Group justify="space-between" gap="sm" wrap="nowrap" align="flex-end">
          <Box style={{ flex: 1, minWidth: 0 }}>
            {bulkActions && keys.length > 0 ? (
              <Group gap="sm">
                <Text size="sm" fw={500}>
                  {t("tenon.table.selected", { count: keys.length })}
                </Text>
                {bulkActions(keys)}
                <Button variant="subtle" color="gray" size={m.controlSize} onClick={() => selection?.clear()}>
                  {t("tenon.table.clear_selection")}
                </Button>
              </Group>
            ) : (
              toolbar
            )}
          </Box>
          {storeKey && (
            <ColumnSettings
              columns={columns}
              toggles={columnSettings.columnsToggle}
              onToggle={(accessor, toggled) =>
                columnSettings.setColumnsToggle(
                  columnSettings.columnsToggle.map((c) => (c.accessor === accessor ? { ...c, toggled } : c)),
                )
              }
              onReset={() => {
                columnSettings.resetColumnsToggle()
                columnSettings.resetColumnsWidth()
              }}
              size={m.controlSize}
            />
          )}
        </Group>
      )}
      <MantineDataTable<T>
        tableRef={table}
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
        // 读取中（例如换了筛选、翻页）保持原来的高度，加载的遮罩盖在上面，表格不会忽高忽低。
        minHeight={records.length === 0 ? (props.loading && height.current) || (props.minHeight ?? 160) : undefined}
        records={records as T[]}
        columns={effectiveColumns}
        storeColumnsKey={storeColumnsKey}
        idAccessor={key as never}
        fetching={props.loading}
        // 放不下时显示横向滚动条：窄屏上提示表格还可以左右滚动。
        scrollAreaProps={{ type: "auto" }}
        loaderSize={22}
        loaderBackgroundBlur={1.5}
        noRecordsText={typeof props.empty === "string" ? props.empty : t("tenon.table.empty")}
        noRecordsIcon={
          <Box c="dimmed" mb={6}>
            <IconInbox size={28} stroke={1.4} />
          </Box>
        }
        rowExpansion={
          props.expand
            ? {
                trigger: "never",
                allowMultiple: true,
                expanded: { recordIds: expanded, onRecordIdsChange: setExpanded as never },
                content: ({ record }) => props.expand?.(record),
              }
            : undefined
        }
        {...(selectionProps as object)}
        {...(sortProps as object)}
        {...(paginationProps as object)}
      />
    </Stack>
  )
}

/** ColumnSettings 的属性。 */
interface ColumnSettingsProps<T> {
  columns: Column<T>[] // 表格的全部列，用来取列的标题
  toggles: { accessor: string; toggleable: boolean; toggled: boolean }[] // 每一列是否可以隐藏、是否显示
  onToggle(accessor: string, toggled: boolean): void // 勾选或取消一列
  onReset(): void // 恢复默认：显示的列和宽度都回到代码中的设置
  size: string // 按钮的尺寸，与表格上方的其他控件相同
}

/**
 * ColumnSettings 是表格右上方的“列设置”按钮：点开后勾选显示的列，底部的“恢复默认”把显示的列和宽度
 * 都恢复为代码中的设置。固定的列（名称、操作）一直显示，不在其中。
 */
function ColumnSettings<T>({ columns, toggles, onToggle, onReset, size }: ColumnSettingsProps<T>): ReactNode {
  const [opened, setOpened] = useState(false)
  const t = useText()
  const titles = new Map(columns.map((c) => [c.accessor, c.title ?? c.accessor]))
  return (
    <Popover opened={opened} onChange={setOpened} position="bottom-end" shadow="md" width={200}>
      <Popover.Target>
        <Tooltip label={t("tenon.table.columns")} disabled={opened}>
          <ActionIcon
            variant="default"
            size={size === "xs" ? "md" : "lg"}
            aria-label={t("tenon.table.columns")}
            aria-expanded={opened}
            onClick={() => setOpened((o) => !o)}
          >
            <IconSettings size={16} stroke={1.6} />
          </ActionIcon>
        </Tooltip>
      </Popover.Target>
      <Popover.Dropdown p="xs">
        <Stack gap={8} px={4} py={4}>
          <Text size="xs" c="dimmed">
            {t("tenon.table.visible_columns")}
          </Text>
          {toggles
            .filter((c) => c.toggleable)
            .map((c) => (
              <Checkbox
                key={c.accessor}
                size="xs"
                label={titles.get(c.accessor)}
                checked={c.toggled}
                onChange={(e) => onToggle(c.accessor, e.currentTarget.checked)}
              />
            ))}
        </Stack>
        <Divider my="xs" />
        <Button
          variant="subtle"
          color="gray"
          size="xs"
          fullWidth
          justify="flex-start"
          onClick={() => {
            onReset()
            setOpened(false)
          }}
        >
          {t("tenon.table.reset_columns")}
        </Button>
      </Popover.Dropdown>
    </Popover>
  )
}

/**
 * 表格中打开详情的链接，例如错误摘要、订单号：过长时省略。表格的行不能点击（用户要能选中、复制各列的内容），
 * 打开详情、对话框的入口用它或操作列中的按钮。
 *
 *	{ accessor: "error", title: "错误", ellipsis: true, render: (j) => <CellLink onClick={() => setOpen(j.id)}>{j.error}</CellLink> }
 */
export function CellLink({ onClick, children }: { onClick: () => void; children: ReactNode }): ReactNode {
  return (
    <Anchor
      component="button"
      type="button"
      size="sm"
      onClick={onClick}
      style={{
        display: "block",
        maxWidth: "100%",
        overflow: "hidden",
        textOverflow: "ellipsis",
        whiteSpace: "nowrap",
        textAlign: "left",
      }}
    >
      {children}
    </Anchor>
  )
}
