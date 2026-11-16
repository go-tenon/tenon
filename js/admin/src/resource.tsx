// 资源的 antd 外观：ProTable 渲染列表，DrawerForm 渲染创建和修改的表单。模型来自 @go-tenon/shell 的 resourceModel：
// 列、筛选条件、表单字段、权限和接口都在定义中，页面只写需要覆盖的部分。

import { DeleteOutlined, EditOutlined, MoreOutlined, PlusOutlined } from "@ant-design/icons"
import { DrawerForm, type ProColumns, ProTable } from "@ant-design/pro-components"
import { describeError } from "@go-tenon/client"
import {
  antdRule,
  formValues,
  type ListRoute,
  type ResourceField,
  type ResourceModel,
  type ResourceRow,
  setServerErrors,
  useResourceList,
  useResourceMutations,
} from "@go-tenon/shell"
import {
  App as AntApp,
  Button,
  DatePicker,
  Dropdown,
  Form,
  Input,
  InputNumber,
  type MenuProps,
  Select,
  Space,
  Switch,
  Tag,
  Typography,
} from "antd"
import dayjs from "dayjs"
import { type ReactNode, useMemo, useState } from "react"

/** 一个字段的输入组件：value、onChange 由 Form.Item 传入。 */
export type FieldInput = (props: {
  field: ResourceField
  /** Form.Item 传入的 id：标签的 for 指向它，输入组件要用在控件上。 */
  id?: string
  value?: unknown
  onChange?: (v: unknown) => void
}) => ReactNode

/** ResourcePage 的选项：没有覆盖时全部按定义渲染。 */
export interface ResourcePageProps {
  model: ResourceModel
  /** 列表的路由：validateSearch 是 model.listSearch。 */
  route: ListRoute<Record<string, unknown>>
  /** 覆盖某一列的显示，例如 { title: (v, row) => <a href={…}>{v}</a> }。 */
  render?: { [field: string]: (value: unknown, row: ResourceRow) => ReactNode }
  /** 覆盖某个表单字段的输入组件。 */
  inputs?: { [field: string]: FieldInput }
  /** 行上的额外操作，排在定义中的操作之后。 */
  rowActions?: (row: ResourceRow) => MenuProps["items"]
  /** 工具栏上的额外按钮，排在“新建”之前。 */
  toolbar?: ReactNode[]
}

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
  if (value === undefined || value === null || value === "")
    return <Typography.Text type="secondary">-</Typography.Text>
  switch (field.kind) {
    case "boolean":
      return value ? <Tag color="success">是</Tag> : <Tag>否</Tag>
    case "select":
      return <Tag>{String(model.display(field, value))}</Tag>
    case "multiselect":
    case "tags": {
      const values = (model.display(field, value) as unknown[]) ?? []
      return values.map((v) => <Tag key={String(v)}>{String(v)}</Tag>)
    }
    case "datetime":
      return dayjs(String(value)).format("YYYY-MM-DD HH:mm")
    case "date":
      return dayjs(String(value)).format("YYYY-MM-DD")
    case "json":
      return <Typography.Text code>{JSON.stringify(value)}</Typography.Text>
    default:
      return String(value)
  }
}

