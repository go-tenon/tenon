// 设置的 shadcn 外观：一组设置是一个表单，字段、显示文字和校验规则来自 @go-tenon/shell 的 settingsModel
// （服务端的 rest.API.Settings）。可以用“保存”按钮，也可以修改后自动保存。

import { type Settings, type SettingsModel, useSettings } from "@go-tenon/shell"
import type { ReactNode } from "react"
import type { FieldValues } from "react-hook-form"
import { AutoSaveStatus, FormError, useApiForm, useAutoSave } from "@/components/tenon/form"
import { PageError, PageLoading } from "@/components/tenon/page"
import { defaultInput, type FieldInput, ResourceFormField } from "@/components/tenon/resource-page"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card"

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

/**
 * 设置的表单：读取设置作为初始值，按权限决定能否修改。没有保存的权限时只读。
 *
 *   <SettingsForm model={blogSettings} autoSave />
 */
export function SettingsForm({ model, autoSave = false, inputs = {}, title }: SettingsFormProps): ReactNode {
  const view = useSettings(model)
  if (view.loading) return <PageLoading rows={3} />
  if (!view.values) return <PageError error={view.error} retry={view.refetch} />
  return <Loaded view={view} values={view.values} autoSave={autoSave} inputs={inputs} title={title} />
}

/** 读到设置之后的表单：初始值只在第一次渲染时交给 react-hook-form。 */
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
  const schema = model.formSchema()
  const { form, onSubmit, formError, pending } = useApiForm<FieldValues>({
    schema,
    rules: model.ruleOptions,
    defaultValues: values,
    submit: (v) => view.save(v),
    guard: !autoSave,
  })
  const autosave = useAutoSave(form, {
    schema,
    rules: model.ruleOptions,
    save: (v) => view.save(v),
    enabled: autoSave && view.canSave,
  })
  const heading = title === undefined ? model.title : title
  return (
    <Card>
      {(heading || autoSave) && (
        <CardHeader>
          {heading && <CardTitle>{heading}</CardTitle>}
          {autoSave && (
            <CardAction>
              <AutoSaveStatus status={autosave.status} />
            </CardAction>
          )}
        </CardHeader>
      )}
      <CardContent>
        <form onSubmit={onSubmit} className="grid gap-4" noValidate>
          <fieldset disabled={!view.canSave} className="grid gap-4 disabled:opacity-70">
            <FormError error={formError} />
            {model.form.map((f) => (
              <ResourceFormField key={f.name} form={form} field={f} input={inputs[f.name] ?? defaultInput} />
            ))}
          </fieldset>
          {!autoSave && view.canSave && (
            <div>
              <Button type="submit" disabled={pending}>
                保存
              </Button>
            </div>
          )}
        </form>
      </CardContent>
    </Card>
  )
}
