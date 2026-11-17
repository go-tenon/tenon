// @vitest-environment happy-dom

import { createMemoryHistory, createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router"
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { CallToAction, CodeWindow, Features, Hero, LogoCloud, ProductShot, Stats, Testimonial } from "./landing"
import { UIProvider } from "./provider"
import { AppRoot, createApp, type RouterContext } from "./shell"
import { SiteLayout } from "./site"
import { polyfill } from "./testing"
import { createUiTheme, type Landing } from "./theme"

afterEach(cleanup)

function Page() {
  return (
    <SiteLayout
      brand="Acme"
      nav={[{ label: "文档", to: "/docs" }]}
      footer={[{ title: "产品", links: [{ label: "价格", href: "#" }] }]}
    >
      <Hero
        badge="1.0 发布"
        title={
          <>
            一个后端，<em>交付整个产品</em>
          </>
        }
        description="一句话说明"
        actions={<button type="button">开始使用</button>}
        note="go install example.com/acme@latest"
        visual={
          <ProductShot url="admin.acme.cn">
            <img alt="后台截图" src="data:," />
          </ProductShot>
        }
      />
      <LogoCloud title="正在使用的团队" items={["云栈科技", "北辰物流"]} />
      <Features
        eyebrow="特性"
        title="为什么选择我们"
        items={[
          { title: "接口即类型", description: "生成客户端" },
          { title: "按声明渲染", wide: true, children: <CodeWindow title="routes.go" code="api.Resource()" /> },
        ]}
      />
      <Stats items={[{ value: "1", unit: "个", label: "部署产物" }]} />
      <Testimonial quote="好用" name="林舟" title="技术负责人" />
      <CallToAction title="开始构建" actions={<button type="button">免费开始</button>} />
    </SiteLayout>
  )
}

function renderSite(landing?: Landing) {
  polyfill()
  const root = createRootRouteWithContext<RouterContext>()({ component: Outlet })
  const home = createRoute({ getParentRoute: () => root, path: "/", component: Page })
  const app = createApp({
    id: `landing-${landing}`,
    routeTree: root.addChildren([home]),
    router: { history: createMemoryHistory({ initialEntries: ["/"] }) },
  })
  render(
    <UIProvider theme={createUiTheme({ preset: "site", landing })} defaultColorScheme="light" env="test">
      <AppRoot app={app} />
    </UIProvider>,
  )
}

describe("落地页", () => {
  it.each(["classic", "dusk", "ink", "mist"] as const)(
    "%s：SiteLayout 带上主题的风格，各区块渲染传入的内容",
    async (landing) => {
      renderSite(landing)
      await screen.findByText("一句话说明")
      expect(document.querySelector(".tn-site")?.getAttribute("data-landing")).toBe(landing)
      expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("一个后端，交付整个产品")
      expect(screen.getByText("admin.acme.cn")).toBeTruthy()
      expect(screen.getByAltText("后台截图")).toBeTruthy()
      expect(screen.getByText("北辰物流")).toBeTruthy()
      expect(screen.getByText("api.Resource()")).toBeTruthy()
      expect(screen.getByText("按声明渲染").closest(".tn-lp-feature")?.getAttribute("data-wide")).toBe("true")
      expect(screen.getByText("部署产物")).toBeTruthy()
      expect(screen.getByText("林").className).toBe("tn-lp-avatar") // 没有头像时显示名字的第一个字
      expect(screen.getByText("免费开始")).toBeTruthy()
    },
  )

  it("主题没有设置风格时是 classic", async () => {
    renderSite()
    await screen.findByText("一句话说明")
    expect(document.querySelector(".tn-site")?.getAttribute("data-landing")).toBe("classic")
  })
})
