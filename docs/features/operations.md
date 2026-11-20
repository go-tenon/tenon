# 后台操作

批量发布、导入、导出这类要跑几分钟的操作：在队列任务中执行，进度、结果和失败的原因记在 `operations` 表中，用户在任务中心看到自己发起的操作和进度。

包：`operation`。

- 操作只记录状态和进度，执行由[队列](../background/queue.md)任务完成。
- 登记操作和发布任务写在同一个事务中，事务回滚时一起撤销。
- [后台导入、导出](import-export.md#background-imports)也是操作，和应用自己的批量操作出现在同一个列表中，用同一种方式推送进度。

<a id="example-usage"></a>
## 示例

`ops` 是 `operation.New(db)`，`tx` 是发起请求的业务事务，`subject` 是发起人，`BulkPublishJob` 是应用的队列 topic：

```go
// 发起：在业务事务中登记，同一个事务发布任务
op, err := ops.Start(ctx, tx, subject, "articles.bulk_publish")
err = BulkPublishJob.Publish(ctx, tx, op.ID)

// 队列任务：报告进度，回调返回 nil 时操作变为 completed
err = ops.Run(ctx, op.ID, func(ctx context.Context, r *operation.Run) error {
    r.SetTotal(len(ids))
    for _, id := range ids {
        // 处理一个单位……
        r.Add(1, 0) // 处理了 1 个，其中失败 0 个
    }
    return nil
}, nil)
```

状态依次是 `pending`（已登记）、`running`、`completed` 或 `failed`。`completed` 的操作中可能有失败的单位，看 `Failed`。

<a id="installation"></a>
## 装配

1. 把 `operation.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行迁移。`imports`、`exports` 的表引用 `operations` 表，用到它们时也要发布 `operation`。
2. 在入口用 `operation.New(a.db)` 创建一次 `*operation.Service`，交给发起和执行操作的业务动作。

<a id="starting"></a>
## 用 `Start` 发起操作

`bulk_publish.go` 在后台逐篇发布文章，每一篇由[业务动作](../concepts/actions.md#writing-actions) `Publish` 完成。`hub` 是入口创建的 `*broadcast.Hub`，用于[推送进度](#progress)：

```go
// BulkPublishRequest 是批量发布的任务：哪一个操作、谁发起的、发布哪些文章。
type BulkPublishRequest struct {
    Operation uuid.UUID        `json:"operation"`
    Subject   identity.Subject `json:"subject"`
    IDs       []uuid.UUID      `json:"ids"`
}

var BulkPublishJob = queue.Topic[BulkPublishRequest]("articles.bulk_publish")

// BulkPublish 在后台发布多篇文章。
type BulkPublish struct {
    db      *bun.DB
    hub     *broadcast.Hub
    ops     *operation.Service
    publish *Publish
}

// Handle 登记操作并发布任务。
func (a *BulkPublish) Handle(ctx context.Context, subject identity.Subject, ids []uuid.UUID) (*operation.Operation, error) {
    var op *operation.Operation
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        var err error
        if op, err = a.ops.Start(ctx, tx, subject, "articles.bulk_publish"); err != nil {
            return err
        }
        return BulkPublishJob.Publish(ctx, tx, BulkPublishRequest{Operation: op.ID, Subject: subject, IDs: ids})
    })
    return op, err
}

