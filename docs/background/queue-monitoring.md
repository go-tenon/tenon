# 队列：失败任务与监控

[队列](queue.md)一页介绍了怎样发布和处理任务。任务上线运行以后，还需要知道它们运行得怎么样：哪些任务失败了、为什么失败、修好之后怎样重新执行，以及队列有没有积压。这一页介绍这些工具：

- **失败任务**：重试次数用完的任务保存在表中，可以查看、重试或删除；
- **日志**和**任务事件**：每个任务的执行记录；
- **队列状态**命令和**队列面板**：查看积压和失败的情况。

阅读前需要了解：[队列](queue.md)。

- [失败任务](#failed-jobs)
- [日志](#logging)
- [任务事件](#job-events)
- [队列状态](#stats-command)
- [队列面板](#panel)
- [常见问题](#troubleshooting)

<a id="failed-jobs"></a>
## 失败任务

失败任务保存在 `queue_failed_jobs` 表中，`queue.FailedJobs` 用来查看和处理它们：

```go
failed := queue.NewFailedJobs(db)

jobs, err := failed.List(ctx, queue.FailedQuery{Topic: "mail.send", Limit: 50}) // 从新到旧
job, err := failed.Get(ctx, id)      // job.Error 是最后一次的错误信息，包括堆栈
err = failed.Delete(ctx, id)         // 已经弃用的任务直接删除
```

修复问题之后，用 `Retry` 重新发布任务，执行次数从 0 开始计算：

```go
err := failed.Retry(ctx, id) // 不存在时返回 queue.ErrFailedJobNotFound
```

同一个原因导致的失败往往有很多条，例如邮件服务宕机了一个小时。`RetryAll` 和 `DeleteAll` 按 topic 批量处理，每次最多 `limit` 条，返回处理的条数；topic 为空时包括全部 topic：

```go
// 后台页面上的「全部重试」：只处理点击之前失败的任务
before := time.Now()
for {
    n, err := failed.RetryAll(ctx, "mail.send", before, 500)
    if err != nil || n < 500 {
        return err
    }
}
```

`before` 是必填的：重试后马上又失败的任务失败时间在它之后，每轮只放回一次。`DeleteAll(ctx, topic, before, limit)` 的用法相同。

失败任务一直保留。`Cleanup` 返回删除保留期以前失败的任务的清理函数，交给[定时任务](scheduling.md#framework-cleanups)每天执行一次：

```go
sched.Add("queue:prune-failed", "@daily", schedule.Cleanup(failed.Cleanup(30*24*time.Hour), 1000))
```

<a id="logging"></a>
## 日志

队列的日志带有 `component=queue` 字段，可以[单独设置级别](../general/logging.md#component-levels)。任务失败后等待重试记录为 `warn`，转入失败任务表记录为 `error`，日志中包含 topic、错误和下次重试的等待时间。与 NATS 的连接断开、重连记在 `component=nats` 的日志中，见[连接 NATS](../libraries/nats.md#connecting)。

处理函数的 `ctx` 中带有 `message_id`（任务 ID）、`attempt`（第几次执行），以及发布时的 `request_id`。使用 `slog.InfoContext(ctx, ...)` 记录的日志会自动带上它们，可以把一个请求和它产生的任务串起来。

<a id="job-events"></a>
## 任务事件

`queue.WithObserver` 让 worker 在每次状态变化时通知你。需要每个任务的执行历史时，把事件写进自己的表：成功的任务只体现在事件中：

```go
w := queue.NewWorker(a.db, a.nc, a.cfg.Queue, nil,
    queue.WithObserver(func(ctx context.Context, e queue.Event) {
        if e.Kind == queue.JobStarted {
            return
        }
        _, err := a.db.NewInsert().Model(&JobRun{
            JobID: e.Job.ID, Topic: e.Job.Topic, Attempt: e.Job.Attempt,
            Status: e.Kind.String(), Duration: e.Duration, Error: errorText(e.Err),
        }).Exec(ctx)
        if err != nil {
            slog.ErrorContext(ctx, "record job run failed", "error", err)
        }
    }))
```

这里把结束的任务写进应用自己的 `job_runs` 表，后台页面查询这张表就能看到执行历史；保留多久由应用决定，用[定时任务](scheduling.md)清理。

| 事件 | 时机 |
| --- | --- |
| `JobStarted` | 任务交给处理函数 |
| `JobSucceeded` | 处理成功并已确认 |
| `JobRetrying` | 处理失败，`RetryAt` 是下一次执行时间 |
| `JobFailed` | 转入失败任务表 |
| `JobReleased` | 停止等待超时，放回 JetStream |

除 `JobStarted` 外，事件都在结果写回之后报告，`Started` 是交给处理函数的时间，`Duration` 是处理耗时，`Err` 是失败原因。`e.Job.AvailableAt` 是任务本次可以执行的时间，给不出时为零值。Observer 在处理任务的 goroutine 中同步调用，耗时会占用并发名额：上面的写库很快没有问题，更重的工作请缓冲后批量处理。

<a id="stats-command"></a>
## 队列状态

任务在两个地方等待：还没转发的在 PostgreSQL 的复制槽中；其余的在 JetStream 中，包括等待处理、处理中和定时投递的任务。`queue` 命令和[队列面板](#panel)把它们放在一起显示。

`queuecmd.New` 返回 `queue` 命令。它和其他需要数据库的命令一样在 [bootstrap.Commands](../concepts/actions.md#as-commands) 中声明，`queuecmd.WithJetStream` 让它同时显示 JetStream 中的状态，使用应用的 NATS 连接 `a.nc`：

```go
queuecmd.New(a.db, queuecmd.WithJetStream(a.nc, a.cfg.Queue)),
```

`queue status` 显示复制槽是否有 worker 在转发、积压了多少 WAL，以及 JetStream 中的 stream 和每个 topic：

```bash
./bin/api queue status
```

```text
slot            tenon_queue_3f2a9c1e5b7d4e60
state           relaying (pid 48213)
wal status      reserved
lag             12.4 KiB
stream          jobs
messages        28 (7.4 KiB)
replicas        1
deferred        5

TOPIC         PENDING  IN FLIGHT  REDELIVERED  LAST DELIVERED       STATE
mail.send     9        3          0            2026-11-12 09:41:07  ok
report.daily  7        0          0            -                    paused until 2026-11-12 10:30:00
sms.send      4        0          0            -                    no handler: no worker has registered this topic
```

- `state` 为 `idle` 时没有 worker 在转发；为 `missing` 或 `lost` 时 worker 拒绝启动，用 `queue slot recreate` 重建复制槽，见[重建复制槽](../installation/operations.md#queue-recovery)。
- `deferred` 是 JetStream 中定时投递的任务：延迟任务和等待重试的任务。
- `PENDING` 是等待处理的任务，`IN FLIGHT` 是已经交给 worker、还没完成的任务，`REDELIVERED` 是租约到期或停止时放回、再次投递的任务。
- `STATE` 为 `no handler` 时，没有 worker 为这个 topic 注册处理函数，任务一直等待，通常是忘了注册或者 topic 名写错了；为 `stalled` 时有任务在等待，却没有 worker 在领取，通常是 worker 全部停止了。

连不上 NATS 时，命令照常输出数据库一侧的状态，然后以错误退出。

stream 和 consumer 也可以用 [nats 命令行](https://docs.nats.io/using-nats/nats-tools/nats_cli)查看，每个 topic 的 consumer 名是把 `.` 换成 `_` 的 topic 名，例如 `nats consumer info jobs mail_send`。接入监控系统用 [prometheus-nats-exporter](https://github.com/nats-io/prometheus-nats-exporter) 采集 NATS 的监控端口。

`queue failed` 从新到旧列出失败任务，错误信息只显示第一行，完整的错误和堆栈用 `FailedJobs.Get` 查看：

```bash
./bin/api queue failed --topic mail.send --limit 10
```

```text
ID                                    TOPIC      ATTEMPTS  FAILED AT            ERROR
0193e7b1-2c4f-7d8e-9a1b-5f6c7d8e9f01  mail.send  5         2026-11-12 09:41:07  send mail: dial tcp 10.0.0.12:587: connection refused
```

<a id="panel"></a>
## 队列面板

`queue/queueweb` 把同样的内容做成网页，只读写队列已有的数据：

- **概览**，每 5 秒刷新一次：
    - 复制槽是否有 worker 在转发、WAL 状态和积压；
    - JetStream 的 stream：消息数、占用的存储、副本数，定时投递的任务数；
    - 每个 topic 一行：等待处理、处理中、重新投递和失败的任务数，最近一次投递的时间，状态与 `queue status` 的 `STATE` 相同；
- **暂停和恢复 topic**：概览中每个 topic 有“暂停 1 小时”按钮，暂停的 topic 有“恢复”按钮，见下文；
- **失败任务**：按 topic 筛选、翻页；详情页显示格式化的 payload、元数据、完整的错误和堆栈；单条重试或删除，按 topic 全部重试或全部删除；

吞吐、耗时等指标交给 Grafana（见[可观测性](../general/telemetry.md#queue)），面板只在页头放它的链接；重建复制槽只能用 `queue slot recreate`。面板不新增任何表。

面板是框架自带的[服务端渲染页面](../web/views.md)，认证由应用决定，通常是 [HTTP Basic 认证](../http/optional-middleware.md#basic-auth)。挂在 HTTP 进程的路由上：

```go
router.Handle(web.AssetsPath+"*", web.Assets())
router.Handle(ui.AssetsPath+"*", ui.Assets())

// a.nc 是应用的 NATS 连接，Admin 不关闭它
admin := queue.NewAdmin(a.nc, a.cfg.Queue)

ops := router.With(httpx.BasicAuth("ops", a.verifyOps))
queueweb.Register(ops, "/ops/queue", a.db, queueweb.Options{
    Admin: admin,
    Links: map[string]string{"Grafana": a.cfg.GrafanaURL},
})
```

`a.verifyOps` 校验运维账号，见 [HTTP Basic 认证](../http/optional-middleware.md#basic-auth)。`Options.Theme` 可以设置面板的[主题](../web/components.md#themes)。不设置 `Admin` 时，面板只显示数据库一侧的状态，也没有暂停按钮；NATS 不可用时，JetStream 一栏显示错误，其余照常显示。

- 全部重试只处理点击之前失败的任务：重试后马上又失败的任务在这一轮里只放回一次。它分批处理，每批 1000 条，直到处理完。
- 暂停一个 topic 后，任务照常发布和保存，worker 不再领取新的任务，处理中的任务不受影响；worker 重启后仍然暂停，一小时后自动恢复。下游服务出了故障时先暂停对应的 topic，免得任务大量失败转入失败任务表。需要更长的暂停时调用 `admin.Pause(ctx, topic, until)`。
- 每次重试、删除、暂停和恢复记一条 Info 日志，带上操作者（Basic 认证的用户名）、topic 和条数。
- 浏览器会给跨站请求自动带上 Basic 凭据，所以面板拒绝跨站的 `POST`（`http.CrossOriginProtection`）。

<a id="troubleshooting"></a>
## 常见问题

**失败任务表越来越大**

失败任务一直保留，直到被删除或重试。用 `failed.Cleanup` 交给定时任务每天清理一次，见[失败任务](#failed-jobs)。

**点了“全部重试”，同一批任务反复被放回**

`RetryAll` 的 `before` 要用点击之前的时间：重试后马上又失败的任务，失败时间在 `before` 之后，不会在同一轮中再被放回。

**topic 的状态是“没有注册处理函数”**

有任务发布到了这个 topic，但没有 worker 为它注册处理函数，任务会一直等待。检查 worker 是否调用了这个 topic 的 `Handle`，以及发布和处理两边的 topic 名是否一致；两边共用同一个 `queue.Topic` 声明就不会写错。

**topic 的状态是“没有 Worker 在处理”**

有任务在等待，却没有 worker 领取它们，通常是所有 worker 都停止了。worker 刚处理完一批、还没有再次领取的瞬间也可能这样显示，持续出现时检查 worker 是否在运行。

**`queue status` 显示积压一直在增长**

没有 worker 在转发，或者 NATS 不可用。检查 worker 的日志和指标，见[运维](../installation/operations.md#queue-monitoring)。

