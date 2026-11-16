// 资源的 shadcn 外观：DataTable（TanStack Table）渲染列表，对话框中的 react-hook-form 渲染创建和修改的表单。
// 模型来自 @go-tenon/shell 的 resourceModel：列、筛选条件、表单字段、权限和接口都在定义中，页面只写需要覆盖的部分。

import { describeError } from "@go-tenon/client"
import {
  formValues,
  type ListRoute,
  type ResourceField,
  type ResourceModel,
  type ResourceRow,
  useResourceList,
  useResourceMutations,
} from "@go-tenon/shell"
import type { ColumnDef } from "@tanstack/react-table"
import { Plus } from "lucide-react"
import { type ReactNode, useEffect, useMemo, useState } from "react"
import { Controller, type FieldValues, type UseFormReturn } from "react-hook-form"
import { DataTable, type RowAction } from "@/components/tenon/data-table"
import { useConfirm } from "@/components/tenon/dialogs"
import { Field, FormError, useApiForm } from "@/components/tenon/form"
import { PageHeader } from "@/components/tenon/page"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"

/** 一个字段的输入组件。id 要放在控件上，标签才能指向它。 */
export type FieldInput = (props: {
  field: ResourceField
  id: string
  value: unknown
  onChange: (v: unknown) => void
  invalid?: boolean
}) => ReactNode

/** ResourcePage 的选项：没有覆盖时全部按定义渲染。 */
export interface ResourcePageProps {
  model: ResourceModel
  /** 列表的路由：validateSearch 是 model.listSearch。 */
  route: ListRoute<Record<string, unknown>>
  /** 覆盖某一列的显示。 */
  render?: { [field: string]: (value: unknown, row: ResourceRow) => ReactNode }
  /** 覆盖某个表单字段的输入组件。 */
  inputs?: { [field: string]: FieldInput }
  /** 行上的额外操作，排在定义中的操作之后。 */
  rowActions?: (row: ResourceRow) => RowAction[]
  /** 页头右侧的额外按钮，排在“新建”之前。 */
  actions?: ReactNode
}

const ALL = "__all__"

const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" })
const dateOnly = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" })

/** 按字段的类型显示一个值：枚举显示文字，布尔显示是否，时间按本地格式，标签逐个显示。 */
export function FieldValue({
  model,
  field,
  value,
}: {
  model: ResourceModel
  field: ResourceField
  value: unknown
}): ReactNode {
  if (value === undefined || value === null || value === "") return <span className="text-muted-foreground">-</span>
  switch (field.kind) {
    case "boolean":
      return value ? <Badge>是</Badge> : <Badge variant="outline">否</Badge>
    case "select":
      return <Badge variant="secondary">{String(model.display(field, value))}</Badge>
    case "multiselect":
    case "tags":
      return (
        <span className="flex flex-wrap gap-1">
          {((model.display(field, value) as unknown[]) ?? []).map((v) => (
            <Badge key={String(v)} variant="outline">
              {String(v)}
            </Badge>
          ))}
        </span>
      )
    case "datetime":
    case "date": {
      const d = new Date(String(value))
      return Number.isNaN(d.getTime()) ? String(value) : (field.kind === "date" ? dateOnly : dateTime).format(d)
    }
    case "json":
      return <code className="text-xs">{JSON.stringify(value)}</code>
    default:
      return String(value)
  }
}

