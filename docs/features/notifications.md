# 站内通知

站内通知是用户的收件箱：每条通知是 `notifications` 表中的一行，属于某个租户中的某个主体，带有已读状态。

包：`notification`。

- 通知在业务事务中用 `tx` 发送，与业务修改一起提交或回滚；用 `db` 发送时，事务回滚后通知仍在。
- 邮件、[移动推送](push.md)等其他渠道由业务动作在同一个事务中显式写出来。

<a id="example-usage"></a>
## 示例

```go
err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
    // ……发布文章
    _, err := notification.Send(ctx, tx, notification.Notification{
        Event: "article.published",
        Data:  map[string]any{"article_id": item.ID, "title": item.Title},
    }, notification.RecipientOf(author))
    return err
})

// 收件人读取自己的通知和未读数
me := notification.RecipientOf(sub)
list, err := notification.List(ctx, db, notification.Query{Recipient: me})
n, err := notification.UnreadCount(ctx, db, me)
```

`db` 是入口打开的 `*bun.DB`，`author` 是文章作者的 `identity.Subject`，`sub` 是 `identity.FromContext(ctx)` 取出的当前主体。

<a id="installation"></a>
## 装配

- 把 `notification.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行迁移。
- 函数直接接收 `bun.IDB`（连接池或事务），没有需要创建的对象。
- 把包级函数 `notification.Erase`、`notification.Export` 登记到[注销账号](privacy.md#framework-sources)和[导出个人数据](privacy.md#sources)，它们处理主体在所有租户中的通知。

<a id="sending"></a>
## 用 `Send` 发送通知

在[业务动作](../concepts/actions.md#writing-actions)的事务中调用。`a.db` 是 `*bun.DB`，`subject` 是执行动作的主体，`item` 是事务中查出的文章，`user.Type` 是应用的用户主体类型：

```go
err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    // ……读取、检查并更新文章
    n := notification.Notification{
        Event: "article.published_by_admin",
        Data:  map[string]any{"article_id": item.ID, "title": item.Title},
    }
    author := notification.Recipient{Tenant: subject.Tenant, Type: user.Type, ID: item.AuthorID}
    _, err := notification.Send(ctx, tx, n, author)
    return err
})
```

- 只填 `Event` 和 `Data`，`ID`（UUIDv7）和时间由 `Send` 填写。
- 每个收件人写入一条，重复的收件人只写一次；返回写入的通知，[实时推送](#realtime)用它。

<a id="recipients"></a>
### 收件人

`notification.Recipient` 由租户和主体（`identity.Ref`）组成，`Tenant` 为 `""` 时是平台本身。同一个用户在两个租户中是两个收件人，各有各的通知，见[多租户](../security/tenancy.md)。`notification.RecipientOf(sub)` 返回主体在当前租户中的收件人。[移动推送](push.md#sending)的 `push.Send` 接收同样的收件人。

<a id="events-and-data"></a>
### 事件与数据

通知只保存事件名和数据，显示时用 `i18n.T(ctx, n.Event, n.Data)` 翻译，改文案、加语言时不用改动已有的通知：

```yaml
# locales/zh-CN.yaml
article.published_by_admin: "你的文章《{{.title}}》已由管理员发布"
```

- 显示需要的内容在发送时写进 `Data`：文章之后可能改名或被删除。
- `Data` 以 JSON 存储，读出时 UUID 是字符串，数字是 `float64`。
- 通知可以与[操作日志](activity.md#events-and-properties)同时写：通知给收件人看，有已读状态，收件人可以删除。

<a id="listing"></a>
## 用 `List` 读取通知

`List` 按时间从新到旧返回一个收件人的通知，`UnreadCount` 返回未读数。通知列表接口的处理器，`h.db` 是 `*bun.DB`：

```go
type IndexInput struct {
    Before uuid.UUID `query:"before" required:"false" doc:"上一页最后一条通知的 ID"`
}

