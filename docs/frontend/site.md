# 官网与文档站

官网、落地页、内容站和文档站用 `site` 样式预设：正文 16px，大标题随屏幕宽度缩放，行距宽松。页面在服务端渲染，搜索引擎可以收录；不随请求变化的页面在构建时预渲染。`tenon add frontend <名称> --style site` 生成的前端已经这样设置好。

阅读前需要了解：[搭建前端应用](app.md)；[TanStack Start 的接入](start.md)。

- [渲染方式](#rendering)
- [官网的布局](#site)
- [落地页](#landing)
    - [风格](#landing-styles)
    - [区块](#landing-blocks)
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

<a id="landing"></a>
## 落地页

首页、产品介绍这类落地页由几个区块拼成：首屏、客户标识、特性、数据、用户评价和行动号召。区块放在 `SiteLayout` 之中，只写内容；外观由主题中的落地页风格决定，四种风格用同一份代码：

```tsx title="src/routes/index.tsx"
import { Button } from "@mantine/core"
import { IconArrowRight, IconBolt, IconLock } from "@tabler/icons-react"
import { CallToAction, Features, Hero, LogoCloud, ProductShot, SiteLayout, Stats, Testimonial } from "@go-tenon/ui"

function Home() {
  return (
    <SiteLayout brand="Acme" nav={nav} actions={<Button size="sm">开始使用</Button>}>
      <Hero
        badge="1.0 正式发布"
        title={
          <>
            一个后端，
            <br />
            <em>交付整个产品</em>
          </>
        }
        description="两三句话介绍产品。"
        actions={
          <>
            <Button size="md" rightSection={<IconArrowRight size={16} />}>开始使用</Button>
            <Button size="md" variant="default">阅读文档</Button>
          </>
        }
        note="go install example.com/acme@latest"
        visual={
          <ProductShot url="app.acme.cn">
            <img src="/screenshot.png" alt="产品截图" />
          </ProductShot>
        }
      />
      <LogoCloud title="正在使用的团队" items={["云栈科技", "北辰物流", "青禾教育"]} />
      <Features
        eyebrow="特性"
        title="为什么选择 Acme"
        items={[
          { icon: <IconBolt size={20} />, title: "快", description: "一两句话说明好处。" },
          { icon: <IconLock size={20} />, title: "安全", description: "一两句话说明好处。" },
        ]}
      />
      <Stats items={[{ value: "99.9", unit: "%", label: "可用性" }]} />
      <Testimonial quote="一位用户的评价。" name="林舟" title="技术负责人" />
      <CallToAction title="现在就开始" actions={<Button size="md">免费开始</Button>} />
    </SiteLayout>
  )
}
```

- 按钮直接用 Mantine 的 `Button`：主要按钮用默认的 `filled`，次要按钮用 `variant="default"`，风格会改变它们的形状和颜色。
- 标题中的 `<em>` 是强调的部分，各风格用品牌色、渐变字等方式突出，不显示为斜体。
- 页面中的其他内容照常用 Mantine 的组件，文字、边框和背景的颜色随风格变化。

<a id="landing-styles"></a>
### 风格

风格在 `src/theme.ts` 中设置，`tenon add frontend --style site --landing <风格>` 生成的前端已经写好：

```ts title="src/theme.ts"
export const theme = createUiTheme({ preset: "site", landing: "dusk", primary: "pale-indigo" })
```

| 风格 | 样子 | 适合 |
| --- | --- | --- |
| `classic`（默认） | 朴素，跟随 Mantine 的主题：浅色背景、品牌色的按钮和图标 | 先上线、以后再设计的官网 |
| `dusk` | 暗色优先：近黑的背景、品牌色的光晕、渐变的标题、半透明的细边框，产品截图带透视 | 开发者工具、技术产品 |
| `ink` | 黑白高对比：首屏的网格线、粗而紧的标题、共用边线的特性格子、等宽数字，按钮是黑色 | 偏工程、偏专业的产品 |
| `mist` | 柔和：粉紫蓝的渐变背景、悬浮的胶囊形页头、毛玻璃卡片、彩色图标、渐变按钮 | 面向大众的产品 |

- 每种风格都有亮色和暗色。`dusk` 的模板默认暗色（`UIProvider` 和 `ColorSchemeScript` 的 `defaultColorScheme="dark"`）。
- `dusk` 的光晕和强调色、`mist` 的按钮和渐变用主题的品牌色；`dusk` 的按钮是黑白的，`ink` 整体是黑白的，不用品牌色。
- 拉丁字母用 Inter：`site` 预设的字体以 Inter 开头，模板在根路由中引入 `@fontsource-variable/inter`；中文仍是系统字体。
- 框架的 `js/playground` 可以对比四种风格：`bun run playground` 后打开 `/?landing=dusk`，页面底部可以切换风格和配色。

<a id="landing-blocks"></a>
### 区块

| 区块 | 内容 |
| --- | --- |
| `Hero` | 首屏：`badge`（短公告）、`title`、`description`、`actions`（按钮）、`note`（按钮下方的一行，例如安装命令）、`visual`（下方的画面） |
| `ProductShot` | 带浏览器窗口边框的产品截图：`url` 是地址栏中的地址，`children` 是截图（`<img>`）或界面 |
| `CodeWindow` | 代码或终端窗口：`title`、`code`；`tone="terminal"` 总是暗色 |
| `LogoCloud` | 客户标识：`title`、`items`（图片或文字） |
| `Features` | 特性：标题区（`eyebrow`、`title`、`description`）和三列卡片。每一项有 `icon`、`title`、`description`，`wide` 占两列，`children` 放在说明下方，例如一段 `CodeWindow` |
| `Stats` | 一行关键数据：`value`、`unit`、`label` |
| `Testimonial` | 用户评价：`quote`、`name`、`title`（职位和公司）、`avatar`（没有时显示名字的第一个字） |
| `CallToAction` | 页面底部的行动号召：`title`、`description`、`actions` |
| `Section` | 自己的区块：同样的宽度、上下间距和标题区（`eyebrow`、`title`、`description`），内容是 `children` |

区块之外的内容（例如价格表）放在 `Section` 中，用 Mantine 的组件写，颜色随风格变化。

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
| `SiteLayout`、`SiteLink`、`FooterColumn` | 官网的布局：`brand`、`nav`、`actions`、`footer`、`note`、`size`；外观随主题的落地页风格 |
| `Landing`、`landings` | 落地页的风格：`classic`、`dusk`、`ink`、`mist`，用于 `createUiTheme({ landing })` |
| `Hero`、`ProductShot`、`CodeWindow`、`LogoCloud`、`Features`、`Stats`、`Testimonial`、`CallToAction`、`Section` | 落地页的区块 |
| `DocsLayout`、`DocsSection`、`useToc`、`headingId` | 文档页、本页目录、标题的 `id` |
