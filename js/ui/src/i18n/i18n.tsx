// 界面文字：框架组件内置的文字（zh-CN.ts、en.ts，消息 ID 以 tenon. 开头）和应用的翻译。createApp 把应用的
// I18n（@go-tenon/client 的 createI18n）交给组件，组件用 useText 取文字；应用的翻译文件中同一个 ID 的消息覆盖内置的。
// 没有应用的 I18n 时（例如在 AppRoot 之外），组件用内置的简体中文。

import { createI18n, type I18n, type MessageData } from "@go-tenon/client"
import { createContext, type ReactNode, useCallback, useContext, useEffect, useSyncExternalStore } from "react"
import { en } from "./en"
import { zhCN } from "./zh-CN"

/** 框架组件内置文字的消息 ID。 */
export type UiMessageId = keyof typeof zhCN

/** 框架组件内置的文字：简体中文和英文。 */
export const uiMessages = { "zh-CN": zhCN, en } as const

/** 没有应用的 I18n 时使用：内置的文字，固定为简体中文。 */
const builtin: I18n = createI18n({ messages: uiMessages, defaultLanguage: "zh-CN", preferred: () => undefined })

const I18nContext = createContext<I18n>(builtin)

// 当前应用的 I18n：I18nProvider 挂载时设置。给 confirm、错误提示这类不在组件中调用的函数使用。
let active: I18n = builtin

/**
 * 把应用的 I18n 交给其中的组件，加入框架内置的文字，并让 <html lang> 跟随当前语言。createApp 已经用它包住了应用。
 */
export function I18nProvider({ i18n, children }: { i18n: I18n; children: ReactNode }): ReactNode {
  if (typeof window !== "undefined") active = i18n
  const language = useSyncExternalStore(i18n.subscribe, i18n.language, i18n.language)
  useEffect(() => {
    active = i18n
    if (typeof document !== "undefined") document.documentElement.lang = language
  }, [i18n, language])
  return <I18nContext.Provider value={i18n}>{children}</I18nContext.Provider>
}

/** 加入框架内置的文字：createApp 对应用的 I18n 调用一次。 */
export function withUiMessages(i18n: I18n): I18n {
  i18n.extend(uiMessages)
  return i18n
}

/** 当前应用的 I18n。 */
export function useI18n(): I18n {
  return useContext(I18nContext)
}

/** 当前的界面语言，例如 zh-CN；语言变化时重新渲染。 */
export function useLanguage(): string {
  const i18n = useI18n()
  return useSyncExternalStore(i18n.subscribe, i18n.language, i18n.language)
}

/**
 * 应用的界面文字的消息 ID 登记在这里：tenon generate 生成的 messages.gen.ts 声明
 *
 *   declare module "@go-tenon/ui" { interface I18nRegister { messageId: MessageId } }
 *
 * 之后 useT 只接受其中的 ID。没有登记时接受任意文字。
 */
// biome-ignore lint/suspicious/noEmptyInterface: 由应用生成的文件扩充
export interface I18nRegister {}

/** 应用的界面文字的消息 ID：I18nRegister 中登记的 messageId，没有登记时是 string。 */
export type MessageId = I18nRegister extends { messageId: infer M extends string } ? M : string

/**
 * 页面取应用的界面文字：返回 t(id, data)，按当前语言翻译，语言变化时重新渲染。
 *
 *   const t = useT()
 *   <Text>{t("inbox.empty")}</Text>
 */
export function useT(): (id: MessageId, data?: MessageData) => string {
  const i18n = useI18n()
  const language = useLanguage()
  // biome-ignore lint/correctness/useExhaustiveDependencies: 语言变化时返回新的函数，让依赖它的组件重新计算
  return useCallback((id: MessageId, data?: MessageData) => i18n.t(id, data), [i18n, language])
}

/** 框架组件取内置文字：返回 t(id, data)，语言变化时重新渲染。 */
export function useText(): (id: UiMessageId, data?: MessageData) => string {
  const i18n = useI18n()
  const language = useLanguage()
  // biome-ignore lint/correctness/useExhaustiveDependencies: 语言变化时返回新的函数，让依赖它的组件重新计算
  return useCallback((id: UiMessageId, data?: MessageData) => i18n.t(id, data), [i18n, language])
}

/** 不在组件中调用的函数取内置文字，例如 confirm：按当前应用的语言。 */
export function text(id: UiMessageId, data?: MessageData): string {
  return active.t(id, data)
}
