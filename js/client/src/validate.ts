// 校验：用 tenon generate 生成的 schemas.ts、messages.ts 校验输入，结果与服务端一致：
// 同样的位置（location）、错误码（validation.*）和翻译后的提示。规则的顺序与 Huma 相同。

import type { FieldError } from "./errors"

/** JSON Schema 的子集：schemas.ts 中保留的关键字。 */
export type Schema = { readonly [key: string]: unknown }

/** validate 的选项。 */
export interface ValidateOptions {
  /** schemas.ts 中的 components，$ref 指向它们。 */
  components?: { readonly [name: string]: Schema }
  /** messages.ts 中的 messages：按语言的提示模板。没有时 message 是错误码。 */
  messages?: { readonly [language: string]: { readonly [code: string]: string } }
  /**
   * 提示的语言，例如 zh-CN，或者返回当前语言的函数（应用的 i18n.language，每次校验时调用）；没有这种语言时依次尝试
   * 它的主语言（zh）和 messages 中的第一种语言。
   */
  language?: string | (() => string | undefined)
  /** 输入的位置，默认 body；校验查询参数时传 query。 */
  location?: string
}

interface Issue {
  location: string
  code: string
  data?: Record<string, string>
  value: unknown
}

/**
 * 用 schema 校验 value，返回全部字段错误；没有错误时返回空数组。
 *
 * 覆盖 Huma 为请求生成的规则：type（含 null）、required、properties、additionalProperties、items、enum、const、
 * minLength、maxLength、pattern（含 patternDescription）、format（date-time、date、time、email、uri、uuid、hostname、
 * ipv4、ipv6）、minimum、maximum、exclusiveMinimum、exclusiveMaximum、multipleOf、minItems、maxItems、uniqueItems、
 * allOf 和 $ref。oneOf、anyOf、not 不在前端校验，以服务端的结果为准。
 */
export function validate(schema: Schema, value: unknown, options: ValidateOptions = {}): FieldError[] {
  const issues: Issue[] = []
  check(schema, value, options.location ?? "body", options.components ?? {}, issues)
  const language = typeof options.language === "function" ? options.language() : options.language
  const templates = pickLanguage(options.messages, language)
  return issues.map((i) => ({
    location: i.location,
    code: i.code,
    message: render(templates?.[i.code] ?? i.code, i.data),
    value: i.value,
  }))
}

