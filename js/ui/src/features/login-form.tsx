// 登录表单：邮箱和密码，调用应用的登录接口，成功后读取身份并跳回登录前的页面。放在 CenteredAuthLayout、
// SplitAuthLayout 等登录页的布局中。

import { Alert, Button, PasswordInput, Stack, Text, TextInput } from "@mantine/core"
import { IconAlertCircle } from "@tabler/icons-react"
import { type FormEvent, type ReactNode, useState } from "react"
import { useApp } from "../app/app"
import { errorMessage } from "../components/feedback"
import { serverErrors } from "../components/form/rules"
import { useText } from "../i18n/i18n"

/** LoginForm 的选项。 */
export interface LoginFormProps {
  /** 调用应用的登录接口：服务端写入会话 cookie。成功后用会话的 load 读取身份。 */
  login: (values: { email: string; password: string }) => Promise<unknown>
  /** 登录前想去的页面（登录页的 ?redirect=），登录后回到这里。 */
  redirect?: string
  /** “忘记密码”链接，例如 <AnchorLink to="/forgot">忘记密码？</AnchorLink>。 */
  forgot?: ReactNode
}

/** 邮箱和密码登录：字段错误显示在输入框下面，其他错误（例如密码不对）显示在表单上方。 */
export function LoginForm({ login, redirect, forgot }: LoginFormProps): ReactNode {
  const app = useApp()
  const t = useText()
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [errors, setErrors] = useState<ReturnType<typeof serverErrors>>({ fields: {} })
  const [pending, setPending] = useState(false)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setPending(true)
    setErrors({ fields: {} })
    try {
      await login({ email, password })
      await app.session.signIn()
      await app.redirectAfterSignIn(redirect)
    } catch (err) {
      setErrors(serverErrors(err))
    } finally {
      setPending(false)
    }
  }
  return (
    <form onSubmit={submit} noValidate>
      <Stack gap="md">
        {errors.form && (
          <Alert color="red" variant="light" icon={<IconAlertCircle size={18} />} role="alert">
            {errorMessage(errors.form)}
          </Alert>
        )}
        <TextInput
          label={t("tenon.login.email")}
          type="email"
          autoComplete="username"
          required
          autoFocus
          value={email}
          onChange={(e) => setEmail(e.currentTarget.value)}
          error={errors.fields.email}
        />
        <PasswordInput
          label={t("tenon.login.password")}
          autoComplete="current-password"
          required
          value={password}
          onChange={(e) => setPassword(e.currentTarget.value)}
          error={errors.fields.password}
        />
        {forgot && (
          <Text size="sm" ta="right" mt={-8}>
            {forgot}
          </Text>
        )}
        <Button type="submit" loading={pending} fullWidth mt="xs">
          {t("tenon.login.submit")}
        </Button>
      </Stack>
    </form>
  )
}