/** 按字段的类型选择输入组件。时间以 ISO 字符串保存，与接口相同。 */
export const defaultInput: FieldInput = ({ field, id, value, onChange }) => {
  switch (field.kind) {
    case "number":
      return <InputNumber id={id} style={{ width: "100%" }} value={value as number} onChange={onChange} />
    case "boolean":
      return <Switch id={id} checked={!!value} onChange={onChange} />
    case "select":
      return <Select id={id} allowClear={!field.required} options={field.options} value={value} onChange={onChange} />
    case "multiselect":
      return <Select id={id} mode="multiple" options={field.options} value={value} onChange={onChange} />
    case "tags":
      return <Select id={id} mode="tags" open={false} value={value} onChange={onChange} />
    case "date":
      return (
        <DatePicker
          id={id}
          style={{ width: "100%" }}
          value={value ? dayjs(String(value)) : null}
          onChange={(d) => onChange?.(d ? d.format("YYYY-MM-DD") : undefined)}
        />
      )
    case "datetime":
      return (
        <DatePicker
          id={id}
          showTime
          style={{ width: "100%" }}
          value={value ? dayjs(String(value)) : null}
          onChange={(d) => onChange?.(d ? d.toISOString() : undefined)}
        />
      )
    case "json":
      return <Input.TextArea id={id} value={JSON.stringify(value ?? null)} disabled />
    default:
      return <Input id={id} value={value as string} onChange={(e) => onChange?.(e.target.value)} />
  }
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
  if (!model.search && model.filters.length === 0) return null
  return (
    <Space wrap>
      {model.search && (
        <Input.Search
          allowClear
          placeholder="搜索"
          defaultValue={filters[model.search] as string}
          onSearch={(q) => list.setFilters({ [model.search as string]: q || undefined })}
          style={{ width: 220 }}
        />
      )}
      {model.filters.map((f) =>
        f.options ? (
          <Select
            key={f.name}
            allowClear
            placeholder={f.label}
            aria-label={f.label}
            options={f.options}
            mode={f.kind === "multiselect" ? "multiple" : undefined}
            value={filters[f.name]}
            onChange={(v) => list.setFilters({ [f.name]: v ?? undefined })}
            style={{ minWidth: 140 }}
          />
        ) : (
          <Input
            key={f.name}
            allowClear
            placeholder={f.label}
            aria-label={f.label}
            defaultValue={filters[f.name] as string}
            onPressEnter={(e) => list.setFilters({ [f.name]: e.currentTarget.value || undefined })}
            style={{ width: 160 }}
          />
        ),
      )}
    </Space>
  )
}

/** 创建和修改的表单：字段、校验规则（与服务端相同）来自定义，服务端的字段错误显示在输入框下面。 */
export function ResourceForm({
  model,
  open,
  row,
  inputs = {},
  onClose,
}: {
  model: ResourceModel
  open: boolean
  /** 修改的行；新建时为空。 */
  row?: ResourceRow
  inputs?: ResourcePageProps["inputs"]
  onClose(): void
}): ReactNode {
  const [form] = Form.useForm()
  const { save } = useResourceMutations(model)
  const { message } = AntApp.useApp()
  const mode = row ? "update" : "create"
  const rule = useMemo(() => antdRule(model.formSchema(mode), model.ruleOptions), [model, mode])
  const [formError, setFormError] = useState<string>()
  return (
    <DrawerForm
      form={form}
      title={`${row ? "修改" : "新建"}${model.title}`}
      open={open}
      initialValues={formValues(model, row)}
      drawerProps={{ destroyOnHidden: true, onClose }}
      onOpenChange={(o) => {
        if (!o) onClose()
        setFormError(undefined)
      }}
      onFinish={async (values) => {
        setFormError(undefined)
        try {
          await save.mutateAsync({ key: row ? model.rowKey(row) : undefined, values })
          message.success(row ? "已保存" : "已创建")
          onClose()
          return true
        } catch (err) {
          const other = setServerErrors(err, form)
          if (other) setFormError(other.message)
          return false
        }
      }}
    >
      {formError && (
        <Typography.Paragraph type="danger" role="alert">
          {formError}
        </Typography.Paragraph>
      )}
      {model.form.map((f) => {
        const Input = inputs[f.name] ?? defaultInput
        return (
          <Form.Item
            key={f.name}
            name={f.name}
            label={f.label}
            required={f.required}
            rules={[rule]}
            valuePropName="value"
          >
            <Input field={f} />
          </Form.Item>
        )
      })}
    </DrawerForm>
  )
}

/**
 * 资源的页面：列表、搜索和筛选、新建和修改的表单、删除和其他操作，按权限显示按钮。
 *
 *	export const Route = createFileRoute("/_app/articles")({ validateSearch: articles.listSearch, component: () => <ResourcePage model={articles} route={Route} /> })
 */
