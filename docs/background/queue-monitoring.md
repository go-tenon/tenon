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

队列的日志带有 `component=queue` 字段，可以[单独设置级别](../general/logging.md#component-levels)。任务失败后等待重试记录为 `warn`，转入失败任务表记录为 `error`，日志中包含 topic、错误和下次重试的等待时间。与 NATS 的连接断开、重连也记在这里。

处理函数的 `ctx` 中带有 `message_id`（任务 ID）、`attempt`（第几次执行），以及发布时的 `request_id`。使用 `slog.InfoContext(ctx, ...)` 记录的日志会自动带上它们，可以把一个请求和它产生的任务串起来。

<a id="job-events"></a>
## 任务事件

`queue.WithObserver` 让 worker 在每次状态变化时通知你。需要每个任务的执行历史时，把事件写进自己的表：成功的任务只体现在事件中：

```go
w := queue.NewWorker(a.db, a.cfg.Queue, nil,
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

每个 topic 积压、处理中、重新投递的任务数由 NATS 统计。每个 topic 是 stream 中的一个 consumer，名字是 topic 把 `.` 换成 `_`，用 [nats 命令行](https://docs.nats.io/using-nats/nats-tools/nats_cli)查看：`nats consumer report jobs` 列出 stream `jobs` 中每个 topic 的积压（Unprocessed）和处理中（Ack Pending）的任务，`nats stream info jobs` 显示 stream 占用的磁盘。`nats consumer pause jobs mail_send 1h` 暂停一个 topic 的消费。接入监控系统用 [prometheus-nats-exporter](https://github.com/nats-io/prometheus-nats-exporter) 采集 NATS 的监控端口。还没转发的任务在复制槽中，积压的字节数用 `pg_replication_slots` 查询，见[部署](../installation/operations.md#queue)；延迟超过 `max_defer` 的任务在 `queue_scheduled` 中。

`queuecmd.New` 返回 `queue` 命令，查看数据库一侧的状态和失败任务。它和其他需要数据库的命令一样在 [bootstrap.Commands](../concepts/actions.md#as-commands) 中声明：

```go
queuecmd.New(a.db),
```

`queue status` 显示复制槽是否有 worker 在转发、积压了多少 WAL，以及 `queue_scheduled` 中等待的任务：

```bash
./bin/api queue status
```

```text
slot            mygoravel_queue_3f2a9c1e5b7d4e60
state           relaying (pid 48213)
wal status      reserved
lag             12.4 KiB
scheduled       3
next scheduled  2026-11-14 09:00:00
```

`state` 为 `idle` 时没有 worker 在转发；为 `missing` 或 `lost` 时 worker 拒绝启动，用 `queue slot recreate` 重建复制槽，见[重建复制槽](../installation/operations.md#queue-recovery)。

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

- **概览**：复制槽是否有 worker 在转发、WAL 状态和积压、`queue_scheduled` 中的任务数和最早的执行时间、各 topic 的失败任务数，每 5 秒刷新一次；
- **失败任务**：按 topic 筛选、翻页；详情页显示格式化的 payload、元数据、完整的错误和堆栈；单条重试或删除，按 topic 全部重试或全部删除；
- **延迟任务**：`queue_scheduled` 中等待的任务，按执行时间排列。

吞吐、耗时等指标交给 Grafana（见[可观测性](../general/telemetry.md#queue)），各 topic 的积压交给 NATS 的监控，面板只在页头放它们的链接；重建复制槽只能用 `queue slot recreate`。面板不新增任何表。

面板是框架自带的[服务端渲染页面](../web/views.md)，认证由应用决定，通常是 [HTTP Basic 认证](../http/optional-middleware.md#basic-auth)。挂在 HTTP 进程的路由上：

```go
router.Handle(web.AssetsPath+"*", web.Assets())
router.Handle(ui.AssetsPath+"*", ui.Assets())

ops := router.With(httpx.BasicAuth("ops", a.verifyOps))
queueweb.Register(ops, "/ops/queue", a.db, queueweb.Options{
    Links: map[string]string{"Grafana": a.cfg.GrafanaURL, "NATS": a.cfg.NATSDashboardURL},
})
```

`a.verifyOps` 校验运维账号，见 [HTTP Basic 认证](../http/optional-middleware.md#basic-auth)。`Options.Theme` 可以设置面板的[主题](../web/components.md#themes)。

- 全部重试只处理点击之前失败的任务：重试后马上又失败的任务在这一轮里只放回一次。它分批处理，每批 1000 条，直到处理完。
- 每次重试和删除记一条 Info 日志，带上操作者（Basic 认证的用户名）、topic 和条数。
- 浏览器会给跨站请求自动带上 Basic 凭据，所以面板拒绝跨站的 `POST`（`http.CrossOriginProtection`）。

<a id="troubleshooting"></a>
## 常见问题

**失败任务表越来越大**

失败任务一直保留，直到被删除或重试。用 `failed.Cleanup` 交给定时任务每天清理一次，见[失败任务](#failed-jobs)。

**点了“全部重试”，同一批任务反复被放回**

`RetryAll` 的 `before` 要用点击之前的时间：重试后马上又失败的任务，失败时间在 `before` 之后，不会在同一轮中再被放回。

**`queue status` 显示积压一直在增长**

没有 worker 在转发，或者 NATS 不可用。检查 worker 的日志和指标，见[运维](../installation/operations.md#queue-monitoring)。

