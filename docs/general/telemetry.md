# 可观测性

应用上线之后，你需要知道它运行得怎么样：每秒处理多少请求、哪个接口变慢了、错误是从哪里来的。可观测性就是回答这些问题的能力，它通常由三类数据组成：**指标**、**链路**和**日志**。

Tenon 使用 [OpenTelemetry](https://opentelemetry.io/docs/languages/go/) 报告这些数据。框架的 HTTP 服务、数据库连接池、队列、调度器、限流器、MCP 服务和实时频道都可以报告指标，前五个还报告链路。`telemetry` 包把指标、链路和应用的日志一起推送到一个 OTLP 接收端，例如 [OpenObserve](https://openobserve.ai/)，应用直接推送。

和 Tenon 的其他功能一样，指标和链路都需要显式开启：把 `MeterProvider`、`TracerProvider` 交给需要报告的组件，没有传入的组件就不报告。Redis、Memcached 等依赖由各自的生态报告，例如 rueidis 的 [rueidisotel](https://pkg.go.dev/github.com/redis/rueidis/rueidisotel)，把同一个 Provider 交给它们即可。

- [示例用法](#example-usage)
- [配置](#setup)
    - [自己创建 Provider](#custom-providers)
- [框架组件](#components)
    - [HTTP 请求](#http)
    - [数据库](#database)
    - [队列](#queue)
    - [定时任务](#schedule)
    - [限流](#ratelimit)
    - [MCP 服务](#mcp)
    - [实时频道](#realtime)
    - [Go 运行时](#runtime)
- [链路与日志](#trace-logs)
- [错误追踪](#error-tracking)
- [告警示例](#alerting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

在入口中最先打开 `telemetry`，再把它的 Provider 交给其他组件：

```go
tel, err := telemetry.Open(ctx, cfg.Telemetry)
if err != nil {
    return err
}
stack.MustAdd("telemetry", tel.Shutdown)
slog.SetDefault(logging.New(cfg.Logs, logging.Export(tel.Logs)))

db, err := database.Open(ctx, cfg.Database, database.WithTelemetry(tel.Meters, tel.Tracers))
// ...
router.Use(httpx.Telemetry(tel.Meters, tel.Tracers))
router.Use(httpx.Defaults(cfg.HTTP)...)
```

这样，日志、数据库语句和 HTTP 请求的指标与链路都会推送到配置的接收端。接收端的地址为空时关闭推送，所以本地开发保持默认配置即可。

<a id="setup"></a>
## 配置

在配置文件的 `telemetry` 段填写接收端：

```yaml
telemetry:
  endpoint: ${OTLP_ENDPOINT:-}    # 例如 http://openobserve:5080/api/default
  headers:
    Authorization: Basic ${OTLP_TOKEN:-}
  service: api
  version: ${APP_VERSION:-}
```

- `endpoint` 是基础地址，日志、指标、链路分别发往其下的 `/v1/logs`、`/v1/metrics`、`/v1/traces`。为空时关闭推送，本地开发保持为空。
- `headers` 附加到每个请求，通常是认证信息。
- `service` 为空时取可执行文件名；`version` 为空时取构建信息中的模块版本。日志、指标、链路都用这里的服务名和版本，`logs` 段沿用它们。
- `sample_ratio` 是新链路的采样比例，默认 `1`，即全部记录。请求带着调用方的链路进来时，跟随调用方的决定。

把 `telemetry.Config` 放进[根配置](configuration.md#loading-configuration)，在入口中**最先**打开它，如[示例用法](#example-usage)。`tel.Shutdown` 最先登记、最后执行，发出缓冲中的数据，其他资源关闭时记录的日志也能推送出去。

`logging.Export` 让日志同时推送出去，经过和标准错误上同样的级别和格式化。`ctx` 中有 span 时，日志记录带上它的链路，日志平台可以从一条日志跳到它所在的链路。标准错误上的日志照常输出，启动阶段的错误和接收端不可用时的排查都靠它。只想推送指标和链路时，不传 `logging.Export` 即可。

三种数据共用一组资源属性：`service.name`、`service.version`、`service.instance.id`、主机名和进程号。`service.instance.id` 每个进程随机生成，同一服务的 `serve`、`work` 进程和多个副本靠它区分，各自写进自己的指标序列。

推送在后台批量进行，请求照常处理，缓冲满时丢弃新数据。推送失败记录为 `component=telemetry` 的错误日志。

<a id="custom-providers"></a>
### 自己创建 Provider

框架组件只依赖 OpenTelemetry 的接口，不一定要用 `telemetry`。需要尾部采样（只保留出错或慢的链路）、同时发往多个后端，或者让 Prometheus 来抓取时，用 OpenTelemetry 的 SDK 自己创建 Provider，或者推送到 [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/) 由它处理。日志用 `logging.Export` 接入任意 `LoggerProvider`。

<a id="components"></a>
## 框架组件

每个组件在一处同时接收 `MeterProvider` 和 `TracerProvider`。只需要其中一种时，另一个传 OpenTelemetry 的 noop 实现（`go.opentelemetry.io/otel/metric/noop`、`go.opentelemetry.io/otel/trace/noop`）。

框架按 [W3C Trace Context](https://www.w3.org/TR/trace-context/) 读写链路，内置了 propagator。

<a id="http"></a>
### HTTP 请求

`httpx.Telemetry` 是一个中间件，安装在 `httpx.Defaults` 之前，访问日志才能带上 `trace_id`，panic 转换成的 `500` 也会被记录：

```go
router.Use(httpx.Telemetry(tel.Meters, tel.Tracers))
router.Use(httpx.Defaults(cfg.HTTP)...)
```

| 指标 | 类型 | 属性 |
| --- | --- | --- |
| `http.server.request.duration` | 直方图，秒 | `http.request.method`、`http.response.status_code`、`http.route`、`url.scheme`；5xx 时还有 `error.type` |

属性遵循 OpenTelemetry 的 [HTTP 语义约定](https://opentelemetry.io/docs/specs/semconv/http/http-metrics/)，现成的 HTTP 仪表盘可以直接使用。`http.route` 是路由模板，例如 `/articles/{id}`；匹配到路由的请求才带这个属性，非标准的请求方法记为 `_OTHER`，指标序列的数量因此由应用的路由决定。

每个请求还是一个服务端 span，名称是方法和路由模板，例如 `GET /articles/{id}`。请求头带有 `traceparent` 时（例如来自前端或另一个服务），span 接在调用方的链路之后；`5xx` 的请求标记为错误，错误链记录为 span 上的 exception 事件，见[错误追踪](#error-tracking)。处理器从请求的 `ctx` 中拿到这个 span，自己创建的 span 都接在它下面。耗时指标也用这个 `ctx` 记录，日志平台可以从一个慢请求的数据点跳到它的链路。

<a id="database"></a>
### 数据库

```go
db, err := database.Open(ctx, cfg.Database, database.WithTelemetry(tel.Meters, tel.Tracers))
```

| 指标 | 类型 | 属性 |
| --- | --- | --- |
| `db.client.operation.duration` | 直方图，秒 | `db.system.name`（`postgresql`）、`db.namespace`（数据库名）、`db.operation.name`（`SELECT`、`INSERT`、`BEGIN` 等）、`db.collection.name`（表）、`server.address`、`server.port`；出错时还有 `error.type` 和 `db.response.status_code` |
| `db.client.connection.count` | 仪表 | `db.client.connection.state`（`idle`、`used`） |
| `db.client.connection.max` | 仪表 | |
| `db.client.connection.wait_time` | 直方图，秒 | 每次取连接的等待时长 |
| `db.client.connection.timeouts` | 计数 | 取连接超过 [`acquire` 的时限](../database/quick-start.md#pool-exhaustion)、返回 `ErrBusy` 的次数 |

属性遵循 OpenTelemetry 的[数据库语义约定](https://opentelemetry.io/docs/specs/semconv/database/database-metrics/)。连接池的指标都带有 `db.client.connection.pool.name`，用 `database.Name` 设置，默认是 `primary`，区分同一个应用的主库、只读副本和其他数据库。`used` 长时间等于 `max`、`wait_time` 变长时，连接池已经成为瓶颈。`db.collection.name` 是 Bun 查询的表，`NewRaw` 写的原始 SQL 没有这个属性。出错时 `error.type` 是 PostgreSQL 的 SQLSTATE，例如唯一约束冲突是 `23505`、死锁是 `40P01`；超时和取消分别是 `timeout`、`canceled`。没有查到行（`sql.ErrNoRows`）不算出错。

`ctx` 中有 span 时，每条语句是它之下的一个 span，名称是操作和表，例如 `SELECT articles`：一个慢请求的链路里能看到它执行了哪些语句、各用了多久，事务的 `BEGIN`、`COMMIT` 也在其中。队列转发任务、调度器加锁这类后台查询不属于任何请求，只计入指标，不产生链路。

span 的 `db.query.text` 是把字面量换成 `?` 之后的 SQL，例如 `SELECT … WHERE ("a"."id" = ?)`。Bun 把参数直接写进 SQL，换掉之后手机号、token 这类值只留在应用中。超过[慢查询](../database/quick-start.md#slow-queries)阈值的语句同样以这种形式记录到日志。

<a id="queue"></a>
### 队列

```go
w := queue.NewWorker(db, cfg.Queue, nil, queue.WithTelemetry(tel.Meters, tel.Tracers))
```

worker 报告每个任务的等待时长 `queue.job.wait` 和处理耗时 `queue.job.duration`，各指标的含义见[队列的指标](../libraries/queue.md#metrics)。积压的任务数由 nsqd 统计，见[队列状态](../libraries/queue.md#stats-command)。

发布任务时自动把 `ctx` 中的链路写入任务的元数据（`traceparent`）。worker 为每次执行创建一个 `process <topic>` span，接在发布任务的 span 之后：发布文章的请求和随后发送通知邮件的任务出现在同一条链路中，中间隔了多久、邮件为什么失败都能看到。每次重试是一个新的 span，失败、重试和租约丢失时标记为错误。

指标和 span 都用 [消息语义约定](https://opentelemetry.io/docs/specs/semconv/messaging/) 的 `messaging.system`（驱动名）和 `messaging.destination.name`（topic）区分队列，按同一个字段就能筛出一个 topic 的指标和链路。

<a id="schedule"></a>
### 定时任务

```go
sched := schedule.New(locker, cfg.Schedule, nil, schedule.WithTelemetry(tel.Meters, tel.Tracers))
```

| 指标 | 类型 | 属性 | 含义 |
| --- | --- | --- | --- |
| `schedule.task.duration` | 直方图，秒 | `schedule.task`、`schedule.task.outcome` | 执行耗时，结果为 `succeeded` 或 `failed`（返回错误或 panic） |
| `schedule.task.skipped` | 计数器 | `schedule.task`、`schedule.task.skip_reason` | 应当执行却被跳过的次数，原因为 `overlap`（上一次还没结束）或 `lock_failed`（锁存储出错） |

部署多个实例时，每次触发只在执行它的实例上记录；由其他实例执行的触发不算跳过。

每次执行创建一个 `schedule <任务名>` span，是一条新链路的起点，任务中发布的队列任务接在它之后。被跳过或由其他实例执行的触发没有 span。

<a id="ratelimit"></a>
### 限流

```go
attempts := ratelimit.Observe(tel.Meters, "login", redislimit.New(rdb, "app:login", cfg))
```

| 指标 | 类型 | 属性 | 含义 |
| --- | --- | --- | --- |
| `ratelimit.requests` | 计数器 | `ratelimit.name`、`ratelimit.result` | 每次 `Allow` 的结果：`allowed`、`exceeded`（配额用完）或 `error`（存储出错） |

被拒绝的请求同时以 `429` 出现在 HTTP 请求的指标中。用法和查询示例见[限流的指标](../libraries/rate-limiting.md#metrics)。

<a id="mcp"></a>
### MCP 服务

```go
srv := mcpx.New(mcpx.Options{Name: "app"}, mcpx.WithTelemetry(tel.Meters, tel.Tracers))
```

| 指标 | 类型 | 属性 |
| --- | --- | --- |
| `mcp.server.operation.duration` | 直方图，秒 | `mcp.method.name`（`tools/call`、`tools/list`、`initialize` 等）、`gen_ai.tool.name`、`gen_ai.operation.name`（工具调用为 `execute_tool`）、`mcp.protocol.version`、`network.transport`（HTTP 为 `tcp`，stdio 为 `pipe`）；出错时还有 `error.type` |

属性遵循 OpenTelemetry 的 [MCP 语义约定](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/mcp.md)，规范目前还在 Development 阶段。工具报告错误时 `error.type` 是 `tool_error`，用它可以统计哪个工具常常失败；调用不存在或没有权限的工具属于调用方的问题，单独统计。`gen_ai.tool.name` 只记录登记过的工具，指标序列的数量保持固定。

每个请求是一个 server span，名称是方法和工具名，例如 `tools/call article_publish`，接在 HTTP 请求的 span 之下。客户端在请求的 `params._meta` 中带了 `traceparent` 时，span 接在客户端的链路之后，并链接到 HTTP 请求的 span。

<a id="realtime"></a>
### 实时频道

设置 `realtime.Options.MeterProvider` 后报告本实例的连接数 `realtime.connections` 和订阅数 `realtime.subscriptions`，见[实时频道的指标](../libraries/realtime.md#metrics)。

<a id="runtime"></a>
### Go 运行时

`telemetry.Open` 开启推送时，还报告进程自身的 Go 运行时指标，例如内存 `go.memory.used`、goroutine 数 `go.goroutine.count`、GC 的目标堆大小 `go.memory.gc.goal`，名称遵循 OpenTelemetry 的 [Go 运行时语义约定](https://opentelemetry.io/docs/specs/semconv/runtime/go-metrics/)。内存持续增长、goroutine 泄漏都能从这里看出来。

<a id="trace-logs"></a>
## 链路与日志

`ctx` 中有 span 时，用它记录的日志自动带上 `trace_id` 和 `span_id`，包括访问日志、队列和调度器的日志，以及处理函数里 `slog.InfoContext(ctx, ...)` 记录的业务日志。在链路追踪系统中可以按 `trace_id` 找到一条链路的全部日志。

用 [`logging.Export`](#setup) 推送的日志把链路写在记录本身，日志平台据此在日志和链路之间跳转。

<a id="error-tracking"></a>
## 错误追踪

组件把出错的执行记录为 span 上的 `exception` 事件：HTTP 请求的 `5xx`（包括 panic 和[页面](../responses/views.md#error-pages)的错误）、失败和重试的队列任务、失败的定时任务、出错的数据库语句和 MCP 调用。事件的属性与[错误日志](logging.md#error-logging)的字段一一对应：

| 属性 | 内容 |
| --- | --- |
| `exception.message` | `err.Error()` |
| `exception.type` | [错误码](errors.md)；没有时是最内层错误的类型，例如 `*pgconn.PgError` |
| `exception.stacktrace` | `%+v` 格式的完整错误链，包含错误**创建处**的堆栈；普通错误没有堆栈时省略 |

只记录 `5xx`，`4xx` 是调用方的问题。按 `exception` 事件聚合错误的链路平台（例如 [SigNoz](https://signoz.io)、[Uptrace](https://uptrace.dev)）据此列出出现过哪些错误、各自的次数和趋势，点进去就是堆栈和整条链路。[Sentry](https://sentry.io) 通过 OTLP 接收链路时，把这些事件变成 Issue 目前还不可靠，错误追踪推荐用前两者。

用户看到的 `5xx` 错误页和提示上有请求编号，它就是访问日志和错误日志中的 `request_id`：按它找到这个请求的日志，日志中的 `trace_id` 再跳到链路。

自己创建的 span 记录错误时用 `logging.RecordError`，属性与框架的组件一致。`span.RecordError` 记录的类型是最外层包装的类型，堆栈是调用它的位置，看不出错误从哪里来：

```go
ctx, span := tracer.Start(ctx, "sync feed")
defer span.End()
if err := sync(ctx); err != nil {
    logging.RecordError(span, err)
    span.SetStatus(codes.Error, "sync failed")
    return err
}
```

<a id="alerting"></a>
## 告警示例

导出到 Prometheus 后，指标名和属性名中的点换成下划线，单位成为后缀，例如 `http_server_request_duration_seconds`：

```promql
# 5 分钟内数据库语句的 p95 耗时超过 100 毫秒
histogram_quantile(0.95, sum by (le) (rate(db_client_operation_duration_seconds_bucket[5m]))) > 0.1

# 5 分钟内 5xx 的比例超过 1%
sum(rate(http_server_request_duration_seconds_count{http_response_status_code=~"5.."}[5m]))
  / sum(rate(http_server_request_duration_seconds_count[5m])) > 0.01

# 限流器的存储出错：使用它的请求都在返回 503
sum by (ratelimit_name) (rate(ratelimit_requests_total{ratelimit_result="error"}[5m])) > 0

# 登录被拒绝的比例超过 20%，可能有人在暴力尝试
sum(rate(ratelimit_requests_total{ratelimit_name="login", ratelimit_result="exceeded"}[5m]))
  / sum(rate(ratelimit_requests_total{ratelimit_name="login"}[5m])) > 0.2

# 每天凌晨的报表任务 25 小时内没有成功过
increase(schedule_task_duration_seconds_count{schedule_task="reports:daily", schedule_task_outcome="succeeded"}[25h]) == 0
```

<a id="api-reference"></a>
## API 参考

### telemetry.Open

```go
func Open(ctx context.Context, cfg Config) (*Providers, error)
```

- **参数**
    - `cfg`：推送的配置，见[配置](#setup)
- **返回**：日志、指标和链路的 Provider

包 `github.com/go-tenon/tenon/telemetry`。开启推送时，同时开始报告 Go 运行时指标。`cfg.Endpoint` 为空时返回空操作的 Provider，调用方按同样的方式使用它。

### telemetry.Providers

| 字段或方法 | 说明 |
| --- | --- |
| `Logs log.LoggerProvider` | 传给 `logging.Export` |
| `Meters metric.MeterProvider` | 传给各组件的 `WithTelemetry` 或 `httpx.Telemetry` |
| `Tracers trace.TracerProvider` | 同上 |
| `Shutdown(ctx) error` | 发出缓冲中的数据并关闭 |

### 各组件的接入点

| 组件 | 写法 |
| --- | --- |
| HTTP | `httpx.Telemetry(mp, tp)`，中间件，安装在 `httpx.Defaults` 之前 |
| 数据库 | `database.Open(ctx, cfg, database.WithTelemetry(mp, tp))` |
| 队列 | `queue.NewWorker(..., queue.WithTelemetry(mp, tp))` |
| 定时任务 | `schedule.New(..., schedule.WithTelemetry(mp, tp))` |
| 限流 | `ratelimit.Observe(mp, name, limiter)` |
| MCP | `mcpx.New(opts, mcpx.WithTelemetry(mp, tp))` |
| 实时频道 | `realtime.Options.MeterProvider` |
| 日志 | `logging.New(cfg, logging.Export(lp))` |
| 自己的 span | `logging.RecordError(span, err)` |

`mp`、`tp` 为 `nil` 时 panic；只需要其中一种时，另一个传 OpenTelemetry 的 noop 实现。
