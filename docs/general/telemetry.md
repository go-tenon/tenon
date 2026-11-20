# 可观测性

用 OpenTelemetry 报告指标和链路，并把指标、链路和日志一起推送到一个 OTLP 接收端，例如 [OpenObserve](https://openobserve.ai/)。框架组件拿到 Provider 后才报告。

包：`telemetry`。

<a id="example-usage"></a>
## 示例：在入口中打开 telemetry

在入口的 `open` 函数中最先打开 `telemetry`，再把 Provider 交给日志和其他组件。`cfg` 是根配置，`stack` 是入口的 `*cleanup.Stack`，`router` 是 `chi.Router`：

```go
tel, err := telemetry.Open(ctx, cfg.Telemetry)
if err != nil {
    return nil, err
}
stack.MustAdd("telemetry", tel.Shutdown)
slog.SetDefault(logging.New(cfg.Logs, logging.Export(tel.Logs)))

db, err := database.Open(ctx, cfg.Database, database.WithTelemetry(tel.Meters, tel.Tracers))
// ……
router.Use(httpx.Telemetry(tel.Meters, tel.Tracers))
router.Use(httpx.Defaults(cfg.HTTP)...)
```

- `tel.Shutdown` 最先登记，最后执行：退出前发出缓冲中的数据，其他资源关闭时的日志也能推送出去。数据在退出前丢失时，检查这一点。
- `telemetry.endpoint` 为空时 `telemetry.Open` 返回空操作的 Provider，代码照常使用它们。本地开发保持为空。

<a id="setup"></a>
## 配置：推送到 OTLP 接收端（`telemetry` 段）

`telemetry.Config` 放在[根配置](configuration.md#loading-configuration)的 `telemetry` 段：

```yaml
telemetry:
  endpoint: ${OTLP_ENDPOINT:-}  # 基础地址，例如 http://openobserve:5080/api/default；为空时不推送
  headers:                      # 附加到每个请求，通常是认证信息
    Authorization: Basic ${OTLP_TOKEN:-}
  service: api                  # service.name；为空时取可执行文件名
  version: ${APP_VERSION:-}     # service.version；为空时取构建信息中的模块版本
  sample_ratio: 1               # 新链路的采样比例，默认 1；带着调用方链路的请求跟随调用方
```

- 日志、指标、链路分别发往 `endpoint` 下的 `/v1/logs`、`/v1/metrics`、`/v1/traces`。
- 三种数据带有相同的资源属性，其中 `service.instance.id` 每个进程随机生成，区分同一服务的各个进程和副本。
- `logging.Export(tel.Logs)` 让日志同时推送出去，级别和格式化与标准错误上的相同。标准错误上的日志照常输出，接收端不可用时靠它排查。
- 推送在后台批量进行，缓冲满时丢弃新数据。推送失败记录为 `component=telemetry` 的错误日志：平台中没有数据时，先看 `endpoint` 是否为空，再看这条日志。

<a id="custom-providers"></a>
### 自己创建 Provider

框架组件只依赖 OpenTelemetry 的接口。需要尾部采样、发往多个后端或者让 Prometheus 抓取时，用 OpenTelemetry 的 SDK 自己创建 Provider，或者推送到 [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/) 处理。日志用 `logging.Export` 接入任意 `log.LoggerProvider`。

<a id="trace-logs"></a>
## 日志中的 `trace_id`：在日志和链路之间跳转

`ctx` 中有 span 时，用它记录的日志自动带上 `trace_id` 和 `span_id`，包括访问日志、队列和调度器的日志和业务日志。`logging.Export` 推送的日志带着链路，日志平台据此在日志和链路之间跳转。用户报告的请求 ID 怎样找到日志见[在日志中找到一次请求](logging.md#finding-a-request)。

日志中没有 `trace_id` 时，是记录日志时没有传入 `ctx`，或者没有安装 `httpx.Telemetry`。

<a id="error-tracking"></a>
## 错误追踪：`exception` 事件和 `logging.RecordError`

框架组件把出错的执行记录为 span 上的 `exception` 事件：HTTP 请求的 `5xx`（包括 panic）、失败和重试的队列任务、失败的定时任务、出错的数据库语句和 MCP 调用。属性与[错误日志](logging.md#error-logging)的字段对应：

| 属性 | 内容 |
| --- | --- |
| `exception.message` | `err.Error()` |
| `exception.type` | [错误码](errors.md)；没有时是最内层错误的类型，例如 `*pgconn.PgError` |
| `exception.stacktrace` | `%+v` 格式的完整错误链，包含错误**创建处**的堆栈 |

自己创建的 span 用 `logging.RecordError` 记录错误，属性与框架一致；`span.RecordError` 只记录最外层包装的类型和调用它的位置。`tracer` 由 `tel.Tracers.Tracer(...)` 创建：

```go
ctx, span := tracer.Start(ctx, "sync feed")
defer span.End()
if err := sync(ctx); err != nil {
    logging.RecordError(span, err)
    span.SetStatus(codes.Error, "sync failed")
    return err
}
```

浏览器中的错误见[前端观测](../frontend/observability.md)。

<a id="components"></a>
## 框架组件的接入点

`mp`、`tp` 是 `telemetry.Open` 返回的 `tel.Meters`、`tel.Tracers`：

| 组件 | 写法 |
| --- | --- |
| HTTP | `httpx.Telemetry(mp, tp)`，中间件，装在 `httpx.Defaults` 之前 |
| 数据库 | `database.Open(ctx, cfg, database.WithTelemetry(mp, tp))` |
| 队列 | `queue.NewWorker(db, nc, cfg, nil, queue.WithTelemetry(mp, tp))` |
| 定时任务 | `schedule.New(locker, cfg, nil, schedule.WithTelemetry(mp, tp))` |
| 限流 | `ratelimit.Observe(mp, name, limiter)` |
| MCP | `mcpx.New(opts, mcpx.WithTelemetry(mp, tp))` |
| 实时频道 | `realtime.Options.MeterProvider` |

- `mp`、`tp` 为 `nil` 时 panic。只要其中一种时，另一个传 noop 实现（`go.opentelemetry.io/otel/metric/noop`、`go.opentelemetry.io/otel/trace/noop`）。
- 没有拿到 Provider 的组件不报告：看不到某个组件的指标时，检查它的接入点。
- 链路用 `traceparent` 传递；指标遵循 OpenTelemetry 的语义约定，现成的仪表盘可以直接用。
- 导出到 Prometheus 后，指标名中的点换成下划线，单位成为后缀，例如 `http_server_request_duration_seconds`。

<a id="http"></a>
### HTTP 请求的指标和链路

`httpx.Telemetry` 装在 `httpx.Defaults` 之前：访问日志才带上 `trace_id`，panic 转换成的 `500` 也被记录。

- 指标 `http.server.request.duration`（直方图，秒），属性 `http.request.method`、`http.response.status_code`、`http.route`、`url.scheme`，5xx 时还有 `error.type`。`http.route` 是路由模板，例如 `/articles/{id}`，只有匹配到路由的请求才有。
- 每个请求是一个服务端 span，例如 `GET /articles/{id}`；请求头带有 `traceparent` 时接在调用方的链路之后。处理器中用请求的 `ctx` 创建的 span 都接在它下面。

<a id="database"></a>
### 数据库的指标和链路

| 指标 | 说明 |
| --- | --- |
| `db.client.operation.duration` | 每条语句的耗时；属性有 `db.operation.name`（`SELECT`、`BEGIN` 等）、`db.collection.name`（表，`NewRaw` 没有）、`server.address`，出错时 `error.type` 是 SQLSTATE（例如 `23505`）、`timeout` 或 `canceled` |
| `db.client.connection.count` | 连接数，按 `db.client.connection.state`（`idle`、`used`） |
| `db.client.connection.max` | 连接池上限 |
| `db.client.connection.wait_time` | 取连接的等待时长 |
| `db.client.connection.timeouts` | 取连接超过 [`acquire` 的时限](../database/database.md#pool-exhaustion)、返回 `ErrBusy` 的次数 |

- 连接池的指标带有 `db.client.connection.pool.name`，由 `database.Name` 设置，默认 `primary`；[副本](../database/database.md#read-replicas)是 `primary-replica-1`……。语句的 `server.address` 是实际执行的库。
- `ctx` 中有 span 时，每条语句是它之下的一个 span，例如 `SELECT articles`；不属于请求的后台查询只计入指标。
- span 的 `db.query.text` 中字面量换成了 `?`，手机号、token 这类参数不进入链路。

<a id="queue"></a>
### 队列的指标和链路

| 指标 | 说明 |
| --- | --- |
| `queue.job.wait` | 任务从可以执行到开始处理等了多久 |
| `queue.job.duration` | 处理耗时，按 `queue.job.outcome`（`succeeded`、`retrying`、`failed`、`released`）计数就是吞吐和失败率 |
| `queue.relay.jobs` | 从 WAL 转发的任务，`queue.relay.route` 是 `published` 或 `deferred` |
| `queue.relay.active` | 这个实例正在转发时为 1 |
| `queue.relay.lag` | 复制槽中还没确认的 WAL（字节），见[监控积压](../installation/operations.md#queue-monitoring) |

- `queue.job.*` 和 span 带有 `messaging.destination.name`（topic），按它筛出一个 topic 的指标和链路。
- 所有实例的 `queue.relay.active` 之和为 0 时没有实例在转发，WAL 会一直累积，要告警。
- 每次执行是一个 `process <topic>` span，接在发布任务的 span 之后：请求和它发布的任务在同一条链路中。

<a id="schedule"></a>
### 定时任务的指标和链路

- `schedule.task.duration`：执行耗时，属性 `schedule.task`（任务名）和 `schedule.task.outcome`（`succeeded`，或 `failed`：返回错误或 panic）。
- `schedule.task.skipped`：应当执行却被跳过的次数，`schedule.task.skip_reason` 是 `overlap`（上一次还没结束）或 `lock_failed`（锁存储出错）。多个实例时，由其他实例执行的触发不算跳过。
- 每次执行是一个 `schedule <任务名>` span，是新链路的起点，任务中发布的队列任务接在它之后。

<a id="ratelimit"></a>
### 限流的指标（`ratelimit.Observe`）

被拒绝的请求返回 `429`，已经计入 [HTTP 请求的指标](#http)。要按限流器统计（包括 HTTP 之外的调用），用 `ratelimit.Observe` 包装限流器。`db` 是入口打开的 `*bun.DB`：

```go
logins := pglimit.New(db, "login:attempts", ratelimit.Config{Rate: 5, Interval: time.Minute, Burst: 5})
attempts := ratelimit.Observe(tel.Meters, "login", logins) // 交给 login.Options.Attempts
```

指标 `ratelimit.requests`（计数器），属性 `ratelimit.name`（`Observe` 的第二个参数）和 `ratelimit.result`：`allowed`、`exceeded` 或 `error`（存储出错）。限流的 `key` 不进入指标；被限流的是谁，看访问日志中状态为 `429` 的请求。

<a id="mcp"></a>
### MCP 服务的指标和链路

- 指标 `mcp.server.operation.duration`（直方图，秒），属性 `mcp.method.name`（`tools/call`、`tools/list` 等）、`gen_ai.tool.name`（只记录登记过的工具）、`network.transport`（HTTP 为 `tcp`，stdio 为 `pipe`）。工具报告错误时 `error.type` 是 `tool_error`。
- 每个请求是一个 server span，例如 `tools/call article_publish`，接在 HTTP 请求的 span 之下；客户端在 `params._meta` 中带了 `traceparent` 时接在客户端的链路之后。

<a id="realtime"></a>
### 实时频道的指标

设置 `realtime.Options.MeterProvider` 后报告本实例的 `realtime.connections`（连接数）和 `realtime.subscriptions`（订阅数）。

<a id="runtime"></a>
### Go 运行时的指标

`telemetry.Open` 开启推送时报告 Go 运行时指标，例如 `go.memory.used`、`go.goroutine.count`。

<a id="alerting"></a>
## 告警规则示例（PromQL）

```promql
# 5 分钟内 5xx 的比例超过 1%
sum(rate(http_server_request_duration_seconds_count{http_response_status_code=~"5.."}[5m]))
  / sum(rate(http_server_request_duration_seconds_count[5m])) > 0.01

# 每天凌晨的报表任务 25 小时内没有成功过
increase(schedule_task_duration_seconds_count{schedule_task="reports:daily", schedule_task_outcome="succeeded"}[25h]) == 0

# 没有实例在转发队列任务（WAL 会一直累积）
sum(queue_relay_active) == 0
```
