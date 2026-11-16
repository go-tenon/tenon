// 表单：react-hook-form 加上生成的校验规则（@go-tenon/shell 的 resolver），提示与服务端相同；
// 提交失败时字段错误显示在对应的输入框下面，其他错误显示在表单上方。useAutoSave 让表单在修改后自动保存。

import type { ErrorInfo, Schema } from "@go-tenon/client"
import {
  type AutoSaveStatus as AutoSaveState,
  createAutoSaver,
  type FormRulesOptions,
  formValidator,
  resolver,
  setServerErrors,
  useUnsavedChanges,
} from "@go-tenon/shell"
import { Check, CircleAlert, LoaderCircle } from "lucide-react"
import { type ReactNode, useEffect, useId, useMemo, useRef, useState, useSyncExternalStore } from "react"
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

/** useAutoSave 的选项。 */
export interface AutoSaveOptions<T extends FieldValues> {
  /** 生成的 schemas.ts 中请求体的规则：不符合时不保存，等用户改正。 */
  schema: Schema
  rules?: FormRulesOptions
  /** 保存：调用接口。抛出的错误按服务端的位置设置到字段；返回 false 表示没有保存成功。 */
  save: (values: T) => Promise<unknown>
  /** 停下多久后保存（毫秒），默认 600。 */
  delay?: number
  /** 为 false 时暂停自动保存，例如数据还在加载。默认 true。 */
  enabled?: boolean
}

/** useAutoSave 的返回值。 */
export interface AutoSave<T extends FieldValues> {
  /** idle、pending（等待保存）、saving、saved、error，交给 AutoSaveStatus 显示。 */
  status: AutoSaveState
  /** 立即保存，不等 delay。force 为 true 时即使与上次保存的相同也提交，例如表单之外的改动。 */
  saveNow: (force?: boolean) => Promise<void>
  /** 把 values 当作已经保存的值，例如重新加载了服务端的数据。 */
  markSaved: (values: T) => void
  /** 放弃还在等待的修改：离开页面时不再保存它们。 */
  discard: () => void
}

/**
 * 自动保存：表单的值变化后，等用户停一会儿再保存，不需要“保存”按钮。校验不通过时不保存；与上次保存成功的值
 * 相同时不保存；保存一次只进行一个，期间的修改在完成后再保存一次；离开页面时立即保存还在等待的修改。
 * 规则见 @go-tenon/shell 的 createAutoSaver。
 *
 *   const form = useForm<Settings>({ resolver: resolver(schema, rules), defaultValues })
 *   const autosave = useAutoSave(form, { schema, rules, save: (v) => api.settings.update(v) })
 *   <AutoSaveStatus status={autosave.status} />
 */
export function useAutoSave<T extends FieldValues>(form: UseFormReturn<T>, options: AutoSaveOptions<T>): AutoSave<T> {
  const latest = useRef(options)
  useEffect(() => {
    latest.current = options
  })
  const validate = useMemo(() => formValidator(options.schema, options.rules), [options.schema, options.rules])
  const [saver] = useState(() =>
    createAutoSaver<T>(form.getValues(), {
      delay: options.delay,
      validate: (values) => Object.keys(validate(values)).length === 0,
      save: async (values) => {
        try {
          if ((await latest.current.save(values)) === false) return false
        } catch (err) {
          setServerErrors(err, form as never)
          throw err
        }
        // 保存的正是当前的值时，表单不再算作有未保存的修改。
        if (JSON.stringify(form.getValues()) === JSON.stringify(values)) form.reset(values, { keepValues: true })
        return true
      },
    }),
  )
  const enabled = options.enabled ?? true
  useEffect(() => {
    if (!enabled) return
    const subscription = form.watch(() => saver.change(form.getValues()))
    return () => subscription.unsubscribe()
  }, [form, saver, enabled])
  // 离开页面时立即保存还在等待的修改。
  useEffect(() => () => void saver.stop(), [saver])
  const status = useSyncExternalStore(saver.subscribe, saver.status, saver.status)
  return { status, saveNow: saver.flush, markSaved: saver.markSaved, discard: saver.discard }
}

/** 自动保存的状态：保存中、已保存、保存失败。没有修改时不显示。 */
export function AutoSaveStatus({ status, className }: { status: AutoSaveState; className?: string }): ReactNode {
  if (status === "idle") return null
  const content = {
    pending: { icon: null, text: "有未保存的修改", tone: "text-muted-foreground" },
    saving: {
      icon: <LoaderCircle className="size-3.5 animate-spin" />,
      text: "保存中…",
      tone: "text-muted-foreground",
    },
    saved: { icon: <Check className="size-3.5" />, text: "已保存", tone: "text-muted-foreground" },
    error: { icon: <CircleAlert className="size-3.5" />, text: "保存失败，修改后会重试", tone: "text-destructive" },
  }[status]
  return (
    <span role="status" className={cn("inline-flex items-center gap-1 text-xs", content.tone, className)}>
      {content.icon}
      {content.text}
    </span>
  )
}
