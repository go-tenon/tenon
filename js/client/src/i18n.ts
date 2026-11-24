// 界面文字的翻译：按当前语言取出翻译文件中的文字。翻译文件与服务端相同，是 go-i18n 的 YAML（locales/<前端>.<语言>.yaml），
// tenon generate 把它们生成为前端的 messages.gen.ts，交给 createI18n。写法与服务端一致：占位符是 {{.name}}，
// 复数按 count 在 zero、one、two、few、many、other 中选择，规则来自 Intl.PluralRules。

/** 一条消息：文字，或者按复数形式给出的几种文字。 */
export type Message = string | { readonly [form in PluralForm]?: string }

/** 复数形式，与 go-i18n 相同。 */
export type PluralForm = "zero" | "one" | "two" | "few" | "many" | "other"

/** 一种语言的全部消息，键是消息 ID。 */
export type MessageTable = { readonly [id: string]: Message }

/** 按语言列出的消息，例如 { "zh-CN": { "inbox.empty": "没有会话" } }。 */
export type Catalog = { readonly [language: string]: MessageTable }

/** 模板数据：占位符 {{.name}} 取其中的 name；复数消息按其中的 count 选择形式。 */
export type MessageData = { readonly [name: string]: unknown }

/** createI18n 的选项。 */
export interface I18nOptions<C extends Catalog> {
  /** 应用的消息，通常是生成的 messages.gen.ts 中的 messages。 */
  messages: C
  /** 默认语言，例如 zh-CN：当前语言中没有某条消息时用它的；也是匹配不到任何语言时的当前语言。 */
  defaultLanguage: string
  /** 用户偏好的语言，按优先顺序，用来决定初始的语言。默认是浏览器的 navigator.languages；没有时用默认语言。 */
  preferred?: () => readonly string[] | undefined
}

/** 界面文字的翻译。Id 是应用消息的 ID。 */
export interface I18n<Id extends string = string> {
  /** 当前的语言：消息中有的一种语言，或者默认语言。 */
  language(): string
  /**
   * 设置当前语言，例如成员在偏好设置中选的语言；不传时按 preferred 重新选择。
   * 传入的语言没有消息时，依次用同一主语言的语言（en-US → en）和默认语言。语言变化时通知订阅者。
   */
  setLanguage(language?: string | null): void
  /**
   * 可以选的语言：默认语言和应用消息中有的语言，默认语言在前。extend 加入的消息只补充这些语言中缺少的文字，
   * 不增加可以选的语言：应用只有简体中文时，界面整个是简体中文。
   */
  languages(): string[]
  /**
   * 按当前语言翻译消息 id，data 是模板数据。当前语言中没有时用默认语言的；默认语言中也没有时返回 id 本身。
   */
  t(id: Id, data?: MessageData): string
  /** 是否有消息 id（任何语言）。 */
  has(id: string): boolean
  /**
   * 加入一组优先级较低的消息，例如框架组件内置的文字：应用消息中同一个 ID 的消息覆盖它们。其中的语言不在 languages 中时不使用。
   */
  extend(catalog: Catalog): void
  /** 请求头：Accept-Language 是当前语言，交给生成客户端的 headers，服务端的提示与界面使用同一种语言。 */
  headers(): { "Accept-Language": string }
  /** 订阅语言的变化，返回取消订阅的函数。 */
  subscribe(listener: () => void): () => void
}

/** 应用消息的 ID：默认语言中有的消息 ID。 */
type IdsOf<C extends Catalog> = Extract<{ [L in keyof C]: keyof C[L] }[keyof C], string>

/**
 * 创建界面文字的翻译。在前端的 src/i18n.ts 中创建一次，交给 createApp、生成客户端的 headers 和 createKit：
 *
 *   import { messages } from "./messages.gen"
 *   export const i18n = createI18n({ messages, defaultLanguage: "zh-CN" })
 *   i18n.t("inbox.empty")
 */
