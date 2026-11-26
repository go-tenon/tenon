# 官网与文档站

官网、落地页、内容站和文档站用 `SiteLayout`、`DocsLayout` 和 `spacious` 样式预设，在服务端渲染，搜索引擎可以收录。`tenon add frontend <名称> --layout site` 生成的前端已经这样设置好。

包：`@go-tenon/ui`。

<a id="rendering"></a>
## 渲染方式

`site` 布局的前端在 `package.json` 中设置 `"tenon": { "ssr": true }`，按页面选择渲染方式：

| 页面 | 渲染 | 设置 |
| --- | --- | --- |
| 首页、产品介绍、文档等内容在代码仓库中的页面 | 构建时预渲染，Go 直接返回 HTML | `vite.config.ts` 中 `tenon({ prerender: ["/", "/docs"] })`，见[预渲染](hosting.md#prerendering) |
| 文章、商品等内容在数据库中的页面 | 请求时在服务端渲染 | 默认 |
| 用户中心等需要登录的页面 | 只在浏览器中渲染 | 路由上设置 `ssr: false` |

- 服务端渲染时调用的是同一组 Go 接口，见[服务端渲染时调用接口](hosting.md#calling-the-api)。
- 读取当前用户、检查权限只在浏览器中进行，所以需要登录的页面不在服务端渲染。
- 不需要搜索引擎收录的站点可以整个只在浏览器中渲染，程序中不带 Bun，见[切换渲染方式](hosting.md#switching)。

<a id="site"></a>
## 官网的布局 `SiteLayout`

`SiteLayout` 是吸顶的页头（标志、导航、操作）、内容和页脚，窄屏时导航收进抽屉。没有传 `children` 时内容是子路由：

```tsx title="src/routes/_site.tsx"
import { createFileRoute } from "@tanstack/react-router"
import { ButtonLink, SiteLayout } from "@go-tenon/ui"

export const Route = createFileRoute("/_site")({ component: Site })

function Site() {
  return (
    <SiteLayout
      brand="Acme"
      nav={[
        { label: "产品", to: "/" },
        { label: "文档", to: "/docs/getting-started", activePrefix: "/docs" },
      ]}
      actions={<ButtonLink to="/login" size="sm">登录</ButtonLink>}
      footer={[{ title: "开发者", links: [{ label: "状态", href: "https://status.example.com" }] }]}
      note="© 2026 Acme"
    />
  )
}
```

- 链接中应用内的地址用 `to`，外部地址用 `href`；顶部导航放站点一级栏目（例如“产品”“文档”），文档链接指向具体章节时设 `activePrefix: "/docs"` 让文档下的所有页面保持选中状态。
- 页头按是否登录切换“登录”“进入控制台”时，用 [`useSignedIn`](app.md#redirect-after-sign-in)。

<a id="home"></a>
## 首页

框架不提供落地页的模板。首页放在 `SiteLayout` 之中，用 Mantine 的组件自己写；生成的 `src/routes/index.tsx` 是一个占位，只有页头和一段文字。

`spacious` 预设的西文字体以 Inter 开头，模板在根路由中引入 `@fontsource-variable/inter/index.css`；汉字仍用系统字体。

<a id="docs"></a>
## 文档页 `DocsLayout`

`DocsLayout` 左侧是目录，中间是正文，右侧是本页目录。它放在 `SiteLayout` 之中，两者的宽度对齐。`html` 是应用准备好的正文，例如 Markdown 转成的 HTML：

```tsx
import { DocsLayout } from "@go-tenon/ui"

const nav = [
  { title: "开始", items: [{ title: "快速开始", to: "/docs/getting-started" }, { title: "安装", to: "/docs/install" }] },
]

function Doc({ html }: { html: string }) {
  return (
    <DocsLayout nav={nav}>
      <div dangerouslySetInnerHTML={{ __html: html }} />
    </DocsLayout>
  )
}
```

- 本页目录按正文中的 `h2`、`h3` 生成，滚动时高亮当前位置。没有 `id` 的标题按文字生成 `id`（保留中文，规则是 `headingId`）；`hideToc` 隐藏本页目录。
- 底部是上一篇和下一篇，按 `nav` 的顺序。入口路由（例如 `/docs`）直接展示首篇文档时，传 `activePath` 指定对应的目录地址以高亮侧栏并生成上一篇和下一篇。
- 窄屏时左侧目录收进抽屉，正文顶部显示目录入口按钮；中等宽度时隐藏本页目录。
- 放在代码仓库中的文档用[预渲染](hosting.md#prerendering)。
