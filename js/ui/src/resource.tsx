// 资源页面：shell/ 中的资源模型（resourceModel）的外观。列表、搜索和筛选、新建和修改的表单（抽屉）、
// 删除和其他操作都来自 Go 中的声明，页面只写需要覆盖的部分。

import { describeError, type ErrorInfo } from "@go-tenon/client"
import {
  ActionIcon,
  Box,
  Button,
  CloseButton,
  Drawer,
  Group,
  Loader,
  Menu,
  MultiSelect,
  Select,
  Stack,
  TextInput,
  useMantineTheme,
} from "@mantine/core"
import { IconDots, IconPencil, IconPlus, IconRefresh, IconSearch, IconTrash } from "@tabler/icons-react"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { type Column, DataTable } from "./data-table"
import { confirm, notifyError, notifySuccess } from "./feedback"
import { DefaultInput, type FieldInput, FieldValue } from "./fields"
import { applyServerErrors, FormError, useSchemaForm } from "./form"
import { Page } from "./page"
import {
  formValues,
  type ListRoute,
  type ResourceField,
  type ResourceModel,
  type ResourceRow,
  useResourceItem,
  useResourceList,
  useResourceMutations,
  useUnsavedChanges,
} from "./shell"
import { layoutMetrics } from "./theme"

/** 行上的一个额外操作。 */
export interface RowAction {
  label: ReactNode
  icon?: ReactNode
  danger?: boolean
  onClick: () => void
}

/** ResourcePage 的选项：没有覆盖时全部按声明渲染。 */
export interface ResourcePageProps {
  model: ResourceModel
  /** 列表的路由：validateSearch 是 model.listSearch。 */
  route: ListRoute<Record<string, unknown>>
  /** 页面的标题，默认是资源的显示文字；null 不显示标题区。 */
  title?: ReactNode | null
  /** 标题下面的说明。 */
  description?: ReactNode
  /** 覆盖某一列的显示，例如 { title: (v, row) => <AnchorLink to=…>{String(v)}</AnchorLink> }。 */
  render?: { [field: string]: (value: unknown, row: ResourceRow) => ReactNode }
  /** 覆盖某个表单字段的输入组件。 */
  inputs?: { [field: string]: FieldInput }
  /** 行上的额外操作，排在声明中的操作之后、删除之前。 */
  rowActions?: (row: ResourceRow) => RowAction[]
  /** 标题区的额外按钮，排在“新建”之前，例如导入、导出。 */
  toolbar?: ReactNode
  /** 点击一行展开的内容。 */
  expand?: (row: ResourceRow) => ReactNode
}

/**
 * 资源的页面：
 *
 *	export const Route = createFileRoute("/_app/articles")({ validateSearch: articles.listSearch, component: () => <ResourcePage model={articles} route={Route} /> })
 */
