# 队列

把发送邮件、生成缩略图、通知第三方这类工作作为任务发布到队列，由 worker 在后台执行，失败时自动重试，次数用完后转入失败任务表。任务在业务事务中写进 PostgreSQL 的 WAL，由 worker 经逻辑复制转发到 NATS JetStream。

包：`queue`、`queue/queuecmd`、`queue/queueapi`、`queue/queuetest`。

- 任务在发布它的事务提交后才投递，事务回滚时任务一起取消。
- 任务至少执行一次：worker 崩溃或停止超时时，任务交给其他 worker 重新执行，所以处理函数要能安全地重复执行。任务之间不保证执行顺序。
- 至少运行一个 worker：任务由 worker 转发到 JetStream 之后才会执行。

<a id="example-usage"></a>
## 示例

注册用户时发布“发送欢迎邮件”的任务，在 worker 中处理。`db` 是入口打开的 `*bun.DB`，`nc` 是应用的 NATS 连接，`cfg.Queue` 是配置中的 `queue` 段：

```go
// 声明 topic：名称和 payload 的类型
var SendWelcome = queue.Topic[uuid.UUID]("users.send_welcome")

// 发布：与业务数据在同一个事务中提交
err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
    if _, err := tx.NewInsert().Model(user).Exec(ctx); err != nil {
        return err
    }
    return SendWelcome.Publish(ctx, tx, user.ID)
})

// 处理：在 worker 进程中注册
w := queue.NewWorker(db, nc, cfg.Queue, nil)
SendWelcome.Handle(w, func(ctx context.Context, id uuid.UUID) error {
    return sendWelcomeEmail(ctx, id) // 返回错误时稍后重试
})
err = w.Run(ctx) // 一直运行到 ctx 被取消
```

<a id="preparing"></a>
## 准备：NATS、PostgreSQL 与迁移

