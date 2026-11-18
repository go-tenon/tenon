// 资源页面：资源模型（model.ts 的 resourceModel）的外观。列表、搜索和筛选、新建和修改的表单、
// 删除和其他操作都来自 Go 中的声明，页面只写需要覆盖的部分。
//
// 新建和修改默认是单独的页面：列表路由下的 new 和 $id/edit 两个子路由，各渲染一个 ResourceFormPage，
// 每个页面是一个标签；字段少的资源可以设置 form="modal"，在列表上方的对话框中填写，不需要子路由。

import { describeError, type ErrorInfo } from "@go-tenon/client"
import {
  ActionIcon,
  Alert,
  Box,
  Button,
  Card,
  CloseButton,
  Group,
  Loader,
  Menu,
  Modal,
  MultiSelect,
  Select,
  Stack,
  TextInput,
  useMantineTheme,
} from "@mantine/core"
import { IconDots, IconPencil, IconPlus, IconTrash } from "@tabler/icons-react"
import { useQueryClient } from "@tanstack/react-query"
import {
  Outlet,
  useChildMatches,
  useMatch,
  useMatches,
  useNavigate,
  useParams,
  useRouter,
} from "@tanstack/react-router"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { useTab, useTabTitle } from "../../app/tabs/tabs"
import { type Column, DataTable } from "../../components/data-table/data-table"
import type { ListRoute } from "../../components/data-table/list"
import { confirm, notifyError, notifySuccess } from "../../components/feedback"
import { applyServerErrors, FormError, useSchemaForm } from "../../components/form/form"
import { useUnsavedChanges } from "../../components/form/rules"
import { Page } from "../../components/page"
import { layoutMetrics } from "../../theme/theme"
import { DefaultInput, type FieldInput, FieldValue } from "./fields"
import {
  formValues,
  type ResourceField,
  type ResourceModel,
  type ResourceRow,
  useResourceItem,
  useResourceList,
  useResourceMutations,
} from "./model"

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
  /** 展开的内容：每行开头一个箭头按钮，点它展开、收起。 */
  expand?: (row: ResourceRow) => ReactNode
  /**
   * 新建和修改的表单：page（默认）是单独的页面，需要列表路由下的 new 和 $id/edit 两个子路由（渲染 ResourceFormPage）；
   * modal 是列表上方的对话框，适合字段少的资源。
   */
  form?: "page" | "modal"
}

/**
 * 资源的页面：
 *
 *	export const Route = createFileRoute("/_app/articles")({ validateSearch: articles.listSearch, component: () => <ResourcePage model={articles} route={Route} /> })
 *	// articles.new.tsx、articles.$id.edit.tsx：新建和修改的页面
 *	export const Route = createFileRoute("/_app/articles/new")({ component: () => <ResourceFormPage model={articles} /> })
 *
 * 打开了新建或修改的页面（子路由）时，这里显示那个页面。
 */
export function ResourcePage(props: ResourcePageProps): ReactNode {
  return useChildMatches().length > 0 ? <Outlet /> : <ResourceList {...props} />
}

const warned = new Set<string>()

