# 应用骨架

`@go-tenon/shell` 是[单页应用](spa.md)骨架的逻辑：把 TanStack Router、TanStack Query 和[会话](clients.md#client-runtime)接到一起，提供登录检查、按权限进入页面、菜单、面包屑、表单、列表和新版本提示。它不依赖组件库：同一份逻辑由运营后台的 antd 外观和 shadcn 外观渲染，外观只负责界面。

- [安装](#installing)
- [创建应用](#creating-the-app)
- [页面的元数据](#route-metadata)
- [登录检查](#requiring-sign-in)
- [权限](#permissions)
- [菜单与面包屑](#menu-and-breadcrumbs)
- [表单](#forms)
- [列表](#lists)
- [新版本提示](#new-version)
- [本地存储](#storage)
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
- `id` 区分同一个域名下的前端，例如 `web` 和 `admin`，见[本地存储](#storage)。
- QueryClient 对 `4xx` 不重试，其他错误重试 2 次；登录、退出、切换账号或租户时清空缓存。也可以传入自己的 `queryClient`。
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

权限名的类型是生成的 `Permission`（见[权限](clients.md#ts-permissions)），写错的权限名在类型检查时报错。前端的检查只决定显示什么，接口仍由服务端检查权限。

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

<a id="forms"></a>
## 表单

表单在前端用生成的 `schemas.ts` 和 `messages.ts` 校验：规则、提示的文字都与服务端相同，见[校验规则与验证提示](clients.md#validation-rules)。服务端返回的字段错误显示在对应的输入框下面。shell 不提供表单组件，只对接 react-hook-form 和 antd Form。

用 react-hook-form 时，交给 `useForm` 一个 `resolver`：

```tsx
import { resolver, setServerErrors } from "@go-tenon/shell"
import { components, schemas } from "@blog/client/api/schemas"
import { messages } from "@blog/client/api/messages"

const rules = { components, messages, language: "zh-CN" }

function ArticleForm() {
  const form = useForm<ArticleChanges>({ resolver: resolver(schemas.article.create.body, rules) })
  const [formError, setFormError] = useState<string>()
  const save = useMutation({
    mutationFn: (changes: ArticleChanges) => api.article.create(changes),
    onError: (err) => setFormError(setServerErrors(err, form)?.message),
  })
  return <form onSubmit={form.handleSubmit((v) => save.mutate(v))}>…</form>
}
```

用 antd Form 时，每个 `Form.Item` 用同一条规则，出错时把 Form 实例交给 `setServerErrors`：

```tsx
const rule = antdRule(schemas.article.create.body, rules)

<Form form={form} onFinish={(v) => save.mutate(v, { onError: (err) => setServerErrors(err, form) })}>
  <Form.Item name="title" rules={[rule]}><Input /></Form.Item>
  <Form.Item name={["items", 0, "name"]} rules={[rule]}><Input /></Form.Item>
</Form>
```

- 字段的路径用点连接，例如 `items.0.name`，与服务端错误的位置 `body.items[0].name` 对应。
- `setServerErrors` 把请求体的字段错误设置到对应字段，返回不属于某个字段的错误（例如 slug 已被使用、断网），它是 `describeError` 的结果：`message` 是服务端翻译好的提示，断网等没有服务端提示时为空，按 `kind` 显示。
- `serverErrors(err)` 只拆分，不设置：`{ fields, form }`；`formValidator(schema, rules)` 返回校验整个表单的函数，其他表单库可以用它们对接。

有未保存的修改时，`useUnsavedChanges` 在离开页面前询问，刷新或关闭浏览器时由浏览器提示。保存成功后先调用 `release`，跳转时不再询问：

```tsx
const unsaved = useUnsavedChanges(form.formState.isDirty)
const save = useMutation({
  mutationFn: …,
  onSuccess: () => {
    unsaved.release()
    void navigate({ to: "/articles" })
  },
})
```

默认用浏览器的确认框，`useUnsavedChanges(dirty, { confirm })` 换成组件库的对话框（`confirm` 返回 `true` 时离开）。

<a id="lists"></a>
## 列表

列表的查询条件、排序和分页保存在地址中：刷新页面、分享链接、浏览器前进后退都回到同一个列表。地址中的参数与接口相同（`page`、`per_page`、`sort` 和各筛选条件），可以直接交给生成的客户端：

```tsx
import { listSearch, useList, useSelection } from "@go-tenon/shell"
import type { ArticleListInput } from "@blog/client/api"
import { components, schemas } from "@blog/client/api/schemas"

export const Route = createFileRoute("/_app/articles/")({
  validateSearch: listSearch<ArticleListInput>(schemas.article.list.query, { components }),
  component: Articles,
})

function Articles() {
  const list = useList(Route)
  const articles = useQuery({ queryKey: ["article", "list", list.params], queryFn: () => api.article.list(list.params) })
  const selection = useSelection<string>(list.params)
  // list.page、list.perPage、list.sort、list.filters
  // list.setFilters({ status: "draft" })、list.setPage(2)、list.setSort("-created")、list.reset()
  // list.tableState：TanStack Table、ProTable 用的 { pageIndex, pageSize, sorting }
}
```

- `listSearch` 按生成的查询参数规则解析地址：值转换为规则中的类型（数字、布尔值、数组），不合法的值、不认识的参数、空值和等于默认值的值都去掉。
- 修改查询条件、排序或每页条数时回到第一页；等于默认值的参数不写进地址。
- `useSelection` 是批量操作的选中项，`resetOn`（例如 `list.params`）变化时清空：换了查询条件或页码后，原来选中的行已经不在眼前。

游标分页的游标不放进地址，“加载更多”用 TanStack Query 的 [useInfiniteQuery](https://tanstack.com/query/latest/docs/framework/react/guides/infinite-queries)，`getNextPageParam` 取响应的 `next_cursor`。

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
| `resolver`、`antdRule`、`formValidator` | 用生成的规则校验表单：react-hook-form、antd Form、其他表单库 |
| `setServerErrors`、`serverErrors` | 把服务端的错误设置到表单的字段 |
| `useUnsavedChanges(dirty, options)` | 有未保存的修改时离开前询问 |
| `listSearch(querySchema, options)`、`useList(Route)` | 列表的查询条件、排序和分页保存在地址中 |
| `useSelection(resetOn)` | 列表的选中项 |
| `safeRedirect(redirect, fallback)` | 只接受同源相对路径的回跳地址 |
| `useNewVersion`、`watchVersion` | 新版本提示 |
| `tenonVersion(options)` | `@go-tenon/shell/vite` 中的构建插件 |
| `appStorage(id)` | 按应用隔离的本地存储 |
