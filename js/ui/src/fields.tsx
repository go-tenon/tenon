// 字段：按 shell/ 中的字段类型（FieldKind）选择 Mantine 的输入组件，按类型显示一个值。
// 资源的表单、设置的表单和列表的单元格都用它们。

import {
  Badge,
  Box,
  Code,
  Group,
  JsonInput,
  MultiSelect,
  NumberInput,
  Select,
  Switch,
  TagsInput,
  Text,
  TextInput,
  useMantineTheme,
} from "@mantine/core"
import { DateInput, DateTimePicker } from "@mantine/dates"
import dayjs from "dayjs"
import { type ReactNode, useCallback, useEffect, useState } from "react"
import type { ResourceField } from "./shell"
import { layoutMetrics } from "./theme"

/** 字段输入组件的属性：value、onChange、error 来自表单（@mantine/form 的 getInputProps）。 */
export interface FieldInputProps {
  field: ResourceField
  value?: unknown
  onChange?: (value: unknown) => void
  error?: ReactNode
  disabled?: boolean
  /** 表单中显示的标签。ResourceForm、SettingsForm 传入字段的显示文字；自己调用 DefaultInput 时可以省略，默认相同。 */
  label?: ReactNode
}

/** 一个字段的输入组件，可以按字段覆盖，例如 inputs={{ content: MarkdownInput }}。 */
export type FieldInput = (props: FieldInputProps) => ReactNode

/** 按字段的类型选择输入组件。日期是 YYYY-MM-DD，时间以 ISO 字符串保存，与接口相同。 */
export function DefaultInput({
  field,
  value,
  onChange,
  error,
  disabled,
  label = field.label,
}: FieldInputProps): ReactNode {
  const common = { label, error, disabled, required: field.required }
  switch (field.kind) {
    case "number":
      return (
        <NumberInput
          {...common}
          value={(value as number | undefined) ?? ""}
          onChange={(v) => onChange?.(v === "" ? undefined : Number(v))}
        />
      )
    case "boolean":
      return (
        <Switch
          label={label}
          error={error}
          disabled={disabled}
          checked={!!value}
          onChange={(e) => onChange?.(e.currentTarget.checked)}
        />
      )
    case "select":
      return (
        <Select
          {...common}
          data={field.options ?? []}
          clearable={!field.required}
          value={(value as string | undefined) ?? null}
          onChange={(v) => onChange?.(v ?? undefined)}
        />
      )
    case "multiselect":
      return (
        <MultiSelect
          {...common}
          data={field.options ?? []}
          value={(value as string[] | undefined) ?? []}
          onChange={(v) => onChange?.(v)}
        />
      )
    case "tags":
      return (
        <TagsInput
          {...common}
          placeholder="输入后按回车"
          value={(value as string[] | undefined) ?? []}
          onChange={(v) => onChange?.(v)}
        />
      )
    case "date":
      return (
        <DateInput
          {...common}
          valueFormat="YYYY-MM-DD"
          clearable={!field.required}
          value={(value as string | undefined) ?? null}
          onChange={(v) => onChange?.(v ? dayjs(v).format("YYYY-MM-DD") : undefined)}
        />
      )
    case "datetime":
      return (
        <DateTimePicker
          {...common}
          valueFormat="YYYY-MM-DD HH:mm"
          clearable={!field.required}
          value={value ? dayjs(String(value)).format("YYYY-MM-DD HH:mm:ss") : null}
          onChange={(v) => onChange?.(v ? dayjs(v).toISOString() : undefined)}
        />
      )
    case "json":
      return <JsonField {...common} value={value} onChange={onChange} />
    default:
      return (
        <TextInput
          {...common}
          value={(value as string | undefined) ?? ""}
          onChange={(e) => onChange?.(e.currentTarget.value)}
        />
      )
  }
}

