// 页面：登录、403、404，以及路由的错误组件。

import { LockOutlined, MailOutlined } from "@ant-design/icons"
import type { SessionBody } from "@go-tenon/client"
import { isForbidden, setServerErrors, useApp } from "@go-tenon/shell"
import { type ErrorComponentProps, useRouter } from "@tanstack/react-router"
import { Alert, Button, Form, Input, Result } from "antd"
import { type ReactNode, useState } from "react"

/** 登录表单的值。 */
export interface LoginValues {
  email: string
  password: string
}

/** LoginPage 的选项。 */
export interface LoginPageProps {
  /** 调用应用的登录接口，返回它的响应体（apitoken.Session）。 */
  login: (values: LoginValues) => Promise<SessionBody>
  /** 登录前想去的页面（登录页的 ?redirect=），登录后回到这里。 */
  redirect?: string
}

/**
 * 登录页：邮箱和密码。登录成功后保存凭据，回到登录前想去的页面（只接受同源的相对路径）。
 * 接口返回的字段错误显示在对应的输入框下面，其他错误（例如密码不对）显示在表单上方。
 */
export function LoginPage({ login, redirect }: LoginPageProps): ReactNode {
  const app = useApp()
  const [form] = Form.useForm<LoginValues>()
  const [error, setError] = useState<string>()
  const [pending, setPending] = useState(false)
  const submit = async (values: LoginValues) => {
    setPending(true)
    setError(undefined)
    try {
      await app.session.signIn(await login(values))
      await app.redirectAfterSignIn(redirect)
    } catch (err) {
      const info = setServerErrors(err, form)
      if (info)
        setError(info.message ?? (info.kind === "network" ? "网络连接失败，请稍后重试" : "登录失败，请稍后重试"))
    } finally {
      setPending(false)
    }
  }
  return (
    <Form form={form} layout="vertical" onFinish={submit} requiredMark={false}>
      {error && <Alert type="error" message={error} showIcon style={{ marginBottom: 16 }} />}
      <Form.Item name="email" label="邮箱" rules={[{ required: true, message: "请输入邮箱" }]}>
        <Input prefix={<MailOutlined />} type="email" autoComplete="username" autoFocus />
      </Form.Item>
      <Form.Item name="password" label="密码" rules={[{ required: true, message: "请输入密码" }]}>
        <Input.Password prefix={<LockOutlined />} autoComplete="current-password" />
      </Form.Item>
      <Button type="primary" htmlType="submit" block loading={pending}>
        登录
      </Button>
    </Form>
  )
}

/** 没有权限（403）。 */
export function ForbiddenPage(): ReactNode {
  const router = useRouter()
  return (
    <Result
      status="403"
      title="没有权限"
      subTitle="你没有访问这个页面的权限，需要时请联系管理员。"
      extra={<Button onClick={() => void router.navigate({ href: "/" })}>回到首页</Button>}
    />
  )
}

/** 页面不存在（404）：交给 createApp 的 router.defaultNotFoundComponent。 */
export function NotFoundPage(): ReactNode {
  const router = useRouter()
  return (
    <Result
      status="404"
      title="页面不存在"
      subTitle="地址可能写错了，或者页面已经删除。"
      extra={<Button onClick={() => void router.navigate({ href: "/" })}>回到首页</Button>}
    />
  )
}

/**
 * 路由的错误组件：交给 createApp 的 router.defaultErrorComponent。缺少权限时显示 403，
 * 其他错误显示说明和重试按钮。错误显示在上级布局之中，菜单和顶栏仍在。
 */
export function RouteError({ error, reset }: ErrorComponentProps): ReactNode {
  const router = useRouter()
  if (isForbidden(error)) return <ForbiddenPage />
  const message = error instanceof Error ? error.message : String(error)
  return (
    <Result
      status="error"
      title="页面出错了"
      subTitle={message}
      extra={
        <Button
          type="primary"
          onClick={() => {
            reset()
            void router.invalidate()
          }}
        >
          重试
        </Button>
      }
    />
  )
}
