// 设置的 antd 外观：一组设置是一个表单，字段、显示文字和校验规则来自 @go-tenon/shell 的 settingsModel
// （服务端的 rest.API.Settings）。可以用“保存”按钮，也可以修改后自动保存。
import {
  type AutoSaveStatus,
  antdRule,
  createAutoSaver,
  formValidator,
  type Settings,
  type SettingsModel,
  setServerErrors,
  useSettings,
} from "@go-tenon/shell"
import { Alert, App as AntApp, Button, Card, Form, Space, Spin, Typography } from "antd"
import { type ReactNode, useEffect, useMemo, useState, useSyncExternalStore } from "react"
import { defaultInput, type FieldInput } from "./resource"

/** SettingsForm 的选项。 */
export interface SettingsFormProps {
  /** 设置的模型：settingsModel(settings.<名称>, kit, queryClient)。 */
  model: SettingsModel
  /** 修改后自动保存，不显示“保存”按钮。默认 false。 */
  autoSave?: boolean
  /** 覆盖某个字段的输入组件，与 ResourcePage 的 inputs 相同。 */
  inputs?: { [field: string]: FieldInput }
  /** 标题，默认是设置的显示文字（<名称>.title）。传 null 不显示。 */
  title?: ReactNode | null
}

const statusText: Record<Exclude<AutoSaveStatus, "idle">, string> = {
  pending: "有未保存的修改",
  saving: "保存中…",
  saved: "已保存",
  error: "保存失败，修改后会重试",
}

/**
 * 设置的表单：读取设置作为初始值，按权限决定能否修改。没有保存的权限时只读。
 *
 *	<SettingsForm model={blogSettings} autoSave />
 */
export function SettingsForm({ model, autoSave = false, inputs = {}, title }: SettingsFormProps): ReactNode {
  const view = useSettings(model)
  if (view.loading) return <Spin style={{ display: "block", padding: 24 }} />
  if (!view.values) {
    return (
      <Alert
        type="error"
        showIcon
        message="设置读取失败"
        action={
          <Button size="small" onClick={view.refetch}>
            重试
          </Button>
        }
      />
    )
  }
  return <Loaded view={view} values={view.values} autoSave={autoSave} inputs={inputs} title={title} />
}

/** 读到设置之后的表单：初始值和自动保存只在第一次渲染时创建，保存后重新读取的值不会打断还在编辑的表单。 */
function Loaded({
  view,
  values,
  autoSave,
  inputs,
  title,
}: {
  view: Settings
  values: Record<string, unknown>
  autoSave: boolean
  inputs: { [field: string]: FieldInput }
  title?: ReactNode | null
}): ReactNode {
  const { model } = view
  const [form] = Form.useForm()
  const { message } = AntApp.useApp()
  const rule = useMemo(() => antdRule(model.formSchema(), model.ruleOptions), [model])
  const [formError, setFormError] = useState<string>()
  const [saving, setSaving] = useState(false)

  const save = async (v: Record<string, unknown>) => {
    setFormError(undefined)
    try {
      await view.save(v)
    } catch (err) {
      const other = setServerErrors(err, form)
      if (other) setFormError(other.message)
      throw err
    }
  }
  const saveRef = useLatest(save)

  // 自动保存：值变化后等用户停下再保存，校验不通过时不保存，离开页面时保存还在等待的修改。
  const [saver] = useState(() => {
    if (!autoSave) return undefined
    const validate = formValidator(model.formSchema(), model.ruleOptions)
    return createAutoSaver<Record<string, unknown>>(values, {
      save: (v) => saveRef.current(v),
      validate: (v) => Object.keys(validate(v)).length === 0,
    })
  })
  useEffect(() => () => void saver?.stop(), [saver])
  const status = useSyncExternalStore(
    saver?.subscribe ?? noopSubscribe,
    () => saver?.status() ?? "idle",
    () => "idle" as const,
  )

  const heading = title === undefined ? model.title : title
  return (
    <Card
      title={heading}
      extra={
        saver &&
        status !== "idle" && (
          <Typography.Text type={status === "error" ? "danger" : "secondary"} role="status">
            {statusText[status]}
          </Typography.Text>
        )
      }
    >
      <Form
        form={form}
        layout="vertical"
        initialValues={values}
        disabled={!view.canSave}
        onValuesChange={() => saver?.change(form.getFieldsValue(true))}
        onFinish={async (v) => {
          setSaving(true)
          try {
            await save(v)
            message.success("已保存")
          } catch {
            // 错误已经显示在字段和表单上方。
          } finally {
            setSaving(false)
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
            <Form.Item key={f.name} name={f.name} label={f.label} required={f.required} rules={[rule]}>
              <Input field={f} />
            </Form.Item>
          )
        })}
        {!saver && view.canSave && (
          <Space>
            <Button type="primary" htmlType="submit" loading={saving}>
              保存
            </Button>
          </Space>
        )}
      </Form>
    </Card>
  )
}

const noopSubscribe = () => () => {}

/** 总是最新的值，给只创建一次的回调使用。 */
function useLatest<T>(value: T): { readonly current: T } {
  const [ref] = useState(() => ({ current: value }))
  ref.current = value
  return ref
}
