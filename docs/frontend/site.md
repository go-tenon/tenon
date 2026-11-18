# 官网与文档站

官网、落地页、内容站和文档站用 `spacious` 样式预设：正文 16px，大标题随屏幕宽度缩放，行距宽松。页面在服务端渲染，搜索引擎可以收录；不随请求变化的页面在构建时预渲染。`tenon add frontend <名称> --layout site` 生成的前端已经这样设置好。

阅读前需要了解：[搭建前端应用](app.md)；[TanStack Start 的接入](start.md)。

- [渲染方式](#rendering)
- [官网的布局](#site)
- [首页](#home)
- [文档页](#docs)
- [API 参考](#api-reference)

<a id="rendering"></a>
## 渲染方式

| 页面 | 渲染 | 设置 |
| --- | --- | --- |
| 首页、产品介绍、文档等内容在代码仓库中的页面 | 构建时预渲染，Bun 直接返回 HTML | `vite.config.ts` 中 `tenon({ prerender: ["/", "/docs"] })`，见[预渲染](start.md#prerendering) |
| 文章、商品等内容在数据库中的页面 | 请求时在服务端渲染，调用同一组 Go 接口 | 默认 |
| 用户中心等需要登录的页面 | 只在浏览器中渲染 | 路由上设置 `ssr: false` |

服务端渲染时调用接口的方式与浏览器中相同，见[调用接口](start.md#calling-the-api)。读取当前用户、检查权限只在浏览器中进行，所以需要登录的页面不在服务端渲染。

<a id="site"></a>
## 官网的布局

`SiteLayout` 是官网和内容站的布局：吸顶的半透明页头（标志、导航、操作）、内容和页脚。窄屏时导航收进右侧的抽屉。

```tsx title="src/routes/_site.tsx"
import { Button } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { SiteLayout } from "@go-tenon/ui"

export const Route = createFileRoute("/_site")({ component: Site })

function Site() {
  return (
    <SiteLayout
      brand="Tenon"
      nav={[
        { label: "产品", to: "/" },
        { label: "文档", to: "/docs/getting-started" },
        { label: "价格", to: "/pricing" },
      ]}
      actions={<Button size="sm">免费试用</Button>}
      footer={[{ title: "开发者", links: [{ label: "文档", to: "/docs/getting-started" }, { label: "状态", href: "https://status.example.com" }] }]}
      note="© 2026 Tenon"
    />
  )
}
```

- 链接中应用内的地址用 `to`，外部地址用 `href`。
- 没有传 `children` 时内容是子路由。
- 页头中的“登录”“进入控制台”按是否登录切换时，用 [`useSignedIn`](app.md#requiring-sign-in)。

<a id="home"></a>
## 首页

框架不提供落地页的模板：首页、产品介绍这类页面因产品而异，放在 `SiteLayout` 之中，用 Mantine 的组件自己写。`tenon add frontend --layout site` 生成的首页是一个占位，只有页头和一段文字，做实际产品时换成自己的内容：

```tsx title="src/routes/index.tsx"
import { Button, Container, Stack, Text, Title } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { SiteLayout } from "@go-tenon/ui"

export const Route = createFileRoute("/")({ component: Home })

function Home() {
  return (
    <SiteLayout brand="Acme" actions={<Button size="sm">开始使用</Button>}>
      <Container size="sm" py={96}>
        <Stack gap="md" align="flex-start">
          <Title order={1}>Acme</Title>
          <Text c="dimmed">这里是首页的占位，换成自己的落地页。</Text>
        </Stack>
      </Container>
    </SiteLayout>
  )
}
```

拉丁字母用 Inter：`spacious` 预设的字体以 Inter 开头，模板在根路由中引入 `@fontsource-variable/inter`；中文仍是系统字体。

<a id="docs"></a>
## 文档页

`DocsLayout` 是文档页：左侧是目录，中间是正文，右侧是本页目录。它放在 `SiteLayout` 之中，两者的宽度对齐：

```tsx
import { DocsLayout } from "@go-tenon/ui"

const nav = [
  { title: "开始", items: [{ title: "快速开始", to: "/docs/getting-started" }, { title: "安装", to: "/docs/install" }] },
  { title: "指南", items: [{ title: "部署", to: "/docs/deploy" }] },
]

function Doc({ html }: { html: string }) {
  return (
    <DocsLayout nav={nav}>
      <div dangerouslySetInnerHTML={{ __html: html }} />
    </DocsLayout>
  )
}
```

- 正文用 Mantine 的 `Typography` 排版：标题、段落、列表、引用、行内代码和代码块。
- 本页目录按正文中的 `h2`、`h3` 生成，滚动时高亮当前的位置。没有 `id` 的标题按文字生成 `id`（保留中文），重复的加序号；`headingId` 是生成的规则。
- 底部是上一篇和下一篇，按目录的顺序。
- 窄屏时隐藏左侧目录，中等宽度时隐藏本页目录。

文档的内容（例如 Markdown 转成的 HTML）由应用准备；放在代码仓库中的文档适合预渲染。

<a id="api-reference"></a>
## API 参考

| 名称 | 说明 |
| --- | --- |
| `SiteLayout`、`SiteLink`、`FooterColumn` | 官网的布局：`brand`、`nav`、`actions`、`footer`、`note`、`size` |
| `DocsLayout`、`DocsSection`、`useToc`、`headingId` | 文档页、本页目录、标题的 `id` |