function check(
  schema: Schema,
  v: unknown,
  path: string,
  components: ValidateOptions["components"] & object,
  out: Issue[],
): void {
  const s = resolve(schema, components)
  if (!s) return
  if (Array.isArray(s.allOf)) {
    for (const sub of s.allOf as Schema[]) check(sub, v, path, components, out)
  }
  const add = (code: string, data?: Record<string, string>, value: unknown = v) =>
    out.push({ location: path, code, data, value })
  const { type, nullable } = types(s.type)
  if (nullable && v === null) return

  switch (type) {
    case "null":
      if (v !== null) add("validation.type")
      break
    case "boolean":
      if (typeof v !== "boolean") add("validation.boolean")
      break
    case "number":
    case "integer": {
      if (typeof v !== "number" || Number.isNaN(v)) {
        add(type === "integer" ? "validation.integer" : "validation.number")
        return
      }
      if (type === "integer" && !Number.isInteger(v)) add("validation.integer")
      if (typeof s.minimum === "number" && v < s.minimum) add("validation.minimum", { Min: num(s.minimum) })
      if (typeof s.exclusiveMinimum === "number" && v <= s.exclusiveMinimum)
        add("validation.exclusive_minimum", { Min: num(s.exclusiveMinimum) })
      if (typeof s.maximum === "number" && v > s.maximum) add("validation.maximum", { Max: num(s.maximum) })
      if (typeof s.exclusiveMaximum === "number" && v >= s.exclusiveMaximum)
        add("validation.exclusive_maximum", { Max: num(s.exclusiveMaximum) })
      if (typeof s.multipleOf === "number") {
        const r = v % s.multipleOf
        if (Math.abs(r) > 1e-9 && Math.abs(r - s.multipleOf) > 1e-9)
          add("validation.multiple_of", { Value: num(s.multipleOf) })
      }
      break
    }
    case "string": {
      if (typeof v !== "string") {
        add("validation.type")
        return
      }
      const length = [...v].length // 按字符（Unicode 码点）计数，与服务端相同
      if (typeof s.minLength === "number" && length < s.minLength) {
        add(s.minLength === 1 ? "validation.filled" : "validation.min_length", { Min: String(s.minLength) })
      }
      if (typeof s.maxLength === "number" && length > s.maxLength)
        add("validation.max_length", { Max: String(s.maxLength) })
      if (typeof s.pattern === "string" && !new RegExp(s.pattern, "u").test(v)) {
        if (typeof s.patternDescription === "string" && s.patternDescription !== "") {
          add("validation.pattern_description", { Description: s.patternDescription })
        } else {
          add("validation.pattern")
        }
      }
      if (typeof s.format === "string") {
        const code = formatError(s.format, v)
        if (code) add(code)
      }
      break
    }
    case "array": {
      if (!Array.isArray(v)) {
        add("validation.type")
        return
      }
      if (typeof s.minItems === "number" && v.length < s.minItems)
        add("validation.min_items", { Min: String(s.minItems) })
      if (typeof s.maxItems === "number" && v.length > s.maxItems)
        add("validation.max_items", { Max: String(s.maxItems) })
      if (s.uniqueItems === true) {
        const seen: unknown[] = []
        for (const item of v) {
          if (seen.some((x) => equal(x, item))) add("validation.unique")
          else seen.push(item)
        }
      }
      if (s.items && typeof s.items === "object") {
        for (const [i, item] of v.entries()) check(s.items as Schema, item, `${path}[${i}]`, components, out)
      }
      break
    }
    case "object": {
      if (typeof v !== "object" || v === null || Array.isArray(v)) {
        add("validation.type")
        return
      }
      checkObject(s, v as Record<string, unknown>, path, components, out)
      break
    }
  }

  if (Array.isArray(s.enum) && !s.enum.some((e) => equal(e, v))) add("validation.one_of")
  if ("const" in s && !equal(s.const, v)) add("validation.one_of")
}

function checkObject(s: Schema, m: Record<string, unknown>, path: string, components: object, out: Issue[]): void {
  const props = (s.properties ?? {}) as Record<string, Schema>
  const required = new Set(Array.isArray(s.required) ? (s.required as string[]) : [])
  const names = Object.keys(props).sort()
  // 值为 undefined 的字段等于没有这个字段：JSON.stringify 不写出它，服务端收不到，例如表单中清空的可选数字。
  const keys = Object.keys(m).filter((k) => m[k] !== undefined)
  // 字段名不区分大小写，与 Huma 的默认行为相同。
  const find = (name: string) => (keys.includes(name) ? name : keys.find((k) => k.toLowerCase() === name.toLowerCase()))
  for (const name of names) {
    const prop = props[name]
    const key = find(name)
    if (key === undefined) {
      if (required.has(name) && !readOnly(prop, components as never)) {
        out.push({ location: join(path, name), code: "validation.required", value: undefined })
      }
      continue
    }
    if (m[key] === null && !required.has(name)) continue
    check(prop, m[key], `${path}.${name}`, components as never, out)
  }
  if (s.additionalProperties === false) {
    const lower = new Set(names.map((n) => n.toLowerCase()))
    for (const k of keys) {
      if (!(k in props) && !lower.has(k.toLowerCase()))
        out.push({ location: `${path}.${k}`, code: "validation.unexpected", value: m })
    }
  } else if (s.additionalProperties && typeof s.additionalProperties === "object") {
    for (const k of keys) {
      if (!(k in props)) check(s.additionalProperties as Schema, m[k], `${path}.${k}`, components as never, out)
    }
  }
}

