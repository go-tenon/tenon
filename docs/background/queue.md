# 队列

有些工作可以在后台完成，例如发送邮件、生成缩略图、通知第三方。在请求中同步完成它们，接口会变慢；邮件服务器暂时不可用时，请求还会直接失败。交给队列，接口立即返回，失败的工作自动重试。

队列解决了这个问题：接口只把工作作为一个**任务**发布出去就返回，由单独的 worker 进程取出执行。`queue` 包把任务交给 [NSQ](https://nsq.io)；执行失败的任务按退避时间自动重试，次数用完后转入失败任务表，等待人工处理。

任务按 topic 分类：发布方把任务发到一个 topic，worker 为每个 topic 注册一个处理函数。

Tenon 的队列有两个特点，在开始之前需要了解。第一，任务和业务数据**在同一个事务中发布**：`Publish` 把任务写进 PostgreSQL 的 WAL（预写日志），事务提交后 worker 经由逻辑复制立即读到，转发到 NSQ；事务回滚时任务一起取消。这样文章和通知任务总是同时生效、同时撤销。任务只写入 WAL，发布任务的事务之间各自独立，提交的吞吐与不发布任务时相当。

第二，每个任务**至少执行一次**：worker 崩溃或停止超时时，正在处理的任务会交给其他 worker 重新执行，处理函数需要能安全地重复执行。任务之间不保证执行顺序。

阅读前需要了解：[数据库事务](../database/quick-start.md#transactions)；[依赖装配](../concepts/dependencies.md)。失败任务的处理、队列的监控和面板见[队列：失败任务与监控](queue-monitoring.md)。

- [示例用法](#example-usage)
- [准备](#preparing)
- [发布任务](#publishing-jobs)
    - [延迟执行](#delayed-jobs)
    - [多步任务](#multi-step-jobs)
- [处理任务](#processing-jobs)
    - [启动 worker](#running-workers)
    - [配置](#configuration)
- [失败与重试](#failures-and-retries)
- [任务的租约与停止](#leases-and-shutdown)
- [测试](#testing)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

下面在注册用户的同时发布一个“发送欢迎邮件”的任务，并在 worker 中处理它：

```go
// 声明 topic：名称和任务内容的类型
var SendWelcome = queue.Topic[uuid.UUID]("users.send_welcome")

// 发布：与业务数据在同一个事务中提交
err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
    if _, err := tx.NewInsert().Model(user).Exec(ctx); err != nil {
        return err
    }
    return SendWelcome.Publish(ctx, tx, user.ID)
})

// 处理：在 worker 进程中注册
w := queue.NewWorker(db, cfg.Queue, nil)
SendWelcome.Handle(w, func(ctx context.Context, id uuid.UUID) error {
    return sendWelcomeEmail(ctx, id) // 返回错误时稍后重试
})
err = w.Run(ctx)
```

注册用户的事务提交之后，任务才会被投递；事务回滚了，任务也就不存在。worker 收到任务后调用处理函数，返回错误时，任务稍后会自动重试。下面先准备 NSQ 和 PostgreSQL，再逐步介绍发布、处理和失败的任务。

<a id="preparing"></a>
## 准备

部署 nsqd，见 [NSQ 的安装文档](https://nsq.io/deployment/installing.html)。nsqd 默认把超出内存队列（每个 channel 10000 条）的消息写到磁盘；任务必须保留，启动时加上 `--mem-queue-size=0`，让每条消息都落盘：

```bash
nsqd --data-path=/var/lib/nsqd --mem-queue-size=0
```

即使这样，nsqd 只把消息保存在本机，没有副本：nsqd 所在的机器损坏时，其中还没处理的任务会丢失；进程崩溃时，正在处理和等待重试的任务也会丢失，因为它们只在内存中。数据盘用可靠的存储，处理函数也要能安全地重复执行，丢失时可以从业务数据中找出没有完成的工作重新发布。

PostgreSQL 需要 17 以上的版本，开启逻辑复制（`wal_level = logical`，修改后需要重启），应用的数据库用户需要 `REPLICATION` 属性（`ALTER ROLE app REPLICATION`）。其他设置和它们的作用见[运维](../installation/operations.md#queue)。云数据库在参数组中开启逻辑复制，例如 RDS 的 `rds.logical_replication`。本地开发时，[入门教程第 2 章](../tutorial/02-infrastructure.md#infrastructure)的 `compose.yaml` 已经设置好了。

队列的迁移创建两张表和一个逻辑复制槽：`queue_scheduled` 保存执行时间较晚的任务，`queue_failed_jobs` 保存失败的任务；复制槽记录 worker 转发到了哪里。使用前先[发布](../database/migrations.md#publishing-framework-migrations)这些迁移：

```go
migrationCmd.AddCommand(migratecmd.Publish(
    migrate.Source{Name: "queue", FS: queue.Migrations()},
))
```

部署时先执行迁移，再启动发布任务的进程：复制槽建立之后发布的任务才会被转发。

> [!WARNING]
> 所有 worker 都停止时，复制槽中没有转发的任务会一直累积，超过上限后任务丢失。请监控积压并设置告警，见[运维](../installation/operations.md#queue-monitoring)。

<a id="publishing-jobs"></a>
## 发布任务

`queue.Topic` 把 topic 名称和任务内容（payload）的类型声明在一起。发布和处理通常不在一处，两边共用这个声明，名称写错或类型不一致时无法通过编译。例如发送邮件的 topic 声明在一个单独的小包 `mailjob` 中，所有要发邮件的业务包都用它：

```go
package mailjob

import (
    "github.com/go-tenon/tenon/mail"
    "github.com/go-tenon/tenon/queue"
)

// Send 把邮件交给 worker 发送。
var Send = queue.Topic[mail.Message]("mail.send")
```

topic 名由字母、数字、`.`、`_`、`-` 组成，加上[配置](#configuration)中的前缀后最长 64 个字符。

`Publish` 把 payload 编码为 JSON，写入传入的数据库。传入事务时，任务和业务数据一起提交。例如发布文章时，通知邮件在同一个事务中渲染并发布：

```go
func (a *Publish) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) (*Article, error) {
    item := new(Article)
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        // ... 检查权限并更新文章状态

        msg, err := a.emails.Render(ctx, "article_published", map[string]any{
            "Title": item.Title,
            "URL":   "https://example.com/" + item.Slug,
        })
        if err != nil {
            return err
        }
        msg.To = []mail.Address{a.editors}
        return mailjob.Send.Publish(ctx, tx, msg)
    })
    if err != nil {
        return nil, err
    }
    return item, nil
}
```

事务提交时 PostgreSQL 通知 worker，任务随即被转发到 NSQ。`PublishAll` 一次发布多个同类任务，批量写入。

每个任务有一个 UUIDv7 作为 ID。`ctx` 中的 `request_id` 会随任务保存，处理任务时还原到处理函数的 `ctx` 中，见[日志](queue-monitoring.md#logging)。

<a id="delayed-jobs"></a>
### 延迟执行

`queue.Delay` 让任务在一段时间之后才执行，`queue.At` 指定执行时间：

```go
var PublishAt = queue.Topic[uuid.UUID]("articles.publish")

err := PublishAt.Publish(ctx, tx, item.ID, queue.At(item.PublishAt))
```

离执行时间不超过 `max_defer`（默认 1 小时）的任务转发为 NSQ 的延迟消息；更晚的先存入 `queue_scheduled`，快到时间再转发，所以延迟多久都可以。NSQ 定期检查到期的延迟消息，新建的 topic 上第一批延迟消息可能晚几秒执行。

业务包自己的 topic 和处理它的业务动作声明在同一个文件中，见[队列任务](../concepts/actions.md#as-jobs)。

<a id="multi-step-jobs"></a>
### 多步任务

有些工作要按顺序分几步完成，例如文章发布后先生成摘要，再推送给订阅者。上一步完成后，在保存结果的事务中发布下一步：

```go
// Job 生成摘要，和下一步“推送给订阅者”一起提交。
func (a *Summarize) Job(ctx context.Context, id uuid.UUID) error {
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
}
```

每一步只依赖自己的 payload，下一步需要的数据（例如上一步的结果）直接放进下一步的 payload，类型由 `queue.Topic` 检查。下一步和这一步的结果在同一个事务中提交：这一步成功提交时才发出下一步，下一步也随之可靠送达。

一组任务并行执行、全部结束后再做下一件事，用[后台操作的并行处理](../features/operations.md#fanout)。

<a id="processing-jobs"></a>
## 处理任务

`Handle` 把一个函数注册为 topic 在 worker 上的处理函数，任务的内容按 JSON 解码为函数的参数类型。`mail.Mailer` 的 `Send` 方法正好符合要求：

```go
mailjob.Send.Handle(w, mailer.Send)
```

处理函数返回 `nil` 时任务完成；返回错误时任务稍后重试，见[失败与重试](#failures-and-retries)。

<a id="running-workers"></a>
### 启动 worker

worker 是进程的一个运行部分（见[多个运行部分](../concepts/dependencies.md#running-parts)）。它做两件事：把发布的任务转发到 NSQ，以及处理已注册 topic 的任务。

一个复制槽同时只能被一个连接读取，所以部署多个 worker 实例时，只有一个在转发，其余的待命，每 5 秒尝试一次；转发的实例停止或失联后，其中一个接手，从已经确认的位置继续。任务的处理照常进行，所有实例都处理任务。在入口的 `bootstrap` 包中：

```go
// Worker 装配队列 worker，返回的函数转发并处理队列任务，直到 ctx 被取消。
func (a *App) Worker(*command.Daemon) (func(context.Context) error, error) {
    mailer, err := newMailer(a.cfg.Mail)
    if err != nil {
        return nil, err
    }

    w := queue.NewWorker(a.db, a.cfg.Queue, nil)
    mailjob.Send.Handle(w, mailer.Send)
    return w.Run, nil
}

// Work 只运行队列 worker，work 命令调用它。
func Work(ctx context.Context, cfg Config) error {
    return run(ctx, cfg, (*App).Worker)
}
```

`newMailer` 见[装配发送器](../libraries/mail.md#assembling-mailer)。业务动作的任务入口也注册在这里，例如定时发布文章，见[队列任务](../concepts/actions.md#as-jobs)。在根命令中用 `command.Run("work", "处理队列任务", bootstrap.Work)` 注册它。

发布的任务要等 worker 转发才会执行，所以部署时**至少运行一个 worker**。`w.Run` 连不上 nsqd 时返回错误；运行后一直到进程收到停止信号，然后停止接收任务，等处理中的任务写回结果才返回；数据库由 `run` 在所有部分都返回之后才关闭。

worker 可以和 HTTP 服务、调度器[运行在同一个进程中](../general/commands.md#running-together)，任务量大了再单独部署，按任务量增减实例数。多个 worker 同时运行是安全的：转发时互不重复，同一个应用的 worker 使用同一个 channel，每个任务只交给其中一个。

<a id="configuration"></a>
### 配置

在应用配置中加入队列段：

```go
type Config struct {
    // ...
    Queue queue.Config `yaml:"queue"`
}
```

不写的项使用下面的默认值，生产环境至少要写 `nsqd` 和 `prefix`：

```yaml
queue:
  nsqd: 127.0.0.1:4150  # nsqd 的 TCP 地址，转发任务时发布到这里
  lookupd: []           # nsqlookupd 的 HTTP 地址；设置后从它们发现所有 nsqd 并消费
  prefix: ""            # 加在每个 topic 前面，例如 "app."，多个应用或环境共用 nsqd 时区分
  channel: worker       # 同一个应用的 worker 使用同一个 channel
  concurrency: 10       # 每个 topic 同时处理的任务数
  max_attempts: 10      # 最多执行的次数
  lease: 30s            # NSQ 等待处理结果的时间，见下文
  max_defer: 1h         # NSQ 延迟消息的最长延迟，要小于等于 nsqd 的 --max-req-timeout
  replication_dsn: ""   # 转发任务的复制连接；数据库连接经过 PgBouncer 时设为直连 PostgreSQL 的地址
  shutdown_timeout: 30s # 停止时等待处理中的任务的最长时间
```

`concurrency` 是每个 worker 实例的上限，部署 3 个实例时同一个 topic 最多有 30 个任务同时执行。

只有一个 nsqd 时写 `nsqd` 即可。部署多个 nsqd 时，每台应用服务器把任务发布到本机的 nsqd（`nsqd` 写本机地址），worker 通过 `lookupd` 找到全部 nsqd 并消费，一个 nsqd 宕机时，其他 nsqd 上的任务照常处理，见 [NSQ 的拓扑](https://nsq.io/deployment/topology_patterns.html)。

`concurrency` 和 `max_attempts` 是所有 topic 的默认值。个别 topic 需要不同的值时，在注册时用 `queue.Concurrency`、`queue.MaxAttempts` 覆盖，例如调用模型的任务 `article.Summarize.Handle(w, summarize.Job, queue.Concurrency(2))`。

<a id="failures-and-retries"></a>
## 失败与重试

处理函数返回错误或 panic 时，任务在一段时间后重试，第 n 次失败后等待约 n⁴ 秒（上下浮动 10%）：

| 失败次数 | 1 | 2 | 3 | 4 | 5 | 9 |
| --- | --- | --- | --- | --- | --- | --- |
| 等待 | 1 秒 | 16 秒 | 81 秒 | 约 4 分钟 | 约 10 分钟 | 约 1.8 小时 |

默认最多执行 10 次，从第一次失败到最后一次重试约 4.3 小时，足以跨过大多数第三方服务的故障。不超过 `max_defer` 的等待由 NSQ 完成，更长的等待存入 `queue_scheduled`。同一个 topic 的其他任务照常处理。

有些错误重试也是徒劳，例如文章已经被删除。用 `queue.Permanent` 包装这类错误，任务直接转入失败任务表：

```go
if errors.Is(err, article.ErrNotFound) {
    return queue.Permanent(err)
}
```

任务内容无法解码为处理函数的参数类型时，同样视为永久失败。

第三方服务限流时往往会告诉你多久之后再试，例如 HTTP 429 响应的 `Retry-After`。用 `queue.RetryAfter` 包装错误，任务在指定的时间之后重试：

```go
if resp.StatusCode == http.StatusTooManyRequests {
    wait, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
    return queue.RetryAfter(errors.New("rate limited"), time.Duration(wait)*time.Second)
}
```

这次重试同样计入执行次数，次数用完时照常转入失败任务表。

任务自己保存了处理状态时，需要在放弃之前记下最终结果，例如把一条投递记录从「重试中」改为「失败」。`queue.AttemptFromContext` 返回本次是第几次执行，以及这是否是最后一次：

```go
if err != nil {
    if _, final := queue.AttemptFromContext(ctx); final {
        markFailed(ctx, a.db, id, err) // 这是最后一次尝试
    }
    return err
}
```

从失败任务表[重新发布](queue-monitoring.md#failed-jobs)的任务从第 1 次重新计数。

<a id="leases-and-shutdown"></a>
## 任务的租约与停止

NSQ 把任务交给 worker 后，等待处理结果的时间是 `lease`（默认 30 秒），处理期间 worker 每 10 秒自动延长一次。任务执行多久都可以，长任务也保持默认配置。`lease` 要小于等于 nsqd 的 `--max-msg-timeout`（默认 15 分钟）。

- **worker 崩溃**：延长随之停止，`lease` 到期后 NSQ 把任务交给其他 worker。
- **停止 worker**：收到停止信号后，worker 停止接收新任务，等待处理中的任务完成，最多等待 `shutdown_timeout`。这期间处理函数的 `ctx` 保持有效。超时仍未完成的任务立即放回 NSQ，处理函数的 `ctx` 以 `queue.ErrShuttingDown` 取消；放回的任务再次执行时计入一次执行。

处理函数应当把 `ctx` 传给数据库查询和外部调用，这样停止超时时能尽快退出。

<a id="testing"></a>
## 测试

处理函数是普通的函数，直接调用它测试即可。发布任务的一方，用 [`dbtest.Open`](../testing/overview.md) 打开执行过 `queue.Migrations()` 的测试数据库，用 `queuetest.Pending` 读出还没转发的任务。它只能看到已经提交的任务，使用时停掉 worker：

```go
db := dbtest.Open(t, migrations.FS) // 包含已发布的 queue 迁移
publish := article.NewPublish(db, emails, editors)

// ... 创建并发布一篇文章

if jobs := queuetest.Pending(t, db, mailjob.Send); len(jobs) != 1 {
    t.Fatalf("queued %d mails", len(jobs))
}
```

需要端到端运行时，`queue/queuetest` 提供连接测试 nsqd 的配置：每次使用随机的 topic 前缀，并行的测试互不干扰，测试结束时删除这些 topic。测试 nsqd 的地址由环境变量 `NSQD_TEST_ADDR`（TCP）和 `NSQD_TEST_HTTP`（HTTP）给出：

```go
w := queue.NewWorker(db, queuetest.Config(t), nil)
article.PublishJob.Handle(w, publish.Job)
queuetest.Run(t, w) // 测试结束时停止
```

<a id="troubleshooting"></a>
## 常见问题

**发布了任务，但处理函数一直没有执行**

检查三点：worker 是否在运行（`work` 或 `start` 命令）；处理函数是否为这个 topic 注册了；发布任务的事务是否提交了，回滚时任务一起取消。

**worker 启动时报错复制槽不存在或已失效**

迁移没有执行，或者复制槽在恢复备份、主备切换后丢失了，见[重建复制槽](../installation/operations.md#queue-recovery)。

**同一个任务执行了两次**

任务至少执行一次：worker 崩溃或停止超时时，任务会在其他 worker 上重新执行。处理函数要能安全地重复执行，例如先检查工作是否已经完成。

**任务失败后没有重试，直接进了失败任务表**

处理函数返回了用 `queue.Permanent` 包装的错误，或者重试次数已经用完，见[失败与重试](#failures-and-retries)。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/queue`。

### Topic

```go
type Topic[T any] string

func (t Topic[T]) Publish(ctx context.Context, db bun.IDB, payload T, opts ...PublishOption) error
func (t Topic[T]) PublishAll(ctx context.Context, db bun.IDB, payloads []T, opts ...PublishOption) error
func (t Topic[T]) Handle(w *Worker, fn func(ctx context.Context, payload T) error, opts ...HandleOption)
```

`Publish` 把 payload 编码为 JSON，写入 `db` 的 WAL，`db` 是事务时随事务提交。`Handle` 在 worker 上注册处理函数。

### Topic.Publish

```go
func (t Topic[T]) Publish(ctx context.Context, db bun.IDB, payload T, opts ...PublishOption) error
```

- **参数**
    - `db`：业务事务；传入连接池时立即发布
    - `payload`：任务的内容，编码为 JSON
    - `opts`：`Delay` 或 `At`，见[延迟执行](#delayed-jobs)
- **返回**：写入失败时的错误

任务写入 `db` 的 WAL，`db` 是事务时随事务一起提交。

### Topic.Handle

```go
func (t Topic[T]) Handle(w *Worker, fn func(ctx context.Context, payload T) error, opts ...HandleOption)
```

- **参数**
    - `w`：worker
    - `fn`：处理函数；返回 `nil` 表示完成，返回错误表示稍后重试
    - `opts`：`Concurrency`、`MaxAttempts`

### PublishOption

```go
func Delay(d time.Duration) PublishOption
func At(t time.Time) PublishOption
```

延迟执行，见[延迟执行](#delayed-jobs)。

### HandleOption

```go
func Concurrency(n int) HandleOption
func MaxAttempts(n int) HandleOption
```

这个 topic 同时处理的任务数和最多尝试的次数，默认取自[配置](#configuration)。

### NewWorker

```go
func NewWorker(db *bun.DB, cfg Config, logger *slog.Logger, opts ...WorkerOption) *Worker
func (w *Worker) Run(ctx context.Context) error
func WithTelemetry(mp metric.MeterProvider, tp trace.TracerProvider) WorkerOption
func WithObserver(o Observer) WorkerOption
```

`Run` 转发发布的任务并处理已注册的 topic，直到 `ctx` 被取消。`WithTelemetry` 见[可观测性](../general/telemetry.md#queue)；`WithObserver` 接收[任务事件](queue-monitoring.md#job-events)。

### 处理函数的返回值

```go
func Permanent(err error) error
func RetryAfter(err error, d time.Duration) error
func AttemptFromContext(ctx context.Context) (attempt int, final bool)
```

`Permanent` 标记为最终失败，直接转入失败任务；`RetryAfter` 指定下次重试的时间；`AttemptFromContext` 返回这是第几次尝试、是否最后一次。见[失败与重试](#failures-and-retries)。

### FailedJobs

```go
func NewFailedJobs(db bun.IDB) *FailedJobs
func (f *FailedJobs) List(ctx context.Context, q FailedQuery) ([]FailedJob, error)
func (f *FailedJobs) Get(ctx context.Context, id uuid.UUID) (FailedJob, error)
func (f *FailedJobs) Retry(ctx context.Context, id uuid.UUID) error
func (f *FailedJobs) RetryAll(ctx context.Context, topic string, before time.Time, limit int) (int, error)
func (f *FailedJobs) Delete(ctx context.Context, id uuid.UUID) error
func (f *FailedJobs) DeleteAll(ctx context.Context, topic string, before time.Time, limit int) (int, error)
func (f *FailedJobs) Counts(ctx context.Context) ([]TopicCount, error)
func (f *FailedJobs) Cleanup(keep time.Duration) func(ctx context.Context, limit int) (int, error)
```

查看、重试和删除失败任务，见[失败任务](queue-monitoring.md#failed-jobs)。

### 其他

```go
func Migrations() fs.FS
func ReadStatus(ctx context.Context, db bun.IDB) (Status, error)
func RecreateSlot(ctx context.Context, db bun.IDB, force bool) error
```

`Migrations` 是队列的迁移；`ReadStatus` 读取复制槽和延迟任务的状态，`queue status` 命令用它；`RecreateSlot` 在复制槽失效后重建，见[部署](../installation/operations.md#queue-recovery)。
