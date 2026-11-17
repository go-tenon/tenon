// @vitest-environment happy-dom
// 页面中的错误交给 onError：路由的错误组件接住的错误不会冒泡到 window，前端观测的 SDK 捕获不到。
import {
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  type ErrorComponentProps,
  Outlet,
} from "@tanstack/react-router"
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import {
  AppRoot,
  createApp,
  type ErrorReport,
  ForbiddenError,
  type RouterContext,
  shouldReport,
  useReportError,
} from "./app"

afterEach(() => cleanup())

const apiError = (status: number, requestId = "req-1") => ({ status, fields: [], detail: "出错了", requestId })

function mount(path: string, onError?: (error: unknown, report: ErrorReport) => void) {
  const root = createRootRouteWithContext<RouterContext>()({ component: Outlet })
  const routes = [
    createRoute({
      getParentRoute: () => root,
      path: "/broken",
      component: () => {
        throw new TypeError("Cannot read properties of undefined (reading 'title')")
      },
    }),
    createRoute({
      getParentRoute: () => root,
      path: "/forbidden",
      beforeLoad: () => {
        throw new ForbiddenError("articles:read")
      },
    }),
    createRoute({
      getParentRoute: () => root,
      path: "/articles/$id",
      loader: ({ params }) => {
        throw apiError(params.id === "missing" ? 404 : 502)
      },
    }),
    createRoute({
      getParentRoute: () => root,
      path: "/custom",
      component: () => {
        throw new Error("custom boom")
      },
      // 路由自己的错误组件：调用 useReportError 报告。
      errorComponent: ({ error }: ErrorComponentProps) => {
        useReportError(error)
        return <p>custom error page</p>
      },
    }),
  ]
  const app = createApp({
    id: "observe",
    routeTree: root.addChildren(routes),
    onError,
    router: {
      history: createMemoryHistory({ initialEntries: [path] }),
      defaultErrorComponent: ({ error }: ErrorComponentProps) => <p>error page: {String((error as Error).message)}</p>,
    },
  })
  render(<AppRoot app={app} />)
  return app
}

describe("onError", () => {
  it("渲染出错：报告一次（路由、地址、分类），外观层的错误组件照常显示", async () => {
    const onError = vi.fn()
    vi.spyOn(console, "error").mockImplementation(() => {})
    mount("/broken", onError)
    await screen.findByText(/error page: Cannot read properties/)
    expect(onError).toHaveBeenCalledOnce()
    const [error, report] = onError.mock.calls[0] as [unknown, ErrorReport]
    expect(error).toBeInstanceOf(TypeError)
    expect(report).toMatchObject({ routeId: "/broken", pathname: "/broken", error: { kind: "unknown" } }) // 代码中的错误，不是网络错误
  })

  it("5xx 报告，带 requestId；404 和缺少权限是预期的，不报告", async () => {
    const onError = vi.fn()
    mount("/articles/1", onError)
    await screen.findByText("error page: undefined")
    expect(onError).toHaveBeenCalledOnce()
    expect(onError.mock.calls[0]?.[1]).toMatchObject({
      routeId: "/articles/$id",
      pathname: "/articles/1",
      error: { kind: "server", status: 502, requestId: "req-1" },
    })
    cleanup()

    mount("/articles/missing", onError)
    await screen.findByText("error page: undefined")
    mount("/forbidden", onError)
    await screen.findAllByText(/error page/)
    expect(onError).toHaveBeenCalledOnce()
  })

  it("路由自己的错误组件用 useReportError 报告；onError 出错不影响页面", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {})
    const onError = vi.fn((_error: unknown, _report: ErrorReport) => {
      throw new Error("sdk not ready")
    })
    mount("/custom", onError)
    await screen.findByText("custom error page")
    expect(onError).toHaveBeenCalledOnce()
    expect(onError.mock.calls[0]?.[1]).toMatchObject({ routeId: "/custom" })
  })

  it("没有设置 onError 时照常显示错误页", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {})
    mount("/broken")
    await screen.findByText(/error page/)
  })
})

describe("shouldReport", () => {
  it("代码中的错误和 5xx 报告；缺少权限、取消、其他接口错误不报告", () => {
    expect(shouldReport(new Error("bug"))).toBe(true)
    expect(shouldReport(new TypeError("Failed to fetch dynamically imported module"))).toBe(true)
    expect(shouldReport(apiError(500))).toBe(true)
    expect(shouldReport(new ForbiddenError("articles:read"))).toBe(false)
    expect(shouldReport(Object.assign(new Error("aborted"), { name: "AbortError" }))).toBe(false)
    for (const status of [400, 401, 403, 404, 409, 422, 429]) expect(shouldReport(apiError(status))).toBe(false)
  })
})
