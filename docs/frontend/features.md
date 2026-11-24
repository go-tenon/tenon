# 后台功能的前端

通知、后台操作、操作记录、导入与导出、队列面板和定时任务面板在 `@go-tenon/ui` 中各有现成的组件；AI 对话只有逻辑（`@go-tenon/ui/chat` 的 hook），界面由应用用 Mantine 自己写。服务端的用法见各节链接的页面。

包：`@go-tenon/ui`。

- 每项功能的接口由应用定义，组件接收生成的客户端的方法，`api` 是生成的客户端。
- 缓存按当前身份区分，退出登录、切换租户后读取新身份的数据。

<a id="notifications"></a>
## 通知：`NotificationsMenu`

`NotificationsMenu` 是顶栏的铃铛：显示未读数，弹出最近的[通知](../features/notifications.md)，点击打开并标记已读。它和 `OperationsMenu` 放在 `FrameLayout` 的 `actions` 中：

```tsx
const inbox: NotificationsSource = {
  list: (before) => api.notification.index({ before }), // 一页的数组，每条 { id, title, body?, read, created_at, href? }
  markRead: (ids) => api.notification.read({ ids }),
  markAllRead: () => api.notification.readAll(),
  badge: (o) => api.notification.inbox(o), // 未读数的事件流
}

<FrameLayout actions={<><NotificationsMenu source={inbox} /><OperationsMenu /></>} … />
```