function resolve(s: Schema, components: ValidateOptions["components"] & object): Schema | undefined {
  let cur: Schema | undefined = s
  for (let depth = 0; cur && typeof cur.$ref === "string"; depth++) {
    if (depth > 32) return undefined
    cur = components[cur.$ref.replace(/^#\/components\/schemas\//, "")]
  }
  return cur
}

function readOnly(prop: Schema, components: ValidateOptions["components"] & object): boolean {
  return prop.readOnly === true || resolve(prop, components)?.readOnly === true
}

function types(t: unknown): { type?: string; nullable: boolean } {
  if (Array.isArray(t)) {
    const rest = t.filter((x) => x !== "null")
    return { type: rest[0] as string | undefined, nullable: rest.length < t.length }
  }
  return { type: t as string | undefined, nullable: false }
}

function join(parent: string, name: string): string {
  return parent === "" ? name : `${parent}.${name}`
}

/** 数字在提示中的写法，与 Go 的 %v 相同：整数没有小数点。 */
function num(n: number): string {
  return String(n)
}

function equal(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false
  if (Array.isArray(a) !== Array.isArray(b)) return false
  const ka = Object.keys(a as object)
  const kb = Object.keys(b as object)
  return (
    ka.length === kb.length &&
    ka.every((k) => equal((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]))
  )
}

const dateRe = /^(\d{4})-(\d{2})-(\d{2})$/
const timeRe = /^([01]\d|2[0-3]):([0-5]\d):([0-5]\d|60)(\.\d+)?(Z|[+-]([01]\d|2[0-3]):[0-5]\d)?$/i

/** 检查 format，不合格时返回错误码。 */
function formatError(format: string, v: string): string | undefined {
  switch (format) {
    case "date-time": {
      const t = v.indexOf("T") >= 0 ? v.indexOf("T") : v.indexOf("t")
      const ok = t === 10 && validDate(v.slice(0, 10)) && /(Z|[+-]\d{2}:\d{2})$/i.test(v) && timeRe.test(v.slice(11))
      return ok ? undefined : "validation.date_time"
    }
    case "date":
      return validDate(v) ? undefined : "validation.date"
    case "time": {
      const m = /^(\d{2}):(\d{2}):(\d{2})(Z|[+-]\d{2}:\d{2})?$/i.exec(v)
      return m && Number(m[1]) < 24 && Number(m[2]) < 60 && Number(m[3]) < 60 ? undefined : "validation.time"
    }
    case "email":
    case "idn-email":
      return /^[^\s@<>()[\],;:"]+@[^\s@<>()[\],;:"]+$/.test(v) ? undefined : "validation.email"
    case "uri":
    case "iri":
      return /^[a-zA-Z][a-zA-Z0-9+.-]*:\S*$/.test(v) ? undefined : "validation.url"
    case "uuid":
      return /^[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}$/.test(v)
        ? undefined
        : "validation.format"
    case "hostname":
    case "idn-hostname":
      return v.length < 256 &&
        /^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$/.test(v)
        ? undefined
        : "validation.format"
    case "ipv4":
      return /^((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)$/.test(v)
        ? undefined
        : "validation.format"
    case "ipv6":
      return v.includes(":") && /^[0-9a-fA-F:.]+$/.test(v) ? undefined : "validation.format"
  }
  return undefined
}

function validDate(v: string): boolean {
  const m = dateRe.exec(v)
  if (!m) return false
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])]
  const date = new Date(Date.UTC(y, mo - 1, d))
  return date.getUTCFullYear() === y && date.getUTCMonth() === mo - 1 && date.getUTCDate() === d
}

/**
 * 按语言选择：language 本身，没有时它的主语言（zh-CN 的 zh，或者同一主语言的其他地区），再没有时第一种语言。
 * 验证提示、枚举和资源的显示文字都按这个规则选择语言。
 */
export function pickLanguage<T>(
  byLanguage: { readonly [language: string]: T } | undefined,
  language?: string,
): T | undefined {
  if (!byLanguage) return undefined
  if (language) {
    if (byLanguage[language]) return byLanguage[language]
    const primary = language.split("-")[0]
    const match = Object.keys(byLanguage).find((l) => l === primary || l.split("-")[0] === primary)
    if (match) return byLanguage[match]
  }
  const first = Object.keys(byLanguage)[0]
  return first ? byLanguage[first] : undefined
}

/** 替换模板中的 {{.名称}}。 */
function render(template: string, data?: Record<string, string>): string {
  return template.replace(/\{\{\s*\.(\w+)\s*\}\}/g, (_, key: string) => data?.[key] ?? "")
}
