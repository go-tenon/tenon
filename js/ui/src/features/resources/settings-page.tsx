// 设置页面：几组设置放在同一个页面，每组一个页内标签，按权限隐藏读不了的组。每组的表单是 SettingsForm。

import type { ReactNode } from "react"
import { useCan } from "../../app/app"
import { Page, type PageProps } from "../../components/page"
import type { FieldInput } from "./fields"
import type { SettingsModel } from "./model"
import { SettingsForm } from "./settings-form"

/** SettingsPage 中的一组设置：设置模型，以及这一组的说明和输入组件。 */
export interface SettingsGroup {
  model: SettingsModel
  /** 表单上面的说明。 */
  description?: ReactNode
  /** 覆盖某个字段的输入组件，与 SettingsForm 相同。 */
  inputs?: { [field: string]: FieldInput }
}

/** SettingsPage 的选项：Page 的选项（tabs 由各组生成），加上各组设置。 */
export interface SettingsPageProps extends Omit<PageProps, "tabs"> {
  /** 页面中的各组设置，按顺序排成标签；不需要说明和输入组件时直接写设置模型。 */
  groups: (SettingsModel | SettingsGroup)[]
  /** 各组都自动保存，见 SettingsForm 的 autoSave。 */
  autoSave?: boolean
}

/**
 * 设置页面：每组设置一个页内标签，标签的文字是这组设置的显示文字，地址中的 ?tab= 是它的名称。
 * 当前身份没有读取权限的组不显示；只剩一组时不显示标签，直接显示表单。children 在标签的上面。
 *
 *	<SettingsPage title="系统设置" groups={[siteSettings, gatewaySettings]} autoSave />
 */
export function SettingsPage({ groups, autoSave, children, ...page }: SettingsPageProps): ReactNode {
  const can = useCan()
  const visible = groups.map((g) => ("model" in g ? g : { model: g })).filter((g) => g.model.allowed(can, "get"))
  const form = (g: SettingsGroup) => (
    <SettingsForm model={g.model} title={null} description={g.description} inputs={g.inputs} autoSave={autoSave} />
  )
  if (visible.length === 1) {
    return (
      <Page {...page}>
        {children}
        {form(visible[0])}
      </Page>
    )
  }
  return (
    <Page {...page} tabs={visible.map((g) => ({ value: g.model.name, label: g.model.title, content: form(g) }))}>
      {children}
    </Page>
  )
}
