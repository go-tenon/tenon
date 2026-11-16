// 资源表格：服务端分页、排序和筛选，状态保存在地址中（@go-tenon/shell 的 useList），基于 TanStack Table。
// 另有列表工具栏、行操作菜单和状态标签。

import { type ListState, type Selection, useTabActive } from "@go-tenon/shell"
import {
  type ColumnDef,
  flexRender,
  getCoreRowModel,
  type RowSelectionState,
  type SortingState,
  useReactTable,
} from "@tanstack/react-table"
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronLeft, ChevronRight, MoreHorizontal } from "lucide-react"
import { type ReactNode, useEffect, useState } from "react"
import { PageEmpty, PageError, PageLoading } from "@/components/tenon/page"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"

/** DataTable 的选项。 */
export interface DataTableProps<T> {
  columns: ColumnDef<T>[]
  /** 当前页的数据；读取中为 undefined。 */
  data?: T[]
  /** 符合条件的总数，用于分页。 */
  total?: number
  /** 列表的状态：useList(Route)。分页、排序保存在地址中，由服务端完成。 */
  list: ListState<Record<string, unknown>>
  /** 读取失败时的错误和重试。 */
  error?: unknown
  retry?: () => void
  /** 行的标识，默认是 row.id。 */
  rowKey?: (row: T) => string
  /** 选中项（useSelection）：有它时第一列是复选框，用于批量操作。 */
  selection?: Selection<string>
  /** 每一行的操作，例如编辑、删除。 */
  rowActions?: (row: T) => RowAction[]
  /** 表格上方的工具栏，例如 <ListToolbar>。 */
  toolbar?: ReactNode
  empty?: ReactNode
}

/** 行操作。 */
export interface RowAction {
  label: string
  onSelect: () => void
  destructive?: boolean
  disabled?: boolean
}

