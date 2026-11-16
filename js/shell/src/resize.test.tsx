// @vitest-environment happy-dom
import { createSession } from "@go-tenon/client"
import { createMemoryHistory, createRootRouteWithContext, createRoute } from "@tanstack/react-router"
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { AppRoot, createApp, type RouterContext } from "./app"
import { clampSize, useResizable } from "./resize"

afterEach(() => {
  cleanup()
  localStorage.clear()
})

function Pane({ side }: { side?: "start" | "end" }) {
  const pane = useResizable({ id: "articles", defaultSize: 280, min: 200, max: 400, side })
  return (
    <div>
      <p data-testid="size">{pane.size}</p>
      <p data-testid="dragging">{String(pane.dragging)}</p>
      <div data-testid="handle" {...pane.separatorProps} />
    </div>
  )
}

async function setup(side?: "start" | "end") {
  const root = createRootRouteWithContext<RouterContext>()()
  const page = createRoute({ getParentRoute: () => root, path: "/", component: () => <Pane side={side} /> })
  const session = createSession({ refresh: async () => ({ access_token: "t", expires_at: "" }) })
  await session.signIn({ access_token: "t", expires_at: "" }, { account: "u1" })
  const app = createApp({
    id: "resize-test",
    routeTree: root.addChildren([page]),
    session,
    router: { history: createMemoryHistory({ initialEntries: ["/"] }) },
  })
  render(<AppRoot app={app} />)
  await screen.findByTestId("size")
}

const size = () => screen.getByTestId("size").textContent
const saved = () => localStorage.getItem("tenon:resize-test:resize:u1:articles")

describe("useResizable", () => {
  it("clampSize 限制在范围内并取整", () => {
    expect(clampSize(100, 200, 400)).toBe(200)
    expect(clampSize(500.4, 200, 400)).toBe(400)
    expect(clampSize(250.6, 200, 400)).toBe(251)
  })

  it("拖动分隔条调整宽度，松开后按账号保存", async () => {
    await setup()
    const handle = screen.getByTestId("handle")
    expect(handle.getAttribute("role")).toBe("separator")
    expect(size()).toBe("280")
    act(() => {
      fireEvent.pointerDown(handle, { button: 0, clientX: 300 })
    })
    expect(screen.getByTestId("dragging").textContent).toBe("true")
    act(() => {
      window.dispatchEvent(new MouseEvent("pointermove", { clientX: 350 }))
    })
    expect(size()).toBe("330")
    act(() => {
      window.dispatchEvent(new MouseEvent("pointermove", { clientX: 1000 }))
      window.dispatchEvent(new MouseEvent("pointerup"))
    })
    expect(size()).toBe("400") // 不超过 max
    expect(saved()).toBe("400")
    expect(screen.getByTestId("dragging").textContent).toBe("false")
  })

  it("方向键、Home、End 调整，双击恢复默认宽度", async () => {
    await setup()
    const handle = screen.getByTestId("handle")
    fireEvent.keyDown(handle, { key: "ArrowRight" })
    expect(size()).toBe("296")
    fireEvent.keyDown(handle, { key: "Home" })
    expect(size()).toBe("200")
    fireEvent.keyDown(handle, { key: "End" })
    expect(size()).toBe("400")
    fireEvent.doubleClick(handle)
    expect(size()).toBe("280")
    expect(saved()).toBe("280")
  })

  it("可以调整的一栏在右侧时，向左拖动变宽", async () => {
    await setup("end")
    const handle = screen.getByTestId("handle")
    act(() => {
      fireEvent.pointerDown(handle, { button: 0, clientX: 500 })
      window.dispatchEvent(new MouseEvent("pointermove", { clientX: 450 }))
      window.dispatchEvent(new MouseEvent("pointerup"))
    })
    expect(size()).toBe("330")
  })

  it("读取保存的宽度", async () => {
    localStorage.setItem("tenon:resize-test:resize:u1:articles", "320")
    await setup()
    expect(size()).toBe("320")
  })
})