func (h *Handlers) Index(ctx context.Context, in *IndexInput) (*IndexOutput, error) {
    sub, _ := identity.FromContext(ctx)
    list, err := notification.List(ctx, h.db, notification.Query{Recipient: notification.RecipientOf(sub), Before: in.Before})
    if err != nil {
        return nil, err
    }
    return &IndexOutput{Body: toDTOs(ctx, list)}, nil
}
```

- `Query` 另有 `Unread`（只返回未读）和 `Limit`（默认 50）。`toDTOs` 是应用的函数，用 `i18n.T` 生成文案，`n.Read()` 报告是否已读。
- 每个函数只读写传入的收件人的通知：别人的通知 ID 被忽略，`Get` 返回 `notification.ErrNotFound`（404）。收件人取自 `RecipientOf(sub)` 时不用另外检查权限。
- `notification.Notification` 是表的 Bun 模型，其他查询直接用 Bun 写，条件以 `tenant, recipient_type, recipient_id` 开头，与索引一致。

<a id="marking-as-read"></a>
### 用 `MarkRead` 标记已读

`in.ID` 是路径参数中的通知 ID，`h.db` 是 `*bun.DB`：

```go
func (h *Handlers) Read(ctx context.Context, in *ReadInput) (*struct{}, error) {
    sub, _ := identity.FromContext(ctx)
    return nil, notification.MarkRead(ctx, h.db, notification.RecipientOf(sub), in.ID)
}
```

`MarkRead` 接受多个 ID，`MarkAllRead` 标记全部，已读的通知保持原来的已读时间。`Delete` 删除通知。

<a id="mail"></a>
## 同时发送邮件

在发送通知的事务中把邮件交给[队列](../background/queue.md#publishing-jobs)，用收件人的语言渲染（见[在 HTTP 之外使用](../general/localization.md#outside-http)）。`tx`、`n` 来自[发送通知](#sending)，`authorUser` 是事务中查出的作者，`a.emails` 是 `*mail.Templates`，`mailjob.Send` 是应用声明的 `queue.Topic[mail.Message]`：

```go
if authorUser.EmailNotifications { // 偏好保存在应用自己的表中
    mctx := i18n.WithLocale(ctx, a.languages, i18n.Match(a.languages, authorUser.Language))
    msg, err := a.emails.Render(mctx, "article_published_by_admin", n.Data)
    if err != nil {
        return err
    }
    msg.To = []mail.Address{{Name: authorUser.Name, Address: authorUser.Email}}
    return mailjob.Send.Publish(ctx, tx, msg)
}
```

<a id="telegram"></a>
## 其他渠道：Telegram、Slack

按邮件的方式接入：在事务中把消息发布到应用声明的 `queue.Topic`，由任务调用第三方接口；短信用 [`sms`](../libraries/sms.md)。任务按对方的错误决定是否重试，`m` 是任务的消息，`r` 是解析出的 Bot API 响应，`err` 是由它构造的错误，`Unlink` 是应用删除绑定的函数：

```go
switch r.ErrorCode {
case http.StatusForbidden: // 用户屏蔽了 bot：删除绑定，不再重试
    if err := Unlink(ctx, a.db, m.ChatID); err != nil {
        return err
    }
    return queue.Permanent(err)
case http.StatusTooManyRequests:
    return queue.RetryAfter(err, time.Duration(r.Parameters.RetryAfter)*time.Second)
}
return err // 其他错误按退避重试
```

- `queue.Permanent` 让任务直接失败，`queue.RetryAfter` 按对方要求等待，见[失败与重试](../background/queue.md#failures-and-retries)。
- 任务至少执行一次，极少数情况下用户收到两条相同的消息。
- 绑定账号时接收对方的 webhook，见[接收 webhook](webhooks.md#receiving)。

<a id="realtime"></a>
## 实时推送未读数

事务提交之后用[广播](../libraries/broadcasting.md#publishing-events)通知收件人的连接。`tx` 是[发送通知](#sending)的事务，`sent` 是 `Send` 的返回值，`a.hub` 是 `*broadcast.Hub`：

```go
for _, n := range sent {
    tx.AfterCommit(func(ctx context.Context) error {
        return errors.Wrap(a.hub.PublishJSON(ctx, inboxTopic(n.Recipient()), n.ID), "broadcast notification")
    })
}

// inboxTopic 是一个收件人的广播主题，例如 inbox:acme:user:u1。
func inboxTopic(r notification.Recipient) string {
    return "inbox:" + r.Tenant + ":" + r.Ref.String()
}
```

- 广播在 [`AfterCommit`](../database/database.md#after-commit) 中发，事务回滚时不发。
- 主题只能由字母、数字和 `_ - : .` 组成，租户和主体 ID 含其他字符时先转换。
- 标记已读之后同样广播，用户的其他标签页随之更新。

<a id="realtime-badge"></a>
### 用事件流推送角标

[事件流接口](../http/sse.md#long-lived-connections)订阅当前用户的主题：连接时先推送一次未读数，之后每收到一条广播重新查询。`h.hub` 是 `*broadcast.Hub`，`inboxTopic` 见[实时推送未读数](#realtime)：

```go
type Badge struct {
    Unread int `json:"unread"`
}

func (h *Handlers) Inbox(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
    sub, _ := identity.FromContext(ctx)
    me := notification.RecipientOf(sub)
    return rest.SSE(func(s *httpx.Stream) error {
        events := h.hub.Subscribe(inboxTopic(me)) // 先订阅再查询，不漏掉之间的通知
        defer events.Close()
        for {
            n, err := notification.UnreadCount(s.Context(), h.db, me)
            if err != nil {
                return err
            }
            if err := s.SendJSON(httpx.Event{Name: "badge"}, Badge{Unread: n}); err != nil {
                return nil
            }
            select {
            case <-s.Done():
                return nil
            case _, ok := <-events.C():
                if !ok {
                    return nil // 订阅已结束，客户端会重连
                }
            }
        }
    }), nil
}
```

用 `rest.RegisterSSE(api, op, map[string]any{"badge": Badge{}}, h.Inbox)` 注册。`@go-tenon/ui` 的 [`NotificationsMenu`](../frontend/layout.md#menus) 是读取 `badge` 事件的铃铛，自己画列表时用 [`useNotifications`](../frontend/features.md#notifications)。

<a id="retention"></a>
## 用 `Cleanup` 删除旧通知

`notification.Cleanup` 返回删除保留期以前的通知（无论是否已读）的清理函数，交给[定时任务](../background/scheduling.md#framework-cleanups)。`sched` 是 `*schedule.Scheduler`：

```go
sched.Add("notifications:cleanup", "0 4 * * *", schedule.Cleanup(notification.Cleanup(a.db, 90*24*time.Hour), 1000))
```
