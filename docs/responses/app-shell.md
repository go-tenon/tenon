# 应用骨架

`@go-tenon/shell` 是[单页应用](spa.md)骨架的逻辑：把 TanStack Router、TanStack Query 和[会话](clients.md#client-runtime)接到一起，提供登录检查、按权限进入页面、菜单、面包屑、布局约定、多标签、租户切换、表单、列表和新版本提示。它不依赖组件库：同一份逻辑由运营后台的 antd 外观和 shadcn 外观渲染，外观只负责界面。

- [安装](#installing)
- [创建应用](#creating-the-app)
- [页面的元数据](#route-metadata)
- [登录检查](#requiring-sign-in)
- [权限](#permissions)
- [菜单与面包屑](#menu-and-breadcrumbs)
- [布局约定](#layouts)
- [多标签](#tabs)
- [租户](#tenants)
- [表单](#forms)
- [列表](#lists)
- [通知](#notifications)
- [后台操作](#operations)
- [AI 对话](#ai-chat)
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

项目允许用户切换布局时（例如在侧栏和顶部导航之间），布局路由用 `useLayoutVariant` 决定当前用哪一种，第一种是默认的；用户选过、并且在允许的范围内时用用户的选择：

```tsx
const [variant, setVariant] = useLayoutVariant(["side", "top"])
return variant === "top" ? <TopLayout /> : <SideLayout />
```

<a id="tabs"></a>
## 多标签

运营后台常用多标签：打开的页面排成一行标签，切换标签时页面保留原来的状态（填了一半的表单、翻到的页、滚动位置）。多标签按需开启，在布局路由中用 `TabsOutlet` 代替 `Outlet`，标签栏由外观放在 `tabs` 插槽中：

```tsx
function Layout() {
  useTabShortcuts() // 可选：Alt+W 关闭当前标签，Alt+← / Alt+→ 切换
  return (
    <SideLayout>
      <Slot name="tabs" />
      <TabsOutlet max={10} />
    </SideLayout>
  )
}
```

布局之下的每个页面是一个标签，标签的标识是页面路由加路径参数：`/users/1` 和 `/users/2` 是两个标签，查询参数随标签保存，切回时恢复。标签的标题是页面路由的 `staticData.title`，页面可以用 `useTabTitle` 改成具体的名称；`staticData.tab` 为 `false` 的页面不进入标签，照常显示：

```tsx
export const Route = createFileRoute("/_app/articles/$id")({
  staticData: { title: "文章" },
  component: ArticlePage,
})

function ArticlePage() {
  const { id } = Route.useParams() // 标准的 hooks：读到的总是这个标签自己的参数
  const article = useQuery(…)
  useTabTitle(article.data && `文章：${article.data.title}`)
}
```

页面照常使用 Router 的标准 hooks（`Route.useParams()`、`useSearch()`、`useLocation()` 等），不需要知道自己是否在标签中：每个标签有自己的路由上下文，隐藏的标签读到的是它最后一次显示时的状态。隐藏的标签放在 React 的 `<Activity mode="hidden">` 中，组件状态保留，effect 暂停：隐藏期间不发出请求，切回时照常恢复。页面需要知道自己是否显示时（例如暂停轮询）用 `useTabActive()`。

> [!NOTE]
> 标签自己的路由上下文依赖 TanStack Router 内部的 `router.stores`。升级 TanStack Router 时，`js/shell/src/tabs.test.tsx` 检查它是否仍然可用。`<Activity>` 需要 React 19.2。

`useTabs()` 返回标签和操作，外观据此渲染标签栏：

```tsx
const { tabs, active, activate, close, closeOthers, closeRight, closeAll, refresh, pin, unpin, move } = useTabs()
// tabs：[{ key, title, icon, href, pinned, dirty, active, closable }]
```

| 操作 | 规则 |
| --- | --- |
| 打开 | 打开新页面时加在最后；已经打开的页面回到它的标签 |
| 关闭 | 关闭当前标签时显示右边的，没有时显示左边的。固定的标签、唯一的标签不能关闭 |
| 关闭其他、右边、全部 | 固定的标签总是保留；关闭全部时没有固定的标签，保留当前标签 |
| 固定 | 固定的标签排在最左边；拖动排序（`move`）只在同一组中移动 |
| 刷新 | 重新挂载这个标签的页面 |
| 数量上限 | 超过 `max`（默认 10）时关闭最久没用的标签，固定的、有未保存修改的除外 |
| 保存 | 标签按账号保存在本地存储中：刷新浏览器后恢复标签栏，页面在打开时才加载。登录、退出、切换账号后按新的账号重新开始 |

在标签中，[`useUnsavedChanges`](#forms) 不在切换标签时询问（切换标签会保留页面的状态），而是把标签标记为有未保存的修改（`dirty`）。关闭、刷新这个标签时询问，刷新或关闭浏览器时由浏览器提示。退出登录前用 `confirmDiscard` 统一询问，切换租户时 `switchTenant` 已经询问：

```tsx
async function signOut() {
  if (!(await confirmDiscard(app))) return
  await api.auth.logout()
  await session.signOut()
}
```

默认用浏览器的确认框，`<TabsOutlet confirm={(message) => …} />` 换成组件库的对话框。

<a id="tenants"></a>
## 租户

多租户应用中，当前租户是会话身份中的 `tenant`。`createApp` 的 `tenancy` 提供租户列表和切换后的去处，`useTenant` 用于顶栏的租户菜单：

```tsx
export const app = createApp({
  id: "console",
  routeTree,
  session,
  tenancy: {
    tenants: () => api.tenant.list(), // 当前用户所属的全部租户 [{ id, name }]
    home: (id) => `/t/${id}/`, // 切换后去哪里，见下表
  },
})

const { current, tenant, tenants, switchTo } = useTenant()
```

切换前，标签中有未保存的修改时先询问（见[多标签](#tabs)），用户选择留下时 `switchTo` 返回 `false`。切换时先更新会话的身份：TanStack Query 的缓存随之清空，页面重新挂载，上一个租户的数据不会出现。然后按后端[确定租户](../security/tenancy.md#identifying-tenants)的方式跳转：

| 后端怎样确定租户 | `home` | 切换后 |
| --- | --- | --- |
| 路径 `/t/{tenant}/…` | 返回新租户的首页，例如 `` (id) => `/t/${id}/` `` | 去新租户的首页，不停留在原来的页面：页面中的资源属于原来的租户 |
| 子域名 | 返回完整的地址，例如 `` (id) => `https://${id}.example.com/` `` | 整页跳转 |
| 请求头 `X-Tenant` | 不设置 | 留在当前页面，按新租户重新读取数据 |

客户端按同样的方式把租户带给接口：请求头方式用 `createClient` 的 `headers`，路径方式用函数形式的 `baseURL`，见[客户端代码](clients.md#creating-the-client)：

```ts
const tenant = () => session.identity()?.tenant
export const api = createClient({ baseURL: "/api", auth: session.auth, headers: () => ({ "X-Tenant": tenant() }) })
// 路径方式：createClient({ baseURL: () => `/api/t/${tenant()}`, auth: session.auth })
```

租户在路径中时，用户也可能直接打开另一个租户的地址。租户布局路由的 `beforeLoad` 调用 `enterTenant`，会话的身份随之切换：

```tsx
export const Route = createFileRoute("/t/$tenant")({
  beforeLoad: ({ context, params }) => enterTenant(context.app, params.tenant),
})
```

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

默认用浏览器的确认框，`useUnsavedChanges(dirty, { confirm })` 换成组件库的对话框（`confirm` 返回 `true` 时离开）。页面在[标签](#tabs)中时，切换标签不询问，关闭标签时才询问。

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

<a id="notifications"></a>
## 通知

`useNotifications` 是[通知](../features/notifications.md)的模型：未读数、列表（从新到旧，“加载更多”翻页）、标记已读。通知的接口由应用定义，把生成的客户端的方法交给它：

```ts
const notifications: NotificationsSource = {
  list: (before) => api.notification.index({ before }).then((l) => l.map(toItem)),
  markRead: (ids) => api.notification.read({ ids }),
  markAllRead: () => api.notification.readAll(),
  badge: (o) => api.notification.inbox(o), // 未读数的事件流，见“实时推送”
}

const { unread, items, hasMore, loadMore, markRead, markAllRead } = useNotifications(notifications)
```

- 未读数来自事件流的 `badge` 事件（`{ "unread": 3 }`），断开后自动重连（等待时间从 1 秒加倍到 30 秒）；每次收到都重新读取列表。没有事件流时传 `unread`，每 60 秒读取一次（`pollInterval`）。
- `items` 的每一项是 `{ id, title, body?, read, created_at, href? }`，`title` 是服务端按语言生成的文案，`href` 是点击后打开的页面。
- `markRead` 先在界面上标记，再调用接口；已读的通知不再调用。
- 列表按身份隔离：切换租户或换了账号后读取新身份的通知。

<a id="operations"></a>
## 后台操作

[后台操作](../features/operations.md)发起后登记到操作中心，顶栏的任务中心和页面读取它们的进度：

```ts
const op = await api.article.bulkPublish({ ids })
trackOperation(app, {
  id: op.id,
  title: `批量发布 ${ids.length} 篇文章`,
  open: (o) => api.article.bulkPublishProgress(op.id, o),
})
```

- `useOperations()` 是登记过的操作，新的在前；`useOperation(id)` 是一个操作。每一项有 `title`、最新的进度 `operation`、比例 `ratio`（不知道总数时为空）、`finished` 和读取失败时的 `error`。
- 进度用 `@go-tenon/client` 的 `watchOperation` 读取，断开后自动重连，操作结束时停止。
- 同一个 ID 只登记一次。结束后仍留在操作中心，`dismissOperation(app, id)` 移除；退出登录、切换身份时清空。

<a id="ai-chat"></a>
## AI 对话

`@go-tenon/shell/chat` 在 Vercel AI SDK 的 `useChat` 之上接好[对话接口](../ai/chat.md#conversation-api)：消息流、历史、断线续传、停止、插话和审批。它是单独的入口，用到时在前端安装 AI SDK：

```sh
bun add ai@^7.0.159 @ai-sdk/react@^4.0.163
```

```tsx
import { useAssistantChat, useAssistantHistory } from "@go-tenon/shell/chat"

function Assistant({ id }: { id: string }) {
  const history = useAssistantHistory({ api: "/api/v1/assistant/chat", id })
  if (!history.data) return null
  return <Conversation id={id} messages={history.data} />
}

function Conversation({ id, messages }: { id: string; messages: UIMessage[] }) {
  const chat = useAssistantChat({ api: "/api/v1/assistant/chat", id, messages })
  // chat.messages、chat.status、chat.busy、chat.pending、chat.send(text)、chat.stop()、chat.respond(id, approved, reason)
}
```

- 先读取历史，再创建对话：`useChat` 用历史作为初始消息，进行中的回答随即接上（`resume`）。
- 请求带上 access token，`401` 时刷新再重试；发送时只发最后一条消息。
- `send`：没有回答进行时发送新消息；回答进行中时[插话](../ai/chat.md#steering)，回答恰好已经结束（`409 ai.finished`）时改为发送新消息。
- `stop`：调用停止接口，服务端取消这次回答（客户端断开连接不会停止它）。
- `pending` 是等待[审批](../ai/chat.md#approval)的工具调用（`{ id, toolName, input }`），`respond` 批准或拒绝；全部有了决定后自动发送，回答继续。
- 插话在回答中是一个 `data-steering` part（`data` 是 `{ text }`），外观把它显示为一条用户消息。

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
| `useNavigation`、`useMenuBadge` | 布局的导航模型；登记菜单项的徽标 |
| `Slot`、`Fill`、`useSlotFilled` | 插槽 |
| `useLayoutState`、`useLayoutVariant`、`useNarrow` | 布局状态、用户选择的布局、窄屏 |
| `TabsOutlet`、`useTabs`、`useTab`、`useTabActive`、`useTabTitle`、`useTabShortcuts` | 多标签 |
| `confirmDiscard(app)` | 有未保存的修改时询问是否放弃，例如退出登录前 |
| `useTenant`、`switchTenant`、`enterTenant` | 当前租户、切换租户 |
| `resolver`、`antdRule`、`formValidator` | 用生成的规则校验表单：react-hook-form、antd Form、其他表单库 |
| `setServerErrors`、`serverErrors` | 把服务端的错误设置到表单的字段 |
| `useUnsavedChanges(dirty, options)` | 有未保存的修改时离开前询问 |
| `listSearch(querySchema, options)`、`useList(Route)` | 列表的查询条件、排序和分页保存在地址中 |
| `useSelection(resetOn)` | 列表的选中项 |
| `safeRedirect(redirect, fallback)` | 只接受同源相对路径的回跳地址 |
| `resourceModel`、`useResourceList`、`useResourceItem`、`useResourceMutations`、`formValues` | [资源](resources.md#model)的模型 |
| `useNotifications` | 通知：未读数、列表、标记已读 |
| `trackOperation`、`dismissOperation`、`clearOperations`、`useOperations`、`useOperation` | 后台操作的进度 |
| `useAssistantChat`、`useAssistantHistory`、`pendingApprovals` | `@go-tenon/shell/chat` 中的 AI 对话 |
| `useNewVersion`、`watchVersion` | 新版本提示 |
| `onError`（`createApp` 的选项）、`useReportError`、`shouldReport` | 页面出错时报告给前端观测的 SDK，见[前端观测](../general/frontend-observability.md#route-errors) |
| `tenonVersion(options)` | `@go-tenon/shell/vite` 中的构建插件；`sourcemaps` 生成 source map 并移出 `dist`，见[前端观测](../general/frontend-observability.md#source-maps) |
| `appStorage(id)` | 按应用隔离的本地存储 |
