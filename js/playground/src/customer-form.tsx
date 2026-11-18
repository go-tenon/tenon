// 自己写的资源表单：新建客户时，选了等级就按等级预填信用额度。这种字段之间的联动在声明中表达不了，
// 表单的内容自己写，页面的外壳（标签标题、保存后回到列表、未保存的修改）仍由 ResourceFormPage 提供。

import { FormError, formValues, ResourceFormButtons, type ResourceFormContext, useResourceForm } from "@go-tenon/ui"
import { NumberInput, Select, Stack, TextInput } from "@mantine/core"

// 各等级默认的信用额度。
const creditOf: Record<string, number> = { 普通: 5000, 银卡: 20000, 金卡: 50000, 钻石: 100000 }

export function CustomerForm({ ctx }: { ctx: ResourceFormContext }) {
  const fields = ctx.editKey ? ctx.model.updateForm : ctx.model.createForm
  const level = fields.find((f) => f.name === "level")
  const { form, error, submit, saving } = useResourceForm(ctx, { initialValues: formValues(fields, ctx.row) })
  return (
    <form noValidate onSubmit={submit}>
      <Stack gap="md">
        <FormError error={error} />
        <TextInput label="名称" required {...form.getInputProps("name")} />
        <Select
          label="等级"
          required
          data={level?.options ?? []}
          {...form.getInputProps("level")}
          onChange={(v) => {
            form.setFieldValue("level", v ?? "")
            if (v) form.setFieldValue("credit", creditOf[v]) // 联动：按等级预填信用额度
          }}
        />
        <NumberInput label="信用额度" description="选择等级后自动填写，可以修改" {...form.getInputProps("credit")} />
        <ResourceFormButtons ctx={ctx} saving={saving} />
      </Stack>
    </form>
  )
}
