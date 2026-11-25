# 搭建前端应用

一个前端是项目中的 `apps/<名称>` 目录，用 TanStack Start 构建，由 Go 托管（见[托管前端与服务端渲染](hosting.md)）。`@go-tenon/ui` 把 TanStack Router、TanStack Query 和会话接在一起，提供主题、登录检查、权限检查、错误页和新版本提示；布局见[布局与导航](layout.md)。

包：`@go-tenon/ui`、`@go-tenon/start`。

<a id="setup"></a>
## 装配

`tenon init` 和 `tenon add frontend <名称>` 生成的前端已经按本节装配好，给已有的前端补上某一部分时照着做。

<a id="installing"></a>
### 依赖与 `vite.config.ts`

`@go-tenon/*` 随框架同步到项目的 `tenon/js`（见[框架的前端包](../installation/tooling.md#framework-packages)），前端用 `workspace:*` 依赖它们。`package.json` 的 `tenon` 字段写挂载路径和渲染方式（见[两种渲染方式](hosting.md#modes)），`typecheck` 先生成路由树：

```json
{
  "tenon": { "base": "/admin/", "ssr": false },
  "scripts": { "typecheck": "tenon-start routes && tsc --noEmit" },
  "dependencies": { "@go-tenon/client": "workspace:*", "@go-tenon/start": "workspace:*", "@go-tenon/ui": "workspace:*" }
}
```

- `vite.config.ts` 只用 `@go-tenon/start/vite` 的 `tenon()`，它包含 TanStack Start、React 和[新版本提示](#new-version)的插件，见[安装 `@go-tenon/start`](hosting.md#setup)。不写 Vite 的 `base`：它取自 `tenon.base`，两者不同时构建报错。
- `src/routes` 中一个文件是一个页面，路由树生成在 `src/routeTree.gen.ts`。`tenon-start routes` 重新生成它，没有运行 `tenon dev` 时新页面也能通过类型检查。

<a id="creating-the-app"></a>
### 用 `createApp` 创建应用

`src/router.tsx` 的 `getRouter` 用 `createApp` 创建 Router 和 QueryClient。服务端渲染时每个请求调用一次 `getRouter`，请求之间不共享缓存：

```tsx title="src/router.tsx"
import { cspNonce } from "@go-tenon/start"
import { createApp, NotFoundPage, RouteError } from "@go-tenon/ui"
import { me, session } from "./auth"
import { i18n } from "./i18n"
import { routeTree } from "./routeTree.gen"

export function getRouter() {
  return createApp({
    id: "admin",
    routeTree,
    i18n,
    session,
    permissions: async () => (await me()).permissions ?? [],
    router: { ssr: { nonce: cspNonce() }, defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFoundPage },
  }).router
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof getRouter>
  }
}
```

- `session` 和 `me` 在 `src/auth.ts` 中，`session` 由 `@go-tenon/client` 的 `createSession` 创建，见[客户端代码](../http/clients.md#creating-the-client)。没有登录功能的应用不传 `session`，一直是未登录。
- `i18n` 在多语言项目的 `src/i18n.ts` 中，控制页面、菜单和组件的语言；单语言项目不传 `i18n`，改用可选的 `language`（默认 `"zh-CN"`）和 `uiMessages` 覆盖内置组件文字，见[界面文字](i18n.md)。
- `id` 区分同一个域名下的前端（例如 `web` 和 `admin`），是[本地存储](#storage)的命名空间。
- 登录、退出、切换账号或租户、接口返回 `401` 时，清空 QueryClient 的缓存，重新运行路由的守卫，并重新挂载布局中的页面。自己写布局时把页面放在 `SessionBoundary` 中，见[自己写布局](layout.md#custom-layouts)。
- 默认的 QueryClient 对 `4xx` 不重试，其他错误重试 2 次；可以传入自己的 `queryClient`。
- 路由的 `basepath` 默认是 `tenon.base`，页面中的地址、`<Link>` 和登录回跳都不含它。
- 读取当前用户、检查权限只在浏览器中进行：服务端渲染的前端中，需要登录的路由设置 `ssr: false`。
- 多租户应用设置 `tenancy`，见[租户](layout.md#tenants)；`onError` 见[前端观测](observability.md#route-errors)。

<a id="root"></a>
### 根路由与 `UIProvider`

根路由用 `createRootRouteWithContext<RouterContext>()` 创建，`beforeLoad` 中通过 `context.app` 使用应用。`shellComponent` 是整个 HTML 文档：

```tsx title="src/routes/__root.tsx"
import "@go-tenon/ui/styles.css"
import type { ReactNode } from "react"
import { ColorSchemeScript, mantineHtmlProps } from "@mantine/core"
import { createRootRouteWithContext, HeadContent, Outlet, Scripts, useRouter } from "@tanstack/react-router"
import { type RouterContext, UIProvider } from "@go-tenon/ui"
import { theme } from "../theme"

export const Route = createRootRouteWithContext<RouterContext>()({ shellComponent: Document, component: Outlet })

function Document({ children }: { children: ReactNode }) {
  const { ssr, context } = useRouter().options
  const nonce = ssr?.nonce
  // 配色保存在这个前端自己的键中，同一个域名下的其他前端（例如管理员端）各记各的。
  const colorScheme = context.app.storage.key("color-scheme")
  return (
    <html lang="zh-CN" {...mantineHtmlProps}>
      <head>
        <ColorSchemeScript nonce={nonce} localStorageKey={colorScheme} />
        <HeadContent />
      </head>
      <body>
        <UIProvider theme={theme} colorSchemeKey={colorScheme}>{children}</UIProvider>
        <Scripts />
      </body>
    </html>
  )
}
```

- `ColorSchemeScript` 是内联脚本，必须带上 `createApp` 的 `router.ssr.nonce`（Go 为每个请求生成），否则被内容安全策略拦下。
- `UIProvider` 安装主题、通知、确认对话框和图标。配色默认跟随系统，用户的选择保存在 `colorSchemeKey` 中；它和 `ColorSchemeScript` 的 `localStorageKey` 是同一个 `app.storage.key("color-scheme")`，同一个域名下的每个前端各记各的配色。组件测试中传 `env="test"`，关闭过渡动画和 portal。

<a id="presets"></a>
## 样式预设与主题 `createUiTheme`

主题在 `src/theme.ts` 中，选一套样式预设，设置品牌色：

```ts title="src/theme.ts"
import { createUiTheme } from "@go-tenon/ui"

export const theme = createUiTheme({ preset: "compact", primary: "blue-gray" })
```

预设只管密度，与布局无关，任何预设都可以配任何布局：

| 预设 | 样子 | 适合 |
| --- | --- | --- |
| `compact` | 正文 14px，控件 32px 高，圆角 4px | 表格和表单多的后台、内部系统 |
| `comfortable` | 正文 14px，控件 36px 高，圆角 8px | 页面不密集的控制台、用户中心 |
| `spacious` | 正文 16px，大标题随屏宽缩放，行距宽松 | 官网、文章、文档，见[官网与文档站](site.md) |

- 一个前端只用一套预设；同一个项目的几个前端可以各用各的预设。
- `tenon add frontend --preset` 选择预设，不写时 `frame` 布局是 `compact`，`rail` 是 `comfortable`，`site` 是 `spacious`。
- `compact` 与 `comfortable` 共用三级字号标尺：`md`（14px，行高 1.55）用于阅读与输入正文，`sm`（13px，行高 1.45）用于界面主文字与表单控件，`xs`（12px，行高 1.4）用于次级信息与元数据。
- 工具条和侧栏检查器等密集区域通过 `data-tn-density="dense"`（控件 30px 高）或 `data-tn-density="inspector"`（控件 28px 高）压低控件高度，同时保持 `sm`（13px）字号，与相邻文本值齐平。
- 控件尺寸和内容内边距在主题的 `other.tenon` 中，用 `layoutMetrics(theme)` 读取。顶栏高度、侧栏宽度由布局决定。

<a id="tokens"></a>
### 设计令牌：品牌色、圆角、字体、日期格式

`createUiTheme` 的选项：

- `preset`：样式预设，默认 `comfortable`。
- `primary`：品牌色，默认 `blue-gray`。写颜色值，或者 `primaryColors` 中 26 种预设的名称（例如 `sky-blue`、`pale-indigo`）。色板由 `@mantine/colors-generator` 生成，很深的颜色（例如 `brown`）在按钮上会比输入的浅一些。
- `radius`：基础圆角（像素），其他圆角按比例缩放。
- `fontFamily`：字体，默认是系统字体。
- `datetimeFormat`：日期时间的格式（dayjs 的写法），默认 `YYYY-MM-DD HH:mm`，`frame` 的模板用 `MM/DD HH:mm`。表格、操作记录、通知都用它，自己写的页面用 `useFormatDateTime()`：`useFormatDateTime()(order.created_at)` 得到 `11/03 10:12`。
- `override`：再合并的 Mantine 主题覆盖，例如某个组件的默认属性。

页面中的颜色从主题中取（`color="deep-orange"`、`c="dimmed"`），不写十六进制值：写死的颜色不随品牌色和暗色模式变化，见[样式](conventions.md#styles)。

<a id="time-zones"></a>
### 时区 `TimeZoneProvider`

时间默认按浏览器的时区显示。用户选了时区时，在布局路由中用 `TimeZoneProvider` 包住布局，`zone` 是 IANA 时区名，为空时按浏览器的时区。`user` 是布局中读取当前用户的查询：

```tsx
<TimeZoneProvider zone={user.data?.time_zone}>
  <RailLayout title="Acme" />
</TimeZoneProvider>
```

- 时区在登录后才读到，并且会变，所以它不是主题的选项。
- `useFormatDateTime` 和框架组件中的时间都按这个时区显示；资源表单的日期时间输入框按它输入，提交时换算成 ISO 时间，只有日期的字段（`format: "date"`）不换算。
- 别的格式用 `inTimeZone(value, useTimeZone()).format("HH:mm")`。

<a id="route-metadata"></a>
## 页面的元数据 `staticData`

页面的标题、图标、顺序和权限写在路由的 `staticData` 中，菜单、面包屑、标签和权限检查都读它：

```tsx title="src/routes/_app/articles/index.tsx"
import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/_app/articles/")({
  staticData: { title: "文章", icon: "article", order: 10, permission: "articles:read" },
  component: ArticlesPage,
})
```

| 字段 | 作用 |
| --- | --- |
| `title` | 菜单、面包屑和标签中的标题 |
| `icon` | 图标的名字，见[侧栏菜单与图标](layout.md#menu) |
| `order` | 同级菜单中的顺序，小的在前，默认 0；相同时按路径排序 |
| `permission` | 进入页面需要的权限，见[权限](#permissions) |
| `menu` | `false` 时不出现在菜单中 |
| `tab` | `false` 时不进入[多标签](layout.md#tabs) |
| `fullBleed` | `true` 时页面铺满布局的内容区，写在模块的路由上时模块下的页面都铺满，见[铺满的模块](layout.md#full-bleed) |

页面不出现在菜单中的原因：没有 `title`；`menu: false`；自己引入了路径参数（例如 `/articles/$id`）；当前用户缺少 `permission`。路径参数来自上级路由时（例如 `/t/$tenant`），当前地址中有这个参数才出现，见[租户](layout.md#tenants)。

<a id="requiring-sign-in"></a>
## 要求登录 `requireAuth`

需要登录的页面放在一个布局路由之下，布局的 `beforeLoad` 用 `requireAuth`：

```tsx title="src/routes/_app.tsx"
import { requireAuth } from "@go-tenon/ui"
import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/_app")({ beforeLoad: requireAuth, component: Layout })
```

- 打开第一个页面之前恢复一次登录状态（`session.restore()`），公开页面也知道是否已经登录。
- 没有登录时跳到 `createApp` 的 `loginPath`（默认 `/login`），带上 `?redirect=<原来的地址>`。
- 退出（`session.signOut()`）或切换身份时重新检查，离开需要登录的页面。
- 登录后又跳回登录页，是恢复登录状态时读不到当前用户：检查会话的 `load` 调用的接口，页面和接口要同源，会话 cookie 只随同源请求发送。

<a id="auth"></a>
### 登录页 `CenteredAuthLayout`、`SplitAuthLayout`、`LoginForm`

`CenteredAuthLayout` 在页面中央放一张卡片，`SplitAuthLayout` 左右分栏，左栏是品牌色的展示区（`aside`、`asideFooter`）。登录页不在应用的布局之下，样式与布局任意搭配，`tenon add frontend --login centered|split|none` 选择。`LoginForm` 是邮箱和密码的表单，放进任何一种样式：

```tsx title="src/routes/login.tsx"
import { createFileRoute } from "@tanstack/react-router"
import { AnchorLink, CenteredAuthLayout, LoginForm } from "@go-tenon/ui"
import { login } from "../auth"

export const Route = createFileRoute("/login")({
  validateSearch: (s: Record<string, unknown>) => ({ redirect: typeof s.redirect === "string" ? s.redirect : undefined }),
  component: Login,
})

function Login() {
  const { redirect } = Route.useSearch()
  return (
    <CenteredAuthLayout brand="Acme" title="登录">
      <LoginForm login={login} redirect={redirect} forgot={<AnchorLink to="/forgot">忘记密码？</AnchorLink>} />
    </CenteredAuthLayout>
  )
}
```

- `login` 调用应用的登录接口，服务端把凭据写进会话 cookie。成功后表单调用 `session.signIn()` 读取当前用户，再回到 `redirect`。
- 字段错误显示在输入框下，其他错误显示在表单上方。
- 浏览器标题是“登录 - Acme”（`title`、`brand` 是文字时）；显示图片标志时传 `logo` 属性，`brand` 保持文字即可保留完整标题。
- 桌面端中用 `BrowserSignIn` 代替 `LoginForm`，在系统浏览器中登录，见[客户端](../clients/overview.md)。

<a id="redirect-after-sign-in"></a>
### 登录后回跳 `redirectAfterSignIn` 与 `useSignedIn`

自己写登录表单时，登录接口成功后读取身份并回跳，`redirect` 是登录页的 `?redirect=`：

```tsx
const app = useApp()
await app.session.signIn()
await app.redirectAfterSignIn(redirect)
```

- 回跳地址只接受同源的相对路径，`//evil.com`、`https://evil.com` 和登录页本身都被忽略，防止开放重定向。
- 没有可用的回跳地址时去 `createApp` 的 `homePath`，没有设置时去菜单中第一个可以进入的页面。

页头用 `useSignedIn()` 判断是否已经登录，登录、退出时随之更新：`useSignedIn() ? <UserMenu … /> : <ButtonLink to="/login">登录</ButtonLink>`。

<a id="permissions"></a>
## 权限 `useCan`

`createApp` 的 `permissions` 读取当前身份的权限，通常原样返回“当前用户”接口中的权限，结果缓存在 QueryClient 中，身份变化后重新读取。`*`、`articles:*` 这类[通配符](../security/authorization.md#route-permissions)与服务端的规则相同。

`staticData` 带 `permission` 的页面在进入之前检查，页面不用自己写：没有登录时跳到登录页；缺少权限时抛出 `ForbiddenError`，`RouteError` 在上级布局之中显示 403；菜单中不显示没有权限的页面。

按权限显示按钮用 `useCan`：

```tsx
const can = useCan()
return can("articles:write") && <Button>写文章</Button>
```

- 权限名用生成的 `Permission` 类型（见[权限](client-react.md#ts-permissions)），写错时类型检查报错。
- 前端的检查只决定显示什么，接口仍由服务端检查权限。
- 改了用户的角色之后，已经打开的页面要刷新才重新读取权限。

<a id="errors"></a>
## 错误页 `RouteError`、`NotFoundPage`

- `RouteError` 是 `router.defaultErrorComponent`：`ForbiddenError` 和 `403` 显示 403，`404` 显示 404 和服务端的说明，断网显示网络错误，其他错误显示可以重试的错误页和请求 ID。它同时把错误报告给 `onError`，见[前端观测](observability.md#route-errors)。
- `NotFoundPage` 是 `router.defaultNotFoundComponent`。
- `ErrorPage` 可以直接使用，`kind` 是 `forbidden`、`not_found`、`server` 或 `network`。

<a id="new-version"></a>
## 新版本提示 `NewVersionNotice`

`tenon()` 插件在构建时把 `version.json` 写入 `dist/client` 并把版本号注入代码。页面每 60 秒和切回页面时读取它，版本号不同时在右下角提示刷新。

- `FrameLayout` 已经包含 `NewVersionNotice`，其他布局需要时自己放一个；自己画提示用 `useNewVersion()`。
- 版本号默认取环境变量 `TENON_BUILD_VERSION`，没有时每次构建随机生成；也可以用 `tenon({ version: commit })` 传入。
- 开发模式下不检查。
- Go 对 `assets/` 之外的文件返回 `Cache-Control: no-cache`（见[托管前端与服务端渲染](hosting.md#wiring)）。生产环境中没有提示时，检查 `dist/client/version.json` 是否存在、是否被代理缓存。

<a id="storage"></a>
## 本地存储 `app.storage`

`app.storage` 是按应用隔离的 `localStorage`，键是 `tenon:<id>:<name>`，值以 JSON 保存。存储不可用（隐私模式、已满）时读到 `undefined`，写入被忽略：

```ts
const app = useApp()
app.storage.set("density", { compact: true })
app.storage.get<{ compact: boolean }>("density")
```

<a id="playground"></a>
## 演示 playground

框架仓库的 `js/playground` 演示布局、登录页和样式预设，在框架仓库的 `js` 目录中运行 `bun run playground`。`/frame/`、`/rail/`、`/`（官网和文档页）、`/login/centered`、`/login/split` 各是一个演示，地址后加 `?scheme=dark` 用暗色打开。