/** JSON 字段：编辑文本，是合法的 JSON 时才交给表单。 */
function JsonField({
  value,
  onChange,
  error,
  ...rest
}: {
  value?: unknown
  onChange?: (v: unknown) => void
  error?: ReactNode
  label?: ReactNode
  disabled?: boolean
  required?: boolean
}) {
  const format = (v: unknown) => (v === undefined ? "" : JSON.stringify(v, null, 2))
  const [text, setText] = useState(() => format(value))
  const [invalid, setInvalid] = useState(false)
  // 表单重置、换了一行数据时，跟随外部的值。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 只在外部的值变化时同步
  useEffect(() => {
    try {
      if (JSON.stringify(JSON.parse(text || "null")) === JSON.stringify(value ?? null)) return
    } catch {}
    setText(format(value))
  }, [JSON.stringify(value)])
  return (
    <JsonInput
      {...rest}
      autosize
      minRows={3}
      formatOnBlur
      value={text}
      error={error ?? (invalid ? "不是合法的 JSON" : undefined)}
      onChange={(t) => {
        setText(t)
        if (t.trim() === "") {
          setInvalid(false)
          onChange?.(undefined)
          return
        }
        try {
          onChange?.(JSON.parse(t))
          setInvalid(false)
        } catch {
          setInvalid(true)
        }
      }}
    />
  )
}

/** 按字段的类型显示一个值：枚举显示文字，布尔显示是否，时间按本地格式，标签逐个显示，空值显示破折号。 */
/**
 * 按主题的 datetimeFormat（运营后台是 MM/DD HH:mm）显示日期时间。后端返回的是带时区的 ISO 时间，显示为浏览器所在时区的时间；
 * 解析不了时原样返回。
 *
 *   const formatDateTime = useFormatDateTime()
 *   formatDateTime(order.created_at) // 11/03 10:12
 */
export function useFormatDateTime(): (value: string | Date) => string {
  const format = layoutMetrics(useMantineTheme()).datetimeFormat ?? "YYYY-MM-DD HH:mm"
  return useCallback(
    (value: string | Date) => {
      const d = dayjs(value)
      return d.isValid() ? d.format(format) : String(value)
    },
    [format],
  )
}

export function FieldValue({
  field,
  value,
  display = (_, v) => v,
}: {
  field: ResourceField
  value: unknown
  /** 枚举的显示文字，通常是 model.display。 */
  display?: (field: ResourceField, value: unknown) => unknown
}): ReactNode {
  const formatDateTime = useFormatDateTime()
  if (value === undefined || value === null || value === "" || (Array.isArray(value) && value.length === 0))
    return (
      <Text span c="dimmed">
        —
      </Text>
    )
  switch (field.kind) {
    case "boolean":
      return (
        <Group gap={6} wrap="nowrap" component="span">
          <Box
            component="span"
            w={6}
            h={6}
            style={{ borderRadius: "50%", flex: "none" }}
            bg={value ? "green.5" : "gray.4"}
          />
          <span>{value ? "是" : "否"}</span>
        </Group>
      )
    case "select":
      return <Badge color="gray">{String(display(field, value))}</Badge>
    case "multiselect":
    case "tags": {
      const values = (display(field, value) as unknown[]) ?? []
      return (
        <Group gap={4}>
          {values.map((v) => (
            <Badge key={String(v)} color="gray">
              {String(v)}
            </Badge>
          ))}
        </Group>
      )
    }
    case "datetime":
      return <span style={{ fontVariantNumeric: "tabular-nums" }}>{formatDateTime(String(value))}</span>
    case "date":
      return <span style={{ fontVariantNumeric: "tabular-nums" }}>{dayjs(String(value)).format("YYYY-MM-DD")}</span>
    case "number":
      return <span style={{ fontVariantNumeric: "tabular-nums" }}>{String(value)}</span>
    case "json":
      return <Code>{JSON.stringify(value)}</Code>
    default:
      return String(value)
  }
}
