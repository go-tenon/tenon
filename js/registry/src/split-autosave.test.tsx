// @vitest-environment happy-dom
// 列表加详情的分栏和表单的自动保存。
import { createSession } from "@go-tenon/client"
import { AppRoot, createApp, type RouterContext, resolver } from "@go-tenon/shell"
import { createMemoryHistory, createRootRouteWithContext, createRoute } from "@tanstack/react-router"
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { Activity, type ReactNode, StrictMode, useState } from "react"
import { useForm } from "react-hook-form"
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest"
import { AutoSaveStatus, Field, useAutoSave } from "@/components/tenon/form"
import { SplitView } from "@/components/tenon/split-view"
import { Input } from "@/components/ui/input"

let narrow = false
beforeAll(() => {
  window.matchMedia = ((query: string) => ({
    matches: narrow,
    media: query,
    addEventListener() {},
    removeEventListener() {},
  })) as never
})

afterEach(() => {
  cleanup()
  localStorage.clear()
  narrow = false
})

async function mount(page: () => ReactNode, strict = false) {
  const root = createRootRouteWithContext<RouterContext>()()
  const route = createRoute({ getParentRoute: () => root, path: "/", component: page })
  const session = createSession({ refresh: async () => ({ access_token: "t", expires_at: "" }) })
  await session.signIn({ access_token: "t", expires_at: "" }, { account: "u1" })
  const app = createApp({
    id: "split-test",
    routeTree: root.addChildren([route]),
    session,
    router: { history: createMemoryHistory({ initialEntries: ["/"] }) },
  })
  render(
    strict ? (
      <StrictMode>
        <AppRoot app={app} />
      </StrictMode>
    ) : (
      <AppRoot app={app} />
    ),
  )
  return app
}

describe("SplitView", () => {
  const page = (show?: "pane" | "main") => () => (
    <SplitView id="articles" pane={<p>文章列表</p>} narrow={show}>
      <p>文章详情</p>
    </SplitView>
  )

  it("宽屏时两侧都显示，分隔条调整左侧的宽度并保存", async () => {
    await mount(page())
    await screen.findByText("文章列表")
    expect(screen.getByText("文章详情")).toBeTruthy()
    const handle = screen.getByRole("separator", { name: "调整宽度" })
    const pane = screen.getByText("文章列表").closest("aside") as HTMLElement
    expect(pane.style.width).toBe("320px")
    fireEvent.keyDown(handle, { key: "ArrowRight" })
    expect(pane.style.width).toBe("336px")
    expect(localStorage.getItem("tenon:split-test:resize:u1:articles")).toBe("336")
  })

  it("窄屏时只显示 narrow 指定的一侧", async () => {
    narrow = true
    await mount(page("main"))
    await screen.findByText("文章详情")
    expect(screen.queryByText("文章列表")).toBeNull()
    expect(screen.queryByRole("separator")).toBeNull()
  })
})

describe("useAutoSave", () => {
  type Settings = { name: string }
  const schema = { type: "object", required: ["name"], properties: { name: { type: "string", minLength: 2 } } }

  function SettingsForm({ save }: { save: (v: Settings) => Promise<unknown> }) {
    const form = useForm<Settings>({ resolver: resolver<Settings>(schema) as never, defaultValues: { name: "原名" } })
    const autosave = useAutoSave(form, { schema, save, delay: 20 })
    return (
      <form>
        <Field form={form} name="name" label="名称">
          {(p) => <Input {...p} />}
        </Field>
        <AutoSaveStatus status={autosave.status} />
        <p data-testid="dirty">{String(form.formState.isDirty)}</p>
      </form>
    )
  }

  it("修改后自动保存；校验不通过时不保存", async () => {
    const save = vi.fn(async () => {})
    await mount(() => <SettingsForm save={save} />)
    const input = await screen.findByLabelText("名称")
    fireEvent.change(input, { target: { value: "x" } }) // 少于 2 个字符
    await act(() => new Promise((r) => setTimeout(r, 60)))
    expect(save).not.toHaveBeenCalled()

    fireEvent.change(input, { target: { value: "新的名称" } })
    expect(screen.getByRole("status").textContent).toBe("有未保存的修改")
    await waitFor(() => expect(save).toHaveBeenCalledWith({ name: "新的名称" }))
    await waitFor(() => expect(screen.getByRole("status").textContent).toBe("已保存"))
    expect(screen.getByTestId("dirty").textContent).toBe("false")
  })

  it("保存失败时显示，服务端的字段错误显示在输入框下面", async () => {
    const save = vi.fn(async () => {
      throw { status: 422, fields: [{ location: "body.name", message: "名称已被使用", code: "taken" }] }
    })
    await mount(() => <SettingsForm save={save} />)
    fireEvent.change(await screen.findByLabelText("名称"), { target: { value: "重复的名称" } })
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("保存失败"))
    expect(await screen.findByText("名称已被使用")).toBeTruthy()
  })

  it("StrictMode 下照常保存", async () => {
    const save = vi.fn(async () => {})
    await mount(() => <SettingsForm save={save} />, true)
    fireEvent.change(await screen.findByLabelText("名称"), { target: { value: "严格模式" } })
    await waitFor(() => expect(save).toHaveBeenCalledWith({ name: "严格模式" }))
  })

  it("Activity 隐藏时保存等待的修改，重新显示后照常保存", async () => {
    const save = vi.fn(async () => {})
    function Tabs() {
      const [visible, setVisible] = useState(true)
      return (
        <>
          <button type="button" onClick={() => setVisible((v) => !v)}>
            切换
          </button>
          <Activity mode={visible ? "visible" : "hidden"}>
            <SettingsForm save={save} />
          </Activity>
        </>
      )
    }
    await mount(Tabs)
    const input = await screen.findByLabelText("名称")
    fireEvent.change(input, { target: { value: "隐藏之前" } })
    fireEvent.click(screen.getByText("切换"))
    await waitFor(() => expect(save).toHaveBeenCalledWith({ name: "隐藏之前" }))
    fireEvent.click(screen.getByText("切换"))
    fireEvent.change(await screen.findByLabelText("名称"), { target: { value: "重新显示" } })
    await waitFor(() => expect(save).toHaveBeenCalledWith({ name: "重新显示" }))
  })

  it("离开页面时立即保存还在等待的修改", async () => {
    const save = vi.fn(async () => {})
    await mount(() => <SettingsForm save={save} />)
    fireEvent.change(await screen.findByLabelText("名称"), { target: { value: "马上离开" } })
    cleanup()
    await waitFor(() => expect(save).toHaveBeenCalledWith({ name: "马上离开" }))
  })
})
