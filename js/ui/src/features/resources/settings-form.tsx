// 设置：设置模型（model.ts 的 settingsModel）的外观。一组值的表单：读取设置作为初始值，按权限决定能否修改，
// 可以自动保存。

import type { ErrorInfo } from "@go-tenon/client"
import { Alert, Box, Button, Card, Group, Skeleton, Stack, Text, Title } from "@mantine/core"
import { IconCheck } from "@tabler/icons-react"
import { type ReactNode, useEffect, useState, useSyncExternalStore } from "react"
import { notifySuccess } from "../../components/feedback"
import { type AutoSaveStatus, createAutoSaver } from "../../components/form/autosave"
import { applyServerErrors, FormError, useSchemaForm } from "../../components/form/form"
import { formValidator, useUnsavedChanges } from "../../components/form/rules"
import { type UiMessageId, useText } from "../../i18n/i18n"
import { DefaultInput, type FieldInput } from "./fields"
import { type Settings, type SettingsModel, useSettings } from "./model"

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

// 自动保存的每种状态的文字的消息 ID。
const statusText: Record<Exclude<AutoSaveStatus, "idle">, UiMessageId> = {
  pending: "tenon.autosave.pending",
  saving: "tenon.autosave.saving",
  saved: "tenon.common.saved",
  error: "tenon.autosave.error",
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
  const t = useText()
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
        <Stack gap="md">
          {model.form.map((f) => (
            <Skeleton key={f.name} h={56} />
          ))}
        </Stack>
      ) : view.values ? (
        <Loaded view={view} values={view.values} autoSave={autoSave} inputs={inputs} />
      ) : (
        <Alert color="red" variant="light" title={t("tenon.settings.load_failed")} role="alert">
          <Button size="xs" variant="default" mt="xs" onClick={view.refetch}>
            {t("tenon.common.retry")}
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
  const t = useText()
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
          notifySuccess(t("tenon.common.saved"))
        } catch {
          // 错误已经显示在字段和表单上方。
        } finally {
          setSaving(false)
        }
      })}
    >
      <Stack gap="md">
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
              {status === "saved" && <IconCheck size={14} color="var(--mantine-color-green-6)" />}
              <Text size="xs" c={status === "error" ? "red" : "dimmed"}>
                {t(statusText[status])}
              </Text>
            </Group>
          )
        ) : view.canSave ? (
          <Group justify="flex-end">
            <Button type="submit" loading={saving} disabled={!form.isDirty()}>
              {t("tenon.common.save")}
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