function ResourceList({
  model,
  route,
  title,
  description,
  render = {},
  inputs,
  rowActions,
  toolbar,
  expand,
  form = "page",
}: ResourcePageProps): ReactNode {
  const view = useResourceList(model, route)
  const { remove, run } = useResourceMutations(model)
  const [editing, setEditing] = useState<{ row?: ResourceRow } | undefined>()
  const router = useRouter()
  const navigate = useNavigate()
  const here = useMatch({ strict: false })
  const base = here.pathname.replace(/\/$/, "")

  // 单独的页面：列表路由下的 new 和 $id/edit 子路由。没有对应的路由时不显示按钮，并提示一次。
  const pages =
    form === "page"
      ? {
          create: Boolean(router.routesById[`${here.routeId}/new` as never]),
          update: Boolean(router.routesById[`${here.routeId}/$id/edit` as never]),
        }
      : { create: true, update: true }
  for (const [op, ok, file] of [
    ["create", pages.create, "new"],
    ["update", pages.update, "$id/edit"],
  ] as const) {
    const id = `${here.routeId}/${file}`
    if (!ok && view.can[op] && !warned.has(id)) {
      warned.add(id)
      console.warn(`@go-tenon/ui: ResourcePage 找不到路由 ${id}，不显示${op === "create" ? "新建" : "修改"}按钮。`)
    }
  }
  const canCreate = view.can.create && pages.create
  const canUpdate = view.can.update && pages.update
  const openCreate = () => (form === "modal" ? setEditing({}) : void navigate({ to: `${base}/new` as never }))
  const openEdit = (row: ResourceRow) =>
    form === "modal"
      ? setEditing({ row })
      : void navigate({ to: `${base}/${encodeURIComponent(model.rowKey(row))}/edit` as never })

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
    render: (row) =>
      render[f.name]?.(row[f.name], row) ?? <FieldValue field={f} value={row[f.name]} display={model.display} />,
  }))

  const actions = model.actions.filter((a) => view.can.action(a.name))
  // 操作列：能修改或删除，或者这一页至少有一行有可用的操作（声明的操作满足 When，或 rowActions 返回了操作）时才显示。
  const rowHasActions = (row: ResourceRow) =>
    actions.some((a) => a.appliesTo(row)) || (rowActions?.(row).length ?? 0) > 0
  const hasRowActions = canUpdate || view.can.delete || view.rows.some(rowHasActions)
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
        const shown = actions.filter((a) => a.appliesTo(row)) // 声明了 When 的操作只在满足条件的行上显示
        const more = shown.length > 0 || extra.length > 0 || view.can.delete
        return (
          <Group gap={2} justify="flex-end" wrap="nowrap" onClick={(e) => e.stopPropagation()}>
            {canUpdate && (
              <ActionIcon aria-label="修改" onClick={() => openEdit(row)}>
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
                  {shown.map((a) => (
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
      {canCreate && (
        <Button leftSection={<IconPlus size={16} />} onClick={openCreate}>
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
      {form === "modal" && (
        <ResourceForm
          model={model}
          open={!!editing}
          row={editing?.row}
          inputs={inputs}
          onClose={() => setEditing(undefined)}
        />
      )}
    </>
  )
}

/** 列表上方的筛选：列表接口的查询参数，按类型显示输入框、下拉选择；修改后写进地址，回到第一页。 */
function Filters({
  model,
  list,
}: {
  model: ResourceModel
  list: ReturnType<typeof useResourceList>["list"]
}): ReactNode {
  const filters = list.filters as Record<string, unknown>
  const size = layoutMetrics(useMantineTheme()).controlSize
  if (model.filters.length === 0) return null
  // 只看声明的筛选条件：地址中可能还有其他参数。
  const active = model.filters.some((f) => filters[f.name] !== undefined)
  return (
    <Group gap="xs">
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

/** 文字的筛选条件，例如 name：回车或离开输入框时生效，有内容时右边是清除按钮。 */
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
      w={180}
      placeholder={field.label}
      aria-label={field.label}
      value={text}
      onChange={(e) => setText(e.currentTarget.value)}
      onKeyDown={(e) => e.key === "Enter" && onChange(text.trim() || undefined)}
      onBlur={() => (text.trim() || undefined) !== value && onChange(text.trim() || undefined)}
      rightSection={
        text ? (
          <CloseButton
            size="sm"
            aria-label={`清除${field.label}`}
            onClick={() => {
              setText("")
              onChange(undefined)
            }}
          />
        ) : undefined
      }
    />
  )
}

const discard = () => confirm({ title: "放弃未保存的修改？", danger: true, confirmLabel: "放弃" })

/** ResourceForm 的选项。 */
export interface ResourceFormProps {
  model: ResourceModel
  open: boolean
  /** 修改的行；新建时为空。有详情接口时打开后读取完整的一行。 */
  row?: ResourceRow
  inputs?: { [field: string]: FieldInput }
  onClose(): void
  /** 自己写的表单内容，代替按声明渲染的字段，见 useResourceForm。 */
  children?: (ctx: ResourceFormContext) => ReactNode
}

/** 新建和修改的表单（对话框）：字段、校验规则（与服务端相同）来自声明，服务端的字段错误显示在输入框下面。 */
export function ResourceForm({ model, open, row, inputs, onClose, children }: ResourceFormProps): ReactNode {
  const key = row ? model.rowKey(row) : undefined
  const item = useResourceItem(model, open ? key : undefined)
  const loading = !!key && model.has.get && item.isPending
  const initial = (item.data as ResourceRow | undefined) ?? row
  // 表单有未保存的修改时，关闭（取消、关闭按钮、Esc）之前询问。
  const dirty = useRef<() => boolean>(() => false)
  const requestClose = async () => {
    if (dirty.current() && !(await discard())) return
    onClose()
  }
  return (
    <Modal
      opened={open}
      onClose={() => void requestClose()}
      size="lg"
      title={`${row ? "修改" : "新建"}${model.title}`}
      closeOnClickOutside={false}
    >
      {open &&
        (loading ? (
          <Group justify="center" py="xl">
            <Loader size="sm" />
          </Group>
        ) : (
          <FormContent
            key={key ?? "new"}
            ctx={{ model, row: initial, editKey: key, dirty, onSaved: onClose, onCancel: () => void requestClose() }}
            inputs={inputs}
            render={children}
          />
        ))}
    </Modal>
  )
}

/** ResourceFormPage 的选项。 */
export interface ResourceFormPageProps {
  model: ResourceModel
  /** 覆盖某个表单字段的输入组件，与 ResourcePage 相同。 */
  inputs?: { [field: string]: FieldInput }
  /**
   * 自己写的表单内容，代替按声明渲染的字段：嵌套的字段、字段之间的联动等声明表达不了的表单。
   * 页面的其余部分不变：标签标题、读取要修改的行、保存或取消后回到列表、关闭标签、离开前询问未保存的修改。见 useResourceForm。
   */
  children?: (ctx: ResourceFormContext) => ReactNode
}

/**
 * 新建和修改的页面，放在列表路由的子路由中：new 是新建，$id/edit 是修改（$id 是行的标识）。
 * 保存或取消后回到列表；在多标签中时同时关闭这个标签。有未保存的修改时，离开前询问。
 *
 *	export const Route = createFileRoute("/_app/articles/$id/edit")({ component: () => <ResourceFormPage model={articles} /> })
 */
export function ResourceFormPage({ model, inputs, children }: ResourceFormPageProps): ReactNode {
  const { id } = useParams({ strict: false }) as { id?: string }
  const matches = useMatches()
  const navigate = useNavigate()
  const tab = useTab()
  const queryClient = useQueryClient()
  const label = id ? "修改" : "新建"
  useTabTitle(`${label}${model.title}`)

  // 列表是上一级路由。在标签中时先关闭这个标签：有未保存的修改时由标签询问，选择不放弃就留在这里；
  // 先跳转的话，这个页面先被卸载、未保存的标记随之释放，关闭时就不再询问。不在标签中时，跳转由 useUnsavedChanges 询问。
  const list = matches[matches.length - 2]?.pathname ?? "/"
  const leave = async () => {
    if (tab.key && !(await tab.close())) return
    await navigate({ to: list as never })
  }

  // 修改：有详情接口时读取完整的一行。没有时在打开页面的那一刻从列表中取出这一行并留住：之后列表不在眼前、
  // 它的数据被丢掉（例如切换标签），这个页面照样有这一行。
  const item = useResourceItem(model, id)
  const [fromList] = useState(() => {
    if (!id || model.has.get) return undefined
    for (const [, data] of queryClient.getQueriesData<{ items?: ResourceRow[] }>({ queryKey: [...model.queryKey] })) {
      const row = data?.items?.find((r) => model.rowKey(r) === id)
      if (row) return row
    }
    return undefined
  })
  const row = id ? ((item.data as ResourceRow | undefined) ?? fromList) : undefined
  const loading = !!id && model.has.get && item.isPending
  const dirty = useRef<() => boolean>(() => false)

  return (
    <Page title={`${label}${model.title}`} crumb={label}>
      <Card>
        {loading ? (
          <Group justify="center" py="xl">
            <Loader size="sm" />
          </Group>
        ) : id && !row ? (
          <Alert color="red" variant="light" title="读取失败" role="alert">
            {model.has.get ? "这一项不存在，或者没有权限查看。" : "这类数据没有读取单条的接口，从列表中打开修改页面。"}
          </Alert>
        ) : (
          <FormContent
            key={id ?? "new"}
            ctx={{ model, row, editKey: id, dirty, onSaved: () => void leave(), onCancel: () => void leave() }}
            inputs={inputs}
            render={children}
          />
        )}
      </Card>
    </Page>
  )
}

/** 自己写的资源表单收到的上下文：ResourceFormPage、ResourceForm 的 children 收到它，交给 useResourceForm。 */
export interface ResourceFormContext {
  model: ResourceModel
  /** 修改时是行的标识，新建时为空。 */
  editKey?: string
  /** 修改的行：有详情接口时是读取到的完整一行；新建时为空。 */
  row?: ResourceRow
  /** 保存成功之后调用：页面中回到列表并关闭标签，对话框中关闭。 */
  onSaved(): void
  /** 取消：页面中回到列表，对话框中关闭；有未保存的修改时先询问。 */
  onCancel(): void
  /** useResourceForm 在这里登记表单有没有未保存的修改，对话框关闭前据此询问。 */
  dirty: { current: () => boolean }
}

/** useResourceForm 的选项。 */
export interface ResourceFormOptions<T> {
  /** 表单的初始值。修改时通常由 ctx.row 得出，例如 formValues(ctx.model.updateForm, ctx.row) 再补上嵌套的字段。 */
  initialValues: T
  /** 保存前把表单的值转换为请求体，例如去掉只在界面中使用的字段。默认原样提交。 */
  toBody?: (values: T) => Record<string, unknown>
}

/**
 * 自己写的资源表单：表单的校验规则（与服务端相同）来自资源的新建或修改接口，提交时调用这个接口，服务端的字段错误
 * （包括业务错误用 errorx.Field 指出的字段）显示在输入框下面，其他错误交给 FormError；保存成功后提示“已保存”或
 * “已创建”，回到列表或关闭对话框；有未保存的修改时离开前询问。表单的内容自己写，嵌套的字段用 form.getInputProps("a.b")：
 *
 *	<ResourceFormPage model={providers}>{(ctx) => <ProviderForm ctx={ctx} />}</ResourceFormPage>
 *
 *	function ProviderForm({ ctx }: { ctx: ResourceFormContext }) {
 *	  const { form, error, submit, saving } = useResourceForm(ctx, { initialValues: { vendor: "", base_urls: {} } })
 *	  return (
 *	    <form noValidate onSubmit={submit}>
 *	      <FormError error={error} />
 *	      ……
 *	      <ResourceFormButtons ctx={ctx} saving={saving} />
 *	    </form>
 *	  )
 *	}
 */
// biome-ignore lint/suspicious/noExplicitAny: 与 useSchemaForm 相同的约束，生成的请求体类型（interface）可以直接作为 T
export function useResourceForm<T extends Record<string, any>>(
  ctx: ResourceFormContext,
  options: ResourceFormOptions<T>,
) {
  const { model, editKey } = ctx
  const form = useSchemaForm<T>({
    schema: model.formSchema(editKey ? "update" : "create"),
    rules: model.ruleOptions,
    initialValues: options.initialValues,
  })
  const { save } = useResourceMutations(model)
  const [error, setError] = useState<ErrorInfo>()
  // 离开页面时询问是否放弃（在标签中时改为标记这个标签有未保存的修改）。
  const unsaved = useUnsavedChanges(form.isDirty(), { confirm: discard })
  ctx.dirty.current = () => form.isDirty()
  useEffect(() => () => unsaved.release(), [unsaved])
  const submit = form.onSubmit(async (values) => {
    setError(undefined)
    try {
      const body = options.toBody ? options.toBody(values) : (values as Record<string, unknown>)
      await save.mutateAsync({ key: editKey, values: body })
      unsaved.release()
      notifySuccess(editKey ? "已保存" : "已创建")
      ctx.dirty.current = () => false
      ctx.onSaved()
    } catch (err) {
      setError(applyServerErrors(form, err))
    }
  })
  return { form, error, submit, saving: save.isPending }
}

/** 资源表单底部的按钮：取消和保存（新建时是“创建”），靠右。 */
export function ResourceFormButtons({ ctx, saving }: { ctx: ResourceFormContext; saving?: boolean }): ReactNode {
  return (
    <Group justify="flex-end" gap="xs" mt="sm">
      <Button variant="default" onClick={ctx.onCancel}>
        取消
      </Button>
      <Button type="submit" loading={saving}>
        {ctx.editKey ? "保存" : "创建"}
      </Button>
    </Group>
  )
}

/** 表单的内容：有 render 时是自己写的表单，否则按声明渲染字段。 */
function FormContent({
  ctx,
  inputs,
  render,
}: {
  ctx: ResourceFormContext
  inputs?: { [field: string]: FieldInput }
  render?: (ctx: ResourceFormContext) => ReactNode
}): ReactNode {
  return render ? render(ctx) : <FormBody ctx={ctx} inputs={inputs} />
}

/** 按声明渲染的表单：新建和修改的字段各来自自己接口的请求体。 */
function FormBody({
  ctx,
  inputs = {},
}: {
  ctx: ResourceFormContext
  inputs?: { [field: string]: FieldInput }
}): ReactNode {
  const fields = ctx.editKey ? ctx.model.updateForm : ctx.model.createForm
  const { form, error, submit, saving } = useResourceForm(ctx, { initialValues: formValues(fields, ctx.row) })
  return (
    <form noValidate onSubmit={submit}>
      <Stack gap="md">
        <FormError error={error} />
        {fields.map((f) => {
          const Input = inputs[f.name] ?? DefaultInput
          return (
            <Box key={f.name}>
              <Input field={f} label={f.label} {...form.getInputProps(f.name)} />
            </Box>
          )
        })}
        <ResourceFormButtons ctx={ctx} saving={saving} />
      </Stack>
    </form>
  )
}
