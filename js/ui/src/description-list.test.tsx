// @vitest-environment happy-dom

import { MantineProvider } from "@mantine/core"
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { DescriptionList } from "./description-list"

afterEach(cleanup)

describe("DescriptionList", () => {
  it("用 dl 渲染名称和值，空值显示“—”，标题和右边的内容在顶部", () => {
    const { container } = render(
      <MantineProvider>
        <DescriptionList
          title="复制槽"
          extra={<span>转发中</span>}
          labelWidth={160}
          items={[
            { label: "名称", value: "tenon_queue_abc", mono: true },
            { label: "进程", value: undefined },
            { label: "积压", value: 0 },
          ]}
        />
      </MantineProvider>,
    )
    expect(screen.getByText("复制槽")).toBeTruthy()
    expect(screen.getByText("转发中")).toBeTruthy()
    const dts = [...container.querySelectorAll("dt")].map((e) => e.textContent)
    const dds = [...container.querySelectorAll("dd")].map((e) => e.textContent)
    expect(dts).toEqual(["名称", "进程", "积压"])
    expect(dds).toEqual(["tenon_queue_abc", "—", "0"]) // 0 不算空值
    expect(container.querySelector("dd")?.hasAttribute("data-mono")).toBe(true)
    expect((container.querySelector("dl") as HTMLElement).style.getPropertyValue("--tn-dl-label")).toBe("160px")
  })
})
