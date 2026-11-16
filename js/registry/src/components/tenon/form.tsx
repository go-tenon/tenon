// 表单：react-hook-form 加上生成的校验规则（@go-tenon/shell 的 resolver），提示与服务端相同；
// 提交失败时字段错误显示在对应的输入框下面，其他错误显示在表单上方。

import type { ErrorInfo, Schema } from "@go-tenon/client"
import { type FormRulesOptions, resolver, setServerErrors, useUnsavedChanges } from "@go-tenon/shell"
import { CircleAlert } from "lucide-react"
import { type ReactNode, useId, useState } from "react"
import { type DefaultValues, type FieldPath, type FieldValues, type UseFormReturn, useForm } from "react-hook-form"
import { Label } from "@/components/ui/label"
import { cn } from "@/lib/utils"

/** useApiForm 的选项。 */
export interface ApiFormOptions<T extends FieldValues> {
  /** 生成的 schemas.ts 中请求体的规则，例如 schemas.article.create.body。 */
  schema: Schema
  /** 生成的 components、messages 和提示的语言。 */
  rules?: FormRulesOptions
  defaultValues?: DefaultValues<T>
  /** 提交：调用接口。抛出的错误按服务端的位置设置到字段。 */
  submit: (values: T) => Promise<unknown>
  /** 有未保存的修改时离开前询问，默认 true。 */
  guard?: boolean
}

/** useApiForm 的返回值。 */
export interface ApiForm<T extends FieldValues> {
  form: UseFormReturn<T>
  /** 交给 <form onSubmit>。 */
  onSubmit: (e?: React.BaseSyntheticEvent) => Promise<void>
  /** 不属于某个字段的错误（例如 slug 已被使用、断网）。 */
  formError?: ErrorInfo
  pending: boolean
}

/**
 * 调用接口的表单：前端按生成的规则校验，提交失败时显示服务端的错误，有未保存的修改时离开前询问。
 *
 *   const { form, onSubmit, formError, pending } = useApiForm<ArticleChanges>({
 *     schema: schemas.article.create.body, rules, submit: (v) => api.article.create(v),
 *   })
 */
export function useApiForm<T extends FieldValues>(options: ApiFormOptions<T>): ApiForm<T> {
  const form = useForm<T>({
    resolver: resolver<T>(options.schema, options.rules) as never,
    defaultValues: options.defaultValues,
  })
  const [formError, setFormError] = useState<ErrorInfo>()
  const [pending, setPending] = useState(false)
  const unsaved = useUnsavedChanges((options.guard ?? true) && form.formState.isDirty)
  const onSubmit = form.handleSubmit(async (values) => {
    setPending(true)
    setFormError(undefined)
    try {
      await options.submit(values)
      unsaved.release()
      form.reset(values)
    } catch (err) {
      setFormError(setServerErrors(err, form as never))
    } finally {
      setPending(false)
    }
  })
  return { form, onSubmit, formError, pending }
}

/** 表单上方的错误。 */
export function FormError({ error }: { error?: ErrorInfo }): ReactNode {
  if (!error) return null
  const message = error.message ?? (error.kind === "network" ? "网络连接失败，请稍后重试。" : "提交失败，请稍后重试。")
  return (
    <div
      role="alert"
      className="flex items-center gap-2 rounded-md border border-destructive/50 px-3 py-2 text-sm text-destructive"
    >
      <CircleAlert className="size-4" />
      {message}
    </div>
  )
}

/**
 * 一个字段：标签、输入框和错误提示。children 是一个函数，拿到 id 和 react-hook-form 的 register 结果：
 *
 *   <Field form={form} name="title" label="标题">{(p) => <Input {...p} />}</Field>
 */
export function Field<T extends FieldValues>({
  form,
  name,
  label,
  description,
  children,
  className,
}: {
  form: UseFormReturn<T>
  name: FieldPath<T>
  label: ReactNode
  description?: ReactNode
  children: (props: ReturnType<UseFormReturn<T>["register"]> & { id: string; "aria-invalid"?: boolean }) => ReactNode
  className?: string
}): ReactNode {
  const id = useId()
  const error = form.getFieldState(name, form.formState).error
  return (
    <div className={cn("grid gap-2", className)}>
      <Label htmlFor={id}>{label}</Label>
      {children({ ...form.register(name), id, "aria-invalid": error ? true : undefined })}
      {description && !error && <p className="text-xs text-muted-foreground">{description}</p>}
      {error?.message && <p className="text-xs text-destructive">{error.message}</p>}
    </div>
  )
}
