// 设置：shell/ 中的设置模型（settingsModel）的外观。一组值的表单：读取设置作为初始值，按权限决定能否修改，
// 可以自动保存。

import type { ErrorInfo } from "@go-tenon/client"
import { Alert, Box, Button, Card, Group, Skeleton, Stack, Text, Title } from "@mantine/core"
import { IconCheck } from "@tabler/icons-react"
import { type ReactNode, useEffect, useState, useSyncExternalStore } from "react"
import { notifySuccess } from "./feedback"
import { DefaultInput, type FieldInput } from "./fields"
import { applyServerErrors, FormError, useSchemaForm } from "./form"
import {
  type AutoSaveStatus,
  createAutoSaver,
  formValidator,
  type Settings,
  type SettingsModel,
  useSettings,
  useUnsavedChanges,
} from "./shell"

/** SettingsForm 的选项。 */
export interface SettingsFormProps {
  model: SettingsModel
  /** 自动保存：没有保存按钮，修改后停下一会儿就保存，显示保存的状态。 */
  autoSave?: boolean
  /** 覆盖某个字段的输入组件。 */
  inputs?: { [field: string]: FieldInput }
  /** 卡片的标题，默认是设置的显示文字；null 不显示。 */
  title?: ReactNode | null
  /** 标题下面的说明。 */
  description?: ReactNode
}

const statusText: Record<Exclude<AutoSaveStatus, "idle">, string> = {
  pending: "有未保存的修改",
  saving: "保存中…",
  saved: "已保存",
  error: "保存失败，修改后会重试",
}

/**
 * 设置的表单：读取设置作为初始值，没有保存的权限时只读。
 *
 *	<SettingsForm model={blogSettings} autoSave />
 */
export function SettingsForm({
  model,
  autoSave = false,
  inputs = {},
  title,
  description,
}: SettingsFormProps): ReactNode {
  const view = useSettings(model)
  const heading = title === undefined ? model.title : title
  return (
    <Card>
      {(heading || description) && (
        <Box mb="lg">
          {heading && <Title order={4}>{heading}</Title>}
          {description && (
            <Text size="sm" c="dimmed" mt={4}>
              {description}
            </Text>
          )}
        </Box>
      )}
      {view.loading ? (
        <Stack gap="md" maw={520}>
          {model.form.map((f) => (
            <Skeleton key={f.name} h={56} />
          ))}
        </Stack>
      ) : view.values ? (
        <Loaded view={view} values={view.values} autoSave={autoSave} inputs={inputs} />
      ) : (
        <Alert color="red" variant="light" title="设置读取失败" role="alert">
          <Button size="xs" variant="default" mt="xs" onClick={view.refetch}>
            重试
          </Button>
        </Alert>
      )}
    </Card>
  )
}

/** 读到设置之后的表单：初始值和自动保存只在第一次渲染时创建，保存后重新读取的值不会打断还在编辑的表单。 */
function Loaded({
  view,
  values,
  autoSave,
  inputs,
}: {
  view: Settings
  values: Record<string, unknown>
  autoSave: boolean
  inputs: { [field: string]: FieldInput }
}): ReactNode {
  const { model } = view
  const form = useSchemaForm({ schema: model.formSchema(), rules: model.ruleOptions, initialValues: values })
  const [formError, setFormError] = useState<ErrorInfo>()
  const [saving, setSaving] = useState(false)

  const save = async (v: Record<string, unknown>) => {
    setFormError(undefined)
    try {
      await view.save(v)
      form.resetDirty(v)
    } catch (err) {
      setFormError(applyServerErrors(form, err))
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
  useEffect(() => {
    saver?.resume()
    return () => void saver?.stop()
  }, [saver])
  const status = useSyncExternalStore(
    saver?.subscribe ?? noopSubscribe,
    () => saver?.status() ?? "idle",
    () => "idle" as const,
  )
  const unsaved = useUnsavedChanges(!saver && form.isDirty())

  return (
    <form
      noValidate
      onSubmit={form.onSubmit(async (v) => {
        setSaving(true)
        try {
          await save(v)
          unsaved.release()
          notifySuccess("已保存")
        } catch {
          // 错误已经显示在字段和表单上方。
        } finally {
          setSaving(false)
        }
      })}
    >
      <Stack gap="md" maw={520}>
        <FormError error={formError} />
        {model.form.map((f) => {
          const Input = inputs[f.name] ?? DefaultInput
          const props = form.getInputProps(f.name)
          return (
            <Input
              key={f.name}
              field={f}
              label={f.label}
              {...props}
              disabled={!view.canSave}
              onChange={(v) => {
                props.onChange(v)
                saver?.change({ ...form.getValues(), [f.name]: v })
              }}
            />
          )
        })}
        {saver ? (
          status !== "idle" && (
            <Group gap={6} role="status">
              {status === "saved" && <IconCheck size={14} color="var(--mantine-color-teal-6)" />}
              <Text size="xs" c={status === "error" ? "red" : "dimmed"}>
                {statusText[status]}
              </Text>
            </Group>
          )
        ) : view.canSave ? (
          <Group>
            <Button type="submit" loading={saving} disabled={!form.isDirty()}>
              保存
            </Button>
          </Group>
        ) : null}
      </Stack>
    </form>
  )
}

const noopSubscribe = () => () => {}

/** 总是最新的值，给只创建一次的回调使用。 */
function useLatest<T>(value: T): { readonly current: T } {
  const [ref] = useState(() => ({ current: value }))
  ref.current = value
  return ref
}
