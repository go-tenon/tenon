// 密钥的输入框：平时遮住中间部分，点眼睛图标查看和修改。用于接口密钥、令牌这类要看得到、改得了，又不希望旁人一眼看全的值。

import { ActionIcon, TextInput, type TextInputProps } from "@mantine/core"
import { IconEye, IconEyeOff } from "@tabler/icons-react"
import { type ChangeEvent, type ReactNode, useState } from "react"

/**
 * maskSecret 遮住密钥的中间部分：保留开头 3 个和末尾 4 个字符，中间的每个字符换成一个圆点，长度与原文相同，
 * 例如 sk-1234567890abcd 显示为 sk-••••••••••abcd；不超过 8 个字符的密钥全部换成圆点。
 */
export function maskSecret(value: string): string {
  if (value.length <= 8) return "•".repeat(value.length)
  return value.slice(0, 3) + "•".repeat(value.length - 7) + value.slice(-4)
}

/** SecretInput 的属性：与 TextInput 相同，value 是密钥本身，onChange 与 TextInput 相同。 */
export type SecretInputProps = Omit<TextInputProps, "value" | "onChange"> & {
  value?: string
  onChange?: (event: ChangeEvent<HTMLInputElement>) => void
}

/**
 * 密钥的输入框：平时显示 maskSecret 的结果，不能编辑；点眼睛图标显示完整的值并可以修改，再点一次遮住。
 * 还没有值时直接可以输入。可以直接展开 @mantine/form 的 getInputProps：
 *
 *   <SecretInput label="密钥" {...form.getInputProps("api_key")} />
 *
 * 资源和设置的表单中，格式为 password 的字段（Go 中写 format:"password"）自动使用它。
 */
export function SecretInput({ value = "", onChange, rightSection, ...props }: SecretInputProps): ReactNode {
  const [shown, setShown] = useState(false)
  const editable = shown || value === ""
  return (
    <TextInput
      {...props}
      value={editable ? value : maskSecret(value)}
      onChange={editable ? onChange : undefined}
      readOnly={!editable || props.readOnly}
      autoComplete="off"
      spellCheck={false}
      rightSection={
        value === "" ? (
          rightSection
        ) : (
          <ActionIcon
            variant="subtle"
            color="gray"
            aria-label={shown ? "隐藏" : "查看和修改"}
            onClick={() => setShown((s) => !s)}
          >
            {shown ? <IconEyeOff size={16} stroke={1.6} /> : <IconEye size={16} stroke={1.6} />}
          </ActionIcon>
        )
      }
    />
  )
}
