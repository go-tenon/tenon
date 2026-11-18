// 认证页：登录、注册、找回密码等页面的布局，以及登录表单。

import type { ErrorInfo } from "@go-tenon/client"
import { Alert, Button, Group, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core"
import { IconAlertCircle } from "@tabler/icons-react"
import { type FormEvent, type ReactNode, useState } from "react"
import { serverErrors, useApp } from "./shell"

/** CenteredAuthLayout 的选项。 */
export interface CenteredAuthLayoutProps {
  /** 卡片上方的标志和名称。 */
  brand?: ReactNode
  title: ReactNode
  /** 标题下面的一句说明。 */
  description?: ReactNode
  /** 卡片下方，例如“还没有账号？注册”。 */
  footer?: ReactNode
  children: ReactNode
}

/** 登录、注册、找回密码等页面的样式之一：页面中央一张卡片。以后的其他样式（例如左右分栏）是另外的组件。 */
export function CenteredAuthLayout({
  brand,
  title,
  description,
  footer,
  children,
}: CenteredAuthLayoutProps): ReactNode {
  return (
    <div className="tn-auth">
      <Stack gap="lg" w="100%" maw={400} align="stretch">
        {brand && <Group justify="center">{brand}</Group>}
        <div className="tn-auth-card">
          <Stack gap={4} mb="xl">
            <Title order={3}>{title}</Title>
            {description && (
              <Text c="dimmed" size="sm">
                {description}
              </Text>
            )}
          </Stack>
          {children}
        </div>
        {footer && (
          <Text ta="center" size="sm" c="dimmed">
            {footer}
          </Text>
        )}
      </Stack>
    </div>
  )
}

/** 没有服务端文案时（断网等）按错误的种类显示的提示。 */
export function errorMessage(info: ErrorInfo): string {
  if (info.message) return info.message
  switch (info.kind) {
    case "network":
      return "网络连接失败，请检查网络后重试"
    case "rate_limited":
      return info.retryAfter ? `尝试次数过多，请 ${info.retryAfter} 秒后再试` : "尝试次数过多，请稍后再试"
    case "server":
      return "服务暂时不可用，请稍后重试"
    case "forbidden":
      return "没有权限执行这个操作"
    case "not_found":
      return "要找的内容不存在"
    default:
      return "操作没有成功，请重试"
  }
}

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
          label="邮箱"
          type="email"
          autoComplete="username"
          required
          autoFocus
          value={email}
          onChange={(e) => setEmail(e.currentTarget.value)}
          error={errors.fields.email}
        />
        <PasswordInput
          label="密码"
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
          登录
        </Button>
      </Stack>
    </form>
  )
}
