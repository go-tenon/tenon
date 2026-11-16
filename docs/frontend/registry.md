# shadcn registry 与控制台

用户端、客户后台这类需要品牌感的前端用 [shadcn](https://ui.shadcn.com) 和 Tailwind CSS 编写。shadcn 的组件不是依赖，而是复制进项目的源码，可以随意修改。框架按同样的方式提供一组组件：tenon 的 shadcn registry，包括布局、标签栏、用户菜单、⌘K、资源表格、表单和对话框。逻辑都来自[应用骨架](app-shell.md)，组件只负责用 shadcn 渲染；运营后台的 antd 外观见[运营后台](admin.md)。

阅读前需要了解：[应用骨架](app-shell.md)；[shadcn](https://ui.shadcn.com) 的基本用法：组件复制进项目，用 Tailwind 的类名写样式。

- [新建控制台](#creating)
- [加入其他组件](#adding)
- [组件](#blocks)
- [给 AI 编程助手的约定](#agents)
- [常见问题](#troubleshooting)

<a id="creating"></a>
## 新建控制台

```bash
go tool tenon add frontend console --template console
```

它在 `apps/console` 中生成一个前端：Vite、Tailwind CSS v4、shadcn（Radix 版本，`nova` 风格），带登录页、布局（默认是内嵌式侧栏，`--layout` 可以选择其他的）、多标签、用户菜单和 ⌘K。下面[组件](#blocks)表中布局到登录表单的条目，连同它们用到的 shadcn 组件（`src/components/ui`）和 tenon 的组件（`src/components/tenon`），从框架复制进来，不需要联网，之后可以随意修改。通知、Markdown 和 AI 对话需要其他 npm 包，用到时再[加入](#adding)。然后按提示在路由中挂载它（见[单页应用](spa.md#mounting)）。

```text
apps/console/
├── AGENTS.md              给 AI 编程助手的约定
├── biome.json             bun run lint：禁止导入 antd
├── components.json        shadcn 的配置：别名 @/components、@/lib
└── src/
    ├── main.tsx           先加载 shadcn 的样式，再加载设计令牌的 tokens.css
    ├── auth.ts            登录、刷新、登出和当前用户的接口
    ├── layouts.ts         可以使用的布局，第一种是默认的
    ├── components/
    │   ├── ui/            shadcn 的组件
    │   └── tenon/         tenon registry 的组件
    └── routes/            login.tsx、_app.tsx（按当前布局渲染）、_app/index.tsx、_app/layouts.tsx（布局页面）
```

品牌色、圆角来自项目的[设计令牌](theme.md)：`main.tsx` 在 shadcn 的默认变量之后加载 `@<项目>/theme/tokens.css`，令牌中写了的变量覆盖默认值，暗色的值随 `.dark` 切换。登录相关的接口、`layouts.ts` 和布局页面的用法与运营后台的模板相同，见[模板里有什么](admin.md#template)。

<a id="adding"></a>
## 加入其他组件

registry 随框架分发：`tenon dev`、`generate` 等命令把框架的前端包复制到 `.tenon/js`，其中 `.tenon/js/registry/r/` 是 registry 的条目。用 shadcn 的命令加入，它同时加入条目用到的 shadcn 组件和 npm 包：

```bash
bunx shadcn@4.23.0 add ./.tenon/js/registry/r/tenon-rail-layout.json
```

升级框架后，条目也随之更新；已经加入项目的组件是项目自己的代码，不会被覆盖，需要时用 `--overwrite` 重新加入。

<a id="blocks"></a>
## 组件

| 条目 | 组件 | 说明 |
| --- | --- | --- |
| `tenon-sidebar-layout` | `SidebarLayout` | 侧栏可收起为图标，顶栏在内容上方；`variant` 为 `inset`（内嵌式，内容区是浮起的卡片）或 `floating`；`tabs` 开启多标签 |
| `tenon-topnav-layout` | `TopNavLayout` | 导航在顶栏，内容居中或全宽；窄屏时导航收进抽屉，`narrowNav="bottom"` 时放到底部标签栏 |
| `tenon-mixed-layout` | `MixedLayout` | 第一级菜单（模块）在顶栏，当前模块下的菜单在侧栏，对应运营后台的 `variant="mix"`；窄屏时侧栏变为抽屉，显示完整的菜单 |
| `tenon-bottom-nav` | `BottomNav` | 底部标签栏：窄屏时第一级菜单固定在屏幕底部，最多 5 项。`TopNavLayout` 的 `narrowNav="bottom"` 使用它，其他布局也可以直接放置 |
| `tenon-rail-layout` | `RailLayout` | 最左侧的图标栏切换模块，旁边是模块内的导航 |
| `tenon-page-layouts` | `CenteredLayout`、`SettingsLayout`、`FullscreenLayout`、`AuthLayout` | 居中窄栏、设置、全屏、登录 |
| `tenon-tab-bar` | `TabBar`、`LayoutContent` | 标签栏：菜单（刷新、固定、关闭）、拖动排序、未保存的标记 |
| `tenon-user-menu`、`tenon-color-mode` | `UserMenu`、`ColorModeProvider` | 用户菜单（颜色模式、退出前询问）；颜色模式保存在本地存储中 |
| `tenon-tenant-switcher` | `TenantSwitcher` | 租户切换，见[租户](app-shell-features.md#tenants) |
| `tenon-command-menu` | `CommandMenu` | ⌘K 搜索并打开菜单中的页面 |
| `tenon-dialogs` | `ConfirmProvider`、`useConfirm`、`NewVersionNotice` | 返回 Promise 的确认对话框；新版本提示 |
| `tenon-page` | `PageHeader`、`PageLoading`、`PageError`、`PageEmpty`、`SidePanel` | 页头、加载、出错、空状态、右侧抽屉 |
| `tenon-split-view` | `SplitView` | 列表加详情的分栏：分隔条可以拖动或用方向键调整，宽度按账号保存；窄屏时只显示一侧 |
| `tenon-data-table` | `DataTable`、`ListToolbar`、`RowActions`、`StatusBadge` | 资源表格：服务端分页、排序和搜索保存在地址中，批量选择，行操作 |
| `tenon-form` | `useApiForm`、`Field`、`FormError`、`useAutoSave`、`AutoSaveStatus` | react-hook-form 加上生成的校验规则，服务端的字段错误显示在输入框下面；自动保存 |
| `tenon-login-form` | `LoginForm` | 登录表单 |
| `tenon-resource-page` | `ResourcePage`、`ResourceForm`、`FieldValue` | 按[资源](resources.md#console)的定义渲染列表、筛选、表单和操作，console 模板已经带着 |
| `tenon-notifications` | `NotificationBell`、`OperationCenter`、`OperationProgress` | 通知铃铛（未读数、列表、标记已读）、后台任务的进度，见[通知](app-shell-features.md#notifications)和[后台操作](app-shell-features.md#operations) |
| `tenon-activity` | `ActivityTimeline` | [操作日志](../features/activity.md#history-page)的时间线：从新到旧分页，代为登录时注明实际操作的人 |
| `tenon-settings-form` | `SettingsForm` | [设置页面](resources.md#settings)：按 `api.Settings` 的声明渲染一组设置的表单，“保存”按钮或自动保存 |
| `tenon-transfer` | `ImportButton`、`ExportButton` | [后台导入](../features/background-imports.md#import-button)和[后台导出](../features/background-exports.md#export-button)：上传、进度、结果，下载没有导入的行或导出的文件 |
| `tenon-markdown` | `Markdown` | 消息的 Markdown：[Streamdown](https://streamdown.ai) 渲染流式输出，代码块用 shadcn 的 Code Block，带语法高亮和复制按钮 |
| `tenon-assistant-chat` | `AssistantChat`、`AssistantButton` | AI 对话，见下文 |

布局按[布局约定](app-shell.md#layouts)实现，插槽的位置与运营后台相同：`brand` 在侧栏顶部，`search`、`actions`、`tenant` 在顶栏右侧，`user` 在侧栏底部。路由元数据中的 `icon` 是名字，映射在 `src/components/tenon/nav.tsx` 的 `icons` 中，名字与运营后台相同，同一份路由在两套外观中都有图标。

资源表格的状态来自 [`useList`](app-shell-features.md#lists)：

```tsx
const list = useList(Route)
const articles = useQuery({ queryKey: ["article", "list", list.params], queryFn: () => api.article.list(list.params) })
const selection = useSelection<string>(list.params)

<DataTable
  columns={columns}
  data={articles.data?.items}
  total={articles.data?.total}
  list={list}
  error={articles.error}
  selection={selection}
  rowActions={(a) => [{ label: "删除", destructive: true, onSelect: () => remove(a) }]}
  toolbar={<ListToolbar list={list} />}
/>
```

点击列头按这一列排序（列的 `id` 就是排序参数），工具栏的搜索输入停顿后才更新地址中的 `q`。

列定义的类型是 `DataTableColumn<T>`，即 [TanStack Table](https://tanstack.com/table) v9 的 `ColumnDef`，功能集固定为表格用到的排序和行选择：

```tsx
const columns: DataTableColumn<Article>[] = [
  { id: "title", accessorKey: "title", header: "标题" },
  { id: "status", header: "状态", enableSorting: false, cell: ({ row }) => <StatusBadge>{row.original.status}</StatusBadge> },
]
```

表单用 `useApiForm`：

```tsx
const { form, onSubmit, formError, pending } = useApiForm<ArticleChanges>({
  schema: schemas.article.create.body,
  rules: { components, messages, language: "zh-CN" },
  submit: (values) => api.article.create(values),
})

<form onSubmit={onSubmit}>
  <FormError error={formError} />
  <Field form={form} name="title" label="标题">{(p) => <Input {...p} />}</Field>
  <Button type="submit" disabled={pending}>保存</Button>
</form>
```

前端按生成的规则校验，提示与服务端相同；有未保存的修改时离开前询问，保存成功后不再询问。

设置这类页面适合自动保存，不需要“保存”按钮。`useAutoSave` 在值变化、用户停下 600 毫秒后保存：校验不通过时不保存；与上次保存成功的值相同时不保存；保存一次只进行一个，期间的修改在完成后再保存一次；离开页面时立即保存还在等待的修改。服务端返回的字段错误同样显示在输入框下面：

```tsx
const form = useForm<SiteSettings>({ resolver: resolver(schemas.settings.update.body, rules), defaultValues })
const autosave = useAutoSave(form, {
  schema: schemas.settings.update.body,
  rules,
  save: (values) => api.settings.update(values),
})

<PageHeader>
  <AutoSaveStatus status={autosave.status} />
</PageHeader>
```

`AutoSaveStatus` 显示“有未保存的修改”“保存中…”“已保存”“保存失败”。数据重新加载后调用 `autosave.markSaved(values)`，表单之外的改动（例如调整了成员）用 `autosave.saveNow(true)` 立即保存。

列表加详情的页面用 `SplitView`：左侧是列表，右侧是选中项的详情，通常是子路由的 `Outlet`。窄屏时只显示一侧，选中了一项时显示详情：

```tsx
function Articles() {
  const { id } = useParams({ strict: false })
  return (
    <SplitView id="articles" pane={<ArticleList />} narrow={id ? "main" : "pane"}>
      {id ? <Outlet /> : <PageEmpty>选择一篇文章</PageEmpty>}
    </SplitView>
  )
}
```

左侧的宽度由 `@go-tenon/shell` 的 `useResizable` 管理，按账号保存；分隔条可以拖动，也可以聚焦后用方向键、Home、End 调整，双击恢复默认宽度。

<a id="assistant-chat"></a>
AI 对话按 shadcn/ui 的 [AI Elements](https://ui.shadcn.com/docs/ai-elements) 组合：消息列表是 Message Scroller，用户的消息是 Bubble，思考过程和工具调用是 Trace，需要审批的工具调用是 Confirmation（Item 加上“批准”“拒绝”），输入框是 Prompt Input（InputGroup）。逻辑来自 [`@go-tenon/shell/chat`](app-shell-features.md#ai-chat)：

```bash
bunx shadcn@4.23.0 add ./.tenon/js/registry/r/tenon-assistant-chat.json  # 同时安装 ai、@ai-sdk/react、streamdown
```

```tsx
<Fill name="actions">
  <NotificationBell source={notifications} />
  <OperationCenter />
  <AssistantButton api="/api/v1/assistant/chat" id={conversationId} toolTitles={{ refund_order: "退款" }} />
</Fill>
```

- 回答用 `Markdown` 渲染；插话显示为单独的用户消息。
- 回答进行中输入的内容插入这次回答，停止按钮停止回答。
- 等待审批时不能发送，批准或拒绝后回答继续。
- 回形针按钮、粘贴或把文件拖进对话都可以添加附件；待发送的图片显示缩略图，可以逐个移除，只有附件也可以发送。发送后附件显示在用户的消息中。回答进行中不能添加附件。`attachments` 设置类型、大小和上传方式，`false` 时不能添加附件，见[附件](app-shell-features.md#chat-attachments)。

确认对话框：`ConfirmProvider` 放在根路由中，之后 `const confirm = useConfirm()`。它可以交给多标签和未保存修改的询问，代替浏览器的确认框：

```tsx
<SidebarLayout title="控制台" tabs={{ confirm: (message) => confirm(message) }} />
```

<a id="agents"></a>
## 给 AI 编程助手的约定

- `AGENTS.md`：用 shadcn 和 Tailwind；页面、接口、列表、表单、权限的写法；颜色用语义类，不写死颜色值；写完运行 `bun run typecheck` 和 `bun run lint`。
- `biome.json`：`bun run lint` 拒绝导入 antd。
- 项目根目录的 `.mcp.json` 加上 shadcn 的 MCP 服务（版本与模板中的 shadcn 相同）；已有的服务不受影响。

<a id="troubleshooting"></a>
## 常见问题

**升级框架后，项目中的组件没有更新**

加入项目的组件是项目自己的代码，不会被覆盖。需要新版本时，用 `--overwrite` 重新加入，再对照自己的修改，见[加入其他组件](#adding)。

**删除、批量操作时没有弹出确认框**

根路由中没有放 `ConfirmProvider`。

**品牌色没有生效**

`main.tsx` 要在 shadcn 的默认变量之后加载项目主题包的 `tokens.css`，见[新建控制台](#creating)和[设计令牌](theme.md)。
