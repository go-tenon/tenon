import { MantineProvider } from "@mantine/core"
import type { ReactNode } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { describe, expect, it } from "vitest"
import { LoadingOverlay, LoadingStage } from "./loading"

function render(node: ReactNode): string {
  return renderToStaticMarkup(<MantineProvider>{node}</MantineProvider>)
}

describe("LoadingOverlay / LoadingStage", () => {
  it("LoadingStage 在 loading 为真时标记 aria-busy 并渲染激活态磨砂覆层", () => {
    const active = render(
      <LoadingStage loading>
        <span>内容</span>
      </LoadingStage>,
    )
    expect(active).toContain('aria-busy="true"')
    expect(active).toContain("tn-loading-overlay")
    expect(active).toContain('data-active="true"')
    expect(active).toContain("mantine-Loader-root")

    const idle = render(
      <LoadingStage loading={false}>
        <span>内容</span>
      </LoadingStage>,
    )
    expect(idle).not.toContain('aria-busy="true"')
    expect(idle).not.toContain("mantine-Loader-root")
  })

  it("LoadingOverlay 支持自定义转圈尺寸", () => {
    const html = render(<LoadingOverlay visible loaderSize={18} />)
    expect(html).toContain("tn-loading-overlay")
    expect(html).toContain('data-active="true"')
    expect(html).toContain("mantine-Loader-root")
  })
})