/** 行操作菜单（“⋯”）。 */
export function RowActions({ actions }: { actions: RowAction[] }): ReactNode {
  if (actions.length === 0) return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" className="size-8" aria-label="操作">
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {actions.map((a) => (
          <DropdownMenuItem
            key={a.label}
            disabled={a.disabled}
            variant={a.destructive ? "destructive" : "default"}
            onSelect={a.onSelect}
          >
            {a.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** 状态标签：tone 决定颜色。文字通常来自生成的 enums.ts 中的显示文字。 */
export function StatusBadge({
  children,
  tone = "default",
}: {
  children: ReactNode
  tone?: "default" | "success" | "warning" | "error" | "muted"
}): ReactNode {
  const tones = {
    default: "",
    success: "border-transparent bg-success text-success-foreground",
    warning: "border-transparent bg-warning text-warning-foreground",
    error: "border-transparent bg-destructive text-white",
    muted: "border-transparent bg-muted text-muted-foreground",
  }
  return (
    <Badge variant={tone === "default" ? "secondary" : "outline"} className={tones[tone]}>
      {children}
    </Badge>
  )
}

/** 列表工具栏：关键词搜索（查询参数 q）和其他筛选、操作。输入停顿后才更新地址。 */
export function ListToolbar({
  list,
  placeholder = "搜索…",
  children,
}: {
  list: ListState<Record<string, unknown>>
  placeholder?: string
  children?: ReactNode
}): ReactNode {
  const q = typeof list.filters.q === "string" ? list.filters.q : ""
  const [value, setValue] = useState(q)
  useEffect(() => setValue(q), [q])
  useEffect(() => {
    if (value === q) return
    const t = setTimeout(() => list.setFilters({ q: value || undefined }), 300)
    return () => clearTimeout(t)
  }, [value, q, list])
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Input value={value} onChange={(e) => setValue(e.target.value)} placeholder={placeholder} className="h-9 w-64" />
      {children}
    </div>
  )
}

/**
 * 资源表格：
 *
 *   const list = useList(Route)
 *   const articles = useQuery({ queryKey: ["article", "list", list.params], queryFn: () => api.article.list(list.params) })
 *   <DataTable columns={columns} data={articles.data?.items} total={articles.data?.total} list={list} error={articles.error} />
 *
 * 点击列头按这一列排序（列的 id 是排序参数），再点一次反向，第三次恢复默认。
 */
export function DataTable<T>({
  columns,
  data,
  total = 0,
  list,
  error,
  retry,
  rowKey = (row) => String((row as { id?: unknown }).id),
  selection,
  rowActions,
  toolbar,
  empty,
}: DataTableProps<T>): ReactNode {
  const active = useTabActive()
  const sorting: SortingState = list.tableState.sorting
  const rowSelection: RowSelectionState = Object.fromEntries([...(selection?.keys ?? [])].map((k) => [k, true]))
  const all: ColumnDef<T>[] = [
    ...(selection
      ? [
          {
            id: "_select",
            header: ({ table }) => (
              <Checkbox
                aria-label="选择本页"
                checked={table.getIsAllPageRowsSelected() || (table.getIsSomePageRowsSelected() && "indeterminate")}
                onCheckedChange={(v) => table.toggleAllPageRowsSelected(Boolean(v))}
              />
            ),
            cell: ({ row }) => (
              <Checkbox
                aria-label="选择"
                checked={row.getIsSelected()}
                onCheckedChange={(v) => row.toggleSelected(Boolean(v))}
              />
            ),
            enableSorting: false,
          } satisfies ColumnDef<T>,
        ]
      : []),
    ...columns,
    ...(rowActions
      ? [
          {
            id: "_actions",
            header: () => null,
            cell: ({ row }) => <RowActions actions={rowActions(row.original)} />,
            enableSorting: false,
          } satisfies ColumnDef<T>,
        ]
      : []),
  ]
  const table = useReactTable({
    data: data ?? [],
    columns: all,
    getRowId: rowKey,
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    manualSorting: true,
    rowCount: total,
    enableSortingRemoval: true,
    state: {
      sorting,
      pagination: { pageIndex: list.tableState.pageIndex, pageSize: list.tableState.pageSize },
      rowSelection,
    },
    onSortingChange: (updater) => {
      const next = typeof updater === "function" ? updater(sorting) : updater
      list.setSort(next[0] ? { field: next[0].id, desc: next[0].desc } : undefined)
    },
    onRowSelectionChange: (updater) => {
      const next = typeof updater === "function" ? updater(rowSelection) : updater
      selection?.set(Object.keys(next).filter((k) => next[k]))
    },
  })
  const pages = Math.max(1, Math.ceil(total / list.perPage))
  return (
    <div className="space-y-3" data-active={active}>
      {toolbar}
      {error ? (
        <PageError error={error} retry={retry} />
      ) : data === undefined ? (
        <PageLoading />
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              {table.getHeaderGroups().map((group) => (
                <TableRow key={group.id}>
                  {group.headers.map((header) => {
                    const sorted = header.column.getIsSorted()
                    return (
                      <TableHead key={header.id}>
                        {header.isPlaceholder ? null : header.column.getCanSort() ? (
                          <button
                            type="button"
                            className="inline-flex items-center gap-1 hover:text-foreground"
                            onClick={header.column.getToggleSortingHandler()}
                          >
                            {flexRender(header.column.columnDef.header, header.getContext())}
                            {sorted === "asc" ? (
                              <ArrowUp className="size-3" />
                            ) : sorted === "desc" ? (
                              <ArrowDown className="size-3" />
                            ) : (
                              <ArrowUpDown className="size-3 opacity-40" />
                            )}
                          </button>
                        ) : (
                          flexRender(header.column.columnDef.header, header.getContext())
                        )}
                      </TableHead>
                    )
                  })}
                </TableRow>
              ))}
            </TableHeader>
            <TableBody>
              {table.getRowModel().rows.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={all.length}>
                    <PageEmpty>{empty}</PageEmpty>
                  </TableCell>
                </TableRow>
              ) : (
                table.getRowModel().rows.map((row) => (
                  <TableRow key={row.id} data-state={row.getIsSelected() ? "selected" : undefined}>
                    {row.getVisibleCells().map((cell) => (
                      <TableCell key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</TableCell>
                    ))}
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      )}
      <div className="flex items-center justify-between text-sm text-muted-foreground">
        <span>
          共 {total} 条{selection && selection.keys.size > 0 ? `，已选 ${selection.keys.size} 条` : ""}
        </span>
        <div className="flex items-center gap-2">
          <span>
            第 {list.page} / {pages} 页
          </span>
          <Button
            variant="outline"
            size="icon"
            className="size-8"
            aria-label="上一页"
            disabled={list.page <= 1}
            onClick={() => list.setPage(list.page - 1)}
          >
            <ChevronLeft />
          </Button>
          <Button
            variant="outline"
            size="icon"
            className="size-8"
            aria-label="下一页"
            disabled={list.page >= pages}
            onClick={() => list.setPage(list.page + 1)}
          >
            <ChevronRight />
          </Button>
        </div>
      </div>
    </div>
  )
}