export function ResourcePage({
  model,
  route,
  title,
  description,
  render = {},
  inputs,
  rowActions,
  toolbar,
  expand,
}: ResourcePageProps): ReactNode {
  const view = useResourceList(model, route)
  const { remove, run } = useResourceMutations(model)
  const m = layoutMetrics(useMantineTheme())
  const [editing, setEditing] = useState<{ row?: ResourceRow } | undefined>()

  const act = async (label: string, go: () => Promise<unknown>) => {
    try {
      await go()
      notifySuccess(`${label}完成`)
    } catch (err) {
      notifyError(err, `${label}没有成功`)
    }
  }

  const columns: Column<ResourceRow>[] = model.columns.map((f, i) => ({
    accessor: f.name,
    title: f.label,
    sortable: f.sortable,
    fixed: i === 0,
    noWrap: f.kind === "datetime" || f.kind === "date" || f.kind === "number",
    textAlign: f.kind === "number" ? "right" : undefined,
    render: (row) =>
      render[f.name]?.(row[f.name], row) ?? <FieldValue field={f} value={row[f.name]} display={model.display} />,
  }))

  const actions = model.actions.filter((a) => view.can.action(a.name))
  const hasRowActions = view.can.update || view.can.delete || actions.length > 0 || !!rowActions
  if (hasRowActions) {
    columns.push({
      accessor: "_actions",
      title: "",
      fixed: true,
      textAlign: "right",
      width: 96,
      render: (row) => {
        const key = model.rowKey(row)
        const extra = rowActions?.(row) ?? []
        const more = actions.length > 0 || extra.length > 0 || view.can.delete
        return (
          <Group gap={2} justify="flex-end" wrap="nowrap" onClick={(e) => e.stopPropagation()}>
            {view.can.update && (
              <ActionIcon aria-label="修改" onClick={() => setEditing({ row })}>
                <IconPencil size={16} stroke={1.6} />
              </ActionIcon>
            )}
            {more && (
              <Menu position="bottom-end" width={160}>
                <Menu.Target>
                  <ActionIcon aria-label="更多操作">
                    <IconDots size={16} stroke={1.6} />
                  </ActionIcon>
                </Menu.Target>
                <Menu.Dropdown>
                  {actions.map((a) => (
                    <Menu.Item
                      key={a.name}
                      color={a.danger ? "red" : undefined}
                      onClick={async () => {
                        if (
                          a.confirm &&
                          !(await confirm({ title: `${a.label}这一项？`, danger: a.danger, confirmLabel: a.label }))
                        )
                          return
                        await act(a.label, () => run.mutateAsync({ action: a.name, key }))
                      }}
                    >
                      {a.label}
                    </Menu.Item>
                  ))}
                  {extra.map((x, i) => (
                    // biome-ignore lint/suspicious/noArrayIndexKey: 应用给出的固定列表
                    <Menu.Item key={i} leftSection={x.icon} color={x.danger ? "red" : undefined} onClick={x.onClick}>
                      {x.label}
                    </Menu.Item>
                  ))}
                  {view.can.delete && (actions.length > 0 || extra.length > 0) && <Menu.Divider />}
                  {view.can.delete && (
                    <Menu.Item
                      color="red"
                      leftSection={<IconTrash size={14} stroke={1.6} />}
                      onClick={async () => {
                        if (
                          await confirm({
                            title: `删除这一项？`,
                            message: "删除后不能恢复。",
                            danger: true,
                            confirmLabel: "删除",
                          })
                        )
                          await act("删除", () => remove.mutateAsync(key))
                      }}
                    >
                      删除
                    </Menu.Item>
                  )}
                </Menu.Dropdown>
              </Menu>
            )}
          </Group>
        )
      },
    })
  }

  const table = (
    <DataTable<ResourceRow>
      records={view.rows}
      total={view.total}
      loading={view.loading}
      list={view.list}
      columns={columns}
      rowKey={(row) => model.rowKey(row)}
      storeKey={model.name}
      toolbar={<Filters model={model} list={view.list} />}
      expand={expand}
      empty={view.error ? "读取失败" : undefined}
    />
  )
  const buttons = (
    <Group gap="xs">
      {toolbar}
      <ActionIcon
        variant="default"
        size={m.controlSize === "xs" ? "md" : "lg"}
        aria-label="刷新"
        onClick={view.refetch}
      >
        <IconRefresh size={16} stroke={1.6} />
      </ActionIcon>
      {view.can.create && (
        <Button leftSection={<IconPlus size={16} />} onClick={() => setEditing({})}>
          新建
        </Button>
      )}
    </Group>
  )
  return (
    <>
      {title === null ? (
        <Stack gap="sm">
          <Group justify="flex-end">{buttons}</Group>
          {table}
        </Stack>
      ) : (
        <Page title={title ?? model.title} description={description} actions={buttons}>
          {view.error ? <FormError error={describeError(view.error)} /> : null}
          {table}
        </Page>
      )}
      <ResourceForm
        model={model}
        open={!!editing}
        row={editing?.row}
        inputs={inputs}
        onClose={() => setEditing(undefined)}
      />
    </>
  )
}

