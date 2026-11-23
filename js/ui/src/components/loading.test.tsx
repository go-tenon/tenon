import { MantineProvider } from "@mantine/core"
import type { ReactNode } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { describe, expect, it } from "vitest"
import { LoadingOverlay, LoadingStage, useAnyFetching } from "./loading"

function render(node: ReactNode): string {
  return renderToStaticMarkup(<MantineProvider>{node}</MantineProvider>)
}

function FetchingProbe(): ReactNode {
  const fetching = useAnyFetching()
  return <span>{fetching ? "fetching" : "idle"}</span>
}

describe("LoadingOverlay / LoadingStage", () => {
  it("LoadingStage 在 loading 为真时标记 aria-busy、data-loading-active 并渲染激活态磨砂覆层", () => {
    const active = render(
      <LoadingStage loading>
        <span>内容</span>
      </LoadingStage>,
    )
    expect(active).toContain('aria-busy="true"')
    expect(active).toContain('data-loading-active="true"')
    expect(active).toContain("tn-loading-overlay")
    expect(active).toContain('data-active="true"')
    expect(active).toContain("mantine-Loader-root")

    const idle = render(
      <LoadingStage loading={false}>
        <span>内容</span>
      </LoadingStage>,
    )
    expect(idle).not.toContain('aria-busy="true"')
    expect(idle).not.toContain('data-loading-active="true"')
    expect(idle).not.toContain("mantine-Loader-root")
  })

  it("LoadingOverlay 支持自定义转圈尺寸与 sticky 视口居中模式", () => {
    const html = render(<LoadingOverlay visible loaderSize={18} sticky />)
    expect(html).toContain("tn-loading-overlay")
    expect(html).toContain('data-active="true"')
    expect(html).toContain('data-sticky="true"')
    expect(html).toContain("tn-loading-overlay-pin")
    expect(html).toContain("mantine-Loader-root")
  })

  it("useAnyFetching 在未包在 QueryClientProvider 中时安全返回 false", () => {
    const html = render(<FetchingProbe />)
    expect(html).toContain("idle")
  })
})
