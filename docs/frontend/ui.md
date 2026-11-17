# 界面组件

`@go-tenon/ui` 是框架的界面层，基于 [Mantine](https://mantine.dev)。它提供三套样式预设：运营后台、SaaS 控制台、官网和内容站，以及按[应用骨架](app-shell.md)的约定实现的布局和部件：应用外壳、登录页、错误页、官网和文档页的布局、顶栏菜单。

菜单、权限、多标签、会话这些逻辑都在 `@go-tenon/shell` 中，这里只负责外观。页面中的其他内容直接使用 Mantine 的组件。

阅读前需要了解：[应用骨架](app-shell.md)；[在 React 中调用接口](client-react.md)。

- [装配](#setup)
- [样式预设](#presets)
- [由设计令牌生成主题](#tokens)
- [应用外壳](#app-layout)
    - [侧栏菜单与图标](#menu)
    - [顶栏与插槽](#slots)
    - [多标签](#tabs)
- [页面](#page)
- [登录页](#auth)
- [顶栏菜单](#menus)
- [错误页](#errors)
- [官网](#site)
- [文档页](#docs)
- [表格](#data-table)
- [表单](#forms)
- [资源页面](#resources)
- [设置](#settings)
- [导入与导出](#transfer)
- [队列面板](#queue-panel)
- [操作记录](#activity)
- [新版本提示](#new-version)
- [确认与提示](#feedback)
- [路由链接](#links)
- [演示](#playground)
- [API 参考](#api-reference)

<a id="setup"></a>
## 装配

`tenon init` 和 `tenon add frontend --style <预设>` 生成的前端已经装配好。前端用 [TanStack Start](start.md) 渲染，`UIProvider` 放在根路由的 `shellComponent`（整个 HTML 文档）中，同时引入样式：

```tsx title="src/routes/__root.tsx"
import "@go-tenon/ui/styles.css"
import type { ReactNode } from "react"
import { ColorSchemeScript, mantineHtmlProps } from "@mantine/core"
import { createRootRouteWithContext, HeadContent, Outlet, Scripts, useRouter } from "@tanstack/react-router"
import type { RouterContext } from "@go-tenon/shell"
import { UIProvider } from "@go-tenon/ui"
import { theme } from "../theme"

export const Route = createRootRouteWithContext<RouterContext>()({ shellComponent: Document, component: Outlet })

function Document({ children }: { children: ReactNode }) {
  const nonce = useRouter().options.ssr?.nonce
  return (
    <html lang="zh-CN" {...mantineHtmlProps}>
      <head>
        <ColorSchemeScript nonce={nonce} />
        <HeadContent />
      </head>
      <body>
        <UIProvider theme={theme}>{children}</UIProvider>
        <Scripts />
      </body>
    </html>
  )
}
```

- `ColorSchemeScript` 在页面显示之前设置亮色或暗色，避免闪烁。它是内联脚本，要带上 Go 为这次请求生成的 CSP nonce（`createApp` 的 `router.ssr.nonce` 来自 `@go-tenon/start` 的 `cspNonce()`）。
- `mantineHtmlProps` 让服务端渲染的 `<html>` 与浏览器设置的配色属性一致，不产生水合警告。

主题在 `src/theme.ts` 中，选一套样式预设，改品牌色：

```ts title="src/theme.ts"
import { createUiTheme } from "@go-tenon/ui"

export const theme = createUiTheme({ preset: "admin", primary: "#4f5bd5" })
```

`UIProvider` 安装 Mantine 的主题、右上角的通知（`@mantine/notifications`）、确认对话框（`@mantine/modals`）和图标名称的对应表。组件测试中传 `env="test"`，关闭过渡动画和 portal。配色默认跟随系统，用户在顶栏切换后保存在浏览器中。

应用的路由使用 `RouteError` 和 `NotFoundPage` 作为默认的错误页，见[应用骨架](app-shell.md#creating-the-app)：

```tsx title="src/router.tsx"
createApp({
  id: "admin",
  routeTree,
  session,
  router: { ssr: { nonce: cspNonce() }, defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFoundPage },
})
```

<a id="presets"></a>
## 样式预设

三套预设共用一套设计语言：系统字体、偏冷的中性色、1 像素的细边框、几乎不用阴影，层级靠留白、字重和背景的明暗区分。亮色和暗色都完整定义，组件中不写死颜色。

| 预设 | 适合 | 特点 |
| --- | --- | --- |
| `themes.admin` | 运营后台、内部系统、以表格和表单为主的后台 | 正文 13px，控件 30px 高，顶栏 48px，圆角 4px，一屏能看到更多数据 |
| `themes.console` | 面向客户的 SaaS 控制台、用户中心 | 正文 14px，控件 36px 高，顶栏 56px，圆角 8px，留白舒展 |
| `themes.site` | 官网、落地页、文章、文档 | 正文 16px，大标题随屏幕宽度缩放，行距宽松，顶栏 64px |

三套的品牌色默认都是偏冷的蓝紫（`#4f5bd5`），在亮色和暗色背景上都清楚。布局用到的尺寸（顶栏高度、侧栏宽度、内容的内边距、控件的默认尺寸）放在主题的 `other.tenon` 中，`AppLayout` 等布局据此排版，用 `layoutMetrics(theme)` 读取。

一个前端只用一套预设。运营后台和面向客户的控制台可以是两个前端，各用自己的预设，品牌色来自同一份[设计令牌](#tokens)。

<a id="tokens"></a>
## 由设计令牌生成主题

`createUiTheme` 由预设和设计令牌生成主题：

```ts
import { createUiTheme } from "@go-tenon/ui"

export const theme = createUiTheme({ preset: "console", primary: "#0f766e", radius: 10 })
```

- `primary` 是品牌色。它生成 10 级色板：同一色相，按感知亮度（OKLCH）分级，第 6 级（亮色模式的主色）就是输入的颜色，很浅或很深的颜色也从浅到深单调。也可以单独调用 `palette(hex)`。
- `radius` 是基础圆角（像素），其他圆角按比例缩放。
- `override` 是再合并的 Mantine 主题覆盖，用于令牌之外的调整，例如某个组件的默认属性。

<a id="app-layout"></a>
## 应用外壳

`AppLayout` 是运营后台和控制台需要登录的布局：左侧是侧栏菜单，顶部是面包屑和插槽，下面是页面。它放在受保护的布局路由上：

```tsx title="src/routes/_app.tsx"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { Fill, requireAuth } from "@go-tenon/shell"
import { AppLayout, NotificationsMenu, OperationsMenu, TenantSwitcher, UserMenu } from "@go-tenon/ui"
import { api } from "../app"

export const Route = createFileRoute("/_app")({ beforeLoad: requireAuth, component: Layout })

function Layout() {
  const me = useQuery({ queryKey: ["me"], queryFn: () => api.user.me() })
  return (
    <>
      <Fill name="actions">
        <OperationsMenu />
      </Fill>
      <Fill name="tenant">
        <TenantSwitcher />
      </Fill>
      <Fill name="user">
        <UserMenu name={me.data?.name} logout={() => api.auth.logout()} />
      </Fill>
      <AppLayout title="博客后台" tabs />
    </>
  )
}
```

- 侧栏可以收起为图标，状态按账号保存在浏览器中。窄屏时侧栏变成抽屉，由顶栏的按钮打开，打开页面后自动收起。
- 面包屑来自路由的标题；最后一级是当前页面，不是链接。窄屏时只显示当前页面。
- `maxWidth` 限制内容区的宽度，适合表单为主的控制台；运营后台通常铺满。

<a id="menu"></a>
### 侧栏菜单与图标

菜单由应用骨架按路由的 `staticData` 生成，按权限过滤，见[应用骨架](app-shell.md)：

```tsx
export const Route = createFileRoute("/_app/orders")({
  staticData: { title: "订单", icon: "order", order: 2, permission: "orders:read" },
  component: Orders,
})
```

- 有子页面的路由是分组，可以展开；当前页面所在的分组自动展开。侧栏收起时，分组用弹出菜单列出子页面。
- `icon` 是图标的名称，对应 [Tabler 图标](https://tabler.io/icons)。内置的名称见 `defaultIcons`，例如 `home`、`dashboard`、`article`、`users`、`team`、`settings`、`safety`、`order`、`product`、`report`、`wallet`。写错的名称显示为一个空心圆，不会报错。
- 需要其他图标时，通过 `UIProvider` 的 `icons` 增加：

```tsx
import { IconTruckDelivery } from "@tabler/icons-react"

<UIProvider theme={themes.admin} icons={{ delivery: IconTruckDelivery }}>
```

菜单项的徽标（例如未读数）用应用骨架的 `useMenuBadge` 设置，侧栏显示为红色的数字或小圆点。

<a id="slots"></a>
### 顶栏与插槽

页面和布局路由用 `Fill` 往插槽放内容，`AppLayout` 决定它们的位置：

| 插槽 | 位置 |
| --- | --- |
| `brand` | 侧栏顶部，名称后面 |
| `search` | 顶栏右侧，最左边，例如搜索框或命令面板的入口 |
| `actions` | 顶栏右侧，例如通知、后台任务，或者当前页面的操作 |
| `tenant` | 顶栏右侧，配色切换之后 |
| `user` | 顶栏最右边 |
| `aside` | 右侧面板，有内容时才出现 |
| `footer` | 内容区的底部 |

顶栏默认有配色切换按钮，`hideColorScheme` 隐藏它。

<a id="tabs"></a>
### 多标签

`tabs` 开启多标签：内容区顶部是标签栏，布局之下的每个页面是一个标签，切换时保留页面的状态。标签栏支持：

- 点击切换，中键或关闭按钮关闭；
- 右键菜单：刷新、固定、关闭、关闭其他、关闭右侧、全部关闭；
- 拖动排序；有未保存修改的标签显示一个圆点。

`tabs` 也可以是 `TabsOutlet` 的选项，例如 `tabs={{ max: 8 }}`。多标签的行为见[应用骨架：页面中的常用功能](app-shell-features.md)。

<a id="page"></a>
## 页面

`Page` 是页面的标题区：标题、一句说明和右侧的操作，所有页面保持一致的层级和间距：

```tsx
import { Button } from "@mantine/core"
import { Page } from "@go-tenon/ui"

function Orders() {
  return (
    <Page title="订单" description="全部渠道的订单，按下单时间倒序。" actions={<Button>新建订单</Button>}>
      {/* 筛选、表格 */}
    </Page>
  )
}
```

<a id="auth"></a>
## 登录页

`AuthLayout` 是登录、注册、找回密码等页面的布局：页面中央一张卡片。`LoginForm` 是邮箱和密码的登录表单：

```tsx title="src/routes/login.tsx"
import { createFileRoute } from "@tanstack/react-router"
import { AnchorLink, AuthLayout, LoginForm } from "@go-tenon/ui"
import { api } from "../app"

export const Route = createFileRoute("/login")({
  validateSearch: (s: Record<string, unknown>) => ({ redirect: typeof s.redirect === "string" ? s.redirect : undefined }),
  component: Login,
})

function Login() {
  const { redirect } = Route.useSearch()
  return (
    <AuthLayout title="登录" description="使用工作邮箱登录。">
      <LoginForm
        login={(values) => api.auth.login(values)}
        redirect={redirect}
        forgot={<AnchorLink to="/forgot">忘记密码？</AnchorLink>}
      />
    </AuthLayout>
  )
}
```

- `login` 调用应用的登录接口，服务端把登录凭据写进会话 cookie。成功后表单调用 `app.session.signIn()`，由会话的 `load` 读取当前用户，再回到登录前想去的页面。
- 接口返回的字段错误显示在对应的输入框下面，其他错误（例如密码不对、尝试次数过多）显示在表单上方。断网等没有服务端文案的错误，按种类显示提示，见 `errorMessage`。

<a id="menus"></a>
## 顶栏菜单

| 组件 | 作用 |
| --- | --- |
| `UserMenu` | 头像，菜单中有名称、邮箱、应用的菜单项和退出登录。`logout` 调用应用的退出接口，之后清除会话中的身份 |
| `TenantSwitcher` | 当前租户的名称，菜单中列出用户所属的全部租户；只有一个租户时只显示名称，不是多租户应用时不显示 |
| `NotificationsMenu` | 铃铛和未读数，弹出最近的通知，点击打开并标记已读。`source` 是通知的接口，见[应用骨架：页面中的常用功能](app-shell-features.md) |
| `OperationsMenu` | 后台任务的进度：导入、导出等耗时的操作。有进行中的任务时显示转动的图标，没有任务时不显示 |
| `ColorSchemeToggle` | 亮色和暗色之间切换，`AppLayout` 默认已经放在顶栏 |

<a id="errors"></a>
## 错误页

- `RouteError` 是路由的默认错误组件：没有权限（`ForbiddenError`、`403`）显示 403，不存在（`404`）显示 404 和服务端的说明，断网显示网络错误，其他错误显示可以重试的错误页和请求 ID。它同时把错误报告给 `AppOptions.onError`。
- `NotFoundPage` 是没有匹配到路由时的 404 页。
- `ErrorPage` 可以直接使用，`kind` 是 `forbidden`、`not_found`、`server` 或 `network`，标题、说明和操作都可以替换。

<a id="site"></a>
## 官网

`SiteLayout` 是官网和内容站的布局：吸顶的半透明页头（标志、导航、操作）、内容和页脚。窄屏时导航收进右侧的抽屉。

```tsx title="src/routes/__root.tsx"
import { Button } from "@mantine/core"
import { createRootRouteWithContext } from "@tanstack/react-router"
import type { RouterContext } from "@go-tenon/shell"
import { SiteLayout } from "@go-tenon/ui"

export const Route = createRootRouteWithContext<RouterContext>()({ component: Site })

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

链接中应用内的地址用 `to`，外部地址用 `href`。没有传 `children` 时内容是子路由。

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

<a id="data-table"></a>
## 表格

`DataTable` 基于 [mantine-datatable](https://icflorescu.github.io/mantine-datatable/)，应用不直接依赖它。分页和排序接[应用骨架](app-shell-features.md)的列表模型 `useList`，保存在地址中：

```tsx
import { useQuery } from "@tanstack/react-query"
import { useList, useSelection } from "@go-tenon/shell"
import { Button } from "@mantine/core"
import { type Column, DataTable } from "@go-tenon/ui"

const columns: Column<Order>[] = [
  { accessor: "number", title: "订单号", fixed: true },
  { accessor: "customer", title: "客户" },
  { accessor: "amount", title: "金额", sortable: true, textAlign: "right", render: (o) => money(o.amount) },
  { accessor: "note", title: "备注", hidden: true },
]

function Orders() {
  const list = useList(Route)
  const orders = useQuery(queries.order.list(list.params))
  const selection = useSelection<string>(list.filters)
  return (
    <DataTable
      records={orders.data?.items ?? []}
      total={orders.data?.total}
      loading={orders.isPending}
      list={list}
      columns={columns}
      selection={selection}
      bulkActions={(ids) => <Button color="red" variant="light">删除 {ids.length} 项</Button>}
      storeKey="orders"
    />
  )
}
```

- 分页显示“1–20，共 N 条”，可以选择每页的条数；翻页、排序都写进地址，排序后回到第一页。没有 `total` 时（游标分页）不显示分页。
- `selection` 显示勾选框，选中在翻页时保留；所以 `useSelection` 的 `resetOn` 用 `list.filters`，换了查询条件才清空。有选中的行时，表格上方显示 `bulkActions`。
- `storeKey` 显示列设置按钮：勾选显示的列，上下移动调整顺序，保存在浏览器中。`fixed` 的列（例如名称、操作）不能隐藏和移动；`hidden` 的列默认隐藏。
- `expand` 点击一行展开明细；`toolbar` 放在表格上方左侧，例如筛选条件。
- 行高、字号随主题的预设：运营后台更紧凑。

<a id="forms"></a>
## 表单

表单用 [@mantine/form](https://mantine.dev/form/use-form/)。`useSchemaForm` 用生成的 `schemas.ts` 校验，规则和提示与服务端相同：

```tsx
import { Button, TextInput } from "@mantine/core"
import { useUnsavedChanges } from "@go-tenon/shell"
import { applyServerErrors, FormError, useSchemaForm } from "@go-tenon/ui"
import type { ErrorInfo } from "@go-tenon/client"
import { useState } from "react"
import { api } from "../app"
import { components, schemas } from "@blog/client/api/schemas"
import { messages } from "@blog/client/api/messages"

function NewArticle() {
  const form = useSchemaForm({
    schema: schemas.article.create.body,
    rules: { components, messages, language: "zh-CN" },
    initialValues: { title: "", slug: "" },
  })
  const [error, setError] = useState<ErrorInfo>()
  const unsaved = useUnsavedChanges(form.isDirty())
  return (
    <form
      onSubmit={form.onSubmit(async (values) => {
        try {
          await api.article.create(values)
          unsaved.release()
        } catch (err) {
          setError(applyServerErrors(form, err))
        }
      })}
    >
      <FormError error={error} />
      <TextInput label="标题" {...form.getInputProps("title")} />
      <TextInput label="链接名" {...form.getInputProps("slug")} />
      <Button type="submit">保存</Button>
    </form>
  )
}
```

- 提交时校验整个表单，之后离开一个字段时校验它。
- `applyServerErrors` 把服务端返回的字段错误显示在对应的输入框下面，返回其他错误，交给 `FormError` 显示在表单上方。
- `useUnsavedChanges`（应用骨架）在有未保存的修改时，离开页面之前询问；页面在标签中时，改为在关闭标签时询问。

按字段类型选择输入组件用 `DefaultInput`，按类型显示一个值用 `FieldValue`，见[资源页面](#resources)。

<a id="resources"></a>
## 资源页面

`ResourcePage` 按 Go 中声明的[资源](resources.md)渲染完整的增删改查页面：列表、搜索和筛选、新建和修改的表单、删除和其他操作，按权限显示按钮。

```tsx title="src/routes/_app/roles.tsx"
import { createFileRoute } from "@tanstack/react-router"
import { ResourcePage } from "@go-tenon/ui"
import { roles } from "../../resources"

export const Route = createFileRoute("/_app/roles")({
  staticData: { title: "角色", icon: "safety", permission: "roles:manage" },
  validateSearch: roles.listSearch,
  component: () => <ResourcePage model={roles} route={Route} />,
})
```

`roles` 是应用骨架的 `resourceModel` 创建的模型，见[资源](resources.md)。页面的行为：

- **列表**：列的显示文字、枚举的文字、布尔、时间按字段的类型显示。可以排序的列点击表头排序，列设置按资源的名称保存在浏览器中。
- **搜索和筛选**：关键词搜索按回车或离开输入框时生效；枚举字段是下拉选择，布尔字段是“是/否”。条件写进地址，有条件时显示“清除筛选”。
- **新建和修改**：右侧的抽屉。表单字段来自请求体，校验与服务端相同；修改时有详情接口就先读取完整的一行。服务端的字段错误显示在输入框下面。有未保存的修改时，关闭抽屉之前询问。
- **删除和操作**：在每一行的“更多操作”菜单中。删除和声明了 `Confirm` 的操作先确认；完成或失败后右上角提示，列表随之刷新。

需要覆盖的部分通过选项传入：

| 选项 | 作用 |
| --- | --- |
| `title`、`description` | 页面的标题和说明；`title={null}` 不显示标题区 |
| `render` | 覆盖某一列的显示，例如 `{ title: (v, row) => <AnchorLink to="/articles/$id" params={{ id: String(row.id) }}>{String(v)}</AnchorLink> }` |
| `inputs` | 覆盖某个表单字段的输入组件，例如 `{ content: MarkdownInput }`，组件的属性是 `FieldInputProps` |
| `rowActions` | 行上的额外操作，排在声明的操作之后、删除之前 |
| `toolbar` | 标题区的额外按钮，例如导入、导出 |
| `expand` | 点击一行展开的内容 |

字段的输入方式按应用骨架的 `FieldKind`：

| 类型 | 输入组件 |
| --- | --- |
| `text` | 文本框 |
| `number` | 数字输入 |
| `boolean` | 开关 |
| `select` | 下拉选择，非必填时可以清除 |
| `multiselect` | 多选 |
| `tags` | 标签输入，回车添加 |
| `date` | 日期选择，值是 `YYYY-MM-DD` |
| `datetime` | 日期和时间选择，值是 ISO 时间 |
| `json` | JSON 编辑，是合法的 JSON 时才交给表单 |

<a id="settings"></a>
## 设置

`SettingsForm` 按 Go 中声明的设置渲染一组值的表单：读取设置作为初始值，没有保存的权限时只读。

```tsx
import { Page, SettingsForm } from "@go-tenon/ui"
import { blogSettings } from "../../resources"

function Settings() {
  return (
    <Page title="设置">
      <SettingsForm model={blogSettings} title={null} />
    </Page>
  )
}
```

- 有修改时“保存”按钮才可用；校验规则与服务端相同，服务端的字段错误显示在输入框下面。
- `autoSave` 去掉保存按钮：修改后停下一会儿就保存，表单下方显示“有未保存的修改”“保存中…”“已保存”；校验不通过时不保存，离开页面时保存还在等待的修改。

<a id="transfer"></a>
## 导入与导出

`ImportButton` 和 `ExportButton` 是[后台导入](../features/background-imports.md)和[后台导出](../features/background-exports.md)的界面，模型是应用骨架的 `useImport`、`useExport`：

```tsx
import { ExportButton, ImportButton } from "@go-tenon/ui"

<ImportButton source={articleImport} template={downloadTemplate} onCompleted={() => list.refetch()} />
<ExportButton source={{ start: () => api.article.exportStart({ format: "xlsx" }), progress }} />
```

- 导入：对话框中点击选择或拖入表格，显示上传和导入的进度；结束后显示成功和没有导入的行数，可以下载没有导入的行，改正后重新导入。失败时显示原因，可以重新选择文件。
- 导出：点击后在后台导出，对话框显示进度，完成后下载。
- 进度同时出现在顶栏的后台任务（`OperationsMenu`）中，关闭对话框不影响导入导出。

<a id="queue-panel"></a>
## 队列面板

`QueuePanel` 是[队列的运维接口](../background/queue-monitoring.md#queue-api)（`queue/queueapi`）的页面。它接收应用生成的客户端分组 `api.queue`，放在一个需要运维权限的路由上：

```tsx title="src/routes/_app/queue.tsx"
import { createFileRoute } from "@tanstack/react-router"
import { QueuePanel } from "@go-tenon/ui"
import { api } from "../../app"

export const Route = createFileRoute("/_app/queue")({
  staticData: { title: "队列", icon: "list", permission: "queue:manage" },
  component: () => <QueuePanel api={api.queue} />,
})
```

- **概览**：复制槽（是否有 Worker 在转发、积压、WAL 状态）、JetStream（消息数、定时投递、副本）和失败任务总数，每 5 秒刷新（`refreshInterval`）。读不到 JetStream 时显示原因，数据库一侧照常显示；应用没有设置 `queueapi.Options.Admin` 时只有数据库一侧。
- **Topic**：每个 topic 的状态、等待、处理中、重新投递、失败的任务数和最近投递的时间。正常和停滞的 topic 可以暂停一小时（先确认），暂停的可以恢复；没有设置 `Admin` 时没有这两个按钮。
- **失败任务**：按 topic 筛选（标签上是条数）、分页。单条重试和删除先确认；“全部重试”“全部删除”处理当前筛选的 topic（全部时是全部 topic），完成后提示条数。
- **详情**：点击一条失败任务，右侧显示完整的错误和堆栈、元数据（例如 `request_id`）和 payload，可以在这里重试或删除。
- 页头是概览返回的外部链接（`queueapi.Options.Links`，例如 Grafana）。

`api.queue` 的形状由 `QueueApi` 描述，不依赖具体的应用；字段与 `queue/queueapi` 的类型一致。

<a id="activity"></a>
## 操作记录

`ActivityTimeline` 按时间从新到旧显示一个对象的历史或一个人的操作记录，到底时“加载更多”。`source.list(before)` 是应用自己的接口，见[操作日志](../features/activity.md#history-page)：

```tsx
<ActivityTimeline source={{ key: ["article", id], list: (before) => api.article.history(id, { before }).then((r) => r.items) }} />
```

点击带 `href` 的记录打开它；`onOpen` 可以换成自己的处理。

<a id="new-version"></a>
## 新版本提示

部署了新版本后，已经打开的页面还在运行旧代码。`NewVersionNotice` 在发现新版本时于右下角提示“刷新”或“稍后”，版本号来自模板中的 `tenonVersion` 插件，见[新版本提示](app-shell.md#new-version)。`AppLayout` 已经包含它，其他布局需要时自己放一个。

<a id="feedback"></a>
## 确认与提示

- `confirm({ title, message, confirmLabel, danger })` 弹出确认对话框，用户确认时返回 `true`。
- `notifySuccess(message)` 在右上角提示操作成功；`notifyError(err, title)` 提示失败，文案取服务端的翻译，没有时按错误的种类给出。

它们需要 `UIProvider`（其中安装了对话框和通知）。

<a id="links"></a>
## 路由链接

直接写 `<Button component={Link} to=…>` 会丢掉路由参数的类型检查。`ButtonLink`、`AnchorLink`、`NavLinkLink`、`ActionIconLink` 用 TanStack Router 的 `createLink` 包装了 Mantine 的组件，`to`、`params`、`search` 按路由表检查：

```tsx
import { ButtonLink } from "@go-tenon/ui"

<ButtonLink to="/articles/$id" params={{ id: article.id }} variant="default">
  查看
</ButtonLink>
```

<a id="playground"></a>
## 演示

框架的 `js/playground` 是三套预设的演示：运营后台（`/admin/`）、SaaS 控制台（`/console/`）、官网（`/`）和文档页（`/docs/getting-started`）、登录页（`/admin/login`）。在框架仓库的 `js` 目录中运行：

```bash
bun run playground
```

地址后加 `?scheme=dark` 用暗色打开。

<a id="api-reference"></a>
## API 参考

包 `@go-tenon/ui`，样式 `@go-tenon/ui/styles.css`。

| 导出 | 说明 |
| --- | --- |
| `UIProvider` | 安装主题、通知和图标：`theme`、`defaultColorScheme`、`icons` |
| `themes`、`Preset` | 三套样式预设：`admin`、`console`、`site` |
| `createUiTheme(options)` | 由预设和设计令牌生成主题：`preset`、`primary`、`radius`、`fontFamily`、`override` |
| `palette(hex)` | 由一个颜色生成 10 级色板 |
| `layoutMetrics(theme)`、`LayoutMetrics` | 主题中的布局尺寸 |
| `AppLayout` | 应用外壳：`title`、`logo`、`tabs`、`maxWidth`、`hideColorScheme` |
| `TabBar` | 标签栏，`AppLayout` 开启 `tabs` 时使用 |
| `Page` | 页面的标题区：`title`、`description`、`actions` |
| `AuthLayout`、`LoginForm`、`errorMessage` | 认证页的布局、登录表单、错误的提示文字 |
| `UserMenu`、`TenantSwitcher`、`NotificationsMenu`、`OperationsMenu`、`ColorSchemeToggle` | 顶栏菜单 |
| `ErrorPage`、`RouteError`、`NotFoundPage` | 错误页 |
| `SiteLayout` | 官网的布局：`brand`、`nav`、`actions`、`footer`、`note`、`size` |
| `DocsLayout`、`useToc`、`headingId` | 文档页、本页目录、标题的 `id` |
| `DataTable`、`Column`、`TableList` | 表格 |
| `useSchemaForm`、`applyServerErrors`、`FormError` | 表单的校验、服务端错误 |
| `DefaultInput`、`FieldInput`、`FieldInputProps`、`FieldValue` | 按字段类型输入和显示 |
| `ResourcePage`、`ResourceForm`、`RowAction` | 资源页面、新建和修改的表单 |
| `SettingsForm` | 设置的表单 |
| `ImportButton`、`ExportButton` | 导入和导出 |
| `QueuePanel`、`QueueApi` | 队列面板和它需要的接口 |
| `ActivityTimeline` | 操作记录的时间线 |
| `NewVersionNotice` | 新版本提示，`AppLayout` 中已经包含 |
| `confirm`、`notifySuccess`、`notifyError` | 确认对话框和提示 |
| `ButtonLink`、`AnchorLink`、`NavLinkLink`、`ActionIconLink` | 类型安全的路由链接 |
| `Icon`、`useIcon`、`defaultIcons`、`IconsProvider` | 图标名称 |