- `badge` 是[推送角标的事件流](../features/notifications.md#realtime-badge)，断开后自动重连。没有事件流时传 `unread: () => Promise<number>`，每 60 秒读取一次（`pollInterval`）。
- 自己写通知列表时用 `useNotifications(source)`：`{ unread, items, hasMore, loadMore, markRead, markAllRead }`。

<a id="operations"></a>
## 后台操作的进度：`trackOperation`

[后台操作](../features/operations.md)发起后用 `trackOperation` 登记，顶栏的 `OperationsMenu` 显示进度，没有操作时不显示。`app` 是 `useApp()` 或 `createApp` 的返回值：

```ts
const op = await api.article.bulkPublish({ ids })
trackOperation(app, {
  id: op.id,
  title: `批量发布 ${ids.length} 篇文章`,
  open: (o) => api.article.bulkPublishProgress(op.id, o),
})
```

- 同一个 ID 只登记一次。结束的操作留在列表中，`dismissOperation(app, id)` 移除；退出登录、切换身份时清空。
- 页面中用 `useOperation(id)`、`useOperations()` 显示进度：`operation` 是最新的进度，`ratio` 是比例（不知道总数时为空），另有 `finished`、`error`。
- [导入与导出](#transfer)的按钮自动登记。

<a id="activity"></a>
## 操作记录：`ActivityLog`

`ActivityLog` 从新到旧以固定分页表格显示一个对象的历史、一个人的操作记录或整个租户的[操作记录](../features/activity.md)，接口和每一条的字段见[在页面中显示](../features/activity.md#history-page)：

```tsx
<ActivityLog source={{ key: ["article", id], list: (p) => api.article.history(id, p) }} />
```

- `key` 区分同一页面上的几份记录或不同的筛选条件，变化时回到第一页；`toolbar` 放在表格上方左侧（例如筛选控件）；`perPageOptions` 覆盖每页条数选项；`onOpen` 替换点击带 `href` 的记录时的处理。
- 自己写时用 `useActivity(source)`：`{ items, total, page, perPage, setPage, setPerPage, loading, error, refresh }`。

<a id="transfer"></a>
## 导入与导出：`ImportButton`、`ExportButton`

`ImportButton`、`ExportButton` 是[后台导入与导出](../features/import-export.md)的按钮和对话框，`source` 的写法见[导入按钮](../features/import-export.md#import-button)和[导出按钮](../features/import-export.md#export-button)。资源页面中放在 `ResourcePage` 的 `toolbar` 中：

```tsx
<ResourcePage model={articles} route={Route} toolbar={<><ImportButton source={articleImport} /><ExportButton source={articleExport} /></>} />
```

- 进度同时出现在 `OperationsMenu` 中，关闭对话框不影响导入导出。`ImportButton` 的 `accept` 默认是 `.xlsx,.csv`。
- 自己写界面时用 `useImport(source)`、`useExport(source)`：`phase`（`idle`、`uploading`、`starting`、`running`、`completed`、`failed`）、`operation`（登记的操作）、`error`、`reset`；导入另有 `uploaded`（上传比例）和 `downloadFailures`，导出另有 `file`（下载地址）。

<a id="queue-panel"></a>
## 队列面板：`QueuePanel`

`QueuePanel` 是[队列运维接口](../background/queue.md#queue-api)（`queue/queueapi`）的页面，接收生成的客户端分组 `api.queue`，放在要求运维权限的路由上：

```tsx
export const Route = createFileRoute("/_app/queue")({
  staticData: { title: "队列", icon: "list", permission: "queue:manage" },
  component: () => <QueuePanel api={api.queue} />,
})
```

- 页内标签是任务类型（可以暂停一小时、恢复）、失败任务（查看、重试、删除）和系统状态（待转发队列 outbox 和 JetStream）。服务端没有设置 `queueapi.Options.Admin` 时不显示暂停和恢复。
- 任务类型优先展示显示名称和说明，悬停时提示内部 topic 名。数据在打开页面、切换标签、操作之后读取，不定时刷新。待转发任务滞留或 JetStream 异常时显示提示。

<a id="schedule-panel"></a>
## 定时任务面板：`SchedulePanel`

`SchedulePanel` 是[定时任务接口](../background/scheduling.md#admin-pages)（`schedule/scheduleapi`）的页面，接收生成的客户端分组 `api.schedule`：

```tsx
export const Route = createFileRoute("/_app/schedules")({
  staticData: { title: "定时任务", icon: "clock", permission: "jobs:manage" },
  component: () => <SchedulePanel api={api.schedule} />,
})
```

页内标签是任务（可以立即执行）和执行记录（按任务、结果筛选）。任务优先展示显示名称、说明和可读执行频率，悬停时提示内部任务名和原始 Cron 表达式。数据按需读取，不定时刷新。

<a id="doctor-panel"></a>
## 环境自检面板：`DoctorPanel`

`DoctorPanel` 是[运行环境自检接口](../installation/doctor.md#admin-page)（`doctor/doctorapi`）的页面，接收生成的客户端分组 `api.system`：

```tsx
export const Route = createFileRoute("/_app/doctor")({
  staticData: { title: "环境自检", icon: "stethoscope", permission: "system:read" },
  component: () => <DoctorPanel api={api.system} />,
})
```

面板汇总通过、警告、失败和跳过数量，按依赖分组展示每项检查的摘要、修改方法与文档链接，支持点击重新检查。

<a id="ai-chat"></a>
## AI 对话：`useAssistantChat`

`@go-tenon/ui/chat` 在 Vercel AI SDK 的 `useChat` 之上接好[对话接口](../ai/chat.md#conversation-api)：消息流、历史、断线续传、停止、插话和审批。它是单独的入口，用到时在前端安装 AI SDK：

```sh
bun add ai@^7.0.159 @ai-sdk/react@^4.0.163
```

先读取历史，再用历史创建对话，进行中的回答随即接上：

```tsx
import { useAssistantChat, useAssistantHistory } from "@go-tenon/ui/chat"
import type { UIMessage } from "ai"

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

- `send`：没有回答进行时发送新消息，回答进行中时[插话](../ai/chat.md#steering)。服务端没有收下消息时，`send` 从 `messages` 中撤回它并抛出错误。
- `stop` 调用停止接口，服务端取消这次回答；客户端断开连接不会停止它。
- `pending` 是等待[审批](../ai/chat.md#approval)的工具调用（`{ id, toolName, input }`），`respond` 批准或拒绝，全部有了决定后自动发送。
- 插话在回答中是 `data-steering` part（`data` 是 `{ text }`），界面把它显示为一条用户消息。

<a id="chat-attachments"></a>
### 附件

`send(text, files)` 的 `files` 随消息交给模型，见[发送图片和文件](../ai/chat.md#files)。`useAssistantChat` 的 `attachments` 设置限制，`false` 时不能发送附件：

| 选项 | 说明 | 默认 |
| --- | --- | --- |
| `accept` | 允许的类型，写法与 `<input accept>` 相同 | `image/*,application/pdf` |
| `maxFileSize` | 单个文件的最大字节数 | 4 MiB |
| `maxTotalSize` | 一条消息中文件的总字节数，设置了 `upload` 时不检查 | 5 MiB |
| `maxFiles` | 一条消息最多带几个文件 | 5 |
| `upload` | `(file) => Promise<string>`：上传文件，返回模型服务能下载的链接 | 以 data URL 发送 |

- data URL 比文件大约三分之一，默认限制下服务端的[请求体上限](../http/requests.md#body-size)要放宽到 8 MiB。
- 设置了 `upload` 时消息中只有链接，例如[直传到 S3](../libraries/filesystem.md) 后签发读取地址。链接由模型服务下载，必须能从公网访问。
- 附件不符合限制、回答进行中带附件（插话只能是文字）或者不能发送附件时，`send` 抛出 `AttachmentError`：`message` 可以直接显示，`reason` 是 `type`、`size`、`total`、`count`、`busy` 或 `disabled`。

<a id="chat-composer"></a>
### 输入框：`useAssistantComposer`

`useAssistantComposer(chat)` 是输入框的逻辑，`chat` 是 `useAssistantChat` 的返回值：

```tsx
const composer = useAssistantComposer(chat)

<Textarea value={composer.text} onChange={(e) => composer.setText(e.currentTarget.value)} disabled={composer.awaiting} />
<Button disabled={!composer.canSend} onClick={() => void composer.submit()}>发送</Button>
{composer.error && <Alert color="red">{composer.error}</Alert>}
```

- `attachments` 是待发送的附件：选择、粘贴或拖入文件时 `add`，`remove` 移除；不符合限制的文件不加入，原因在 `attachments.error`。`canAttach` 为 `false` 时不显示添加附件的入口。
- `canSend`：有文字或附件、没有等待审批的工具调用（`awaiting`），并且回答进行中时只有文字。
- `submit` 发送并清空输入框，没有发出去时恢复文字和附件，原因在 `error` 中。
- `fileParts(message.parts)` 取出消息中的文件片段（`{ mediaType, filename, url }`），用于显示已经发送的附件。