// OperationProgress 是返回给前端的操作，ErrorMessage 是失败原因的译文。
type OperationProgress struct {
    *operation.Operation
    ErrorMessage string `json:"error_message,omitempty"`
}
```

- `HTTP` 方法调用 `Handle`，返回 `OperationProgress`，前端用其中的 `id` [读取进度](#progress)；接口的 `DefaultStatus` 设为 `http.StatusAccepted`。
- kind 由应用定义，写成“模块.动作”，任务中心按它显示操作的名称。
- 任务的 payload 带着发起人，任务按它的权限检查每一篇文章：`op.Owner()` 只有 ID、类型和租户，没有权限。

<a id="running"></a>
## 用 `Run` 执行操作

worker 注册任务，见[启动 worker](../background/queue.md#running-workers)：

```go
article.BulkPublishJob.Handle(w, article.NewBulkPublish(a.db, a.hub, a.ops, article.NewPublish(a.db)).Job)
```

```go
// Job 逐篇发布，每篇保存一次进度。
func (a *BulkPublish) Job(ctx context.Context, req BulkPublishRequest) error {
    return a.ops.Run(ctx, req.Operation, func(ctx context.Context, r *operation.Run) error {
        r.SetTotal(len(req.IDs))
        for _, id := range req.IDs[r.Operation().Processed:] { // 重试时跳过已经保存的
            _, err := a.publish.Handle(ctx, req.Subject, id)
            switch {
            case err == nil, errors.Is(err, ErrAlreadyPublished):
                r.Add(1, 0)
            case errorx.CodeOf(err) != "":
                r.Add(1, 1) // 文章已被删除、没有权限：只记这一篇失败
            default:
                return err // 数据库不可用等，队列稍后重试
            }
            if err := r.Save(ctx, nil); err != nil {
                return err
            }
        }
        return nil
    }, a.saved)
}
```

`Run` 先把操作变为 `running`，再调用回调；`a.saved` 见[推送进度](#progress)。回调的返回值决定结果：

| 回调返回 | 结果 |
| --- | --- |
| `nil` | `completed`，没有保存的进度一起保存 |
| 带错误码的业务错误 | `failed`，`Error` 是错误码，不再重试 |
| 其他错误，例如数据库不可用 | 队列稍后重试，从上次保存的进度继续，没有保存的进度丢弃 |
| 最后一次重试仍然出错，或者 `queue.Permanent` 标记的错误 | `failed`，`Error` 是 `operation.failed` |

一个单位的业务错误只影响这个单位时，用 `r.Add(1, 1)` 记为失败，回调不返回错误。

<a id="saving-progress"></a>
## 用 `Save` 保存进度与断点

`r` 是 `Run` 回调收到的 `*operation.Run`。`SetTotal`、`Add` 只改内存中的进度，`Save` 在一个事务中保存：

- 重试时 `r.Operation()` 是上次保存的操作，从 `Processed` 之后继续。每个单位保存一次，每个单位只处理一次；每 100 个保存一次时，重试会再处理最多 99 个单位，处理必须能重复执行。
- `r.Save(ctx, write)` 的 `write` 在同一个事务中写入应用自己的断点，例如导入的失败行和处理到的行号：进度和断点一起提交。
- 结果要和完成状态一起提交时用 `r.Complete(ctx, write)` 结束，例如导出的下载地址：前端看到 `completed` 时一定能读到地址。
- 每次重试都从头开始的操作在回调开头调用 `r.Restart()`，把进度清零。
- 同一个操作的两次执行都保存时（例如任务的租约过期后被另一个 worker 领取），后保存的那次 `Save` 返回错误，回调直接返回这个错误。

<a id="fanout"></a>
## 用 `Fanout` 并行处理

每个单位很慢又彼此独立时（例如为每篇文章调用模型生成摘要），用 `operation.Fanout` 把每个单位发布成一个队列任务，多个 worker 并行处理，全部结束后操作变为 `completed`。topic 的 payload 是 `operation.Unit[T]`，`T` 是一个单位的业务数据：

```go
type SummarizeUnit struct {
    Subject identity.Subject `json:"subject"`
    ID      uuid.UUID        `json:"id"`
}

var SummarizeJob = queue.Topic[operation.Unit[SummarizeUnit]]("articles.summarize")

// 发起：在登记操作的事务 tx 中加入单位，每个单位发布为一个任务
if op, err = a.ops.Start(ctx, tx, subject, "articles.summarize"); err != nil {
    return err
}
return operation.Fanout(ctx, tx, op.ID, SummarizeJob, units...)

// worker：HandleUnit 把处理一个单位的函数包装为任务的处理函数
SummarizeJob.Handle(w, operation.HandleUnit(a.ops, func(ctx context.Context, op *operation.Operation, u SummarizeUnit) error {
    return summarize(ctx, u.Subject, u.ID)
}, saved))
```

| 处理函数返回 | 单位 |
| --- | --- |
| `nil` | 成功，`Processed` 加一 |
| 带错误码的业务错误 | 失败，`Processed`、`Failed` 各加一，不再重试 |
| 其他错误 | 队列重试；最后一次仍然出错时失败，任务转入[失败任务表](../background/queue.md#failed-jobs)，之后重试成功时 `Failed` 减一 |

- 每个单位结束时在一个事务中调用 `saved`；最后一个单位结束时 `op.Finished()` 为 `true`，在这里发送通知或发布下一步的任务，只执行一次。
- 已经成功的单位再次投递时跳过；处理函数返回之后、记下结果之前进程崩溃时会再执行一次，处理必须能重复执行。
- 每个单位都要锁住操作的行记下结果。单位很多、每个又很快时用 `Run` 在一个任务中处理。

<a id="progress"></a>
## 推送进度

`Run`、`HandleUnit` 每次保存进度（变为 `running`、每次 `Save`、结束）都在同一个事务中调用 `saved`。`saved` 在提交后发布[广播](../libraries/broadcasting.md)，提醒正在查看的连接；操作结束时在这个事务中发送[通知](notifications.md)，通知和状态一起提交，只发一次：

```go
// operationTopic 是一个操作的广播主题，每次保存进度后发布一条空消息。
func operationTopic(id uuid.UUID) string { return "operation:" + id.String() }