export function createI18n<C extends Catalog>(options: I18nOptions<C>): I18n<IdsOf<C>> {
  const listeners = new Set<() => void>()
  const lower: Catalog[] = [] // extend 加入的消息，后加入的优先
  const preferred = options.preferred ?? browserLanguages
  const all = () => [options.messages, ...lower]
  const languages = () => [...new Set([options.defaultLanguage, ...Object.keys(options.messages)])]
  const resolve = (wanted: readonly (string | null | undefined)[]) =>
    matchLanguage(wanted, languages()) ?? options.defaultLanguage
  // requested 是 setLanguage 要求的语言；没有要求时按偏好选择。
  let requested: string | null = null
  const choose = () => resolve(requested ? [requested] : (preferred() ?? []))
  let current = choose()
  const change = (next: string) => {
    if (next === current) return
    current = next
    for (const l of [...listeners]) l()
  }

  const lookup = (language: string, id: string): Message | undefined => {
    if (options.messages[language]?.[id] !== undefined) return options.messages[language][id]
    for (let i = lower.length - 1; i >= 0; i--) {
      const m = lower[i][language]?.[id]
      if (m !== undefined) return m
    }
    return undefined
  }

  return {
    language: () => current,
    setLanguage(language) {
      requested = language || null
      change(choose())
    },
    languages,
    t(id, data) {
      const message = lookup(current, id) ?? lookup(options.defaultLanguage, id)
      if (message === undefined) return id
      const used = lookup(current, id) !== undefined ? current : options.defaultLanguage
      return formatMessage(message, used, data)
    },
    has: (id) => all().some((catalog) => Object.values(catalog).some((table) => table[id] !== undefined)),
    extend(catalog) {
      if (!lower.includes(catalog)) lower.push(catalog)
    },
    headers: () => ({ "Accept-Language": current }),
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
}

/**
 * 在 available 中为 wanted（按优先顺序）选出一种语言：依次看每一种偏好，先找相同的，再找它的主语言（en-US → en），
 * 再找同一主语言的其他语言（zh-HK → zh-TW）；都没有时返回 undefined。比较不区分大小写。
 */
export function matchLanguage(
  wanted: readonly (string | null | undefined)[],
  available: readonly string[],
): string | undefined {
  const lowered = available.map((l) => l.toLowerCase())
  for (const w of wanted) {
    if (!w) continue
    const exact = lowered.indexOf(w.toLowerCase())
    if (exact >= 0) return available[exact]
    const primary = w.toLowerCase().split("-")[0]
    const same = lowered.indexOf(primary)
    if (same >= 0) return available[same]
    const sibling = lowered.findIndex((l) => l.split("-")[0] === primary)
    if (sibling >= 0) return available[sibling]
  }
  return undefined
}

/**
 * 把一条消息格式化为文字：复数消息按 data.count 和语言的复数规则选择形式（没有这种形式时用 other），
 * 再把 {{.name}} 换成 data 中的值。data 中没有的占位符换成空字符串，与 Go 模板相同。
 */
export function formatMessage(message: Message, language: string, data?: MessageData): string {
  let template: string
  if (typeof message === "string") {
    template = message
  } else {
    const count = Number(data?.count ?? 0)
    const form = new Intl.PluralRules(language).select(count) as PluralForm
    template = message[form] ?? message.other ?? ""
  }
  if (!template.includes("{{")) return template
  return template.replace(/\{\{\s*\.(\w+)\s*\}\}/g, (_, name: string) => {
    const value = data?.[name]
    return value === undefined || value === null ? "" : String(value)
  })
}

/** 浏览器的语言偏好；不在浏览器中（Node、服务端渲染）时没有。 */
function browserLanguages(): readonly string[] | undefined {
  const nav = (globalThis as { navigator?: { languages?: readonly string[]; language?: string } }).navigator
  if (!nav) return undefined
  return nav.languages?.length ? nav.languages : nav.language ? [nav.language] : undefined
}
