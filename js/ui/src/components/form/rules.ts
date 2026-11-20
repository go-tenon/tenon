// 表单：用生成的 schemas.ts 和 messages.ts 在前端校验（规则和提示与服务端相同），把服务端返回的错误显示到字段上。
// 不依赖表单库：@go-tenon/ui 的 useSchemaForm、applyServerErrors 把它们接到 @mantine/form。

import {
  describeError,
  type ErrorInfo,
  fieldErrors,
  type Schema,
  type ValidateOptions,
  validate,
} from "@go-tenon/client"
import { useBlocker } from "@tanstack/react-router"
import { useCallback, useContext, useEffect, useMemo, useRef } from "react"
import { TabPaneContext } from "../../app/tabs/context"
import { text } from "../../i18n/i18n"

/** 校验的选项：生成的 components、messages，以及提示的语言。 */
export type FormRulesOptions = Pick<ValidateOptions, "components" | "messages" | "language">

/** 字段路径（例如 items.0.name）到提示的映射；没有错误时为空对象。 */
export type FieldMessages = Record<string, string>

/** 返回校验整个表单的函数：用 schema 校验 values，每个字段取第一条提示。 */
export function formValidator(schema: Schema, options: FormRulesOptions = {}): (values: unknown) => FieldMessages {
  return (values) => fieldErrors(validate(schema, values, { ...options, location: "body" }))
}

/** 服务端错误拆分的结果。 */
export interface ServerErrors {
  /** 请求体中字段的错误：字段路径到提示。 */
  fields: FieldMessages
  /**
   * 不属于某个字段的错误（例如 slug 已被使用、断网），显示在表单上方；只有字段错误时为空。
   * message 是服务端翻译好的提示，断网等没有服务端提示时为空，由界面按 kind 显示。
   */
  form?: ErrorInfo
}

/** 拆分接口返回的错误：请求体的字段错误按 errors[].location 对应到字段，其余作为整个表单的错误。 */
export function serverErrors(err: unknown): ServerErrors {
  const fields = fieldErrors(err)
  return Object.keys(fields).length > 0 ? { fields } : { fields, form: describeError(err) }
}

/** useUnsavedChanges 的选项。 */
export interface UnsavedChangesOptions {
  /** 询问是否离开，返回 true 时离开。默认用 window.confirm 和 message。 */
  confirm?: () => boolean | Promise<boolean>
  /** 默认的询问文字。 */
  message?: string
}

/** useUnsavedChanges 的返回值。 */
export interface UnsavedChanges {
  /** 保存成功后、跳转之前调用：这一次离开不再询问。 */
  release(): void
}

/**
 * 有未保存的修改（dirty 为 true）时：在应用内离开页面前询问，刷新或关闭浏览器时由浏览器提示。
 * dirty 来自表单，例如 @mantine/form 的 form.isDirty()。
 *
 * 页面在标签中时（见 TabsOutlet），切换标签会保留页面的状态，所以不询问；改为登记这个标签有未保存的修改，
 * 在关闭、刷新这个标签，以及 confirmDiscard（退出登录、切换租户）时询问。
 *
 *   const unsaved = useUnsavedChanges(form.isDirty())
 *   onSuccess: () => { unsaved.release(); navigate({ to: "/articles" }) }
 */
export function useUnsavedChanges(dirty: boolean, options: UnsavedChangesOptions = {}): UnsavedChanges {
  const state = useRef({ dirty, released: false })
  state.current.dirty = dirty
  useEffect(() => {
    if (dirty) state.current.released = false
  }, [dirty])
  const pane = useContext(TabPaneContext)
  const setTabDirty = pane?.setDirty
  useEffect(() => {
    setTabDirty?.(dirty)
  }, [setTabDirty, dirty])
  const ask = options.confirm ?? (() => window.confirm(options.message ?? text("tenon.unsaved.leave")))
  useBlocker({
    shouldBlockFn: async () => {
      if (pane || !state.current.dirty || state.current.released) return false
      return !(await ask())
    },
    enableBeforeUnload: () => !pane && state.current.dirty && !state.current.released,
  })
  const release = useCallback(() => {
    state.current.released = true
    setTabDirty?.(false)
  }, [setTabDirty])
  // 返回同一个对象：调用方常在 useEffect 的清理中 release（卸载时释放），对象每次都变的话，
  // 每次重新渲染都会 release，标签上“有未保存的修改”的标记随之丢失，关闭标签时不再询问。
  return useMemo(() => ({ release }), [release])
}