/** datetime-local 输入框的值（本地时间）与 ISO 字符串之间的转换。 */
const toLocalInput = (iso: unknown) => {
  if (!iso) return ""
  const d = new Date(String(iso))
  if (Number.isNaN(d.getTime())) return ""
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** 按字段的类型选择输入组件。时间以 ISO 字符串保存，与接口相同；标签用逗号分隔。 */
export const defaultInput: FieldInput = ({ field, id, value, onChange, invalid }) => {
  switch (field.kind) {
    case "number":
      return (
        <Input
          id={id}
          type="number"
          aria-invalid={invalid}
          value={value === undefined || value === null ? "" : String(value)}
          onChange={(e) => onChange(e.target.value === "" ? undefined : Number(e.target.value))}
        />
      )
    case "boolean":
      return <Checkbox id={id} checked={!!value} onCheckedChange={(v) => onChange(v === true)} />
    case "select":
      return (
        <Select value={(value as string) ?? ""} onValueChange={(v) => onChange(v)}>
          <SelectTrigger id={id} aria-invalid={invalid} className="w-full">
            <SelectValue placeholder="请选择" />
          </SelectTrigger>
          <SelectContent>
            {field.options?.map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {o.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )
    case "multiselect": {
      const selected = (value as string[] | undefined) ?? []
      return (
        <fieldset id={id} className="flex flex-wrap gap-3">
          {field.options?.map((o) => (
            <div key={o.value} className="flex items-center gap-1.5">
              <Checkbox
                id={`${id}-${o.value}`}
                checked={selected.includes(o.value)}
                onCheckedChange={(v) =>
                  onChange(v === true ? [...selected, o.value] : selected.filter((s) => s !== o.value))
                }
              />
              <Label htmlFor={`${id}-${o.value}`} className="font-normal">
                {o.label}
              </Label>
            </div>
          ))}
        </fieldset>
      )
    }
    case "tags":
      return (
        <Input
          id={id}
          aria-invalid={invalid}
          placeholder="用逗号分隔"
          defaultValue={((value as string[] | undefined) ?? []).join(", ")}
          onChange={(e) =>
            onChange(
              e.target.value
                .split(/[,，]/)
                .map((s) => s.trim())
                .filter(Boolean),
            )
          }
        />
      )
    case "date":
      return (
        <Input
          id={id}
          type="date"
          aria-invalid={invalid}
          value={(value as string) ?? ""}
          onChange={(e) => onChange(e.target.value || undefined)}
        />
      )
    case "datetime":
      return (
        <Input
          id={id}
          type="datetime-local"
          aria-invalid={invalid}
          value={toLocalInput(value)}
          onChange={(e) => onChange(e.target.value ? new Date(e.target.value).toISOString() : undefined)}
        />
      )
    case "json":
      return <Textarea id={id} value={JSON.stringify(value ?? null)} disabled />
    default:
      return (
        <Input
          id={id}
          aria-invalid={invalid}
          value={(value as string) ?? ""}
          onChange={(e) => onChange(e.target.value)}
        />
      )
  }
}

/** 列表上方的搜索和筛选：搜索在输入停顿后写进地址，筛选立即写进地址。 */
function Toolbar({
  model,
  list,
}: {
  model: ResourceModel
  list: ReturnType<typeof useResourceList>["list"]
}): ReactNode {
  const filters = list.filters as Record<string, unknown>
  const q = model.search && typeof filters[model.search] === "string" ? (filters[model.search] as string) : ""
  const [value, setValue] = useState(q)
  useEffect(() => setValue(q), [q])
  useEffect(() => {
    if (!model.search || value === q) return
    const t = setTimeout(() => list.setFilters({ [model.search as string]: value || undefined }), 300)
    return () => clearTimeout(t)
  }, [value, q, list, model.search])
  if (!model.search && model.filters.length === 0) return null
  return (
    <div className="flex flex-wrap items-center gap-2">
      {model.search && (
        <Input
          aria-label="搜索"
          placeholder="搜索…"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          className="h-9 w-64"
        />
      )}
      {model.filters.map((f) =>
        f.options && f.kind === "select" ? (
          <Select
            key={f.name}
            value={(filters[f.name] as string) ?? ALL}
            onValueChange={(v) => list.setFilters({ [f.name]: v === ALL ? undefined : v })}
          >
            <SelectTrigger aria-label={f.label} className="h-9 w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>全部{f.label}</SelectItem>
              {f.options.map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <Input
            key={f.name}
            aria-label={f.label}
            placeholder={f.label}
            defaultValue={(filters[f.name] as string) ?? ""}
            onKeyDown={(e) => {
              if (e.key === "Enter") list.setFilters({ [f.name]: e.currentTarget.value || undefined })
            }}
            className="h-9 w-40"
          />
        ),
      )}
    </div>
  )
}

/** 创建和修改的表单：字段、校验规则（与服务端相同）来自定义，服务端的字段错误显示在输入框下面。 */
export function ResourceForm({
  model,
  row,
  inputs = {},
  onDone,
}: {
  model: ResourceModel
  /** 修改的行；新建时为空。 */
  row?: ResourceRow
  inputs?: ResourcePageProps["inputs"]
  onDone(): void
}): ReactNode {
  const { save } = useResourceMutations(model)
  const { form, onSubmit, formError, pending } = useApiForm<FieldValues>({
    schema: model.formSchema(row ? "update" : "create"),
    rules: model.ruleOptions,
    defaultValues: formValues(model, row),
    submit: async (values) => {
      await save.mutateAsync({ key: row ? model.rowKey(row) : undefined, values })
      onDone()
    },
    guard: false,
  })
  return (
    <form onSubmit={onSubmit} className="grid gap-4" noValidate>
      <FormError error={formError} />
      {model.form.map((f) => (
        <ResourceFormField key={f.name} form={form} field={f} input={inputs[f.name] ?? defaultInput} />
      ))}
      <DialogFooter>
        <Button type="submit" disabled={pending}>
          保存
        </Button>
      </DialogFooter>
    </form>
  )
}

/** 表单中的一个字段：标签、输入组件和错误提示。设置的表单（SettingsForm）也用它。 */
export function ResourceFormField({
  form,
  field,
  input: FieldInputComponent,
}: {
  form: UseFormReturn<FieldValues>
  field: ResourceField
  input: FieldInput
}) {
  return (
    <Field form={form} name={field.name} label={field.required ? `${field.label} *` : field.label}>
      {(p) => (
        <Controller
          control={form.control}
          name={field.name}
          render={({ field: c }) => (
            <FieldInputComponent
              field={field}
              id={p.id}
              value={c.value}
              onChange={c.onChange}
              invalid={p["aria-invalid"]}
            />
          )}
        />
      )}
    </Field>
  )
}

/**
 * 资源的页面：页头、搜索和筛选、表格、新建和修改的对话框、删除和其他操作，按权限显示按钮。
 * 删除和带 Confirm 的操作用 useConfirm 询问，根路由中要有 ConfirmProvider。
 *
 *	export const Route = createFileRoute("/_app/articles")({ validateSearch: articles.listSearch, component: () => <ResourcePage model={articles} route={Route} /> })
 */
export function ResourcePage({ model, route, render = {}, inputs, rowActions, actions }: ResourcePageProps): ReactNode {
  const view = useResourceList(model, route)
  const { remove, run } = useResourceMutations(model)
  const confirm = useConfirm()
  const [editing, setEditing] = useState<{ row?: ResourceRow } | undefined>()
  const [failure, setFailure] = useState<unknown>()

  const attempt = async (go: () => Promise<unknown>) => {
    setFailure(undefined)
    try {
      await go()
    } catch (err) {
      setFailure(err)
    }
  }

  const columns = useMemo<ColumnDef<ResourceRow>[]>(
    () =>
      model.columns.map((f) => ({
        id: f.name,
        accessorKey: f.name,
        header: f.label,
        enableSorting: f.sortable,
        cell: ({ row }) =>
          render[f.name]?.(row.original[f.name], row.original) ?? (
            <FieldValue model={model} field={f} value={row.original[f.name]} />
          ),
      })),
    [model, render],
  )

  const menu = (row: ResourceRow): RowAction[] => {
    const key = model.rowKey(row)
    const items: RowAction[] = []
    if (view.can.update) items.push({ label: "修改", onSelect: () => setEditing({ row }) })
    for (const a of model.actions) {
      if (!view.can.action(a.name)) continue
      items.push({
        label: a.label,
        destructive: a.danger,
        onSelect: () =>
          void (async () => {
            if (a.confirm && !(await confirm({ title: `${a.label}这一项？`, action: a.label, destructive: a.danger })))
              return
            await attempt(() => run.mutateAsync({ action: a.name, key }))
          })(),
      })
    }
    items.push(...(rowActions?.(row) ?? []))
    if (view.can.delete) {
      items.push({
        label: "删除",
        destructive: true,
        onSelect: () =>
          void (async () => {
            if (!(await confirm({ title: "删除这一项？", action: "删除", destructive: true }))) return
            await attempt(() => remove.mutateAsync(key))
          })(),
      })
    }
    return items
  }

  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={model.title}>
        {actions}
        {view.can.create && (
          <Button onClick={() => setEditing({})}>
            <Plus />
            新建
          </Button>
        )}
      </PageHeader>
      {failure ? (
        <p role="alert" className="text-sm text-destructive">
          {describeError(failure).message}
        </p>
      ) : null}
      <DataTable
        columns={columns}
        data={view.loading ? undefined : view.rows}
        total={view.total}
        list={view.list}
        error={view.error}
        retry={view.refetch}
        rowKey={model.rowKey}
        rowActions={menu}
        toolbar={<Toolbar model={model} list={view.list} />}
      />
      <Dialog open={!!editing} onOpenChange={(open) => !open && setEditing(undefined)}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>
              {editing?.row ? "修改" : "新建"}
              {model.title}
            </DialogTitle>
          </DialogHeader>
          {editing && (
            <ResourceForm model={model} row={editing.row} inputs={inputs} onDone={() => setEditing(undefined)} />
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
