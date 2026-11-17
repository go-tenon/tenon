# 布局与导航

运营后台和控制台的页面放在 `AppLayout` 中：顶栏占满整行，左边是标志，中间是多标签（开启时），右边是操作和用户菜单；顶栏下面左侧是页面搜索和按权限过滤的侧栏菜单，右侧是页面，可以开启多标签。这一页介绍 `AppLayout`、顶栏、页面搜索、多标签、租户切换和页面的标题区，最后是自己写布局时用到的导航模型和插槽。

官网和文档站用 `SiteLayout`、`DocsLayout`，见[官网与文档站](site.md)。

阅读前需要了解：[搭建前端应用](app.md)。

- [应用外壳](#app-layout)
    - [侧栏菜单与图标](#menu)
    - [页面搜索](#search)
    - [顶栏与插槽](#slots)
    - [顶栏菜单](#menus)
- [页面的标题区](#page)
- [多标签](#tabs)
- [租户](#tenants)
- [自己写布局](#custom-layouts)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="app-layout"></a>
## 应用外壳

`AppLayout` 放在需要登录的布局路由上，它之下的页面都在其中显示：

```tsx title="src/routes/_app.tsx"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { AppLayout, OperationsMenu, requireAuth, UserMenu } from "@go-tenon/ui"
import { api, logout, me } from "../auth"

export const Route = createFileRoute("/_app")({ beforeLoad: requireAuth, component: Layout })

function Layout() {
  const user = useQuery({ queryKey: ["me"], queryFn: me })
  return (
    <AppLayout
      title="博客后台"
      tabs
      actions={<OperationsMenu />}
      user={<UserMenu name={user.data?.name} logout={logout} />}
    />
  )
}
```

| 参数 | 内容 |
| --- | --- |
| `title`、`logo` | 顶栏左侧的名称和标志，点击回到首页；没有标志时显示名称的首字 |
| `user` | 顶栏最右边的用户菜单，通常是 `UserMenu` |
| `actions` | 顶栏中每个页面都有的操作，例如 `NotificationsMenu`、`OperationsMenu` |
| `search` | 侧栏顶部的[页面搜索](#search)，默认开启；`false` 时不显示 |
| `tabs` | 开启[多标签](#tabs)：`true` 或者 `TabsOutlet` 的选项，例如 `{ max: 8 }`、`{ home: false }` |
| `maxWidth` | 内容区的最大宽度，适合表单为主的控制台；运营后台通常铺满 |

- 顶栏最左边的按钮把侧栏收起为只有图标的窄栏，状态按账号保存在浏览器中。窄屏（小于 768px）时侧栏是顶栏下面的抽屉，由顶栏的汉堡按钮打开、关闭，打开页面后自动收起。
- 面包屑在页面的标题上面，由 [`Page`](#page) 显示。
- 行高、字号、顶栏高度随主题的[样式预设](app.md#presets)：运营后台更紧凑。
- 右下角的[新版本提示](app.md#new-version)已经包含在内。

**区域**用布局路由区分：一组页面共用一个布局，例如 `_app` 用 `AppLayout`，登录页不在 `_app` 之下，用 `AuthLayout`。

<a id="menu"></a>
### 侧栏菜单与图标

菜单由路由的 `staticData` 生成（见[页面的元数据](app.md#route-metadata)），按权限过滤：

```tsx
export const Route = createFileRoute("/_app/orders")({
  staticData: { title: "订单", icon: "order", order: 2, permission: "orders:read" },
  component: Orders,
})
```

- 带 `title` 的路由按路由树嵌套，跳过没有标题的布局路由；按 `order`、再按路径排序。没有权限、`menu: false`、带路径参数的页面不出现，其中没有页面的分组也不出现。
- 有子页面的路由是分组，可以展开；当前页面所在的分组自动展开，名称用主色，当前页面是主色实底；详情页这类不在菜单中的页面高亮它所在的分组。第一级菜单的图标在浅色的方块中；下级菜单向右缩进，左侧一条竖线，设置了 `icon` 时也显示图标。侧栏收起时，页面悬停显示名称，分组点击或悬停弹出子页面。
- `icon` 是图标的名称，对应 [Tabler 图标](https://tabler.io/icons)。内置的名称见 `defaultIcons`，例如 `home`、`dashboard`、`article`、`users`、`team`、`settings`、`safety`、`order`、`product`、`report`、`wallet`。写错的名称显示为一个空心圆，不会报错。需要其他图标时，通过 `UIProvider` 的 `icons` 增加：

```tsx
import { IconTruckDelivery } from "@tabler/icons-react"

<UIProvider theme={theme} icons={{ delivery: IconTruckDelivery }}>
```

菜单项的徽标（例如未读数）是运行时才知道的，由页面或功能模块登记，侧栏显示为红色的数字或小圆点：

```tsx
useMenuBadge("/_app/notifications", unread) // 组件卸载、值为 0 或空时去掉
```

<a id="search"></a>
### 页面搜索

侧栏顶部是页面搜索的入口（收起时是一个放大镜），按 Ctrl+K（macOS 上是 ⌘K）或 `/` 也能打开。它列出菜单中可以进入的页面，已经按权限过滤，说明是页面所在的分组；输入时按名称和分组筛选，选择后打开页面。不需要时设置 `search={false}`。

<a id="slots"></a>
### 顶栏与插槽

顶栏左边是收起侧栏的按钮（窄屏时是汉堡按钮）和标志，中间是[标签栏](#tabs)（开启多标签时）；右边从左到右是 `actions` 插槽、`actions` 参数、租户切换和 `user`。多租户的应用（`createApp` 设置了 `tenancy`）自动显示租户切换 `TenantSwitcher`，不需要自己放。

每个页面都有的内容用参数传给 `AppLayout`；某个页面才有的内容由页面用 `Fill` 放进插槽，离开页面时移除：

```tsx
function ArticlesPage() {
  return (
    <>
      <Fill name="actions">
        <Button>写文章</Button>
      </Fill>
      …
    </>
  )
}
```

| 插槽 | 位置 |
| --- | --- |
| `actions` | 顶栏中，`actions` 参数之前 |
| `aside` | 右侧面板，有内容时才出现 |
| `footer` | 内容区的底部 |

同一个插槽可以有多份内容，按放入的顺序排列。内容用 portal 放进插槽，仍在页面自己的组件树中：读取页面的状态和上下文，随页面一起更新。

<a id="menus"></a>
### 顶栏菜单

| 组件 | 作用 |
| --- | --- |
| `UserMenu` | 只显示头像（`avatar`，没有时是 `name` 的首字），菜单中有应用的菜单项（`items`）、配色（亮色、暗色、跟随系统，`colorScheme={false}` 时不显示）、“多标签”的开关（布局开启了多标签时）和退出登录。`logout` 调用应用的退出接口，之后清除会话中的身份；标签中有未保存的修改时先询问 |
| `TenantSwitcher` | 当前租户的名称，菜单中列出用户所属的全部租户；只有一个租户时只显示名称，不是多租户应用时不显示。`AppLayout` 已经放好 |
| `NotificationsMenu` | 铃铛和未读数，弹出最近的通知，点击打开并标记已读，见[通知](features.md#notifications) |
| `OperationsMenu` | 后台任务的进度：导入、导出等耗时的操作。有进行中的任务时显示转动的图标，没有任务时不显示，见[后台操作](features.md#operations) |
| `ColorSchemeToggle` | 亮色和暗色之间切换的图标按钮，自己写布局时用；`AppLayout` 中配色在 `UserMenu` 里 |

<a id="page"></a>
## 页面的标题区

`Page` 是页面的标题区：面包屑、标题、一句说明和右侧的操作，所有页面保持一致的层级和间距：

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

面包屑在标题上面，来自路由的标题：最后一级是当前页面，前面的是链接。不需要时设置 `breadcrumbs={false}`，例如首页。

页面中的其他内容直接使用 Mantine 的组件。表格和表单见[列表与表单](lists-forms.md)。

<a id="tabs"></a>
## 多标签

运营后台常用多标签：打开的页面排成一行标签，切换标签时页面保留原来的状态（填了一半的表单、翻到的页、滚动位置）。`AppLayout` 设置 `tabs` 即可开启：标签栏在顶栏中间，侧栏展开时从侧栏与内容区的分界线开始；布局之下的每个页面是一个标签。

- 用户可以在头像菜单中关掉多标签，按账号保存；关掉后页面不再保留状态。窄屏时不显示标签栏。
- 第一次使用（这个账号没有保存过标签）时，菜单的第一个页面作为固定的标签放在最前面；`tabs={{ home: false }}` 时不这样做。
- 切换页面时（点标签、点菜单、搜索跳转），新页面淡入，同时从下方上移 6px；系统开启“减少动态效果”时不播放。

标签的标识是页面路由加路径参数：`/users/1` 和 `/users/2` 是两个标签，查询参数随标签保存，切回时恢复。标签的标题是页面路由的 `staticData.title`，页面可以用 `useTabTitle` 改成具体的名称；`staticData.tab` 为 `false` 的页面不进入标签，照常显示：

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
> 标签自己的路由上下文依赖 TanStack Router 内部的 `router.stores`。升级 TanStack Router 时，`js/ui/src/shell/tabs.test.tsx` 检查它是否仍然可用。`<Activity>` 需要 React 19.2。

标签栏的操作：点击切换，中键点击关闭；标签上没有关闭按钮，右键菜单中有关闭、固定、重新加载、关闭其他标签页、关闭右侧标签页；可以拖动排序；固定的标签显示图钉，有未保存修改的显示一个圆点。放不下时两端出现箭头，点击向那一侧滚动，还有被挡住的标签的一端渐隐。最后一个标签右边是刷新按钮，重新加载当前页面。

| 操作 | 规则 |
| --- | --- |
| 打开 | 打开新页面时加在最后；已经打开的页面回到它的标签 |
| 关闭 | 关闭当前标签时显示右边的，没有时显示左边的。固定的标签也可以单独关闭；唯一的标签不能关闭 |
| 关闭其他、右边、全部 | 固定的标签总是保留；关闭全部时没有固定的标签，保留当前标签 |
| 固定 | 固定的标签排在最左边；拖动排序只在同一组中移动 |
| 刷新 | 重新挂载这个标签的页面 |
| 数量上限 | 超过 `max`（默认 10）时关闭最久没用的标签，固定的、有未保存修改的除外 |
| 保存 | 标签按账号保存在[本地存储](app.md#storage)中：刷新浏览器后恢复标签栏，页面在打开时才加载。登录、退出、切换账号后按新的账号重新开始 |

布局路由中调用 `useTabShortcuts()` 开启快捷键：Alt+W 关闭当前标签，Alt+← / Alt+→ 切换。

**未保存的修改。** 在标签中，[`useUnsavedChanges`](lists-forms.md#unsaved-changes) 不在切换标签时询问（切换标签会保留页面的状态），而是把标签标记为有未保存的修改。关闭、刷新这个标签时询问，刷新或关闭浏览器时由浏览器提示。`UserMenu` 退出登录前、`switchTenant` 切换租户前都会询问；自己写的退出按钮用 `confirmDiscard`：

```tsx
async function signOut() {
  if (!(await confirmDiscard(app))) return
  await api.auth.logout()
  await session.signOut()
}
```

<a id="tenants"></a>
## 租户

多租户应用中，当前租户是会话身份中的 `tenant`。`createApp` 的 `tenancy` 提供租户列表和切换后的去处，顶栏的 `TenantSwitcher` 据此显示租户菜单：

```tsx
createApp({
  id: "console",
  routeTree,
  session,
  tenancy: {
    tenants: () => api.tenant.list(), // 当前用户所属的全部租户 [{ id, name }]
    home: (id) => `/t/${id}/`, // 切换后去哪里，见下表
  },
})
```

切换前，标签中有未保存的修改时先询问，用户选择留下时不切换。切换时先更新会话的身份：TanStack Query 的缓存随之清空，页面重新挂载，上一个租户的数据不会出现。然后按后端[确定租户](../security/tenancy.md#identifying-tenants)的方式跳转：

| 后端怎样确定租户 | `home` | 切换后 |
| --- | --- | --- |
| 路径 `/t/{tenant}/…` | 返回新租户的首页，例如 `` (id) => `/t/${id}/` `` | 去新租户的首页，不停留在原来的页面：页面中的资源属于原来的租户 |
| 子域名 | 返回完整的地址，例如 `` (id) => `https://${id}.example.com/` `` | 整页跳转 |
| 请求头 `X-Tenant` | 不设置 | 留在当前页面，按新租户重新读取数据 |

客户端按同样的方式把租户带给接口：请求头方式用 `createClient` 的 `headers`，路径方式用函数形式的 `baseURL`，见[客户端代码](../http/clients.md#creating-the-client)：

```ts
const tenant = () => session.identity()?.tenant
export const api = createClient({ baseURL: "/api", session, headers: () => ({ "X-Tenant": tenant() }) })
// 路径方式：createClient({ baseURL: () => `/api/t/${tenant()}`, session })
```

租户在路径中时，用户也可能直接打开另一个租户的地址。租户布局路由的 `beforeLoad` 调用 `enterTenant`，会话的身份随之切换：

```tsx
export const Route = createFileRoute("/t/$tenant")({
  beforeLoad: ({ context, params }) => enterTenant(context.app, params.tenant),
})
```

自己画租户菜单时用 `useTenant()`：`{ current, tenant, tenants, switchTo }`，用户选择留下时 `switchTo` 返回 `false`。

<a id="custom-layouts"></a>
## 自己写布局

`AppLayout` 不合适时（例如顶部导航的布局），可以自己写一个。布局读取导航模型和布局状态，把插槽放到自己的位置上；页面只往插槽中放内容，不关心当前是哪一种布局，多标签和权限过滤照常工作：

```tsx
function TopLayout() {
  const { menu, active, crumbs } = useNavigation()
  return (
    <>
      <header>
        <TopMenu items={menu} active={active} />
        <Slot name="actions" />
      </header>
      {useSlotFilled("aside") && <aside><Slot name="aside" /></aside>}
      <main>
        <TabBar />
        <TabsOutlet max={10} />
      </main>
    </>
  )
}
```

| 名称 | 内容 |
| --- | --- |
| `useNavigation()` | `menu`：按权限过滤后的菜单树；`active`：当前位置在菜单中的路径，从第一级到最深一级的菜单项 id；`crumbs`：面包屑 |
| `useMenu()`、`useBreadcrumbs()` | 只要菜单或面包屑时用 |
| `firstPage(menu)`、`pageUnder(menu, path)` | 第一个可以进入的页面；某个分组下第一个可以进入的页面，例如点击面包屑中的分组时跳转到它 |
| `Slot`、`useSlotFilled(name)` | 放置插槽；插槽中是否有内容，例如没有侧边面板时不留出位置 |
| `useLayoutState()` | `[state, setState]`：`collapsed`（侧栏收起为图标）、`tabs`（用户打开了多标签），按账号保存；`navOpen`（窄屏时导航抽屉打开，不保存） |
| `useNarrow()` | 是否是窄屏（默认宽度小于 768px） |
| `TabBar`、`TabsOutlet` | 标签栏（32px 高，为顶栏设计）；代替 `Outlet` 渲染标签中的页面：`max` 数量上限，`confirm` 换掉询问未保存修改的浏览器确认框，`home` 第一次使用时固定菜单的第一个页面 |
| `useTabs()` | 自己画标签栏时用：`tabs`（`[{ key, title, icon, href, pinned, dirty, active, closable }]`）和 `activate`、`close`、`closeOthers`、`closeRight`、`closeAll`、`refresh`、`pin`、`unpin`、`move` |

<a id="troubleshooting"></a>
## 常见问题

**新加的页面没有出现在菜单中**

见[搭建前端应用的常见问题](app.md#troubleshooting)。

**页面放进 `Fill` 的按钮没有出现在顶栏**

插槽的名字写错了：`AppLayout` 只有 `actions`、`aside`、`footer` 三个插槽。用户菜单这类每个页面都有的内容是 `AppLayout` 的参数。

**页面上没有面包屑**

面包屑由 `Page` 显示：页面没有用 `Page`，或者设置了 `breadcrumbs={false}`。

**切换标签后，页面中填了一半的表单不见了**

页面不在标签中：检查布局是否开启了 `tabs`、用户是否在头像菜单中关掉了多标签，以及页面路由的 `staticData.tab` 是否为 `false`。

**切换租户后，页面上还是上一个租户的数据**

创建 `kit` 或 `queries` 时没有传 `scope: session.scope`，缓存的 key 中没有租户，见[在 React 中调用接口](client-react.md#tanstack)。

<a id="api-reference"></a>
## API 参考

| 名称 | 说明 |
| --- | --- |
| `AppLayout` | 应用外壳：`title`、`logo`、`user`、`actions`、`search`、`tabs`、`maxWidth` |
| `Fill`、`Slot`、`useSlotFilled`、`SlotName` | 插槽 |
| `UserMenu`、`TenantSwitcher`、`NotificationsMenu`、`OperationsMenu`、`ColorSchemeToggle` | 顶栏菜单；`UserMenu` 的选项：`name`、`avatar`、`logout`、`items`、`colorScheme` |
| `Icon`、`useIcon`、`defaultIcons`、`IconsProvider` | 图标名称 |
| `useMenuBadge(routeId, value)` | 登记菜单项的徽标 |
| `Page` | 页面的标题区：`title`、`description`、`actions`、`breadcrumbs` |
| `TabBar`、`TabsOutlet` | 标签栏；标签中的页面（`max`、`confirm`、`home`） |
| `useTabs`、`useTab`、`useTabActive`、`useTabTitle`、`useTabShortcuts` | 多标签 |
| `confirmDiscard(app)` | 有未保存的修改时询问是否放弃，例如退出登录前 |
| `useTenant`、`switchTenant`、`enterTenant` | 当前租户、切换租户 |
| `useNavigation`、`useMenu`、`useBreadcrumbs` | 导航模型、菜单、面包屑 |
| `firstPage`、`pageUnder` | 菜单中第一个可以进入的页面 |
| `useLayoutState`、`useNarrow` | 布局状态、窄屏 |
