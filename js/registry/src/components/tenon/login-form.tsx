// 登录表单：邮箱和密码。登录成功后保存凭据，回到登录前想去的页面（只接受同源的相对路径）。

import type { SessionBody } from "@go-tenon/client"
import { serverErrors, useApp } from "@go-tenon/shell"
import { type FormEvent, type ReactNode, useState } from "react"
import { FormError } from "@/components/tenon/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

/** LoginForm 的选项。 */
export interface LoginFormProps {
  /** 调用应用的登录接口，返回它的响应体（apitoken.Session）。 */
  login: (values: { email: string; password: string }) => Promise<SessionBody>
  /** 登录前想去的页面（登录页的 ?redirect=）。 */
  redirect?: string
}

/** 登录表单。接口返回的字段错误显示在输入框下面，其他错误（例如密码不对）显示在表单上方。 */
export function LoginForm({ login, redirect }: LoginFormProps): ReactNode {
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
      await app.session.signIn(await login({ email, password }))
      await app.redirectAfterSignIn(redirect)
    } catch (err) {
      setErrors(serverErrors(err))
    } finally {
      setPending(false)
    }
  }
  return (
    <form onSubmit={submit} className="grid gap-4">
      <FormError error={errors.form} />
      <div className="grid gap-2">
        <Label htmlFor="login-email">邮箱</Label>
        <Input
          id="login-email"
          type="email"
          autoComplete="username"
          required
          autoFocus
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          aria-invalid={errors.fields.email ? true : undefined}
        />
        {errors.fields.email && <p className="text-xs text-destructive">{errors.fields.email}</p>}
      </div>
      <div className="grid gap-2">
        <Label htmlFor="login-password">密码</Label>
        <Input
          id="login-password"
          type="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          aria-invalid={errors.fields.password ? true : undefined}
        />
        {errors.fields.password && <p className="text-xs text-destructive">{errors.fields.password}</p>}
      </div>
      <Button type="submit" disabled={pending}>
        登录
      </Button>
    </form>
  )
}
