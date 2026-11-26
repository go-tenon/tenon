import { MantineProvider } from "@mantine/core"
import type { ReactNode } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { describe, expect, it } from "vitest"
import { type Column, DataTable } from "./data-table"

interface Row {
  id: string
  name: string
  amount: number
  note: string
}

const rows: Row[] = [{ id: "1", name: "A", amount: 100, note: "备注" }]

const columns: Column<Row>[] = [
  { accessor: "name", title: "名称", fixed: true },
  { accessor: "amount", title: "金额" },
  { accessor: "note", title: "备注" },
]

function render(node: ReactNode): string {
  return renderToStaticMarkup(<MantineProvider>{node}</MantineProvider>)
}

describe("DataTable", () => {
  it("默认关闭列宽调整，传入 resizable 后才为非 fixed 列启用", () => {
    const off = render(<DataTable records={rows} columns={columns} storeKey="orders" />)
    expect(off).not.toContain("mantine-datatable-header-cell-resizable")
    expect(off).not.toContain("mantine-datatable-header-resizable-handle")
    // 关闭列宽调整时仍保留列设置按钮。
    expect(off).toContain('aria-label="列设置"')

    const on = render(<DataTable records={rows} columns={columns} storeKey="orders" resizable />)
    expect(on).toContain("mantine-datatable-header-cell-resizable")
    expect(on).toContain("mantine-datatable-header-resizable-handle")
    const nameHeader = on.match(/<th [^>]*data-accessor="name"[^>]*>/)?.[0]
    const amountHeader = on.match(/<th [^>]*data-accessor="amount"[^>]*>/)?.[0]
    expect(nameHeader).toBeDefined()
    expect(nameHeader).not.toContain("mantine-datatable-header-cell-resizable")
    expect(amountHeader).toContain("mantine-datatable-header-cell-resizable")

    // 不传 storeKey 时单独开启 resizable 同样生效。
    const withoutStore = render(<DataTable records={rows} columns={columns} resizable />)
    expect(withoutStore).toContain("mantine-datatable-header-resizable-handle")
    expect(withoutStore).not.toContain('aria-label="列设置"')
  })
})
