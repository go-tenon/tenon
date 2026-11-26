# 布局与导航

后台的布局、侧栏菜单、页面的标题区、多标签和租户切换。`FrameLayout` 是顶栏加侧栏菜单，可以开启多标签，适合表格和表单多的后台；`RailLayout` 是模块图标栏加二级导航，适合以模块切换为主的控制台。官网和文档站见[官网与文档站](site.md)。

包：`@go-tenon/ui`。

- 布局只描述界面的样子。菜单按路由的 `staticData` 生成、按权限过滤，租户由 `createApp` 的 `tenancy` 接入，布局本身不绑定身份、权限或租户。
- 需要登录的页面放在布局路由 `_app` 下；登录页在 `_app` 之外，用 `CenteredAuthLayout` 或 `SplitAuthLayout`。

<a id="frame-layout"></a>
## 用 `FrameLayout` 搭建后台的布局

`tenon add frontend --layout frame` 生成的布局路由，加上了多标签和后台任务菜单：

```tsx title="src/routes/_app.tsx"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { FrameLayout, OperationsMenu, requireAuth, UserMenu } from "@go-tenon/ui"
import { logout, me } from "../auth"

export const Route = createFileRoute("/_app")({ beforeLoad: requireAuth, component: Layout })

function Layout() {
  const user = useQuery({ queryKey: ["me"], queryFn: me })
  return (
    <FrameLayout
      title="博客后台" // 顶栏左侧的名称，也是浏览器标题中的应用名
      tabs // 开启多标签，也可以是 { max: 8 }、{ home: false }
      actions={<OperationsMenu />} // 每个页面都有的顶栏操作
      user={<UserMenu name={user.data?.name} logout={logout} />}
    />
  )
}
```