/** 列表上方的搜索和筛选：修改后写进地址，回到第一页。 */
function Filters({
  model,
  list,
}: {
  model: ResourceModel
  list: ReturnType<typeof useResourceList>["list"]
}): ReactNode {
  const filters = list.filters as Record<string, unknown>
  const size = layoutMetrics(useMantineTheme()).controlSize
  if (!model.search && model.filters.length === 0) return null
  // 只看声明的搜索和筛选条件：地址中可能还有其他参数。
  const active = [model.search, ...model.filters.map((f) => f.name)].some((n) => n && filters[n] !== undefined)
  return (
    <Group gap="xs">
      {model.search && (
        <SearchInput
          value={filters[model.search] as string | undefined}
          size={size}
          onSearch={(q) => list.setFilters({ [model.search as string]: q })}
        />
      )}
      {model.filters.map((f) => (
        <FilterInput
          key={f.name}
          field={f}
          size={size}
          value={filters[f.name]}
          onChange={(v) => list.setFilters({ [f.name]: v })}
        />
      ))}
      {active && (
        <Button variant="subtle" color="gray" size={size} onClick={list.reset}>
          清除筛选
        </Button>
      )}
    </Group>
  )
}

function SearchInput({
  value,
  size,
  onSearch,
}: {
  value?: string
  size: string
  onSearch: (q: string | undefined) => void
}) {
  const [text, setText] = useState(value ?? "")
  useEffect(() => setText(value ?? ""), [value])
  return (
    <TextInput
      size={size}
      w={220}
      placeholder="搜索"
      aria-label="搜索"
      leftSection={<IconSearch size={14} />}
      value={text}
      onChange={(e) => setText(e.currentTarget.value)}
      onKeyDown={(e) => e.key === "Enter" && onSearch(text.trim() || undefined)}
      onBlur={() => (text.trim() || undefined) !== value && onSearch(text.trim() || undefined)}
      rightSection={
        text ? (
          <CloseButton
            size="sm"
            aria-label="清除搜索"
            onClick={() => {
              setText("")
              onSearch(undefined)
            }}
          />
        ) : undefined
      }
    />
  )
}

function FilterInput({
  field,
  value,
  size,
  onChange,
}: {
  field: ResourceField
  value: unknown
  size: string
  onChange: (v: unknown) => void
}): ReactNode {
  if (field.options && field.kind === "multiselect")
    return (
      <MultiSelect
        size={size}
        miw={160}
        placeholder={field.label}
        aria-label={field.label}
        data={field.options}
        value={(value as string[] | undefined) ?? []}
        onChange={(v) => onChange(v.length ? v : undefined)}
        clearable
      />
    )
  if (field.options)
    return (
      <Select
        size={size}
        w={150}
        placeholder={field.label}
        aria-label={field.label}
        data={field.options}
        value={(value as string | undefined) ?? null}
        onChange={(v) => onChange(v ?? undefined)}
        clearable
      />
    )
  if (field.kind === "boolean")
    return (
      <Select
        size={size}
        w={120}
        placeholder={field.label}
        aria-label={field.label}
        data={[
          { value: "true", label: "是" },
          { value: "false", label: "否" },
        ]}
        value={value === undefined ? null : String(value)}
        onChange={(v) => onChange(v === null ? undefined : v === "true")}
        clearable
      />
    )
  return <FilterText field={field} value={value as string | undefined} size={size} onChange={onChange} />
}

function FilterText({
  field,
  value,
  size,
  onChange,
}: {
  field: ResourceField
  value?: string
  size: string
  onChange: (v: unknown) => void
}) {
  const [text, setText] = useState(value ?? "")
  useEffect(() => setText(value ?? ""), [value])
  return (
    <TextInput
      size={size}
      w={160}
      placeholder={field.label}
      aria-label={field.label}
      value={text}
      onChange={(e) => setText(e.currentTarget.value)}
      onKeyDown={(e) => e.key === "Enter" && onChange(text.trim() || undefined)}
      onBlur={() => (text.trim() || undefined) !== value && onChange(text.trim() || undefined)}
    />
  )
}

