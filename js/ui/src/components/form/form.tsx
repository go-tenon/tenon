// 表单：基于 @mantine/form。校验规则来自生成的 schemas.ts（与服务端相同），服务端返回的字段错误显示在字段下面，
// 其他错误显示在表单上方；有未保存的修改时，离开页面之前询问。

import type { ErrorInfo, Schema } from "@go-tenon/client"
import { Alert } from "@mantine/core"
import { type UseFormReturnType, useForm } from "@mantine/form"
import { IconAlertCircle } from "@tabler/icons-react"
import { type ReactNode, useMemo } from "react"
import { errorMessage } from "../feedback"
import { type FormRulesOptions, formValidator, serverErrors } from "./rules"

/** useSchemaForm 的选项。 */
export interface SchemaFormOptions<T> {
  /** 请求体的 schema，例如 schemas.article.create.body，或者 model.formSchema("create")。 */
  schema: Schema
  /** components、messages 和 language，例如 model.ruleOptions。 */
  rules?: FormRulesOptions
  initialValues: T
}

/**
 * @mantine/form 的表单，用生成的规则校验：
 *
 *   const form = useSchemaForm({ schema: schemas.article.create.body, rules: { components, messages }, initialValues })
 *   <form onSubmit={form.onSubmit(save)}><TextInput {...form.getInputProps("title")} /></form>
 *
 * T 的约束与 useForm 相同（Record<string, any>）：生成的客户端中请求体是 interface，可以直接作为 T，
 * 例如 initialValues: { name: "" } as ArticleChanges。
 */
// biome-ignore lint/suspicious/noExplicitAny: 与 @mantine/form 的 useForm 相同的约束，interface 才能满足它
export function useSchemaForm<T extends Record<string, any>>({
  schema,
  rules,
  initialValues,
}: SchemaFormOptions<T>): UseFormReturnType<T> {
  const validate = useMemo(() => formValidator(schema, rules), [schema, rules])
  // 提交时校验整个表单；之后离开一个字段时校验它，修改时清除它的错误。
  return useForm<T>({
    mode: "controlled",
    initialValues,
    validate: (values) => validate(values),
    validateInputOnBlur: true,
  })
}

/**
 * 把接口返回的错误设置到表单：字段错误显示在字段下面，返回其他错误（没有时为 undefined），交给 FormError 显示。
 *
 *   catch (err) { setFormError(applyServerErrors(form, err)) }
 */
export function applyServerErrors(
  form: Pick<UseFormReturnType<never>, "setFieldError">,
  err: unknown,
): ErrorInfo | undefined {
  const { fields, form: other } = serverErrors(err)
  for (const [path, message] of Object.entries(fields)) form.setFieldError(path as never, message)
  return other
}

/** 表单上方的错误：不属于某个字段的错误，例如 slug 已被使用、断网。 */
export function FormError({ error }: { error?: ErrorInfo }): ReactNode {
  if (!error) return null
  return (
    <Alert color="red" variant="light" icon={<IconAlertCircle size={18} />} role="alert">
      {errorMessage(error)}
    </Alert>
  )
}
