# 运营后台

运营后台用 [antd](https://ant.design) 和 [ProComponents](https://procomponents.ant.design) 编写，数据密度高、组件齐全。`@go-tenon/admin` 是它的外观：布局、标签栏、登录页、错误页、用户菜单、租户切换、通知、后台任务和 AI 对话。逻辑都来自[应用骨架](app-shell.md)（`@go-tenon/shell`）：导航模型、插槽、布局状态、多标签和权限，这里只负责用 antd 渲染。

阅读前需要了解：[应用骨架](app-shell.md)；[入门教程第 8 章](../tutorial/08-admin.md)用 admin 模板做过一个后台。

- [新建运营后台](#creating)
- [模板里有什么](#template)
- [全局配置](#provider)
- [布局](#layouts)
- [页面与部件](#pages)
- [通知、后台任务与 AI 对话](#features)
- [给 AI 编程助手的约定](#agents)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="creating"></a>
## 新建运营后台

在项目中用 admin 模板加一个前端：

```bash
go tool tenon add frontend admin --template admin
```

它在 `apps/admin` 中生成前端，挂在 `/admin/`，然后按提示在路由中挂载它（见[单页应用](spa.md#mounting)）。`@go-tenon/admin` 随框架分发，复制到 `.tenon/js/admin`，并加入 Bun workspace。只有用 admin 模板的项目才加入，其他项目不会因此装上 antd。

ProComponents 3 仍是预发布版本，模板锁定在 `3.1.15-8`，正式版发布后再升级。

<a id="template"></a>
## 模板里有什么

```text
apps/admin/
├── AGENTS.md              给 AI 编程助手的约定：允许使用的库和写法
├── biome.json             bun run lint：禁止导入 shadcn、Radix、Tailwind
└── src/
    ├── app.ts             createApp：会话、权限、404 和错误页
    ├── auth.ts            登录、刷新、登出和当前用户的接口
    └── routes/
        ├── __root.tsx     AdminProvider（设计令牌）和新版本提示
        ├── login.tsx      登录页
        ├── _app.tsx       需要登录的布局：侧栏、多标签、用户菜单、租户切换
        └── _app/index.tsx 首页
```

`src/auth.ts` 按约定的路径调用登录相关的接口：`POST /api/auth/login`、`/api/auth/refresh`、`/api/auth/logout` 和 `GET /api/me`（返回 `{ name, permissions? }`）。应用按[教程第 6 章](../tutorial/06-login.md)的写法注册它们后，登录就能使用；接口名或路径不同时只改这个文件。没有登录时打开任何页面都会跳到登录页，登录后回到原来的页面。

页面放在 `src/routes/_app/` 中，一个文件一个页面，路由的 `staticData` 决定菜单：

```tsx
export const Route = createFileRoute("/_app/articles/")({
  staticData: { title: "文章", icon: "file-text", order: 10, permission: "articles:read" },
  component: Articles,
})
```

<a id="provider"></a>
## 全局配置

`AdminProvider` 放在根路由中，包住全部页面：antd 的中文、设计令牌、暗色与紧凑算法，以及 antd 的 `App`（`App.useApp()` 的 message、modal）：

```tsx
import { antd } from "@todo/theme"

<AdminProvider tokens={antd}>
  <Outlet />
</AdminProvider>
```

- 品牌色和圆角来自项目的[设计令牌](theme.md)。默认只取这两项（`use="brand"`），其余保持 antd 的紧凑风格；`use="full"` 使用全部令牌。暗色时 antd 的暗色算法会以令牌的暗色品牌色为种子，按暗色背景微调。
- 令牌的 `density` 为 `compact` 时使用紧凑算法，也可以用 `compact` 指定。
- 颜色模式（跟随系统、亮色、暗色）由用户菜单切换，保存在本地存储中，`useAdmin().mode` 读取它。
- 路由元数据中的 `icon` 是名字，例如 `file-text`。默认登记了常用的十几个（`defaultIcons`），用 `icons` 加上其他的，避免引入整个图标库：

```tsx
import { CloudOutlined } from "@ant-design/icons"

<AdminProvider tokens={antd} icons={{ cloud: <CloudOutlined /> }}>
```

<a id="layouts"></a>
## 布局

布局按[布局约定](app-shell.md#layouts)实现：读取导航模型和布局状态，把插槽放到自己的位置上；页面只往插槽中放内容。

| 组件 | 布局 | 适合 |
| --- | --- | --- |
| `AdminLayout variant="side"` | 侧栏可收起为图标，面包屑在顶栏 | 大多数后台 |
| `AdminLayout variant="top"` | 导航在顶栏 | 页面少的后台 |
| `AdminLayout variant="mix"` | 一级导航在顶栏，二级在侧栏 | 模块多、每个模块页面多的后台 |
| `RailLayout` | 最左侧的图标栏切换模块，旁边是模块内的菜单 | 客服、IM、项目管理这类工作台 |
| `CenteredLayout` | 没有侧栏，内容限宽居中（`width`，默认 720） | 简单的用户端、个人中心 |
| `SettingsLayout` | 页面内左侧是二级导航，右侧是内容 | 设置、个人资料 |
| `FullscreenLayout` | 只保留返回和操作入口 | 编辑器、画布、数据大屏 |
| `AuthLayout` | 卡片居中，或者左右分栏（`side`） | 登录、注册、找回密码 |

`AdminLayout` 基于 ProLayout。`variant` 传数组时用户可以在这几种之间切换，选择保存在布局状态中；`tabs` 开启[多标签](app-shell-features.md#tabs)，标签栏放在页面上方：

```tsx
export const Route = createFileRoute("/_app")({ beforeLoad: requireAuth, component: Layout })

function Layout() {
  return (
    <>
      <Fill name="user"><UserMenu name={me.data?.name} logout={logout} /></Fill>
      <Fill name="tenant"><TenantSwitcher /></Fill>
      <AdminLayout title="运营后台" variant={["side", "top"]} tabs />
    </>
  )
}
```

插槽的位置：`brand` 在菜单头部，`search`、`actions`、`tenant` 在顶栏右侧，`user` 在头像的位置，`footer` 在页脚。页面往 `actions` 放顶栏上的按钮：

```tsx
<Fill name="actions"><Button type="primary">写文章</Button></Fill>
```

侧栏的收起状态保存在布局状态中，按账号保存。

<a id="pages"></a>
## 页面与部件

| 组件 | 说明 |
| --- | --- |
| `LoginPage` | 邮箱和密码。`login` 调用登录接口，成功后保存凭据，回到登录前想去的页面；接口的字段错误显示在输入框下面，其他错误在表单上方 |
| `RouteError` | 交给 `createApp` 的 `router.defaultErrorComponent`：缺少权限时显示 403，其他错误显示说明和重试按钮，都在上级布局之中 |
| `NotFoundPage`、`ForbiddenPage` | 404、403 |
| `UserMenu` | 头像和名称，下拉中是颜色模式和退出。退出前，标签中有未保存的修改时先询问；`logout` 调用应用的登出接口 |
| `TenantSwitcher` | 当前租户和切换，见[租户](app-shell-features.md#tenants)；只有一个租户时只显示名称 |
| `NewVersionNotice` | 发布新版本后提示打开着的页面刷新，放在根路由中一次 |
| `TabBar` | 标签栏：右键菜单（刷新、固定、关闭、关闭其他、关闭右侧、关闭全部）、拖动排序、未保存的标记。`AdminLayout` 的 `tabs` 开启时自动放置 |

表单用 antd 的 `Form`，校验规则用 [`antdRule`](app-shell-features.md#forms)，与服务端相同；列表用 ProTable，查询条件保存在地址中，见[列表](app-shell-features.md#lists)。

<a id="features"></a>
## 通知、后台任务与 AI 对话

框架功能的界面，逻辑见应用骨架的[通知](app-shell-features.md#notifications)、[后台操作](app-shell-features.md#operations)和 [AI 对话](app-shell-features.md#ai-chat)：

```tsx
<Fill name="actions">
  <NotificationBell source={notifications} />
  <OperationCenter />
  <AssistantButton api="/api/v1/assistant/chat" id={conversationId} toolTitles={{ refund_order: "退款" }} />
</Fill>
```

| 组件 | 说明 |
| --- | --- |
| `NotificationBell` | 铃铛，角标是未读数；点开是通知列表、“全部已读”和“加载更多”。点击一条通知标记已读并打开它的 `href`，`onOpen` 可以改为自己处理 |
| `OperationCenter` | 任务中心：角标是进行中的操作数，点开是 `trackOperation` 登记的操作和进度，结束的可以移除。没有登记过操作时不显示 |
| `OperationProgress` | 页面中一个操作的进度：`<OperationProgress id={op.id} />` |
| `AssistantChat` | AI 对话（[Ant Design X](https://x.ant.design)）：回答用 Markdown 渲染（`@ant-design/x-markdown`），思考过程可以展开，工具调用列出参数和结果；需要审批的调用显示“批准”和“拒绝”，等待审批时不能发送；回答进行中输入的内容插入这次回答，停止按钮停止回答；插话显示为单独的用户消息 |
| `AssistantButton` | 顶栏的按钮，点开是侧边的 `AssistantChat` |

`AssistantChat` 和 `AssistantButton` 在单独的入口 `@go-tenon/admin/chat` 中，用到时安装：

```sh
bun add ai@^7.0.159 @ai-sdk/react@^4.0.163 @ant-design/x@^2.10.0 @ant-design/x-markdown@^2.10.0
```

`toolTitles` 是工具的显示名称；应用自己的 `data-*` 片段用 `renderPart` 渲染。

<a id="agents"></a>
## 给 AI 编程助手的约定

模板带着让 AI 编程助手照着写的约定：

- `AGENTS.md`：只用 antd 和 ProComponents；页面、接口、表单、列表、权限的写法；写完运行 `bun run typecheck` 和 `bun run lint`。
- `biome.json`：`bun run lint` 拒绝导入 shadcn、Radix、Tailwind、lucide，提示改用 antd。
- 项目根目录的 `.mcp.json` 加上 antd 的 MCP 服务（`@ant-design/cli`，版本与 antd 相同），可以查询组件的属性、令牌和示例；已有的服务不受影响。

<a id="troubleshooting"></a>
## 常见问题

**登录后接口都返回 404**

模板按约定的路径调用登录相关的接口（`/api/auth/login`、`/api/auth/refresh`、`/api/auth/logout`、`/api/me`）。应用的路径不同时，修改 `src/auth.ts`，见[模板里有什么](#template)。

**打开 `/admin/` 得到 404**

前端还没有挂载到路由上。按 `tenon add frontend` 打印的代码挂载，见[挂载](spa.md#mounting)。

**菜单中的图标没有显示**

路由元数据中的 `icon` 名字没有登记。默认只登记了常用的十几个，用 `icons` 加上其他的，见[全局配置](#provider)。

<a id="api-reference"></a>
## API 参考

| 名称 | 说明 |
| --- | --- |
| `AdminProvider`、`useAdmin` | 全局配置；颜色模式、图标 |
| `AdminLayout`、`RailLayout`、`CenteredLayout`、`SettingsLayout`、`FullscreenLayout`、`AuthLayout` | 布局 |
| `TabBar`、`Crumbs`、`NavLink`、`CollapseButton` | 自定义布局用的部件 |
| `LoginPage`、`RouteError`、`NotFoundPage`、`ForbiddenPage` | 页面 |
| `UserMenu`、`TenantSwitcher`、`NewVersionNotice` | 顶栏的部件 |
| `NotificationBell`、`OperationCenter`、`OperationProgress`、`OperationProgressBar` | 通知和后台任务 |
| `AssistantChat`、`AssistantButton` | `@go-tenon/admin/chat` 中的 AI 对话 |
| `ResourcePage`、`ResourceForm`、`FieldValue`、`defaultInput` | [资源](resources.md#admin)的页面 |
| `defaultIcons` | 默认登记的图标 |