/** ResourceForm 的选项。 */
export interface ResourceFormProps {
  model: ResourceModel
  open: boolean
  /** 修改的行；新建时为空。有详情接口时打开后读取完整的一行。 */
  row?: ResourceRow
  inputs?: { [field: string]: FieldInput }
  onClose(): void
}

/** 新建和修改的表单（右侧抽屉）：字段、校验规则（与服务端相同）来自声明，服务端的字段错误显示在输入框下面。 */
export function ResourceForm({ model, open, row, inputs, onClose }: ResourceFormProps): ReactNode {
  const key = row ? model.rowKey(row) : undefined
  const item = useResourceItem(model, open ? key : undefined)
  const loading = !!key && model.has.get && item.isPending
  const initial = (item.data as ResourceRow | undefined) ?? row
  // 表单有未保存的修改时，关闭（取消、关闭按钮、Esc）之前询问。
  const dirty = useRef<() => boolean>(() => false)
  const requestClose = async () => {
    if (dirty.current() && !(await confirm({ title: "放弃未保存的修改？", danger: true, confirmLabel: "放弃" }))) return
    onClose()
  }
  return (
    <Drawer
      opened={open}
      onClose={() => void requestClose()}
      position="right"
      size="md"
      title={`${row ? "修改" : "新建"}${model.title}`}
      closeOnClickOutside={false}
    >
      {open &&
        (loading ? (
          <Group justify="center" py="xl">
            <Loader size="sm" />
          </Group>
        ) : (
          <FormBody
            key={key ?? "new"}
            model={model}
            row={initial}
            editKey={key}
            inputs={inputs}
            dirty={dirty}
            onSaved={onClose}
            onCancel={() => void requestClose()}
          />
        ))}
    </Drawer>
  )
}

function FormBody({
  model,
  row,
  editKey,
  inputs = {},
  dirty,
  onSaved,
  onCancel,
}: {
  model: ResourceModel
  row?: ResourceRow
  editKey?: string
  inputs?: { [field: string]: FieldInput }
  dirty: { current: () => boolean }
  onSaved(): void
  onCancel(): void
}): ReactNode {
  const form = useSchemaForm({
    schema: model.formSchema(editKey ? "update" : "create"),
    rules: model.ruleOptions,
    initialValues: formValues(model, row),
  })
  const { save } = useResourceMutations(model)
  const [formError, setFormError] = useState<ErrorInfo>()
  const unsaved = useUnsavedChanges(form.isDirty())
  dirty.current = () => form.isDirty()
  useEffect(() => () => unsaved.release(), [unsaved])
  return (
    <form
      noValidate
      onSubmit={form.onSubmit(async (values) => {
        setFormError(undefined)
        try {
          await save.mutateAsync({ key: editKey, values })
          unsaved.release()
          notifySuccess(editKey ? "已保存" : "已创建")
          dirty.current = () => false
          onSaved()
        } catch (err) {
          setFormError(applyServerErrors(form, err))
        }
      })}
    >
      <Stack gap="md">
        <FormError error={formError} />
        {model.form.map((f) => {
          const Input = inputs[f.name] ?? DefaultInput
          return (
            <Box key={f.name}>
              <Input field={f} label={f.label} {...form.getInputProps(f.name)} />
            </Box>
          )
        })}
        <Group justify="flex-end" gap="xs" mt="sm">
          <Button variant="default" onClick={onCancel}>
            取消
          </Button>
          <Button type="submit" loading={save.isPending}>
            {editKey ? "保存" : "创建"}
          </Button>
        </Group>
      </Stack>
    </form>
  )
}
