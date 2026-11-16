# 应用骨架：页面中的常用功能

[应用骨架](app-shell.md)一页介绍了怎样创建应用、要求登录、按权限进入页面，以及菜单和布局。这一页介绍页面中常用的几样功能，它们彼此独立，用到哪个读哪一节：

- **多标签**：像浏览器一样，在后台中同时打开几个页面；
- **租户**：一个账号属于多个组织时，切换当前的组织；
- **表单**和**列表**：提交表单、显示服务端的错误，列表的筛选、排序和分页；
- **通知**、**后台操作**、**操作日志**、**AI 对话**：接上框架对应的服务端功能。

阅读前需要了解：[应用骨架](app-shell.md)；[在 React 中调用接口](client-react.md)。

- [多标签](#tabs)
- [租户](#tenants)
- [表单](#forms)
- [列表](#lists)
- [通知](#notifications)
- [后台操作](#operations)
- [操作日志](#activity)
- [AI 对话](#ai-chat)

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

表单在前端用生成的 `schemas.ts` 和 `messages.ts` 校验：规则、提示的文字都与服务端相同，见[校验规则与验证提示](client-react.md#validation-rules)。服务端返回的字段错误显示在对应的输入框下面。shell 不提供表单组件，只对接 react-hook-form 和 antd Form。

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

**自动保存。** 设置这类页面可以不要“保存”按钮，修改后自动保存。`createAutoSaver(initial, { save, validate, delay })` 是与表单库无关的部分：把值的变化交给 `change`，它在用户停下 `delay`（默认 600 毫秒）后保存；校验不通过、与上次保存成功的值相同时不保存；保存一次只进行一个，期间的变化在完成后再保存一次；`stop` 立即保存还在等待的变化，`discard` 放弃它们。`status()` 和 `subscribe` 给出状态（`pending`、`saving`、`saved`、`error`）。shadcn 外观的 `useAutoSave` 用它接上 react-hook-form，见 [shadcn registry](registry.md#blocks)；antd Form 可以在 `onValuesChange` 中调用 `change`。

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

<a id="activity"></a>
## 操作日志

`useActivity(source)` 是[操作日志](../features/activity.md)的模型：一个对象的历史，或者一个人的操作记录，从新到旧分页读取（`items`、`hasMore`、`loadMore`、`refresh`）。接口由应用定义，每一条是服务端翻译好的文字、操作人和时间，见[在页面中显示](../features/activity.md#history-page)：

```ts
const history = useActivity({ key: ["article", id], list: (before) => api.article.history(id, { before }).then((r) => r.items) })
```

`key` 区分不同的历史；缓存按当前身份区分。两套外观的 `ActivityTimeline` 用它渲染时间线。

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

<a id="troubleshooting"></a>
## 常见问题

**切换租户后，页面上还是上一个租户的数据**

创建 `queries` 时没有传 `scope: session.scope`，缓存的 key 中没有租户，见[在 React 中调用接口](client-react.md#tanstack)。

**表单提交后，服务端返回的字段错误没有显示在输入框下面**

在 `onError` 中把错误交给 `setServerErrors`，它把 `422` 的字段错误设置到对应的字段上。还要检查表单字段的名字是否与接口请求体中的字段名一致，嵌套字段用点连接，例如 `items.0.name`，见[表单](#forms)。

**列表的筛选条件刷新页面后丢失了**

筛选条件保存在了组件的状态（例如 `useState`）中。改用 `listSearch` 和 `useList`，条件保存在地址中，刷新、前进后退、分享链接时都能保留，见[列表](#lists)。