export function ResourcePage({
  model,
  route,
  render = {},
  inputs,
  rowActions,
  toolbar = [],
}: ResourcePageProps): ReactNode {
  const view = useResourceList(model, route)
  const { remove, run } = useResourceMutations(model)
  const { modal, message } = AntApp.useApp()
  const [editing, setEditing] = useState<{ row?: ResourceRow } | undefined>()

  const confirmThen = (title: string, danger: boolean, action: () => Promise<unknown>) =>
    modal.confirm({
      title,
      okButtonProps: { danger },
      onOk: async () => {
        try {
          await action()
          message.success("已完成")
        } catch (err) {
          message.error(describeError(err).message)
        }
      },
    })

  const columns: ProColumns<ResourceRow>[] = model.columns.map((f) => ({
    title: f.label,
    dataIndex: f.name,
    key: f.name,
    sorter: f.sortable,
    sortOrder:
      view.list.tableState.sorting[0]?.id === f.name
        ? view.list.tableState.sorting[0]?.desc
          ? "descend"
          : "ascend"
        : null,
    render: (_, row) =>
      render[f.name]?.(row[f.name], row) ?? <FieldValue model={model} field={f} value={row[f.name]} />,
  }))

  const menu = (row: ResourceRow): MenuProps["items"] => {
    const key = model.rowKey(row)
    const items: MenuProps["items"] = model.actions
      .filter((a) => view.can.action(a.name))
      .map((a) => ({
        key: `action:${a.name}`,
        label: a.label,
        danger: a.danger,
        onClick: () => {
          const go = () => run.mutateAsync({ action: a.name, key })
          if (a.confirm) confirmThen(`${a.label}这一项？`, a.danger, go)
          else
            void go().then(
              () => message.success("已完成"),
              (err) => message.error(describeError(err).message),
            )
        },
      }))
    items.push(...(rowActions?.(row) ?? []))
    if (view.can.delete) {
      if (items.length) items.push({ type: "divider" })
      items.push({
        key: "delete",
        label: "删除",
        icon: <DeleteOutlined />,
        danger: true,
        onClick: () => confirmThen(`删除这一项？`, true, () => remove.mutateAsync(key)),
      })
    }
    return items
  }
  const hasRowActions =
    view.can.update || view.can.delete || model.actions.some((a) => view.can.action(a.name)) || !!rowActions
  if (hasRowActions) {
    columns.push({
      title: "操作",
      key: "_actions",
      fixed: "right",
      width: 120,
      render: (_, row) => {
        const items = menu(row)
        return (
          <Space size={0}>
            {view.can.update && (
              <Button type="link" size="small" icon={<EditOutlined />} onClick={() => setEditing({ row })}>
                修改
              </Button>
            )}
            {items && items.length > 0 && (
              <Dropdown menu={{ items }} trigger={["click"]}>
                <Button type="text" size="small" aria-label="更多操作" icon={<MoreOutlined />} />
              </Dropdown>
            )}
          </Space>
        )
      },
    })
  }

  return (
    <>
      <ProTable<ResourceRow>
        rowKey={(row) => model.rowKey(row)}
        headerTitle={model.title}
        columns={columns}
        dataSource={view.rows}
        loading={view.loading}
        search={false}
        options={{ reload: () => view.refetch(), density: false, setting: false }}
        toolbar={{ filter: <Filters model={model} list={view.list} /> }}
        toolBarRender={() => [
          ...toolbar,
          view.can.create && (
            <Button key="create" type="primary" icon={<PlusOutlined />} onClick={() => setEditing({})}>
              新建
            </Button>
          ),
        ]}
        pagination={{
          current: view.list.page,
          pageSize: view.list.perPage,
          total: view.total,
          showSizeChanger: true,
        }}
        onChange={(pagination, _filters, sorter) => {
          const s = Array.isArray(sorter) ? sorter[0] : sorter
          const field = s?.order ? String(s.columnKey ?? s.field) : undefined
          const sort = field ? `${s?.order === "descend" ? "-" : ""}${field}` : undefined
          if (sort !== view.list.sort) view.list.setSort(sort)
          else if (pagination.pageSize && pagination.pageSize !== view.list.perPage)
            view.list.setPerPage(pagination.pageSize)
          else if (pagination.current && pagination.current !== view.list.page) view.list.setPage(pagination.current)
        }}
      />
      {view.error ? (
        <Typography.Paragraph type="danger" role="alert">
          {describeError(view.error).message}
        </Typography.Paragraph>
      ) : null}
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
