# 广播

把一条消息经由 NATS 送到每一个实例，再交给各实例上订阅了它的连接，例如编辑 A 在实例 1 上发布文章，编辑 B 在实例 2 上的 [SSE](../http/sse.md) 连接收到通知。消息按主题（topic）分发，例如 `articles`、`room:42`；[实时频道](realtime.md)的 WebSocket 也用它。

包：`broadcast`、`broadcast/natsbroadcast`、`broadcast/broadcasttest`。

<a id="example-usage"></a>
## 示例

```go
hub := broadcast.New(natsbroadcast.New(nc, "app.broadcast"))

// 一个实例上订阅
sub := hub.Subscribe("articles")
defer sub.Close()

// 任意实例上发布，每个实例上订阅了 articles 的连接都会收到
err := hub.PublishJSON(ctx, "articles", ArticlePublished{ID: id, Title: title})

for m := range sub.C() {
    // m.Topic、m.Data
}
```

`nc` 是[入口中创建的 NATS 连接](nats.md#connecting)。

<a id="creating-the-hub"></a>
## 创建 Hub

每个进程创建一个 `*broadcast.Hub`，所有长连接共用。在入口创建，登记关闭：

```go
hub := broadcast.New(natsbroadcast.New(nc, "app.broadcast")) // 日志默认写到 slog.Default()，用 broadcast.WithLogger 覆盖
stack.MustAdd("broadcast", func(context.Context) error { return hub.Close() })
```

Hub 创建后就在后台接收消息，`Close` 停止接收并结束全部订阅。

<a id="publishing-events"></a>
## 提交事务之后发布事件：`PublishJSON`

“发布文章”的业务动作在事务中用 [`tx.AfterCommit`](../database/database.md#after-commit) 登记事件，提交之后才发出：`Publish` 不参与事务，提前发布时客户端可能收到随后被回滚的通知。`a.hub` 由构造函数传入，`item` 是事务中更新的文章，`subject` 是调用者的主体：

```go
type ArticlePublished struct {
    ID     uuid.UUID `json:"id"`
    Title  string    `json:"title"`
    Author string    `json:"author"`
}

// 在 Publish.Handle 的事务中：
tx.AfterCommit(func(ctx context.Context) error {
    err := a.hub.PublishJSON(ctx, "articles", ArticlePublished{ID: item.ID, Title: item.Title, Author: subject.ID})
    return errors.Wrap(err, "broadcast article published")
})
```

- 发布小的领域事件，大的内容只带 ID；每个连接收到后按自己的权限和语言转换。
- 发布失败时记录 Error 日志，业务结果照旧。
- 主题由字母、数字和 `_ - : .` 组成，`.` 两侧必须有其他字符，最长 200 字节。不合法时 `Publish` 返回错误，`Subscribe` panic。
- 多租户应用的主题带上租户，见[缓存、广播与其他资源](../security/tenancy.md#other-resources)。

<a id="pushing-to-browsers"></a>
## 推送给客户端：`Subscribe`

SSE 处理器订阅主题，从 `sub.C()` 读消息推送，完整写法见[长连接](../http/sse.md#long-lived-connections)。

- 一个订阅可以订阅多个主题，例如 `hub.Subscribe("articles", "comments")`。
- `sub.C()` 被关闭表示订阅结束，`sub.Err()` 返回原因，见[投递保证](#delivery-guarantees)。这时处理器返回，连接结束，客户端重连并[补发](#replaying-missed-events)。
- 撤销登录后立即断开 SSE 连接用 `broadcast.NewCredentials(hub)`，见[立即断开](../http/sse.md#immediate-revocation)。

<a id="replaying-missed-events"></a>
### 断线补发：`Last-Event-ID`

客户端重连时在 `Last-Event-ID` 中发回最后收到的事件 ID，处理器从应用自己的表中查出之后的数据补发，框架不保存事件历史。文章的 ID 是 UUIDv7，按时间排序。`hub` 是入口创建的 Hub，`publishedAfter` 是查询某篇之后发布的文章的业务动作，`eventOf` 把文章转为 `ArticlePublished`：

```go
return rest.SSE(func(s *httpx.Stream) error {
    sub := hub.Subscribe("articles") // 先订阅，再查询
    defer sub.Close()
    if last, err := uuid.Parse(s.LastEventID()); err == nil {
        missed, err := publishedAfter.Handle(s.Context(), last)
        if err != nil {
            return err
        }
        for _, item := range missed {
            if err := s.SendJSON(httpx.Event{Name: "published", ID: item.ID.String()}, eventOf(item)); err != nil {
                return nil
            }
        }
    }
    // ……接着处理 sub.C()，见长连接
}), nil
```

必须先订阅、再查询：反过来时，查询之后、订阅之前发布的事件两边都收不到。先订阅时同一篇文章可能既被查到、又从订阅中收到，按 ID 去重。

<a id="backends"></a>
## NATS 后端：`natsbroadcast`

`natsbroadcast.New(nc, prefix)` 经由 NATS 的发布订阅在实例之间传递消息，不需要 JetStream。主题 `room:42` 的消息发布到 `<prefix>.room:42`，每个实例只向 NATS 订阅当前有本地订阅者的精确 subject，最后一个订阅者离开时自动退订。

- 同一个应用的所有实例使用同一套 NATS 和同一个前缀，否则收不到其他实例的消息；前缀区分共用 NATS 的应用和环境。
- 单机部署也经由 NATS：[不停机升级](../installation/operations.md#upgrade-overlap)时新旧进程并存，旧进程发布的事件要送到已经连到新进程的浏览器。
- `nc` 用 [`natsx.Connect`](nats.md#connecting) 创建：它一直重连，并且回显发布者自己的消息。
- 超过 NATS `max_payload`（默认 1MB）的消息分片发送、收齐后拼回，缺一片时整条丢弃。

<a id="delivery-guarantees"></a>
## 投递保证

消息只投递给当前在线的订阅，最多一次。消息可能送不到时，Hub 结束订阅：

| 原因 | `sub.Err()` | 发生时 |
| --- | --- | --- |
| 订阅跟不上 | `broadcast.ErrSlow` | 订阅的缓冲区（64 条）已满，例如浏览器网络很慢；只结束这一个订阅 |
| 后端可能丢了消息 | `broadcast.ErrLost` | 与 NATS 的连接中断过，或者 NATS 客户端丢弃过消息 |
| 订阅被关闭 | `broadcast.ErrClosed` | 调用了 `sub.Close()` 或 `hub.Close()` |

订阅结束后处理器返回，客户端重连并按 `Last-Event-ID` [补发](#replaying-missed-events)，消息因此完整、有序。

<a id="testing"></a>
## 测试

`broadcasttest.NewBackend()` 是内存后端，只用于测试。同一个后端上的多个 Hub 各自维护独立的主题订阅，互相只投递已订阅主题的消息，相当于多个实例：

```go
hub := broadcast.New(broadcasttest.NewBackend())
sub := hub.Subscribe("articles")
// ……调用发布事件的业务动作
msg := broadcasttest.Receive(t, sub) // 5 秒内收不到时测试失败
```

实现其他后端时，用 `broadcasttest.TestBackend` 检查它符合 `broadcast.Backend` 的约定。
