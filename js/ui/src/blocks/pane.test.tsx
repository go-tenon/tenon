import { MantineProvider } from "@mantine/core"
import type { ReactNode } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { describe, expect, it } from "vitest"
import { AsidePane, PaneBody } from "./pane"

function render(node: ReactNode): string {
  return renderToStaticMarkup(<MantineProvider>{node}</MantineProvider>)
}

describe("PaneBody", () => {
  it("未传 loading 时直接渲染滚动区，传入 loading 时使用视口定位壳和磨砂覆层", () => {
    const plain = render(<PaneBody>正文</PaneBody>)
    expect(plain).not.toContain("tn-pane-body-shell")
    expect(plain).not.toContain("tn-loading-overlay")

    const busy = render(<PaneBody loading>正文</PaneBody>)
    expect(busy).toContain("tn-pane-body-shell")
    expect(busy).toContain('data-loading-active="true"')
    expect(busy).toContain('aria-busy="true"')
    expect(busy).toContain("mantine-Loader-root")
  })
})

describe("AsidePane", () => {
  it("关闭时不渲染子内容", () => {
    const closed = render(
      <AsidePane opened={false} onClose={() => {}}>
        <div>详情内容</div>
      </AsidePane>,
    )
    expect(closed).not.toContain("详情内容")
  })
})
