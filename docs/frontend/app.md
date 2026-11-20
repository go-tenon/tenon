# 搭建前端应用

一个前端是项目中的 `apps/<名称>` 目录：用 [TanStack Start](start.md) 构建，由 Go 托管（见[托管前端与服务端渲染](hosting.md)），界面和应用骨架来自框架的 `@go-tenon/ui`。`@go-tenon/ui` 基于 [Mantine](https://mantine.dev)，把 TanStack Router、TanStack Query 和[会话](client-react.md#client-runtime)接到一起，提供登录检查、按权限进入页面、菜单、布局、表格、表单和资源页面。

这一页介绍一个前端怎样装配起来：创建应用、主题、页面的元数据、登录、权限、错误页和新版本提示。页面放在什么布局中见[布局与导航](layout.md)，页面里的列表和表单见[列表与表单](lists-forms.md)。

阅读前需要了解：[TanStack Start 的接入](start.md)；[入门教程第 5、6 章](../tutorial/05-pages.md)；TanStack Router 的文件路由（教程第 5 章介绍过）。

- [装配](#setup)
    - [依赖与构建](#installing)
    - [创建应用](#creating-the-app)
    - [根路由](#root)
- [样式预设与主题](#presets)
- [页面的元数据](#route-metadata)
- [登录](#requiring-sign-in)
- [权限](#permissions)
- [错误页](#errors)
- [新版本提示](#new-version)
- [本地存储](#storage)
- [演示](#playground)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="setup"></a>
## 装配

`tenon init` 和 `tenon add frontend --layout <布局>` 生成的前端已经按本节装配好。下面是各部分的作用，给已有的前端加上它时照着做。

<a id="installing"></a>
### 依赖与构建

`@go-tenon/ui`、`@go-tenon/start` 与 `@go-tenon/client` 一样随框架分发，`tenon` 把它们复制到项目的 `.tenon/js`（见[框架的前端包](../installation/tooling.md#framework-packages)）。它们列在根目录 `package.json` 的 `workspaces` 中，前端依赖它们：

```json
{
  "dependencies": {
    "@go-tenon/client": "workspace:*",
    "@go-tenon/start": "workspace:*",
    "@go-tenon/ui": "workspace:*",
    "@mantine/core": "^9.8.1",
    "@tanstack/react-query": "^5.85.0",
    "@tanstack/react-router": "^1.170.0",
    "@tanstack/react-start": "^1.169.5"
  },
  "scripts": {
    "typecheck": "tenon-start routes && tsc --noEmit"
  }
}
```

页面使用 TanStack Router [基于文件的路由](https://tanstack.com/router/latest/docs/framework/react/routing/file-based-routing)：`src/routes` 中一个文件是一个页面，TanStack Start 的 Vite 插件生成路由树 `src/routeTree.gen.ts`。`typecheck` 先用 `tenon-start routes` 重新生成它，新加了页面但没有运行 `tenon dev` 时类型检查也是对的；它用的是同一个 Vite 插件，生成的文件与 `tenon dev` 生成的相同，见 [TanStack Start 的接入](start.md#api-reference)。`vite.config.ts` 用 `@go-tenon/start/vite` 的 `tenon()`，它包含 TanStack Start、React 和[新版本提示](#new-version)的插件：

```ts
import { tenon } from "@go-tenon/start/vite"
import { defineConfig } from "vite"

export default defineConfig({
  base: "/admin/",
  plugins: [tenon()],
})
```

<a id="creating-the-app"></a>
### 创建应用

`createApp` 创建 Router 和 QueryClient。TanStack Start 从 `src/router.tsx` 的 `getRouter` 取得 Router；服务端渲染时每个请求调用一次 `getRouter`，所以每次都创建新的应用，不在请求之间共享缓存：

```tsx title="src/router.tsx"
import { cspNonce } from "@go-tenon/start"
import { createApp, NotFoundPage, RouteError } from "@go-tenon/ui"
import { me, session } from "./auth"
import { routeTree } from "./routeTree.gen"

export function getRouter() {
  return createApp({
    id: "admin",
    routeTree,
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

会话在 `src/auth.ts` 中创建，见[客户端代码](../http/clients.md#creating-the-client)：

```ts title="src/auth.ts"
import { createSession } from "@go-tenon/client"

// 读取当前用户；没有登录时接口返回 401
export const session = createSession({ load: async () => ({ account: (await me()).id }) })
```

- 没有登录功能的应用不传 `session`，应用一直是未登录的。
- 会话变化（登录、退出、接口返回 `401`）时，应用重新运行路由的守卫：正在看的页面需要登录时，跳到登录页。
- 读取当前用户、检查权限只在浏览器中进行。后台这类要登录的前端整个只在浏览器中渲染（`package.json` 中 `"ssr": false`）；服务端渲染的前端中，需要登录的页面设置 `ssr: false`，见 [TanStack Start 的接入](start.md)。
- 前端挂在子路径（例如 `/admin/`）时，路由的 `basepath` 默认是 Vite 的 `base`：页面中的地址、`<Link>`、登录回跳都不含它。
- `id` 区分同一个域名下的前端，例如 `web` 和 `admin`，见[本地存储](#storage)。
- QueryClient 对 `4xx` 不重试，其他错误重试 2 次；登录、退出、切换账号或租户时清空缓存，并重新挂载页面：页面中按身份计算的查询重新读取。重新挂载的只是布局中的页面（`FrameLayout` 用 `SessionBoundary` 包住页面），HTML 文档和布局保持不变。也可以传入自己的 `queryClient`。
- `router` 传给 Router 的其他选项：CSP 的 `nonce`、默认的错误页 `RouteError` 和 404 页 `NotFoundPage`（见[错误页](#errors)）。`router.Wrap` 包住整个应用；`createApp` 在它里面提供 QueryClient 和应用。
- 多租户应用还要设置 `tenancy`，见[租户](layout.md#tenants)。

<a id="root"></a>
### 根路由

根路由用 `createRootRouteWithContext<RouterContext>()` 创建，各路由的 `beforeLoad` 通过 `context.app` 使用应用。`shellComponent` 是整个 HTML 文档，`UIProvider` 放在其中，同时引入样式：

```tsx title="src/routes/__root.tsx"
import "@go-tenon/ui/styles.css"
import type { ReactNode } from "react"
import { ColorSchemeScript, mantineHtmlProps } from "@mantine/core"
import { createRootRouteWithContext, HeadContent, Outlet, Scripts, useRouter } from "@tanstack/react-router"
import { type RouterContext, UIProvider } from "@go-tenon/ui"
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
- `UIProvider` 安装 Mantine 的主题、右上角的通知（`@mantine/notifications`）、确认对话框（`@mantine/modals`）和图标名称的对应表。组件测试中传 `env="test"`，关闭过渡动画和 portal。配色默认跟随系统，用户在用户菜单中切换后保存在浏览器中。

<a id="presets"></a>
## 样式预设与主题

主题在 `src/theme.ts` 中，选一套样式预设，改品牌色：

```ts title="src/theme.ts"
import { createUiTheme } from "@go-tenon/ui"

export const theme = createUiTheme({ preset: "compact", primary: "blue-gray" })
```

三套预设共用一套设计语言：系统字体、偏冷的中性色、1 像素的细边框、几乎不用阴影，层级靠留白、字重和背景的明暗区分。亮色和暗色都完整定义，组件中不写死颜色。

预设只管密度，按样子命名，与布局无关：`compact` 也可以配 `RailLayout`，`comfortable` 也可以配 `FrameLayout`。

| 预设 | 样子 | 适合 |
| --- | --- | --- |
| `compact` | 紧凑：正文 14px，控件 32px 高，圆角 4px，一屏能看到更多数据 | 表格和表单多的界面，例如运营后台、商家后台、内部系统 |
| `comfortable` | 舒展：正文 14px，控件 36px 高，圆角 8px，留白舒展 | 页面不密集的应用，例如 SaaS 控制台、聊天工具、用户中心 |
| `spacious` | 宽松：正文 16px，大标题随屏幕宽度缩放，行距宽松 | 官网、落地页、文章、文档 |

一个前端只用一套预设。一个项目中的几个前端可以各用自己的预设，品牌色相同。控件的默认尺寸和内容的内边距放在主题的 `other.tenon` 中，组件据此排版，用 `layoutMetrics(theme)` 读取；顶栏高度、侧栏宽度这些是各个布局自己的，不随预设变化。

<a id="tokens"></a>
`createUiTheme` 的选项是设计令牌：

- `preset` 是样式预设，默认 `comfortable`；`themes.compact`、`themes.comfortable`、`themes.spacious` 是不改令牌的三套主题。
- `primary` 是品牌色，默认是第一种预设 `blue-gray`。可以写颜色值，也可以写 Mantine [颜色生成器](https://mantine.dev/colors-generator)中预设的名称，名称是官方英文名的小写连字符写法：

  `blue-gray`、`brown`、`tomato`、`deep-orange`、`bright-orange`、`yellow`、`bright-green`、`green`、`light-blue`、`sky-blue`、`pale-blue`、`bright-blue`、`deep-blue`、`pale-indigo`、`purple`、`pale-purple`、`violet`、`pale-violet`、`dark-pink`、`bright-pink`、`pink`、`magenta`、`bright-red`、`pale-red`、`deep-red`、`red`

  色板由 Mantine 官方的生成器（`@mantine/colors-generator`）生成，与颜色生成器页面上的结果一致：输入的颜色按亮度落在 10 级中的一级，亮色模式的主色是第 6 级，暗色模式是第 5 级，所以很深的颜色（例如 `brown`、`deep-red`）在按钮上会比输入的颜色浅一些。名称对应的颜色值在 `primaryColors` 中，也可以单独调用 `palette(color)`。
- `radius` 是基础圆角（像素），其他圆角按比例缩放。
- `fontFamily` 是字体，不设置时用系统字体，不额外加载字体：西文字体在前，字母、数字和 `•` 这类西文符号用西文字体；汉字回落到后面的苹方、思源黑体、微软雅黑。引号 `‘’“”`、省略号 `…`、破折号 `—`、间隔号 `·` 是中英文共用的字符，中文页面（`<html lang="zh-CN">`）中它们由中文字体显示为全角，英文页面中是西文的样式；自己设置 `fontFamily` 时同样如此。
- `datetimeFormat` 是日期时间的显示格式（dayjs 的写法），默认 `YYYY-MM-DD HH:mm`；表格多的界面可以用短一些的 `MM/DD HH:mm`。表格、操作记录、通知中的时间都用它。
- `override` 是再合并的 Mantine 主题覆盖，用于令牌之外的调整，例如某个组件的默认属性。
- `datetimeFormat` 是日期时间的显示格式（[dayjs 的写法](https://day.js.org/docs/en/display/format)），默认随预设：`admin` 是 `MM/DD HH:mm`（例如 11/03 10:12），其他是 `YYYY-MM-DD HH:mm`。后端返回带时区的 ISO 时间，默认显示为浏览器所在时区的时间，可以用 `TimeZoneProvider` 换成别的时区（见下）。表格中的日期时间字段、操作记录、通知都用它；自己写的页面用 `useFormatDateTime()`：

```tsx
const formatDateTime = useFormatDateTime()
formatDateTime(order.created_at) // 11/03 10:12
```

<a id="time-zones"></a>
**时区。** 用户在偏好设置中选了时区时，用 `TimeZoneProvider` 把它交给页面。`zone` 是 IANA 时区名，例如 `Asia/Kolkata`，为空时按浏览器的时区。时区通常在登录后才读到，并且会变，所以它不是主题的选项，而是包在布局外面：

```tsx
function Layout() {
  const user = useQuery({ queryKey: ["me"], queryFn: me })
  return (
    <TimeZoneProvider zone={user.data?.time_zone}>
      <RailLayout title="Acme" user={<UserMenu name={user.data?.name} logout={logout} />} />
    </TimeZoneProvider>
  )
}
```

- `useFormatDateTime` 按这个时区显示，表格、操作记录、通知、队列和定时任务面板中的时间随之变化。
- 资源表单中的日期时间输入框按这个时区显示和输入，提交时换算成 ISO 时间。只有日期的字段（`format: "date"`）是日历上的一天，不换算。
- 自己写的页面需要别的格式时，用 `inTimeZone(value, useTimeZone()).format("HH:mm")`。

<a id="route-metadata"></a>
## 页面的元数据

页面的标题、图标、顺序和权限写在路由的 `staticData` 中，菜单、面包屑、标签的标题和权限检查都来自这里：

```tsx title="src/routes/_app/articles/index.tsx"
import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/_app/articles/")({
  staticData: { title: "文章", icon: "article", order: 10, permission: "articles:read" },
  component: ArticlesPage,
})
```

| 字段 | 说明 |
| --- | --- |
| `title` | 菜单、面包屑和标签中的标题。没有标题的路由（例如布局路由）不出现在菜单和面包屑中 |
| `icon` | 图标的名字，见[侧栏菜单与图标](layout.md#menu) |
| `order` | 在同级菜单中的顺序，小的在前，默认 0；相同时按路径排序 |
| `permission` | 进入页面需要的权限，见[权限](#permissions) |
| `menu` | `false` 时不出现在菜单中；自己引入路径参数的页面（例如详情页 `/articles/$id`）总是不出现，参数来自上级路由的见[租户](layout.md#tenants) |
| `tab` | `false` 时不进入[多标签](layout.md#tabs)，照常显示 |

<a id="requiring-sign-in"></a>
## 登录

需要登录的页面放在一个布局路由之下，布局的 `beforeLoad` 使用 `requireAuth`：

```tsx title="src/routes/_app.tsx"
import { requireAuth } from "@go-tenon/ui"
import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/_app")({ beforeLoad: requireAuth, component: Layout })
```

- 打开第一个页面之前恢复登录状态（`session.restore()`，读一次当前用户），之后不再重复；公开页面的页头也知道是否已经登录。
- 没有登录时跳到 `loginPath`（默认 `/login`），带上 `?redirect=<原来的地址>`。
- 退出（`session.signOut()`）、切换身份时重新运行检查，离开需要登录的页面。

<a id="auth"></a>
**登录页。** 登录、注册、找回密码等页面有自己的样式，现在有两种：`CenteredAuthLayout` 在页面中央放一张卡片；`SplitAuthLayout` 左右分栏，见下文。登录页不在布局之下，用哪种样式与应用用哪种布局无关，可以任意搭配；生成前端时用 `tenon add frontend --login centered|split` 选择。表单和样式分开，`LoginForm` 是邮箱和密码的登录表单，可以放进任何一种样式：

```tsx title="src/routes/login.tsx"
import { createFileRoute } from "@tanstack/react-router"
import { AnchorLink, CenteredAuthLayout, LoginForm } from "@go-tenon/ui"
import { api } from "../auth"

export const Route = createFileRoute("/login")({
  validateSearch: (s: Record<string, unknown>) => ({ redirect: typeof s.redirect === "string" ? s.redirect : undefined }),
  component: Login,
})

function Login() {
  const { redirect } = Route.useSearch()
  return (
    <CenteredAuthLayout title="登录" description="使用工作邮箱登录。">
      <LoginForm
        login={(values) => api.auth.login(values)}
        redirect={redirect}
        forgot={<AnchorLink to="/forgot">忘记密码？</AnchorLink>}
      />
    </CenteredAuthLayout>
  )
}
```

- `login` 调用应用的登录接口，服务端把登录凭据写进会话 cookie。成功后表单调用 `app.session.signIn()`，由会话的 `load` 读取当前用户，再回到登录前想去的页面。
- 接口返回的字段错误显示在对应的输入框下面，其他错误（例如密码不对、尝试次数过多）显示在表单上方。断网等没有服务端文案的错误，按种类显示提示，见 `errorMessage`。

换成左右分栏时，把 `CenteredAuthLayout` 换成 `SplitAuthLayout`，表单不变。左栏是品牌色的展示区：`brand` 是顶部的标志，`aside` 是中间的内容，例如一句口号和几条产品介绍，`asideFooter` 是底部的一行小字。左栏的文字是白色，放在里面的 `Title`、`Text` 不用另设颜色。窄屏时左栏隐藏，`brand` 移到表单上方：

```tsx
<SplitAuthLayout
  brand="Acme"
  aside={<Title order={1}>把团队的工作放在一处</Title>}
  asideFooter="© 2026 Acme"
  title="登录"
  description="欢迎回来。"
>
  <LoginForm login={(values) => api.auth.login(values)} redirect={redirect} />
</SplitAuthLayout>
```

自己写登录表单时，登录成功后用 `redirectAfterSignIn` 回到原来的地址：

```tsx
async function onSuccess(body: SessionBody) {
  await session.signIn(body)
  await app.redirectAfterSignIn(redirect)
}
```

回跳地址只接受同源的相对路径，`//evil.com`、`https://evil.com` 等都被忽略，防止开放重定向；登录页本身也被忽略。没有可用的回跳地址时，去 `homePath`；没有设置 `homePath` 时，去菜单中第一个可以进入的页面。

页头这类位置用 `useSignedIn()` 判断是否已经登录，登录、退出时随之更新：

```tsx
const signedIn = useSignedIn()
return signedIn ? <button onClick={() => session.signOut()}>退出</button> : <Link to="/login">登录</Link>
```

后台的用户菜单 `UserMenu` 中已经有“退出登录”，见[顶栏菜单](layout.md#menus)。

<a id="permissions"></a>
## 权限

`createApp` 的 `permissions` 读取当前身份的权限，通常调用应用的“当前用户”接口，原样返回账号的权限。结果缓存在 QueryClient 中，身份变化后重新读取。权限中可以有 `*`（全部权限）和 `articles:*`（`articles:` 开头的每个权限）这样的[通配符](../security/authorization.md#route-permissions)，页面、菜单和 `useCan` 都按与服务端相同的规则匹配，例如拥有 `*` 的管理员能进入每个页面。

带 `permission` 的页面在进入之前检查：

- 没有登录时，与 `requireAuth` 一样跳到登录页；
- 缺少权限时抛出 `ForbiddenError`，页面不加载。错误由这个路由的 `errorComponent`（或 Router 的 `defaultErrorComponent`）在上级布局之中显示，菜单和顶栏仍在，`RouteError` 显示为 403。
- 菜单中不显示没有权限的页面。

页面不用自己写检查。按权限显示按钮用 `useCan`：

```tsx
const can = useCan()
return can("articles:write") && <Button>写文章</Button>
```

权限名的类型是生成的 `Permission`（见[权限](client-react.md#ts-permissions)），写错的权限名在类型检查时报错。前端的检查只决定显示什么，接口仍由服务端检查权限。

<a id="errors"></a>
## 错误页

- `RouteError` 是路由的默认错误组件：没有权限（`ForbiddenError`、`403`）显示 403，不存在（`404`）显示 404 和服务端的说明，断网显示网络错误，其他错误显示可以重试的错误页和请求 ID。它同时把错误报告给 `createApp` 的 `onError`，见[前端观测](observability.md#route-errors)。
- `NotFoundPage` 是没有匹配到路由时的 404 页。
- `ErrorPage` 可以直接使用，`kind` 是 `forbidden`、`not_found`、`server` 或 `network`，标题、说明和操作都可以替换。

<a id="new-version"></a>
## 新版本提示

发布新版本后，已经打开的页面还在运行旧代码。`@go-tenon/start/vite` 的 `tenon()` 插件在构建时把 `version.json` 写入浏览器的构建结果 `dist/client`，并把版本号注入代码；页面每 60 秒和切回页面时读取 `version.json`，版本号不同时在右下角提示“刷新”或“稍后”。

- `FrameLayout` 已经包含提示 `NewVersionNotice`，其他布局需要时自己放一个；自己画提示时用 `useNewVersion()`，它返回 `{ available, reload, dismiss }`。
- 版本号默认取环境变量 `TENON_BUILD_VERSION`，没有时每次构建随机生成一个；可以用 `tenon({ version: commit })` 传入。一次构建中 TanStack Start 会多次加载 `vite.config.ts`，默认的版本号在构建进程中只生成一次，页面中的与 `version.json` 中的相同。
- 开发模式下不检查。
- Go 对 `assets/` 之外的文件返回 `Cache-Control: no-cache`，`version.json` 不会被缓存，见[托管前端与服务端渲染](hosting.md#wiring)。

<a id="storage"></a>
## 本地存储

`app.storage` 是按应用隔离的 `localStorage`，键是 `tenon:<id>:<name>`，值以 JSON 保存。侧栏是否收起、打开的标签都按账号保存在这里。存储不可用（隐私模式、已满）时读到 `undefined`，写入被忽略：

```ts
const app = useApp()
app.storage.set("density", { compact: true })
app.storage.get<{ compact: boolean }>("density")
```

<a id="playground"></a>
## 演示

框架的 `js/playground` 是布局、登录页和样式预设的演示：

| 地址 | 演示 | 预设 |
| --- | --- | --- |
| `/frame/` | `FrameLayout` | `compact` |
| `/rail/` | `RailLayout` | `comfortable` |
| `/`、`/docs/getting-started` | `SiteLayout`、`DocsLayout` | `spacious` |
| `/login/` | 登录页的全部样式，每种一个地址：`/login/centered` 是 `CenteredAuthLayout`，`/login/split` 是 `SplitAuthLayout` | `comfortable` |

在框架仓库的 `js` 目录中运行：

```bash
bun run playground
```

地址后加 `?scheme=dark` 用暗色打开。

<a id="troubleshooting"></a>
## 常见问题

**新加的页面没有出现在菜单中**

检查路由的 `staticData` 中有没有 `title`：没有标题的路由不出现在菜单中。自己引入路径参数的页面（例如 `/articles/$id`）、`menu: false` 的页面也不出现；路径参数来自上级路由（例如 `/t/$tenant`）时，当前地址中要有这个参数。另外，当前用户缺少 `permission` 中的权限时，菜单中也看不到它。

**登录后又跳回了登录页**

恢复登录状态时读不到当前用户。检查会话的 `load` 调用的接口是否正常，以及是否通过同一个源访问页面和接口：会话 cookie 只在同源请求中发送，`Secure` 的 cookie 只在 HTTPS 和 `localhost` 上保存。

**进入页面时显示 403，但用户应该有权限**

`permissions` 读到的权限中没有这一项。看“当前用户”接口返回的 `permissions`；修改了用户的角色后，前端要重新读取权限，刷新页面即可。

**发布了新版本，但页面上一直没有新版本提示**

开发模式下不检查新版本。生产环境中检查 `dist/client/version.json` 是否存在，以及它是否被缓存了，见[新版本提示](#new-version)。

<a id="api-reference"></a>
## API 参考

包 `@go-tenon/ui`，样式 `@go-tenon/ui/styles.css`。

| 名称 | 说明 |
| --- | --- |
| `createApp(options)` | 创建应用：`id`、`routeTree`、`session`、`loginPath`、`homePath`、`permissions`、`tenancy`、`queryClient`、`router`、`onError` |
| `RouterContext` | 根路由的上下文类型：`createRootRouteWithContext<RouterContext>()` |
| `AppRoot` | 不用 TanStack Start 时渲染应用（RouterProvider）；用 Start 时由它渲染 `getRouter` 返回的 Router |
| `UIProvider` | 安装主题、通知、对话框和图标：`theme`、`defaultColorScheme`、`icons`、`env` |
| `themes`、`Preset` | 三套样式预设：`admin`、`console`、`site` |
| `createUiTheme(options)` | 由预设和设计令牌生成主题：`preset`、`primary`、`radius`、`fontFamily`、`datetimeFormat`、`override` |
| `useFormatDateTime()` | 按主题的 `datetimeFormat` 和 `TimeZoneProvider` 的时区显示日期时间 |
| `TimeZoneProvider`、`useTimeZone()`、`inTimeZone(value, zone)` | 页面中显示和输入时间使用的时区，见[时区](#time-zones) |
| `palette(color)`、`primaryColors`、`PrimaryColor` | 由预设的名称或颜色值生成 10 级色板；预设的主题色 |
| `layoutMetrics(theme)`、`LayoutMetrics` | 主题中的布局尺寸 |
| `requireAuth` | 布局路由的 `beforeLoad`：没有登录时跳到登录页 |
| `app.redirectAfterSignIn(redirect)`、`safeRedirect(redirect, fallback)` | 登录后回到原来的地址；只接受同源相对路径的回跳地址 |
| `CenteredAuthLayout`、`SplitAuthLayout`、`LoginForm`、`errorMessage` | 登录页的两种样式（中央卡片、左右分栏）、登录表单、错误的提示文字 |
| `app.permissions()`、`app.permissionsQuery()` | 当前身份的权限；交给 `useQuery` 的查询选项 |
| `useApp`、`useSignedIn`、`useCan` | 在应用的路由之内使用 |
| `ForbiddenError`、`isForbidden` | 缺少权限时抛出的错误 |
| `RouteError`、`NotFoundPage`、`ErrorPage` | 错误页 |
| `useReportError`、`shouldReport` | 页面出错时报告给前端观测的 SDK，见[前端观测](observability.md#route-errors) |
| `NewVersionNotice`、`useNewVersion`、`watchVersion` | 新版本提示 |
| `tenon({ prerender, version, sourcemaps })` | `@go-tenon/start/vite` 的 Vite 插件：TanStack Start、React 和构建版本；`sourcemaps` 见[前端观测](observability.md#source-maps) |
| `appStorage(id)` | 按应用隔离的本地存储 |