- **NATS** 2.15 或更新的版本，开启 JetStream：`nats-server -js -sd /var/lib/nats`。worker 启动时自己创建 stream 和 consumer。生产环境用三台服务器组成[集群](https://docs.nats.io/running-a-nats-service/configuration/clustering/jetstream_clustering)并把 `replicas` 设为 3：单台服务器默认每 2 分钟同步一次磁盘，断电时最近的任务可能丢失；只有一台时在 NATS 配置中设置 `jetstream { sync_interval: always }`。
- **PostgreSQL** 17 或更新的版本，`wal_level = logical`（修改后重启），应用的数据库用户有 `REPLICATION` 属性（`ALTER ROLE app REPLICATION`）；RDS 这类云数据库在参数组中开启逻辑复制。其他设置见[运维](../installation/operations.md#queue)，[入门教程](../tutorial/02-infrastructure.md#infrastructure)的 `compose.yaml` 已经设置好。`queue.Doctor` 逐项检查这些要求，见[自检](../installation/doctor.md)。
- **迁移**：`queue.Migrations()` 创建失败任务表 `queue_failed_jobs` 和逻辑复制槽，加入[发布命令](../database/migrations.md#publishing-framework-migrations)：

```go
migrationCmd.AddCommand(migratecmd.Publish(
    queue.Migrations(),
))
```

部署时先执行迁移，再启动发布任务的进程：复制槽建立之前发布的任务不会被转发。

> [!WARNING]
> 所有 worker 都停止时，复制槽中没有转发的 WAL 一直累积，超过上限后复制槽失效，任务丢失。监控积压并设置告警，见[运维](../installation/operations.md#queue-monitoring)。

<a id="publishing-jobs"></a>
## 发布队列任务：`queue.Topic` 与 `Publish`

`queue.Topic` 把 topic 名称和 payload 的类型声明在一起，发布方和处理方共用这一个声明，名称写错或类型不一致时编译不通过。多个业务包共用的 topic 放在单独的小包中；业务模块自己的 topic 和业务动作写在一起，见[队列任务](../concepts/actions.md#as-jobs)：

```go
package mailjob

import (
    "github.com/go-tenon/tenon/mail"
    "github.com/go-tenon/tenon/queue"
)

// Send 把邮件交给 worker 发送。
var Send = queue.Topic[mail.Message]("mail.send")
```

`Publish` 把 payload 编码为 JSON 写入传入的 `db`：传入事务 `tx` 时任务随事务提交，传入连接池时立即发布。`a.db` 是 `*bun.DB`，`msg` 是渲染好的 `mail.Message`：

```go
err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    // ... 更新文章状态
    return mailjob.Send.Publish(ctx, tx, msg)
})
```

- topic 名由字母、数字、`.`、`_`、`-` 组成，最长 64 个字符，`.` 分隔的每一段不能为空。只差在 `.` 和 `_` 的两个 topic（`mail.send` 和 `mail_send`）对应同一个 consumer，不能同时使用。
- `PublishAll` 批量发布多个同类任务。
- `ctx` 中的 `request_id` 和链路随任务保存，处理时还原到处理函数的 `ctx` 中。

<a id="delayed-jobs"></a>
### 延迟执行：`queue.Delay`、`queue.At`

`queue.Delay(d)` 让任务在一段时间之后执行，`queue.At(t)` 指定执行时间，`tx` 是业务事务：

```go
var PublishAt = queue.Topic[uuid.UUID]("articles.publish")

err := PublishAt.Publish(ctx, tx, item.ID, queue.At(item.PublishAt))
```

未到时间的任务保存为 JetStream 的定时消息，精确到秒，延迟多久都可以。几个月后才执行的任务也一直在 JetStream 中，所以生产环境的 stream 要有多个副本，见[运维](../installation/operations.md#queue-jetstream)。

<a id="multi-step-jobs"></a>
### 多步任务

按顺序分几步完成的工作，在保存这一步结果的事务中发布下一步：这一步提交时才发出下一步，下一步需要的数据放进它的 payload。在这一步的 `Job` 中，`a.db` 是 `*bun.DB`，`NotifySubscribers` 是下一步的 `queue.Topic[uuid.UUID]`：

```go
summary, err := a.generate(ctx, id) // 调用模型，不放在事务中
if err != nil {
    return err
}
return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    _, err := tx.NewUpdate().Model((*Article)(nil)).Set("summary = ?", summary).Where("id = ?", id).Exec(ctx)
    if err != nil {
        return err
    }
    return NotifySubscribers.Publish(ctx, tx, id)
})
```

一组任务并行执行、全部结束后再做下一件事，用[后台操作的并行处理](../features/operations.md#fanout)。

<a id="processing-jobs"></a>
## 处理任务：`Topic.Handle`

`Handle` 把函数注册为 topic 在 worker 上的处理函数，payload 按 JSON 解码为函数的参数类型。`w` 是 `queue.NewWorker` 返回的 worker，`mail.Mailer` 的 `Send` 方法正好符合签名：

```go
mailjob.Send.Handle(w, mailer.Send)
```

- 返回 `nil` 时任务完成；返回错误时按[失败与重试](#failures-and-retries)处理。
- 处理函数的 `ctx` 中的查询都在主库执行：任务在事务提交后立即送达，读库可能还没有这些数据。
- 把 `ctx` 传给数据库查询和外部调用，停止 worker 时处理函数才能及时退出，见[租约与停止](#leases-and-shutdown)。
- 个别 topic 用 `queue.Concurrency`、`queue.MaxAttempts` 覆盖配置，例如 `Summarize.Handle(w, s.Job, queue.Concurrency(2))`。

<a id="running-workers"></a>
### 启动 worker

worker 是常驻进程的一个[运行部分](../concepts/dependencies.md#running-parts)，转发发布的任务并处理已注册 topic 的任务。在入口的 `bootstrap` 包中，`a.nc` 是 `natsx.Connect` 创建的 NATS 连接（见[连接 NATS](../libraries/nats.md#connecting)），`newMailer` 见[装配发送器](../libraries/mail.md#assembling-mailer)，`article.RegisterJobs` 登记业务模块的任务，见[队列任务](../concepts/actions.md#as-jobs)：

```go
// Worker 装配队列 worker，返回的函数转发并处理队列任务，直到 ctx 被取消。
func (a *App) Worker(*command.Daemon) (func(context.Context) error, error) {
    mailer, err := newMailer(a.cfg.Mail)
    if err != nil {
        return nil, err
    }
    w := queue.NewWorker(a.db, a.nc, a.cfg.Queue, nil)
    mailjob.Send.Handle(w, mailer.Send)
    article.RegisterJobs(w, a.db)
    return w.Run, nil
}
```

在 `Parts` 中登记为 `{Name: "worker", Short: "处理队列任务", Start: a.Worker}`，`serve` 运行全部部分，`serve worker` 只运行它，见[serve 命令](../general/commands.md#serve-command)。

- `w.Run` 建不了 stream 或 consumer（例如 NATS 不可用）时返回错误。worker 不关闭 `a.nc`。
- 多个实例同时运行是安全的：一个复制槽同时只能由一个连接读取，所以一个实例转发，其余的每 5 秒尝试接手；所有实例都处理任务，每个任务只交给其中一个。
- 任务量大时单独部署 `serve worker` 并增减实例数，单独部署时不需要健康检查，见[部署](../installation/deployment.md#health-checks)。

<a id="configuration"></a>
### 配置

`queue.Config` 放进根配置，字段标签为 `yaml:"queue"`；NATS 的地址在 `nats` 段，见[连接 NATS](../libraries/nats.md#connecting)：

```yaml
queue:
  stream: jobs          # 保存任务的 stream，多个应用或环境共用 NATS 时用它区分；默认 jobs
  replicas: 1           # stream 的副本数，NATS 集群中设为 3；默认 1
  concurrency: 10       # 每个 worker 实例中每个 topic 同时处理的任务数；默认 10
  max_attempts: 10      # 最多执行的次数，用完后转入失败任务表；默认 10
  lease: 30s            # JetStream 等待处理结果的时间，见“租约与停止”；默认 30s
  replication_dsn: ""   # 转发任务的复制连接；数据库连接经过 PgBouncer 时设为直连 PostgreSQL 的地址
  shutdown_timeout: 30s # 停止时等待处理中的任务的最长时间；默认 30s
```

- topic `mail.send` 的任务发布到 subject `jobs.job.mail.send`，定时消息在 `jobs.delay.*`，`jobs` 是 `stream` 的值。
- stream 已经存在时，worker 检查它的保留策略和 subject，更新 `replicas`，其他设置保持不变。stream 设置了 `max_age` 或 discard old 时 worker 拒绝启动：这两种设置会删掉还没执行的任务。同名 stream 的设置不符时换一个 `stream` 名。

<a id="failures-and-retries"></a>
## 失败与重试

处理函数返回错误时，是否重试由错误的[分类](../general/errors.md#error-kinds)决定：

| 处理函数返回的错误 | 结果 |
| --- | --- |
| 系统故障：`errorx.Unavailable`、`errorx.Internal`、不是业务错误的普通错误、panic | 按退避时间重试 |
| `errorx.ResourceExhausted`，或带有 `errorx.RetryAfter` 的错误 | 重试，有等待时间时按它 |
| 其他业务错误，例如 `errorx.NotFound`、`errorx.Conflict` | 不重试，直接转入失败任务表 |
| payload 无法解码为参数类型 | 不重试，直接转入失败任务表 |

业务错误表示这次调用本身不成立（例如文章已被删除），重试结果不变，所以处理函数直接返回业务动作的错误。

第 n 次失败后等待约 n⁴ 秒（上下浮动 10%，最长 24 小时）：

| 失败次数 | 1 | 2 | 3 | 4 | 5 | 9 |
| --- | --- | --- | --- | --- | --- | --- |
| 等待 | 1 秒 | 16 秒 | 81 秒 | 约 4 分钟 | 约 10 分钟 | 约 1.8 小时 |

默认最多执行 10 次，从第一次失败到最后一次重试约 4.3 小时。等待期间同一个 topic 的其他任务照常处理。

<a id="marking-errors"></a>
### 用 `queue.Permanent`、`queue.Retry`、`queue.RetryAfter` 改变重试

- `queue.Permanent(err)`：重试也是徒劳的系统错误（例如推送服务拒绝了配置错误的请求），任务直接转入失败任务表。
- `queue.Retry(err)`：稍后可能消失的业务错误，任务照常重试，例如数据由另一个系统同步过来、还没同步到：

```go
if errors.Is(err, crm.ErrCustomerNotFound) {
    return queue.Retry(err)
}
```

- `queue.RetryAfter(err, d)`：在 `d` 之后重试，例如第三方返回 429 和 `Retry-After`（`resp` 是第三方的 `*http.Response`）：

```go
if resp.StatusCode == http.StatusTooManyRequests {
    wait, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
    return queue.RetryAfter(errors.New("rate limited"), time.Duration(wait)*time.Second)
}
```

三者同时出现时 `Permanent` 优先。每次重试都计入执行次数，次数用完时照常转入失败任务表。

<a id="final-attempt"></a>
### 在最后一次失败时记下结果：`queue.AttemptFromContext`

任务自己保存了处理状态时（例如投递记录的“重试中”），在放弃之前改为最终结果。`queue.AttemptFromContext` 返回第几次执行、是否最后一次，`queue.IsPermanent` 报告这个错误是否让任务直接结束：

```go
if err != nil {
    if _, final := queue.AttemptFromContext(ctx); final || queue.IsPermanent(err) {
        markFailed(ctx, a.db, id, err) // 任务不会再执行
    }
    return err
}
```

从失败任务表[重新发布](#failed-jobs)的任务从第 1 次重新计数。

<a id="leases-and-shutdown"></a>
## 租约与停止

JetStream 把任务交给 worker 后等待结果的时间是 `lease`（默认 30 秒），处理期间 worker 每 `lease/3` 自动延长一次，所以任务执行多久都可以，长任务也保持默认配置。worker 只在有空闲的并发名额时领取任务。

- **worker 崩溃**：`lease` 到期后任务交给其他 worker。
- **停止 worker**：停止领取新任务，等待处理中的任务完成，最多 `shutdown_timeout`。超时的任务放回 JetStream，处理函数的 `ctx` 以 `queue.ErrShuttingDown` 取消；worker 等这些处理函数返回后才停止，之后关闭数据库。放回的任务再次执行时计入一次执行。

<a id="testing"></a>
## 测试

处理函数是普通的函数，直接调用它测试。

发布任务的一方：用 [`dbtest.Open`](../testing/testing.md#creating-test-databases) 打开包含 `queue` 迁移的测试数据库，`queuetest.Pending` 读出已提交、还没转发的任务，这时不运行 worker：

```go
db := dbtest.Open(t, migrations.FS)
// ... 调用发布文章的业务动作
if jobs := queuetest.Pending(t, db, mailjob.Send); len(jobs) != 1 {
    t.Fatalf("queued %d mails", len(jobs))
}
```

端到端运行 worker 时，`queuetest.Conn` 连接环境变量 `NATS_TEST_URL` 给出的测试 NATS，`queuetest.Config` 使用随机名字的 stream，测试结束时删除：

```go
w := queue.NewWorker(db, queuetest.Conn(t), queuetest.Config(t), nil)
article.PublishAt.Handle(w, publish.Job)
queuetest.Run(t, w) // 测试结束时停止
```

<a id="failed-jobs"></a>
## 失败任务：`queue.FailedJobs`

不再重试的任务保存在 `queue_failed_jobs` 表中，一直保留到被重试或删除。`db` 是 `*bun.DB`：

```go
failed := queue.NewFailedJobs(db)

jobs, err := failed.List(ctx, queue.FailedQuery{Topic: "mail.send", Limit: 50}) // 从新到旧
job, err := failed.Get(ctx, id) // job.Error 是最后一次的错误，包括堆栈
err = failed.Retry(ctx, id)     // 修复问题后重新发布；不存在时返回 queue.ErrFailedJobNotFound
err = failed.Delete(ctx, id)
```

`RetryAll(ctx, topic, before, limit)` 和 `DeleteAll` 按 topic 批量处理（topic 为空时是全部），每次最多 `limit` 条，返回处理的条数，循环调用直到返回值小于 `limit`。`before` 取开始处理之前的时间：重试后马上又失败的任务失败时间在它之后，一轮中只放回一次。

`Cleanup(保留期)` 返回删除早于保留期的失败任务的函数，注册为每天的[定时任务](scheduling.md#framework-cleanups)，`sched` 是入口中的 `*schedule.Scheduler`：

```go
sched.Add("queue:prune-failed", "@daily", schedule.Cleanup(failed.Cleanup(30*24*time.Hour), 1000))
```

<a id="logging"></a>
## 日志

- 队列的日志带有 `component=queue`，可以[单独设置级别](../general/logging.md#component-levels)。任务等待重试记为 `warn`，转入失败任务表记为 `error`。NATS 的断开和重连在 `component=nats` 的日志中。
- 处理函数的 `ctx` 中带有 `message_id`（任务 ID）、`attempt`（第几次执行）和发布时的 `request_id`，`slog.InfoContext(ctx, ...)` 记录的日志自动带上它们，请求和它产生的任务由此串起来。

<a id="job-events"></a>
## 任务事件：`queue.WithObserver`

`queue.WithObserver` 让 worker 在每次状态变化时调用一个函数。成功的任务只体现在事件中，需要执行历史时把事件写进应用自己的表，用[定时任务](scheduling.md)清理。`JobRun` 是应用的模型：

```go
w := queue.NewWorker(a.db, a.nc, a.cfg.Queue, nil,
    queue.WithObserver(func(ctx context.Context, e queue.Event) {
        if e.Kind == queue.JobStarted {
            return
        }
        _, err := a.db.NewInsert().Model(&JobRun{JobID: e.Job.ID, Topic: e.Job.Topic,
            Status: e.Kind.String(), Duration: e.Duration}).Exec(ctx)
        if err != nil {
            slog.ErrorContext(ctx, "record job run failed", "error", err)
        }
    }))
```

- 事件的 `Kind`：`JobStarted`（交给处理函数）、`JobSucceeded`（成功并已确认）、`JobRetrying`（失败，`RetryAt` 是下一次执行时间）、`JobFailed`（转入失败任务表）、`JobReleased`（停止等待超时，放回 JetStream）。
- 除 `JobStarted` 外，事件在结果写回之后报告；`Err` 是失败原因。
- Observer 在处理任务的 goroutine 中同步调用，耗时占用并发名额；较重的工作缓冲后批量处理。

<a id="stats-command"></a>
## 队列状态：`queue status` 命令

`queuecmd.New` 返回 `queue` 命令，在 [bootstrap.Commands](../concepts/actions.md#as-commands) 中声明；`queuecmd.WithJetStream` 让它同时显示 JetStream 中的状态，`a.nc` 是应用的 NATS 连接：

```go
queuecmd.New(a.db, queuecmd.WithJetStream(a.nc, a.cfg.Queue)),
```

`queue status` 显示复制槽（还没转发的任务）和 JetStream（其余的任务）：

```text
slot            tenon_queue_3f2a9c1e5b7d4e60
state           relaying (pid 48213)
lag             12.4 KiB
stream          jobs
deferred        5

TOPIC         PENDING  IN FLIGHT  REDELIVERED  LAST DELIVERED       STATE
mail.send     9        3          0            2026-11-12 09:41:07  ok
sms.send      4        0          0            -                    no handler: no worker has registered this topic
```

- `state` 为 `idle` 时没有 worker 在转发；为 `missing` 或 `lost` 时 worker 拒绝启动，用 `queue slot recreate` 重建，见[重建复制槽](../installation/operations.md#queue-recovery)。
- `deferred` 是延迟任务和等待重试的任务。
- `STATE` 为 `no handler` 时没有 worker 注册这个 topic，任务一直等待，通常是忘了 `Handle` 或 topic 名不一致；为 `stalled` 时有任务等待却没有 worker 领取，通常是 worker 全部停止了。
- 连不上 NATS 时，命令输出数据库一侧的状态后以错误退出。
- topic 的 consumer 名是把 `.` 换成 `_` 的 topic 名，例如 `nats consumer info jobs mail_send`。

`queue failed --topic mail.send --limit 10` 从新到旧列出失败任务，错误只显示第一行。

<a id="queue-api"></a>
## 运维接口：`queueapi.Register`

`queue/queueapi` 把队列状态和失败任务做成 JSON 接口，`@go-tenon/ui` 的 [`QueuePanel`](../frontend/features.md#queue-panel) 使用它们。`api` 是 `rest.New` 创建的 API，`a.tokens` 是 `*apitoken.Store`：

```go
opts := queueapi.Options{
    Topics: []queue.TopicMeta{
        mailjob.Send.Meta("发送邮件", "向用户发送系统通知和验证邮件"),
    },
}
if a.nc != nil { // 生成文档和客户端的 DocRouter 没有 NATS 连接
    opts.Admin = queue.NewAdmin(a.nc, a.cfg.Queue)
}
queueapi.Register(api, rest.RequireToken(api, a.tokens.Validate, "queue:manage"), a.db, opts)
```

- 第二个参数保护全部接口，权限写进[权限目录](../security/authorization.md#permission-catalog)。
- `Topics` 为各 topic 提供显示名称和说明（也可写 i18n 消息 ID 或在翻译文件中定义 `queue.topic.<topic>.label` / `description`）；面板优先展示显示名称，悬停时提示内部 topic 名。
- 没有 `Admin` 时概览中没有 `jetstream` 字段，暂停和恢复返回 `503 queue.no_admin`；这两个接口总是注册，生成的客户端与运行时一致。

| 接口 | 作用 |
| --- | --- |
| `GET /queue/status`（`queue-status`） | 概览：复制槽、stream、每个 topic 一行、失败任务总数 |
| `GET /queue/failed`（`queue-failed-list`） | 失败任务列表，按 `topic` 筛选，分页 |
| `GET /queue/failed/{id}`（`queue-failed-get`） | 失败任务详情：payload、元数据、完整的错误 |
| `POST /queue/failed/{id}/retry`（`queue-failed-retry`） | 重新发布一条 |
| `DELETE /queue/failed/{id}`（`queue-failed-delete`） | 删除一条 |
| `POST /queue/failed/retry?topic=`（`queue-failed-retry-all`） | 重新发布一个 topic（为空时全部）的失败任务 |
| `DELETE /queue/failed?topic=`（`queue-failed-delete-all`） | 删除一个 topic（为空时全部）的失败任务 |
| `POST /queue/topics/{topic}/pause`（`queue-topic-pause`） | 暂停 topic 一小时 |
| `POST /queue/topics/{topic}/resume`（`queue-topic-resume`） | 恢复 topic |

- topic 的 `state` 是 `ok`、`no_handler`、`paused`、`stalled`，或只有失败任务数的 `absent`。读不到 JetStream（3 秒超时）时概览照常返回数据库一侧，`jetstream.error` 是错误信息。
- 全部重试、全部删除只处理请求之前失败的任务，分批处理直到完成。
- 暂停的 topic 照常发布和保存任务，worker 不再领取，一小时后自动恢复；下游故障时先暂停，免得任务大量转入失败任务表。更长的暂停用 `Admin.Pause(ctx, topic, until)`。
- 失败任务不存在返回 `404 queue.failed_job_not_found`；没有 worker 注册过的 topic，暂停和恢复返回 `404 queue.topic_not_found`。
- 每次重试、删除、暂停和恢复记一条 Info 日志，带上操作者、topic 和条数。