func (a *BulkPublish) saved(ctx context.Context, tx database.Tx, op *operation.Operation) error {
    tx.AfterCommit(func(ctx context.Context) error {
        return a.hub.Publish(ctx, operationTopic(op.ID), nil)
    })
    if !op.Finished() {
        return nil
    }
    _, err := notification.Send(ctx, tx, notification.Notification{
        Event: "articles.bulk_published",
        Data:  map[string]any{"operation_id": op.ID, "status": op.Status, "failed": op.Failed},
    }, notification.RecipientOf(op.Owner()))
    return err
}
```

进度接口用 [`rest.RegisterSSE`](../http/sse.md) 注册：`rest.RegisterSSE(api, op, map[string]any{"progress": OperationProgress{}}, a.Progress)`。

```go
type OperationIDInput struct {
    ID uuid.UUID `path:"id"`
}

// Progress 处理 GET /articles/bulk-publish/{id}/progress：先推送当前进度，每次保存后再推送，结束后关闭连接。
func (a *BulkPublish) Progress(ctx context.Context, in *OperationIDInput) (*huma.StreamResponse, error) {
    subject, _ := identity.FromContext(ctx)
    if _, err := a.ops.Get(ctx, subject, in.ID); err != nil {
        return nil, err // 不存在或不是发起人：404
    }
    return rest.SSE(func(s *httpx.Stream) error {
        events := a.hub.Subscribe(operationTopic(in.ID)) // 先订阅，再读取，中间保存的进度也能收到
        defer events.Close()
        for {
            op, err := a.ops.Get(s.Context(), subject, in.ID)
            if err != nil {
                return err
            }
            p := OperationProgress{Operation: op, ErrorMessage: op.ErrorMessage(s.Context())}
            if err := s.SendJSON(httpx.Event{Name: "progress"}, p); err != nil || op.Finished() {
                return nil
            }
            select {
            case <-s.Done():
                return nil
            case _, ok := <-events.C():
                if !ok {
                    return nil // 订阅已结束，客户端重连后先收到当前进度
                }
            }
        }
    }), nil
}
```

广播只提醒连接重新读取，每次推送的都是 `Get` 读出的当前进度，只有发起人能读到。worker 和 SSE 连接在不同的实例上时，广播由 Hub 的后端送达。

<a id="watching-progress"></a>
## 在前端读取进度

[进度接口](#progress)每次保存都推送一个 `progress` 事件，字段见[客户端协议](../http/protocol.md#operations)。`@go-tenon/client` 的 `watchOperation` 读取事件流直到操作结束，断开时自动重连：

```ts
import { progressRatio, watchOperation } from "@go-tenon/client"

// api.article.bulkPublishProgress 是进度接口生成的客户端方法
const done = await watchOperation((o) => api.article.bulkPublishProgress(op.id, o), {
  onProgress: (p) => setProgress(progressRatio(p)), // total 为 0 时是 undefined
})
if (done.status === "failed") showError(done.error)
```

React 前端用 `@go-tenon/ui` 的 [`trackOperation`](../frontend/features.md#operations) 登记操作，顶栏的任务中心和页面都显示它的进度。

<a id="listing"></a>
## 用 `List` 列出任务中心的操作

`List` 按时间从新到旧列出 `subject` 发起的操作，导入、导出和批量操作都在其中。`a.ops` 是 `*operation.Service`：

```go
list, err := a.ops.List(ctx, subject, operation.Query{Limit: 20})
```

`Query.Kind` 只列出一类操作；翻页时把上一页最后一个操作的 ID 传给 `Query.Before`。

<a id="cleanup"></a>
## 清理旧的操作

用[定时任务](../background/scheduling.md#framework-cleanups)定期删除旧的操作，导入、导出的记录和导入的失败行随之删除。`sched` 是入口的 `*schedule.Scheduler`：

```go
sched.Add("operations:cleanup", "0 3 * * *", schedule.Cleanup(a.ops.Cleanup(30*24*time.Hour), 1000))
```

注销账号时用 `operation.Erase` 删除一个主体发起的全部操作，见[注销账号](privacy.md)。