- 其他参数：`logo` 是顶栏左侧的标志（默认是名称的首字），`search={false}` 去掉[页面搜索](#search)，`maxWidth` 限制内容区的宽度（默认铺满），`documentTitle={false}` 见[浏览器标题](#document-title)。
- 侧栏可以收起为图标栏，状态按账号保存；窄屏（小于 768px）时侧栏是抽屉。新版本提示已经包含在内。

<a id="menu"></a>
### 侧栏菜单与图标

菜单由路由的 `staticData` 生成，字段见[页面的元数据](app.md#route-metadata)：

```tsx
export const Route = createFileRoute("/_app/orders")({
  staticData: { title: "订单", icon: "order", order: 2, permission: "orders:read" },
  component: Orders,
})
```

- 带 `title` 的路由按路由树嵌套，跳过没有标题的布局路由，同级按 `order`、再按路径排序。有子页面的路由是可以展开的分组。
- 页面不出现在菜单中的情况：没有 `title`；用户没有 `permission`；`menu: false`；路由自己引入了路径参数（例如详情页 `/articles/$id`）。菜单高亮当前页面最近的、在菜单中的上级；要让详情页高亮列表，把它们放在同一个布局路由下：`articles.tsx` 写 `staticData`，列表是 `articles/index.tsx`，详情页是 `articles/$id.tsx`。分组中没有可以进入的页面时，分组也不出现。路径参数来自上级路由时，链接填上当前地址中的值，见[租户](#entering-tenants)。
- `icon` 是 `defaultIcons` 中的名称，例如 `home`、`dashboard`、`article`、`users`、`settings`、`order`；写错的名称显示为空心圆。其他 [Tabler 图标](https://tabler.io/icons)通过 `UIProvider` 的 `icons` 增加：`<UIProvider theme={theme} icons={{ delivery: IconTruckDelivery }}>`。
- 菜单项的徽标（例如未读数）用 `useMenuBadge("/_app/notifications", unread)` 登记，第一个参数是页面或模块的路由 id；组件卸载、值为 0 或空时去掉。`RailLayout` 把模块的徽标显示在图标栏中图标的右上角。

<a id="search"></a>
### 页面搜索

侧栏顶部是页面搜索，按 Ctrl+K（macOS 上是 ⌘K）或 `/` 打开，列出菜单中可以进入的页面。`FrameLayout` 设置 `search={false}` 时不显示。

<a id="slots"></a>
### 用 `Fill` 往顶栏放页面的按钮

每个页面都有的内容是 `FrameLayout` 的参数；某个页面才有的内容由页面用 `Fill` 放进插槽，离开页面时移除：

```tsx
<Fill name="actions">
  <Button>写文章</Button>
</Fill>
```

插槽有 `actions`（顶栏中，`actions` 参数之前）、`aside`（右侧面板，有内容时才出现）、`footer`（内容区底部）、`user-menu`（`UserMenu` 中配色的下面）和 `nav`（`RailLayout` 的二级导航，见[铺满的模块](#full-bleed)）。同一个插槽中的内容按放入的顺序排列；内容仍在页面自己的组件树中，随页面一起更新。

<a id="menus"></a>
### 顶栏菜单：`UserMenu`、`NotificationsMenu`、`OperationsMenu`

- `UserMenu`：头像（`avatar`，没有时是 `name` 的首字），`statusColor` 在头像右下角显示状态点；菜单中是 `name`、`email`、应用的菜单项 `items`、配色（`colorScheme={false}` 时不显示）、`user-menu` 插槽和退出登录。退出时调用 `logout`（应用的退出接口），再清除会话中的身份；有未保存的修改时先询问。
- `NotificationsMenu`：铃铛和未读数，见[通知](features.md#notifications)。`OperationsMenu`：导入、导出等后台任务的进度，见[后台操作](features.md#operations)。
- `LanguageMenu`：列出 `languages`，选择后调用 `onChange`，换文案、记住选择由应用决定。`ColorSchemeToggle`：配色切换按钮，自己写布局时用。

<a id="page"></a>
## 页面的标题区：`Page`

页面用 `Page` 显示面包屑、标题、一句说明和右侧的操作，所有页面的层级和间距一致：

```tsx
<Page title="订单" description="全部渠道的订单，按下单时间倒序。" actions={<Button>新建订单</Button>}>
  {/* 筛选、表格 */}
</Page>
```

- 面包屑来自路由的标题。首页这类页面设置 `breadcrumbs={false}`；路由没有自己的标题时（例如新建页面），用 `crumb="新建"` 在最后加一级。
- `back` 在标题左边放返回按钮，给内页用，例如团队成员页返回团队列表：`back={() => navigate({ to: "/settings/teams" })}`。
- `width="narrow"` 将页面内容约束在 720px（适合设置表单页），也可以直接传像素值。
- 表格和表单见[列表与表单](lists-forms.md)。

<a id="page-tabs"></a>
### 用 `Page` 的 `tabs` 分页内标签

内容多的页面用 `tabs` 分成几个页内标签，在标题区下面切换，不把几块内容上下平铺：

```tsx
<Page
  title="队列"
  tabs={[
    { value: "topics", label: "任务类型", content: <Topics /> },
    { value: "failed", label: "失败任务", count: failed, alert: true, content: <FailedJobs /> },
  ]}
>
  <Problems /> {/* children 在标签的上面 */}
</Page>
```

- 当前的标签记在地址的 `?tab=` 中（第一个标签不写）。路由设置了 `validateSearch` 时要保留 `tab` 参数，否则切换不生效。
- 没打开过的标签不渲染、不发请求；打开过的保留状态。`count` 为 0 时不显示，`alert` 让它是红色。

<a id="tabs"></a>
## 多标签

`FrameLayout` 设置 `tabs` 开启多标签：布局之下的每个页面是一个标签，切换标签时页面保留状态（填了一半的表单、翻到的页）。用户可以在头像菜单中关掉多标签，窄屏时不显示标签栏。多标签用到 React 的 `<Activity>`，需要 React 19.2。

```tsx
export const Route = createFileRoute("/_app/articles/$id")({
  staticData: { title: "文章" }, // 标签的默认标题；tab: false 时不进入标签
  component: ArticlePage,
})

function ArticlePage() {
  const { id } = Route.useParams() // 读到的总是自己标签的值
  const article = useQuery(kit.queries.article.get(id)) // kit 来自 src/api.ts
  useTabTitle(article.data && `文章：${article.data.title}`)
  // ……
}
```

- 标签的标识是页面路由加路径参数：`/users/1` 和 `/users/2` 是两个标签，查询参数随标签保存。
- 隐藏的标签中 effect 暂停、不发请求。页面需要知道自己是否显示时（例如暂停轮询）用 `useTabActive()`。
- 第一次使用时，菜单的第一个页面作为固定的标签放在最前面，`tabs={{ home: false }}` 时不放。超过 `max`（默认 10）时关闭最久没用的标签，固定的、有未保存修改的除外。
- 标签按账号保存在[本地存储](app.md#storage)中，刷新浏览器后恢复。
- 布局中调用 `useTabShortcuts()` 开启快捷键：Alt+W 关闭当前标签，Alt+← / Alt+→ 切换。

<a id="tabs-unsaved"></a>
### 标签中的未保存修改与 `confirmDiscard`

页面在标签中时，[`useUnsavedChanges`](lists-forms.md#unsaved-changes) 在切换标签时不询问，只把标签标记为有未保存的修改：关闭、刷新这个标签时询问，刷新或关闭浏览器时由浏览器提示。`UserMenu` 退出登录、`switchTenant` 切换租户之前同样询问。

自己写的退出按钮先调用 `confirmDiscard`。`app` 在组件中用 `useApp()` 取得，`logout` 是 `src/auth.ts` 中的退出函数：

```tsx
async function signOut() {
  if (!(await confirmDiscard(app))) return // 用户选择留下
  await logout()
  app.session.signOut()
}
```

<a id="document-title"></a>
## 浏览器标题

布局把浏览器标题设为“页面 - 应用名”，例如“文章 - 博客后台”。应用名是布局的 `title`；页面是当前标签的标题（`useTabTitle` 设置的优先），或路由的 `staticData.title`。登录页的 `CenteredAuthLayout`、`SplitAuthLayout` 设为“登录 - 应用名”。

应用名在运行时才知道时（例如可以修改的站点名称），把读到的值传给 `title`，`kit` 来自 `src/api.ts`：

```tsx
const site = useQuery(kit.queries.site.get()) // 读到之前是 undefined
return <FrameLayout title={site.data?.name} tabs />
```

根路由 `head` 中的 `title` 是页面加载完成之前的标题，要保留。应用自己管理标题时（例如每个页面的 `head` 中写 SEO 标题），给布局设置 `documentTitle={false}`。运行时动态替换浏览器标签图标用 `useFavicon(url)`。

<a id="tenants"></a>
## 租户

当前租户是会话身份中的 `tenant`。`createApp` 的 `tenancy` 提供租户列表和切换后的去处：

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

切换时先更新会话的身份，TanStack Query 的缓存随之清空、页面重新挂载，然后按后端[确定租户](../security/tenancy.md#identifying-tenants)的方式跳转：

| 后端怎样确定租户 | `home` | 切换后 |
| --- | --- | --- |
| 路径 `/t/{tenant}/…` | 新租户的首页，例如 `` (id) => `/t/${id}/` `` | 去新租户的首页：原来页面中的资源属于原来的租户 |
| 子域名 | 完整的地址，例如 `` (id) => `https://${id}.example.com/` `` | 整页跳转 |
| 请求头 `X-Tenant` | 不设置 | 留在当前页面，按新租户重新读取数据 |

`src/api.ts` 中的客户端按同样的方式把租户带给接口，见[创建客户端](../http/clients.md#creating-the-client)：

```ts
const tenant = () => session.identity()?.tenant
export const api = createClient({ baseURL: "/api", session, headers: () => ({ "X-Tenant": tenant() }) })
// 路径方式：createClient({ baseURL: () => `/api/t/${tenant()}`, session })
export const kit = createKit(api, { scope: session.scope })
```

`kit` 必须带 `scope: session.scope`：查询的缓存按账号和租户区分，否则切换后可能显示上一个租户的数据。

框架的布局中没有租户菜单。自己画时用 `useTenant()`：`current`、`tenant`、`tenants`、`switchTo(id)`；用户选择留下时 `switchTo` 返回 `false`。

<a id="entering-tenants"></a>
### 用 `enterTenant` 进入路径中的租户

用户可能直接打开另一个租户的地址。租户布局路由的 `beforeLoad` 调用 `enterTenant`，会话的身份随之切换：

```tsx
export const Route = createFileRoute("/t/$tenant")({
  beforeLoad: ({ context, params }) => enterTenant(context.app, params.tenant),
})
```

租户布局路由本身不写 `title`。它下面的页面照常出现在菜单中，链接填上当前的租户：在 `/t/acme/` 下是 `/t/acme/inbox`。还没有进入租户的页面中，这些页面不出现。

<a id="rail-layout"></a>
## 用 `RailLayout` 搭建双栏导航

`tenon add frontend --layout rail` 生成的布局路由用 `RailLayout`：最左侧是模块的图标栏，旁边是当前模块的二级导航。用户菜单放在图标栏底部，其中放进入设置的菜单项：

```tsx
<RailLayout
  title="Acme"
  user={<UserMenu name={user.data?.name} position="right-end" logout={logout}
    items={[{ label: "设置", icon: <IconSettings size={16} />, to: "/settings/profile" }]} />}
/>
```

- 导航与 `FrameLayout` 一样来自[路由的 `staticData`](#menu)。第一级是模块，在图标栏中；模块下的页面和分组在二级导航中。
- 在模块父路由的 `staticData` 中设置 `preserveSearch: ["preset", "from", "to", ...]` 后，模块内切换二级导航页面时自动保留这些查询参数，适合多张报表共享时间与筛选条件。
- `settings`（默认 `/settings`）下的页面不进图标栏，从用户菜单进入；其中的页面不显示面包屑，内页用 `Page` 的 `back` 返回。
- 图标栏的标志下方、底部支持 `railTop`、`railBottom` 扩展（例如全局搜索按钮）；`headerHeight`（默认 52px）写入 `--tn-rail-header`，供各栏顶栏齐平。
- 窄屏（`compactQuery`，默认 `max-width: 48em`）时二级导航是抽屉，点击导航项后自动关闭。内容区最宽 `maxWidth`（默认 1152px），二级导航宽 `navWidth`（默认 260px）。`RailLayout` 没有多标签。
- 二级导航的收起状态按账号保存，页面可用 `useNavControl()` 读取展开状态并触发切换；没有子菜单且未使用 `<Fill name="nav">` 的单页模块自动隐藏二级导航。

<a id="full-bleed"></a>
### 铺满的模块：`fullBleed`、`nav` 插槽和 `useCollapseNav`

收件箱、聊天这类多栏的模块铺满内容区，二级导航是模块自己的面板，例如带数量的收件箱列表：

```tsx title="src/routes/_app/inbox.tsx"
export const Route = createFileRoute("/_app/inbox")({
  staticData: { title: "收件箱", icon: "message", fullBleed: true },
  component: Inbox,
})

function Inbox() {
  const [detail, setDetail] = useState(false)
  const wide = useMediaQuery("(min-width: 1600px)")
  useCollapseNav(detail && !wide) // 窗口不够宽时，打开详情先收起二级导航
  return (
    <div style={{ display: "flex", height: "100%" }}>
      <Fill name="nav">
        <NavLink label="我的" rightSection={<Text size="xs" c="dimmed">8</Text>} active />
        <NavLink label="未指派" rightSection={<Text size="xs" c="dimmed">5</Text>} />
      </Fill>
      <ScrollArea w={320}>{/* 会话列表 */}</ScrollArea>
      <ScrollArea style={{ flex: 1 }}>{/* 会话 */}</ScrollArea>
    </div>
  )
}
```

- `fullBleed: true` 的页面没有最大宽度、内边距和面包屑，高度占满内容区。内容区本身不滚动，页面中的各栏自己滚动。写在模块的路由上时，模块下的页面都铺满。`FrameLayout` 同样支持它。
- `<Fill name="nav">` 有内容时，二级导航显示它，替换按路由生成的链接；顶部的模块名和收起按钮保留，窄屏时它在抽屉中。二级导航中的 `NavLink` 自动用紧凑的样式，需要分组时用 `<RailSection title="…" collapsible storeKey="…" action={…}>`：`action` 在标题行右侧放置独立操作按钮，`storeKey` 按账号记住展开和收起状态。把 `Fill` 放在模块的布局路由中，模块内切换页面时面板保持不变。
- `useCollapseNav(when)` 在 `when` 为 `true` 时临时收起二级导航，变回 `false` 或组件卸载时恢复，保存的收起状态保持不变。临时收起期间用户点了展开，以用户为准，直到下一次要求收起。

<a id="panes"></a>
### 多栏面板：`Pane`、`PaneHeader`、`PaneToolbar` 与 `PaneBody`

`fullBleed` 页面用 `Pane` 组织多栏工作台，每栏的顶栏高度自动对齐 `--tn-rail-header`（52px），水平边距对齐 `--tn-pane-px`：

```tsx
import { Pane, PaneBody, PaneHeader, PaneToolbar } from "@go-tenon/ui"

<div style={{ display: "flex", height: "100%" }}>
  <Pane
    border="right"
    resizable={{ storageKey: "inbox-list", defaultWidth: 320, min: 260, max: 420, side: "right" }}
  >
    <PaneHeader title="我的" subtitle="3 个待回复" showNavToggle />
    <PaneToolbar>{/* 搜索与筛选控件自动应用 30px 高 / 13px 字号的 dense 密度 */}</PaneToolbar>
    <PaneBody>{/* 列表内容独立滚动 */}</PaneBody>
  </Pane>
  <Pane>
    <PaneHeader title="对话" actions={actions} />
    <PaneBody>{/* 主内容 */}</PaneBody>
  </Pane>
</div>
```

- `Pane` 不传 `width` 和 `resizable` 时自动占满剩余宽度；传入 `resizable` 时内建鼠标拖拽、方向键微调（Shift 加速）和双击 / Home 恢复默认，并通过 `app.storage` 按账号持久化栏宽。需要直接读取宽度状态时使用 `useResizableWidth`。
- `PaneHeader` 的 `showNavToggle` 在布局二级导航收起时自动显示展开按钮，`back` 显示返回按钮；窄屏允许内容折行时传 `fixed={false}`。
- `PaneToolbar` 自动开启 `data-tn-density="dense"`，内部的输入框、按钮与分段切换保持 30px 高度和 13px 字号。
- `PaneBody` 传入 `loading` 时，在当前可见视口中央叠加轻磨砂加载覆层与转圈，适合配合 [`useSmoothTransition`](lists-forms.md#loading-transition) 处理全栏筛选刷新。
- 右侧详情栏使用 `AsidePane` 与 `useAsidePaneState(storageKey)`：按账号持久化展开偏好，宽屏内联为可拖拽 `Pane`（中等宽屏自动临时收起二级导航），中屏退化为右侧 `Drawer`，窄屏全屏覆盖展示。

<a id="custom-layouts"></a>
## 自己写布局

布局读取导航模型，把插槽放到自己的位置上；页面只往插槽中放内容，多标签和权限过滤照常工作：

```tsx
function TopLayout() {
  const { menu, active } = useNavigation()
  useDocumentTitle(formatDocumentTitle(usePageTitle(true), "Acme")) // “页面 - Acme”
  return (
    <>
      <header>
        <TopMenu items={menu} active={active} />
        <Slot name="actions" />
      </header>
      {useSlotFilled("aside") && <aside><Slot name="aside" /></aside>}
      <main>
        <TabBar />
        <SessionBoundary>
          <TabsOutlet max={10} />
        </SessionBoundary>
      </main>
    </>
  )
}
```

- `useNavigation()` 返回 `menu`（按权限过滤后的菜单树）、`active`（当前位置从第一级到最深一级的菜单项 id）和 `crumbs`（面包屑）。
- `SessionBoundary` 只包住页面（`Outlet` 或 `TabsOutlet`），登录、退出、切换账号或租户时重新挂载其中的页面。包住布局或根路由的 HTML 文档时，每次打开已登录的页面，整个文档都会重建。
- `TabsOutlet` 代替 `Outlet` 开启多标签；自己画标签栏时用 `useTabs()`。`useLayoutState()` 是侧栏收起、多标签开关这些按账号保存的状态，`useNarrow()` 判断窄屏。
- `useFullBleed()` 是当前页面是否要求铺满（`staticData.fullBleed`），`useNavCollapseRequested()` 是是否有页面用 `useCollapseNav` 要求临时收起导航，`MenuBadge` 显示菜单项的徽标。
