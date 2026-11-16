# 应用骨架

`@go-tenon/shell` 是[单页应用](spa.md)骨架的逻辑：把 TanStack Router、TanStack Query 和[会话](client-react.md#client-runtime)接到一起，提供登录检查、按权限进入页面、菜单、面包屑、布局约定、多标签、租户切换、表单、列表和新版本提示。它不依赖组件库：同一份逻辑由运营后台的 antd 外观和 shadcn 外观渲染，外观只负责界面。

这一页介绍创建应用、页面的元数据、登录检查、权限、菜单和布局。多标签、租户、表单、列表、通知等页面中的常用功能见[应用骨架：页面中的常用功能](app-shell-features.md)。

阅读前需要了解：[单页应用](spa.md)；[入门教程第 5、6 章](../tutorial/05-pages.md)；TanStack Router 的文件路由（教程第 5 章介绍过）。

- [安装](#installing)
- [创建应用](#creating-the-app)
- [页面的元数据](#route-metadata)
- [登录检查](#requiring-sign-in)
- [权限](#permissions)
- [菜单与面包屑](#menu-and-breadcrumbs)
- [布局约定](#layouts)
- [新版本提示](#new-version)
- [本地存储](#storage)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="installing"></a>
## 安装

`tenon init` 和 `tenon add frontend` 生成的前端已经按本节配置好：`src/app.ts` 创建应用，页面在 `src/routes` 中。下面是各部分的作用，给已有的前端加上它时照着做。

`@go-tenon/shell` 与 `@go-tenon/client` 一样随框架分发，`tenon` 把它复制到项目的 `.tenon/js/shell`（见[框架的前端包](../installation/tooling.md#framework-packages)）。它列在根目录 `package.json` 的 `workspaces` 中，前端依赖它：

```json
{
  "dependencies": {
    "@go-tenon/client": "workspace:*",
    "@go-tenon/shell": "workspace:*",
    "@tanstack/react-query": "^5.85.0",
    "@tanstack/react-router": "^1.170.0"
  },
  "devDependencies": {
    "@tanstack/router-cli": "^1.167.46",
    "@tanstack/router-plugin": "^1.168.48"
  },
  "scripts": {
    "typecheck": "tsr generate && tsc --noEmit"
  }
}
```

页面使用 TanStack Router [基于文件的路由](https://tanstack.com/router/latest/docs/framework/react/routing/file-based-routing)：`src/routes` 中一个文件是一个页面，Vite 插件生成路由树 `src/routeTree.gen.ts`。路由树在 `tenon dev`、`tenon build` 运行 Vite 时自动更新；`typecheck` 先用 `tsr generate` 重新生成它，新加了页面但没有运行 `tenon dev` 时类型检查也是对的。`vite.config.ts` 加上路由插件（放在 React 插件之前）和[新版本提示](#new-version)的插件：

```ts
import { tanstackRouter } from "@tanstack/router-plugin/vite"
import react from "@vitejs/plugin-react"
import { tenonVersion } from "@go-tenon/shell/vite"
import { defineConfig } from "vite"

export default defineConfig({
  base: "/admin/",
  plugins: [tanstackRouter({ target: "react", autoCodeSplitting: true }), react(), tenonVersion()],
})
```

<a id="creating-the-app"></a>
## 创建应用

`createApp` 创建 Router 和 QueryClient，`AppRoot` 渲染应用：

```ts title="src/app.ts"
import { createSession } from "@go-tenon/client"
import { createApp } from "@go-tenon/shell"
import { routeTree } from "./routeTree.gen"
import { api } from "./client"

export const session = createSession({
  refresh: async () => api.auth.refresh(),
  // 刷新被拒绝（登录已失效）时重新运行守卫，离开需要登录的页面
  onSignedOut: () => void app.router.invalidate(),
})
session.connect(api)

export const app = createApp({
  id: "admin",
  routeTree,
  session,
  permissions: async () => (await api.user.me()).permissions,
})

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof app.router
  }
}
```

```tsx title="src/main.tsx"
import { AppRoot } from "@go-tenon/shell"
import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { app } from "./app"

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AppRoot app={app} />
  </StrictMode>,
)
```

- 没有登录功能的应用不传 `session`，应用一直是未登录的。
- 前端挂在子路径（例如 `/admin/`）时，路由的 `basepath` 默认是 Vite 的 `base`：页面中的地址、`<Link>`、登录回跳都不含它。
- `id` 区分同一个域名下的前端，例如 `web` 和 `admin`，见[本地存储](#storage)。
- QueryClient 对 `4xx` 不重试，其他错误重试 2 次；登录、退出、切换账号或租户时清空缓存，并重新挂载页面：页面中按身份计算的查询重新读取。也可以传入自己的 `queryClient`。
- `router` 传给 Router 的其他选项，例如外观提供的 `defaultErrorComponent`、`defaultNotFoundComponent`。

根路由用 `createRootRouteWithContext<RouterContext>()` 创建，各路由的 `beforeLoad` 通过 `context.app` 使用应用：

```tsx title="src/routes/__root.tsx"
import type { RouterContext } from "@go-tenon/shell"
import { createRootRouteWithContext, Outlet } from "@tanstack/react-router"

export const Route = createRootRouteWithContext<RouterContext>()({ component: Outlet })
```

<a id="route-metadata"></a>
## 页面的元数据

页面的标题、图标、顺序和权限写在路由的 `staticData` 中，菜单、面包屑和权限检查都来自这里：

```tsx title="src/routes/_app/articles/index.tsx"
import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/_app/articles/")({
  staticData: { title: "文章", icon: "file-text", order: 10, permission: "articles:read" },
  component: ArticlesPage,
})
```

| 字段 | 说明 |
| --- | --- |
| `title` | 菜单、面包屑中的标题。没有标题的路由（例如布局路由）不出现在菜单和面包屑中 |
| `icon` | 图标的名字，由外观映射为组件库的图标 |
| `order` | 在同级菜单中的顺序，小的在前，默认 0；相同时按路径排序 |
| `permission` | 进入页面需要的权限，见[权限](#permissions) |
| `menu` | `false` 时不出现在菜单中；带路径参数的页面（例如 `/articles/$id`）总是不出现 |

<a id="requiring-sign-in"></a>
## 登录检查

需要登录的页面放在一个布局路由之下，布局的 `beforeLoad` 使用 `requireAuth`：

```tsx title="src/routes/_app.tsx"
import { requireAuth } from "@go-tenon/shell"
import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/_app")({ beforeLoad: requireAuth, component: Layout })
```

- 打开第一个页面之前恢复登录状态（`session.restore()`，用 cookie 换 access token），之后不再重复；公开页面的页头也知道是否已经登录。
- 没有登录时跳到 `loginPath`（默认 `/login`），带上 `?redirect=<原来的地址>`。
- 退出（`session.signOut()`）、切换身份时重新运行检查，离开需要登录的页面。

登录成功后，`redirectAfterSignIn` 回到原来的地址：

```tsx
const { redirect } = Route.useSearch()

async function onSuccess(body: SessionBody) {
  await session.signIn(body)
  await app.redirectAfterSignIn(redirect)
}
```

页头这类位置用 `useSignedIn()` 判断是否已经登录，登录、退出时随之更新：

```tsx
const signedIn = useSignedIn()
return signedIn ? <button onClick={() => session.signOut()}>退出</button> : <Link to="/login">登录</Link>
```

回跳地址只接受同源的相对路径，`//evil.com`、`https://evil.com` 等都被忽略，防止开放重定向；登录页本身也被忽略。没有可用的回跳地址时，去 `homePath`；没有设置 `homePath` 时，去菜单中第一个可以进入的页面。

<a id="permissions"></a>
## 权限

`permissions` 读取当前身份的权限，通常调用应用的“当前用户”接口。结果缓存在 QueryClient 中，身份变化后重新读取。

带 `permission` 的页面在进入之前检查：

- 没有登录时，与 `requireAuth` 一样跳到登录页；
- 缺少权限时抛出 `ForbiddenError`，页面不加载。错误由这个路由的 `errorComponent`（或 Router 的 `defaultErrorComponent`）在上级布局之中显示，菜单和顶栏仍在。外观用 `isForbidden(error)` 判断并显示 403。

页面不用自己写检查。按权限显示按钮用 `useCan`：

```tsx
const can = useCan()
return can("articles:write") && <button>写文章</button>
```

权限名的类型是生成的 `Permission`（见[权限](client-react.md#ts-permissions)），写错的权限名在类型检查时报错。前端的检查只决定显示什么，接口仍由服务端检查权限。

<a id="menu-and-breadcrumbs"></a>
## 菜单与面包屑

`useMenu` 由路由的元数据生成菜单，`useBreadcrumbs` 生成当前位置的面包屑，外观把它们渲染为侧栏和顶栏：

```tsx
const menu = useMenu() // [{ id, title, icon, path, children? }]
const crumbs = useBreadcrumbs() // [{ title, path }]
```

- 带 `title` 的路由按路由树嵌套，跳过没有标题的布局路由；按 `order`、再按路径排序。
- 没有权限、`menu: false`、带路径参数的页面不出现；其中没有页面的分组也不出现。
- `firstPage(menu)` 是第一个可以进入的页面；`pageUnder(menu, path)` 是某个分组下第一个可以进入的页面，例如点击面包屑中的分组时跳转到它。

<a id="layouts"></a>
## 布局约定

一个布局读取导航模型和布局状态，把插槽放到自己的位置上；页面只往插槽中放内容，不关心当前是哪一种布局。侧栏、顶部导航、图标栏等具体的布局由运营后台的 antd 外观和 shadcn 外观按这个约定实现，项目也可以自己写，只要遵守这个约定，页面、多标签和权限过滤都照常工作。

**区域**用布局路由选择：一组页面共用一个布局，不同的组用不同的布局，例如 `_app` 用侧栏，`_settings` 用设置布局，`_auth` 用登录布局。

<a id="navigation"></a>
### 导航模型

```tsx
const { menu, active, modules, module, secondary, crumbs } = useNavigation()
```

| 字段 | 内容 |
| --- | --- |
| `menu` | 按权限过滤后的菜单树，见[菜单与面包屑](#menu-and-breadcrumbs) |
| `active` | 当前位置在菜单中的路径：从第一级到最深一级的菜单项 id，用于高亮当前项、展开所在的分组。详情页这类不在菜单中的页面，高亮它所在的分组 |
| `modules`、`module` | 第一级菜单和当前所在的那一项，“顶栏 + 侧栏”、“图标栏 + 二级面板”用它们 |
| `secondary` | 当前第一级下的菜单，用于二级导航、二级面板 |
| `crumbs` | 面包屑 |

菜单项的徽标（例如未读通知数）是运行时才知道的，由页面或功能模块登记，菜单项上带上 `badge`：

```tsx
useMenuBadge("/_app/notifications", unread) // 组件卸载、值为 0 或空时去掉
```

<a id="slots"></a>
### 插槽

布局用 `<Slot>` 放置插槽，页面用 `<Fill>` 往插槽放内容，离开页面时自动移除。同一个插槽可以有多份内容，按放入的顺序排列：

```tsx
// 布局
<header>
  <Slot name="brand" />
  <Slot name="actions" />
  <Slot name="user" />
</header>
{useSlotFilled("aside") && <aside><Slot name="aside" /></aside>}

// 页面
<Fill name="actions"><Button>写文章</Button></Fill>
```

| 插槽 | 内容 |
| --- | --- |
| `brand`、`user`、`tenant`、`search`、`footer` | 品牌、用户菜单、租户切换、搜索与命令面板入口、页脚，通常由应用在布局路由中放一次 |
| `actions`、`aside` | 顶栏操作、侧边面板，随页面变化 |
| `nav`、`subnav` | 主导航、二级导航，通常由布局按 `useNavigation` 自己渲染 |
| `tabs` | 标签栏 |

内容用 portal 放进插槽，仍在页面自己的组件树中：读取页面的状态和上下文，随页面一起更新。`useSlotFilled(name)` 报告插槽中是否有内容，例如没有侧边面板时不留出位置。

<a id="layout-state"></a>
### 布局状态

```tsx
const [state, setState] = useLayoutState()
// state.collapsed、state.secondaryWidth、state.aside、state.maximized、state.density、state.navOpen
setState({ collapsed: !state.collapsed })
```

布局状态保存在[本地存储](#storage)中，按账号区分：换个账号登录各是各的。`navOpen`（窄屏时导航抽屉打开）只在当前页面有效，不保存。`useNarrow()` 报告是否是窄屏（默认宽度小于 768px），布局据此把主导航收进抽屉或底部标签栏。

底部标签栏的内容由 `useBottomNav(max = 5)` 给出：第一级菜单，最多 `max` 项，分组进入它下面第一个可以进入的页面，每一项带着是否是当前位置（`active`）和徽标。它只适合第一级菜单不多的前端，例如用户端；shadcn 的外观见 [BottomNav](registry.md#blocks)。

项目允许用户切换布局时（例如在侧栏和顶部导航之间），布局路由用 `useLayoutVariant` 决定当前用哪一种，第一种是默认的；用户选过、并且在允许的范围内时用用户的选择：

```tsx
const [variant, setVariant] = useLayoutVariant(["side", "top"])
return variant === "top" ? <TopLayout /> : <SideLayout />
```

<a id="new-version"></a>
## 新版本提示

发布新版本后，已经打开的页面还在运行旧代码。`tenonVersion` 插件在构建时把 `version.json` 写入 `dist`，并把版本号注入代码；`useNewVersion` 每 60 秒和切回页面时读取 `version.json`，版本号不同时提示用户刷新：

```tsx
const { available, reload, dismiss } = useNewVersion()
// available 为 true 时显示提示：“刷新”调用 reload，“稍后”调用 dismiss
```

- 版本号默认每次构建随机生成，可以用 `tenonVersion({ version: commit })` 传入。
- 开发模式下不检查。
- `httpx.SPA` 对文件名没有哈希的文件返回 `Cache-Control: no-cache`，`version.json` 不会被缓存，见[缓存与前端路由](spa.md#caching-and-routing)。

<a id="storage"></a>
## 本地存储

`app.storage` 是按应用隔离的 `localStorage`，键是 `tenon:<id>:<name>`，值以 JSON 保存，用于侧栏折叠、主题等偏好。存储不可用（隐私模式、已满）时读到 `undefined`，写入被忽略：

```ts
app.storage.set("sider", { collapsed: true })
app.storage.get<{ collapsed: boolean }>("sider")
```

<a id="troubleshooting"></a>
## 常见问题

**新加的页面没有出现在菜单中**

检查路由的 `staticData` 中有没有 `title`：没有标题的路由不出现在菜单中。带路径参数的页面（例如 `/articles/$id`）、`menu: false` 的页面也不出现。另外，当前用户缺少 `permission` 中的权限时，菜单中也看不到它。

**登录后又跳回了登录页**

恢复登录状态时用 cookie 换 access token 失败了。检查刷新接口是否正常，以及是否通过同一个端口访问页面和接口（cookie 只在同源请求中发送）。

**进入页面时显示 403，但用户应该有权限**

`permissions` 读到的权限中没有这一项。看“当前用户”接口返回的 `permissions`；修改了用户的角色后，前端要重新读取权限，刷新页面即可。

**发布了新版本，但页面上一直没有新版本提示**

开发模式下不检查新版本。生产环境中检查 `dist/version.json` 是否存在，以及它是否被缓存了，见[新版本提示](#new-version)。

<a id="api-reference"></a>
## API 参考

| 名称 | 说明 |
| --- | --- |
| `createApp(options)` | 创建应用：`id`、`routeTree`、`session`、`loginPath`、`homePath`、`permissions`、`queryClient`、`router` |
| `AppRoot` | 渲染应用：QueryClientProvider 和 RouterProvider |
| `requireAuth` | 布局路由的 `beforeLoad`：没有登录时跳到登录页 |
| `app.redirectAfterSignIn(redirect)` | 登录后回到原来的地址 |
| `app.permissions()`、`app.permissionsQuery()` | 当前身份的权限；交给 `useQuery` 的查询选项 |
| `ForbiddenError`、`isForbidden` | 缺少权限时抛出的错误 |
| `useApp`、`useSignedIn`、`useCan`、`useMenu`、`useBreadcrumbs` | 在 `AppRoot` 之内使用 |
| `buildMenu`、`firstPage`、`pageUnder`、`breadcrumbs` | 菜单和面包屑的纯函数，外观可以直接使用 |
| `useNavigation`、`useMenuBadge` | 布局的导航模型；登记菜单项的徽标 |
| `Slot`、`Fill`、`useSlotFilled` | 插槽 |
| `useLayoutState`、`useLayoutVariant`、`useNarrow` | 布局状态、用户选择的布局、窄屏 |
| `useBottomNav`、`bottomNav` | 窄屏时底部标签栏的项 |
| `useResizable(options)`、`clampSize` | 可以拖动调整宽度的分栏：宽度按账号保存，分隔条的属性（拖动、方向键、双击恢复） |
| `TabsOutlet`、`useTabs`、`useTab`、`useTabActive`、`useTabTitle`、`useTabShortcuts` | 多标签 |
| `confirmDiscard(app)` | 有未保存的修改时询问是否放弃，例如退出登录前 |
| `useTenant`、`switchTenant`、`enterTenant` | 当前租户、切换租户 |
| `resolver`、`antdRule`、`formValidator` | 用生成的规则校验表单：react-hook-form、antd Form、其他表单库 |
| `setServerErrors`、`serverErrors` | 把服务端的错误设置到表单的字段 |
| `useUnsavedChanges(dirty, options)` | 有未保存的修改时离开前询问 |
| `createAutoSaver(initial, options)` | 自动保存，与表单库无关，见[表单](app-shell-features.md#forms) |
| `listSearch(querySchema, options)`、`useList(Route)` | 列表的查询条件、排序和分页保存在地址中 |
| `useSelection(resetOn)` | 列表的选中项 |
| `safeRedirect(redirect, fallback)` | 只接受同源相对路径的回跳地址 |
| `resourceModel`、`useResourceList`、`useResourceItem`、`useResourceMutations`、`formValues` | [资源](resources.md#model)的模型 |
| `useNotifications` | 通知：未读数、列表、标记已读 |
| `useActivity` | 操作日志：从新到旧分页读取 |
| `useImport`、`useExport` | 后台导入与导出：上传、发起、进度和结果 |
| `settingsModel`、`useSettings` | 设置页面的模型，见[设置页面](resources.md#settings) |
| `trackOperation`、`dismissOperation`、`clearOperations`、`useOperations`、`useOperation` | 后台操作的进度 |
| `useAssistantChat`、`useAssistantHistory`、`pendingApprovals` | `@go-tenon/shell/chat` 中的 AI 对话 |
| `useNewVersion`、`watchVersion` | 新版本提示 |
| `onError`（`createApp` 的选项）、`useReportError`、`shouldReport` | 页面出错时报告给前端观测的 SDK，见[前端观测](../general/frontend-observability.md#route-errors) |
| `tenonVersion(options)` | `@go-tenon/shell/vite` 中的构建插件；`sourcemaps` 生成 source map 并移出 `dist`，见[前端观测](../general/frontend-observability.md#source-maps) |
| `appStorage(id)` | 按应用隔离的本地存储 |
