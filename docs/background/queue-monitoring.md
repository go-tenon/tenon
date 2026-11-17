# 队列：失败任务与监控

[队列](queue.md)一页介绍了怎样发布和处理任务。任务上线运行以后，还需要知道它们运行得怎么样：哪些任务失败了、为什么失败、修好之后怎样重新执行，以及队列有没有积压。这一页介绍这些工具：

- **失败任务**：重试次数用完的任务保存在表中，可以查看、重试或删除；
- **日志**和**任务事件**：每个任务的执行记录；
- **队列状态**命令和**运维接口**：查看积压和失败的情况，运维页面调用后者。

阅读前需要了解：[队列](queue.md)。

- [失败任务](#failed-jobs)
- [日志](#logging)
- [任务事件](#job-events)
- [队列状态](#stats-command)
- [运维接口](#queue-api)
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

任务在两个地方等待：还没转发的在 PostgreSQL 的复制槽中；其余的在 JetStream 中，包括等待处理、处理中和定时投递的任务。`queue` 命令和[运维接口](#queue-api)把它们放在一起显示。

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

<a id="queue-api"></a>
## 运维接口

`queue/queueapi` 把同样的内容做成 JSON 接口，注册在应用的 [REST API](../http/README.md) 上，供运维页面调用。它只读写队列已有的数据，不新增任何表：吞吐、耗时等指标交给 Grafana（见[可观测性](../general/telemetry.md#queue)），接口只把它的链接交给前端；重建复制槽只能用 `queue slot recreate`。

```go
// a.nc 是应用的 NATS 连接，Admin 不关闭它
admin := queue.NewAdmin(a.nc, a.cfg.Queue)

queueapi.Register(api, rest.RequireToken(api, a.tokens.Validate, "queue:manage"), a.db, queueapi.Options{
    Admin: admin,
    Links: map[string]string{"Grafana": a.cfg.GrafanaURL},
})
```

第二个参数保护全部接口，通常像上面这样要求一项运维权限，见[在路由上声明权限](../security/authorization.md#route-permissions)；权限名由应用决定，记得写进[权限目录](../security/authorization.md#permission-catalog)。不设置 `Admin` 时，接口只返回数据库一侧的状态，也不注册暂停和恢复；`Links` 中地址为空的项不返回。

| 方法和路径 | operationId | 作用 |
| --- | --- | --- |
| `GET /queue/status` | `queue-status` | 概览：复制槽、JetStream 的 stream、每个 topic 一行，失败任务总数，外部链接 |
| `GET /queue/failed?topic=&page=&per_page=` | `queue-failed-list` | 失败任务列表，从新到旧，按 topic 筛选、分页；同时返回每个 topic 的失败任务数 |
| `GET /queue/failed/{id}` | `queue-failed-get` | 失败任务详情：payload（原样返回的 JSON）、元数据、完整的错误和堆栈 |
| `POST /queue/failed/{id}/retry` | `queue-failed-retry` | 重新发布一条失败任务 |
| `DELETE /queue/failed/{id}` | `queue-failed-delete` | 删除一条失败任务 |
| `POST /queue/failed/retry?topic=` | `queue-failed-retry-all` | 重新发布一个 topic 的失败任务，`topic` 为空时是全部 topic，返回条数 |
| `DELETE /queue/failed?topic=` | `queue-failed-delete-all` | 删除一个 topic 的失败任务，`topic` 为空时是全部 topic，返回条数 |
| `POST /queue/topics/{topic}/pause` | `queue-topic-pause` | 暂停 topic 一小时，见下文；设置了 `Admin` 时才有 |
| `POST /queue/topics/{topic}/resume` | `queue-topic-resume` | 恢复 topic；设置了 `Admin` 时才有 |

路径相对于 API 的 `BasePath`。operationId 都以 `queue-` 开头，[生成的客户端](../http/clients.md)中是 `api.queue.status()`、`api.queue.failedList()` 这样的函数。时间都是 RFC 3339 格式。

- 概览中每个 topic 的 `state` 与 `queue status` 的 `STATE` 相同：`ok`、`no_handler`、`paused`、`stalled`；`absent` 表示没有它在 JetStream 中的状态，只有失败任务数。`ok` 和 `stalled` 的 topic 可以暂停，`paused` 的可以恢复。
- 读不到 JetStream（NATS 不可用，或者 3 秒内没有读完）时，概览照常返回数据库一侧的状态，`jetstream.error` 是错误信息。
- 全部重试、全部删除只处理请求之前失败的任务：重试后马上又失败的任务在这一轮里只放回一次。它分批处理，每批 1000 条，直到处理完。
- 暂停一个 topic 后，任务照常发布和保存，worker 不再领取新的任务，处理中的任务不受影响；worker 重启后仍然暂停，一小时后自动恢复。下游服务出了故障时先暂停对应的 topic，免得任务大量失败转入失败任务表。需要更长的暂停时调用 `admin.Pause(ctx, topic, until)`。
- 失败任务不存在时返回 `404 queue.failed_job_not_found`；没有 worker 为 topic 注册过处理函数时，暂停和恢复返回 `404 queue.topic_not_found`。
- 每次重试、删除、暂停和恢复记一条 Info 日志，带上操作者（`subject`，主体的 `Ref`）、topic 和条数。

接口只返回数据，面板页面由前端实现。框架计划在 `@go-tenon/ui` 中提供现成的队列面板页面，目前还没有，在那之前用生成的客户端自己搭建。

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

