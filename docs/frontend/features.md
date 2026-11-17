# 后台功能

框架的几项服务端功能在前端有现成的部分：通知、后台操作、操作记录、导入与导出和队列面板各有一个组件，放进页面就能用；AI 对话目前只有逻辑（`@go-tenon/ui/chat` 的 hook），对话界面由应用自己画。每项功能的接口由应用定义，把生成的客户端的方法交给组件即可。它们彼此独立，用到哪个读哪一节。

阅读前需要了解：[搭建前端应用](app.md)；[布局与导航](layout.md)。

- [通知](#notifications)
- [后台操作](#operations)
- [操作记录](#activity)
- [导入与导出](#transfer)
- [队列面板](#queue-panel)
- [AI 对话](#ai-chat)
    - [附件](#chat-attachments)
    - [输入框](#chat-composer)
- [API 参考](#api-reference)

<a id="notifications"></a>
## 通知

`NotificationsMenu` 是顶栏的铃铛：显示未读数，弹出最近的[通知](../features/notifications.md)，点击打开并标记已读。通知的接口由应用定义，把生成的客户端的方法交给它：

```tsx
const inbox: NotificationsSource = {
  list: (before) => api.notification.index({ before }).then((l) => l.map(toItem)),
  markRead: (ids) => api.notification.read({ ids }),
  markAllRead: () => api.notification.readAll(),
  badge: (o) => api.notification.inbox(o), // 未读数的事件流，见“实时推送”
}

<AppLayout actions={<NotificationsMenu source={inbox} />} … />
```

- 未读数来自事件流的 `badge` 事件（`{ "unread": 3 }`），断开后自动重连（等待时间从 1 秒加倍到 30 秒）；每次收到都重新读取列表。没有事件流时传 `unread`，每 60 秒读取一次（`pollInterval`）。
- 每一条是 `{ id, title, body?, read, created_at, href? }`，`title` 是服务端按语言生成的文案，`href` 是点击后打开的页面。
- 标记已读先在界面上标记，再调用接口；已读的通知不再调用。
- 列表按身份隔离：切换租户或换了账号后读取新身份的通知。

自己画通知列表（例如一个“全部通知”页面）时用 `useNotifications(source)`：`{ unread, items, hasMore, loadMore, markRead, markAllRead }`。

<a id="operations"></a>
## 后台操作

[后台操作](../features/operations.md)发起后登记到操作中心，顶栏的 `OperationsMenu` 显示它们的进度：有进行中的任务时显示转动的图标，没有任务时不显示。

```ts
const op = await api.article.bulkPublish({ ids })
trackOperation(app, {
  id: op.id,
  title: `批量发布 ${ids.length} 篇文章`,
  open: (o) => api.article.bulkPublishProgress(op.id, o),
})
```

- 进度用 `@go-tenon/client` 的 `watchOperation` 读取，断开后自动重连，操作结束时停止。
- 同一个 ID 只登记一次。结束后仍留在操作中心，`dismissOperation(app, id)` 移除；退出登录、切换身份时清空。
- 页面中显示进度时用 `useOperation(id)`，全部操作用 `useOperations()`（新的在前）。每一项有 `title`、最新的进度 `operation`、比例 `ratio`（不知道总数时为空）、`finished` 和读取失败时的 `error`。
- [导入与导出](#transfer)的按钮自动登记，不用自己调用 `trackOperation`。

<a id="activity"></a>
## 操作记录

`ActivityTimeline` 按时间从新到旧显示一个对象的历史或一个人的[操作记录](../features/activity.md)，到底时“加载更多”。`list(before)` 是应用自己的接口，每一条是服务端翻译好的文字、操作人和时间，见[在页面中显示](../features/activity.md#history-page)：

```tsx
<ActivityTimeline source={{ key: ["article", id], list: (before) => api.article.history(id, { before }).then((r) => r.items) }} />
```

- `key` 区分不同的历史；缓存按当前身份区分。
- 点击带 `href` 的记录打开它；`onOpen` 可以换成自己的处理。
- 自己画时用 `useActivity(source)`：`{ items, hasMore, loadMore, refresh }`。

<a id="transfer"></a>
## 导入与导出

`ImportButton` 和 `ExportButton` 是[后台导入](../features/background-imports.md)和[后台导出](../features/background-exports.md)的按钮和对话框：

```tsx
import { ExportButton, ImportButton } from "@go-tenon/ui"

<ImportButton source={articleImport} template={downloadTemplate} onCompleted={() => list.refetch()} />
<ExportButton source={{ start: () => api.article.exportStart({ format: "xlsx" }), progress }} />
```

- 导入：对话框中点击选择或拖入表格，依次上传、发起导入、显示进度；结束后显示成功和没有导入的行数，可以下载没有导入的行，改正后重新导入。失败时显示原因，可以重新选择文件。
- 导出：点击后在后台导出，对话框显示进度，完成后下载。
- 进度同时出现在顶栏的 `OperationsMenu` 中，关闭对话框不影响导入导出。
- 资源页面中，把它们放在 `ResourcePage` 的 `toolbar` 中。

自己画时用 `useImport(source)`、`useExport(source)`。它们给出 `phase`（`idle`、`uploading`（只有导入）、`starting`、`running`、`completed`、`failed`）、`operation`（其中 `total`、`processed`、`failed`）、上传和发起时的 `error`、导入的 `uploaded`（上传的比例）和 `downloadFailures`、导出的 `file`，以及回到 `idle` 的 `reset`。

<a id="queue-panel"></a>
## 队列面板

`QueuePanel` 是[队列的运维接口](../background/queue-monitoring.md#queue-api)（`queue/queueapi`）的页面。它接收应用生成的客户端分组 `api.queue`，放在一个需要运维权限的路由上：

```tsx title="src/routes/_app/queue.tsx"
import { createFileRoute } from "@tanstack/react-router"
import { QueuePanel } from "@go-tenon/ui"
import { api } from "../../auth"

export const Route = createFileRoute("/_app/queue")({
  staticData: { title: "队列", icon: "list", permission: "queue:manage" },
  component: () => <QueuePanel api={api.queue} />,
})
```

页面分成三个[页内标签](layout.md#page-tabs)：

- **任务类型**：每个 topic 的状态、等待中、处理中、重新投递、空闲 Worker、失败的任务数和最近处理的时间；没有处理程序、停滞的 topic 在状态下面说明原因。正常和停滞的 topic 可以暂停一小时（先确认），暂停的可以恢复；应用没有设置 `queueapi.Options.Admin` 时不显示这两个按钮。
- **失败任务**：标签上是失败的条数。按任务类型筛选（类型不多时并排，多了是下拉选择）、分页。每一行有“重试”，“更多”菜单中是查看详情和删除，都先确认；勾选几行可以一起重试或删除；右边的按钮写明一次处理的条数（“重试全部 23 个”“重试这 12 个”），删除在旁边的菜单中。点击一条，在对话框中显示完整的错误和堆栈、元数据（例如 `request_id`）和任务内容。
- **系统状态**：复制槽（PostgreSQL）和任务流（JetStream）两张[描述列表](lists-forms.md#description-list)并排，右上角是各自的状态（圆点和文字）；列出复制槽名、WAL 状态、积压、Worker 进程，以及任务流的消息、定时投递、副本等，给开发人员排查问题用。

数据和其他列表一样按需读取：打开页面、切换标签和筛选、做完操作、切回浏览器窗口时读取，不定时刷新；要立即刷新，用标签栏的刷新按钮。

复制槽不存在或失效、没有 Worker 在转发、积压过多、读不到 JetStream 时，标签上面显示醒目的提示和处理办法；平时不显示。

`api.queue` 的形状由 `QueueApi` 描述，不依赖具体的应用；字段与 `queue/queueapi` 的类型一致。

<a id="ai-chat"></a>
## AI 对话

`@go-tenon/ui/chat` 在 Vercel AI SDK 的 `useChat` 之上接好[对话接口](../ai/chat.md#conversation-api)：消息流、历史、断线续传、停止、插话和审批。框架还没有现成的对话界面，消息列表、输入框由应用用 Mantine 自己画，逻辑都在这些 hook 中。

它是单独的入口，用到时在前端安装 AI SDK：

```sh
bun add ai@^7.0.159 @ai-sdk/react@^4.0.163
```

```tsx
import { useAssistantChat, useAssistantHistory } from "@go-tenon/ui/chat"

function Assistant({ id }: { id: string }) {
  const history = useAssistantHistory({ api: "/api/v1/assistant/chat", id })
  if (!history.data) return null
  return <Conversation id={id} messages={history.data} />
}

function Conversation({ id, messages }: { id: string; messages: UIMessage[] }) {
  const chat = useAssistantChat({ api: "/api/v1/assistant/chat", id, messages })
  // chat.messages、chat.status、chat.busy、chat.pending、chat.send(text, files)、chat.stop()、chat.respond(id, approved, reason)
}
```

- 先读取历史，再创建对话：`useChat` 用历史作为初始消息，进行中的回答随即接上（`resume`）。
- 请求同源发送会话 cookie，`401` 时会话变为未登录；发送时只发最后一条消息。
- `send`：没有回答进行时发送新消息；回答进行中时[插话](../ai/chat.md#steering)，回答恰好已经结束（`409 ai.finished`）时改为发送新消息。服务端没有收下新消息时，`send` 把它从 `messages` 中撤回并抛出错误，调用方可以把内容放回输入框。
- `stop`：调用停止接口，服务端取消这次回答（客户端断开连接不会停止它）。
- `pending` 是等待[审批](../ai/chat.md#approval)的工具调用（`{ id, toolName, input }`），`respond` 批准或拒绝；全部有了决定后自动发送，回答继续。
- 插话在回答中是一个 `data-steering` part（`data` 是 `{ text }`），界面把它显示为一条用户消息。

<a id="chat-attachments"></a>
### 附件

`send(text, files)` 的 `files` 是附件，和文字一起交给模型，模型直接看图、读 PDF，见[发送图片和文件](../ai/chat.md#files)。可以只有附件、没有文字。附件按 `useAssistantChat` 的 `attachments` 检查：

```tsx
const chat = useAssistantChat({
  api: "/api/v1/assistant/chat",
  id,
  messages,
  attachments: { accept: "image/*,.pdf", maxFileSize: 4 << 20 },
})
```

| 选项 | 说明 | 默认 |
| --- | --- | --- |
| `accept` | 允许的类型，与 `<input accept>` 的写法相同 | `image/*,application/pdf` |
| `maxFileSize` | 单个文件的最大字节数 | 4 MiB |
| `maxTotalSize` | 一条消息中文件的总字节数，设置了 `upload` 时不检查 | 5 MiB |
| `maxFiles` | 一条消息最多带几个文件 | 5 |
| `upload` | `(file) => Promise<string>`：上传文件，返回模型服务能下载的链接，消息中只放链接 | 不设置，以 data URL 发送 |

- 不设置 `upload` 时，文件以 data URL 随消息发送。data URL 比文件大约三分之一，默认限制下请求体最多约 7 MiB，服务端的[请求体上限](../http/requests.md#body-size)要放宽到 8 MiB；调整了 `maxTotalSize` 时相应调整。
- 设置了 `upload` 时，请求体和对话存储都只有链接，适合较大的文件，例如用 `@go-tenon/client` 的 `uploadFile` [直传到 S3](../libraries/direct-uploads.md) 后签发一个读取地址。链接由模型服务下载，必须能从公网访问。
- `attachments: false` 时不能发送附件。
- 附件不符合限制、回答进行中带附件（插话只能是文字）或者不能发送附件时，`send` 抛出 `AttachmentError`，它的 `message` 可以直接显示，`reason` 是 `type`、`size`、`total`、`count`、`busy` 或 `disabled`。

<a id="chat-composer"></a>
### 输入框

`useAssistantComposer(chat)` 是输入框的逻辑，界面只负责显示：

```tsx
const chat = useAssistantChat({ api, id, messages, attachments: { accept: "image/*" } })
const composer = useAssistantComposer(chat)

<Textarea value={composer.text} onChange={(e) => composer.setText(e.currentTarget.value)} disabled={composer.awaiting} />
<Button disabled={!composer.canSend} onClick={() => void composer.submit()}>发送</Button>
{composer.error && <Alert color="red">{composer.error}</Alert>}
```

- `text`、`setText`：输入的文字。
- `attachments`：待发送的附件（`useAttachments`），选择、粘贴或拖入文件时 `add`，`remove` 移除一个；`canAttach` 为 `false` 时（`attachments: false`）不显示添加附件的入口。不符合限制的文件不加入，`attachments.error` 是原因；`items` 中图片的 `preview` 是预览地址，移除和卸载时释放。
- `canSend`：有文字或附件、没有等待审批的工具调用，并且回答进行中时只有文字。`awaiting` 表示有等待审批的工具调用。
- `submit`：发送并清空输入框。消息没有发出去时恢复文字和附件，原因在 `error` 中：附件不符合限制、上传失败、插话失败，或者服务端没有收下消息（响应不是 2xx，例如 `409` 对话正在回答，或者请求根本没有到达）。服务端收下之后才出错的（例如回答中途断线）不恢复：消息已经在对话中，回答在服务端继续，断线后接上。
- `error`：附件、发送或对话出错的说明，可以直接显示。

`fileParts(message.parts)` 取出消息中的文件片段（`{ mediaType, filename, url }`），用于显示已经发送的附件。

<a id="api-reference"></a>
## API 参考

| 名称 | 说明 |
| --- | --- |
| `NotificationsMenu`、`useNotifications`、`NotificationsSource` | 通知：顶栏的铃铛；未读数、列表、标记已读 |
| `OperationsMenu`、`trackOperation`、`dismissOperation`、`clearOperations`、`useOperations`、`useOperation` | 后台操作的进度 |
| `ActivityTimeline`、`useActivity` | 操作记录：时间线；从新到旧分页读取 |
| `ImportButton`、`ExportButton`、`useImport`、`useExport` | 后台导入与导出 |
| `QueuePanel`、`QueueApi` | 队列面板和它需要的接口 |
| `useAssistantChat`、`useAssistantHistory`、`useAssistantComposer`、`useAttachments`、`pendingApprovals`、`fileParts`、`AttachmentError` | `@go-tenon/ui/chat` 中的 AI 对话 |
