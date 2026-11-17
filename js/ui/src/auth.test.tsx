// @vitest-environment happy-dom
import { type RouterContext, requireAuth } from "@go-tenon/shell"
import { createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { act, cleanup, fireEvent, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { AuthLayout, errorMessage, LoginForm } from "./auth"
import { renderApp } from "./testing"

afterEach(cleanup)

function tree(login: (v: { email: string; password: string }) => Promise<unknown>) {
  const root = createRootRouteWithContext<RouterContext>()({ component: Outlet })
  const secret = createRoute({
    getParentRoute: () => root,
    path: "/secret",
    beforeLoad: requireAuth,
    component: () => <p>机密页面</p>,
  })
  const page = createRoute({
    getParentRoute: () => root,
    path: "/login",
    validateSearch: (s: Record<string, unknown>) => ({
      redirect: typeof s.redirect === "string" ? s.redirect : undefined,
    }),
    component: function Login() {
      const { redirect } = page.useSearch()
      return (
        <AuthLayout title="登录" description="使用工作邮箱登录">
          <LoginForm login={login} redirect={redirect} />
        </AuthLayout>
      )
    },
  })
  return root.addChildren([secret, page])
}

async function submit(email: string, password: string) {
  fireEvent.change(screen.getByLabelText(/邮箱/), { target: { value: email } })
  fireEvent.change(screen.getByLabelText(/密码/), { target: { value: password } })
  await act(async () =>
    fireEvent.submit(screen.getByRole("button", { name: "登录" }).closest("form") as HTMLFormElement),
  )
}

describe("LoginForm", () => {
  it("接口的错误显示在表单上方，字段错误显示在输入框下面", async () => {
    const login = vi
      .fn()
      .mockRejectedValueOnce({ status: 401, code: "auth.invalid_credentials", detail: "邮箱或密码不对", fields: [] })
      .mockRejectedValueOnce({
        status: 422,
        detail: "参数不对",
        fields: [{ location: "body.email", message: "邮箱格式不对" }],
      })
    await renderApp(tree(login), { path: "/login", signedIn: false })
    await screen.findByText("使用工作邮箱登录")
    await submit("ming@example.com", "x")
    expect((await screen.findByRole("alert")).textContent).toContain("邮箱或密码不对")
    await submit("ming", "x")
    expect(await screen.findByText("邮箱格式不对")).toBeTruthy()
    expect(screen.queryByRole("alert")).toBeNull()
  })

  it("登录成功后读取身份，回到登录前想去的页面", async () => {
    let signedIn = false
    const login = vi.fn(async () => {
      signedIn = true
    })
    const app = await renderApp(tree(login), { path: "/secret", signedIn: false })
    await screen.findByText("使用工作邮箱登录")
    expect(app.router.state.location.pathname).toBe("/login")
    // 会话的 load 在登录后返回身份：测试的会话总是返回未登录，这里换成服务端已经写好 cookie 的情况。
    app.session.signIn = async () => app.session.setIdentity(signedIn ? { account: "u1" } : null)
    await submit("ming@example.com", "secret123")
    await screen.findByText("机密页面")
    expect(login).toHaveBeenCalledWith({ email: "ming@example.com", password: "secret123" })
    expect(app.router.state.location.pathname).toBe("/secret")
  })
})

describe("errorMessage", () => {
  it("没有服务端文案时按种类给出提示", () => {
    expect(errorMessage({ kind: "network", fields: [], params: {} })).toContain("网络")
    expect(errorMessage({ kind: "rate_limited", retryAfter: 30, fields: [], params: {} })).toContain("30 秒")
    expect(errorMessage({ kind: "business", message: "邮箱已注册", fields: [], params: {} })).toBe("邮箱已注册")
  })
})
