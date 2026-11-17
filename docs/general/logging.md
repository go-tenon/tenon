# 日志

日志记录程序运行时发生了什么：收到了哪些请求，做了什么操作，哪里出了错。线上出问题时，日志往往是你唯一的线索。

Tenon 使用 Go 标准库的 [`log/slog`](https://pkg.go.dev/log/slog) 记录**结构化日志**：每条日志除了一句消息，还带着若干个“键=值”形式的字段，并以 JSON 输出，方便日志平台按字段搜索和统计。全局日志器就是 `slog.Default()`，框架的组件和你的业务代码都通过它记录日志。

`logging` 包在 slog 之上只做了几件事：按配置构造日志器，让日志自动带上请求 ID 等关联字段，以及提供扩展处理链的方法（见[日志进阶](logging-advanced.md)）。除此之外，你使用的都是标准的 slog API。

阅读前需要了解：[context](../concepts/go-primer.md#context)；[配置](configuration.md)。

- [示例用法](#example-usage)
- [写入日志](#writing-log-messages)
- [上下文信息](#contextual-information)
    - [调用关联](#request-correlation)
    - [添加自定义字段](#custom-context-fields)
- [错误日志](#error-logging)
- [配置](#creating-a-logger)
    - [level](#config-level)
    - [format](#formats)
    - [time_zone](#config-time-zone)
    - [add_source](#config-add-source)
    - [输出位置](#config-writer)
    - [按组件设置级别](#component-levels)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

在应用的入口安装一次全局日志器：

```go
slog.SetDefault(logging.New(cfg.Logs))
```

之后，框架组件（访问日志、panic 恢复、5xx 诊断）和你的业务代码都通过它记录日志。在业务代码中，直接使用 slog 的包级函数：

```go
slog.InfoContext(ctx, "article published", "article_id", item.ID)
```

```json
{"time":"…","level":"INFO","msg":"article published","request_id":"9F2K…","article_id":"0f8c…"}
```

注意输出中的 `request_id`：你并没有写它，是因为传入了请求的 `ctx`，日志自动带上了这个请求的 ID，见[请求关联](#request-correlation)。

<a id="writing-log-messages"></a>
## 写入日志

业务代码直接使用 slog 的包级函数记录日志，以键值对的形式附加结构化字段。例如文章发布成功后：

```go
func (a *Publish) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) (*Article, error) {
    // ……在事务中检查并更新文章状态
    slog.InfoContext(ctx, "article published",
        "article_id", item.ID,
        "author_id", item.AuthorID,
    )
    return item, nil
}
```

请尽量使用带 `Context` 后缀的方法（`InfoContext`、`ErrorContext` 等）并传入当前的 `ctx`，这样日志才能自动带上请求 ID，见下一节。

如果某个动作需要单独的日志器（例如写入审计日志），像接收数据库一样通过构造函数接收 `*slog.Logger` 即可，见[依赖装配](../concepts/dependencies.md#constructor-injection)。

字段名应当保持稳定，例如始终使用 `article_id`，这样才方便在日志系统中检索和聚合。

<a id="contextual-information"></a>
## 上下文信息

<a id="request-correlation"></a>
### 调用关联

每个入口在调用业务代码之前，都会把这次调用的关联字段放进 `ctx`。业务代码只要配合传入的 `ctx` 记录日志（`slog.InfoContext(ctx, ...)`），日志就会自动带上它们，不需要知道调用来自哪个入口：

| 入口 | 自动带上的字段 |
| --- | --- |
| HTTP 接口 | `request_id` |
| 队列任务 | `message_id`（任务 ID）、`attempt`（第几次执行），以及发布任务时 `ctx` 中的 `request_id` |
| 定时任务 | `task`（任务名）、`request_id`（每次执行一个） |
| MCP 工具 | `request_id`：经由 HTTP 时是 HTTP 请求的，以 stdio 运行时每次调用一个 |

队列任务带着发布方的 `request_id`，所以一个请求（或一次定时任务）和它产生的任务可以串起来，见[队列的日志](../background/queue-monitoring.md#logging)。下面以 HTTP 接口为例说明这些字段从哪里来。

安装[默认中间件](../http/middleware.md#default-middleware)后，`httpx.AccessLog` 会为每个请求记录一条[访问日志](../http/middleware.md#access-log)，`rest.New` 创建的 API 会为 5xx 错误记录诊断信息：

```go
router.Use(httpx.Defaults(a.cfg.HTTP)...)
api := rest.New(router, rest.Options{})
```

`httpx.AssignRequestID` 还会用 `logging.WithRequestID` 把[请求 ID](../http/middleware.md#request-id) 放入请求的 context。之后，只要你在业务代码中配合请求的 `ctx` 记录日志（`slog.InfoContext(ctx, ...)`），这些日志就会自动带上 `request_id` 字段：

```json
{"level":"INFO","msg":"article published","request_id":"9F2K…","article_id":"0f8c…","author_id":"alice"}
```

这样，同一个请求的访问日志、错误诊断、业务日志，以及它发布的队列任务的日志，都可以通过 `request_id` 关联起来。部署在反向代理之后时，还可以沿用代理生成的请求 ID，把代理日志也关联进来。

需要把请求 ID 告诉调用方时（例如出错时提示用户“报告问题时附上请求编号”），用 `logging.RequestID(ctx)` 读取。它读的就是日志中的 `request_id` 字段，所以在 HTTP 请求和它发布的队列任务中读到的是同一个值；以后新增的入口只要在开始处理时调用 `logging.WithRequestID`，日志和读取就都能用上。

`ctx` 中有 OpenTelemetry 的 span 时，日志还会自动带上 `trace_id` 和 `span_id`，在链路追踪系统中可以从一个 span 直接找到它的日志，见[链路与日志](telemetry.md#trace-logs)。

<a id="custom-context-fields"></a>
### 添加自定义字段

你也可以把自己的字段放入 context，之后使用这个 context 记录的所有日志都会带上它们：

```go
ctx = logging.WithAttrs(ctx, slog.String("component", "article"))
```

多次调用 `WithAttrs` 会累加字段，父 context 保持原样。

<a id="error-logging"></a>
## 错误日志

业务代码只需添加上下文并返回错误。Tenon 会在 HTTP 边界为每个 `5xx` 错误记录一条诊断日志。如果每一层都记录一次，同一个错误就会在日志中出现很多遍，反而难以排查。

| 日志 | 记录者 | 内容 |
| --- | --- | --- |
| 访问日志 | `httpx.AccessLog` | 每个请求的方法、路由、状态码、耗时、客户端地址，以及业务错误的错误码 |
| 错误诊断 | `rest.Register` | `5xx` 错误的完整错误链 |
| 认证后端故障 | `rest.RequireBearer` | 校验器返回 `5xx` 类错误时的完整错误链 |

确实需要自己记录错误时（例如后台任务失败），把错误直接作为字段值传入即可：

```go
slog.ErrorContext(ctx, "sync feed failed", "feed_id", id, "error", err)
```

`logging.New` 默认启用 `logging.ErrorFormatter`，它会把 `error` 类型的字段展开为一个分组：

```json
{"level":"ERROR","msg":"sync feed failed","feed_id":"…","error":{"message":"article.slug_taken: pq: duplicate key","code":"article.slug_taken","stack":"article.slug_taken: pq: duplicate key\n(1) article.slug_taken\nWraps: (2) attached stack trace\n  -- stack trace:\n  | example.com/app/internal/article.(*Update).Handle\n …"}}
```

| 字段 | 内容 |
| --- | --- |
| `message` | `err.Error()` |
| `code` | [错误码](errors.md)，没有时省略 |
| `stack` | `%+v` 格式的完整错误链，包含错误**创建处**的堆栈；与 `message` 相同（普通错误没有堆栈）时省略 |

详见[错误处理](errors.md#reporting-errors)。在链路上记录同一个错误用 `logging.RecordError`，属性与这些字段对应，见[错误追踪](telemetry.md#error-tracking)。

<a id="creating-a-logger"></a>
## 配置

日志的级别、格式等需要按环境调整的值来自 `logging.Config`，通常放在配置文件的 `logs` 段：

```yaml
logs:
  level: ${LOG_LEVEL:-info}
  format: ${LOG_FORMAT:-json}
  time_zone: UTC
```

把 `logging.Config` 放进应用的根配置结构体，`config.Load` 会自动填充默认值并校验。`logging.Config` 的零值也可以直接使用，所以不写 `logs` 段也能得到合理的日志。下面逐个介绍这些字段。

<a id="config-level"></a>
### level

记录日志的最低级别：`debug`、`info`、`warn` 或 `error`，默认是 `info`。低于这个级别的日志会被丢弃。只想调整一部分日志的级别时，见[按组件设置级别](#component-levels)。

<a id="formats"></a>
### format

输出格式，`json` 或 `text`，默认是 `json`。两种格式分别面向机器和人：

- `json`：每行一个 JSON 对象，供日志系统采集和检索。生产环境使用它。
- `text`：本地开发时阅读，基于 [tint](https://github.com/lmittmann/tint)。时间显示到毫秒，级别缩写为 `DBG`、`INF`、`WRN`、`ERR`，分组字段以 `.` 连接：

  ```text
  2026-11-10 17:05:12.345 INF http request component=http request.method=GET response.status=200 request_id=9F2K…
  2026-11-10 17:05:12.401 ERR request failed error.message=boom request_id=9F2K…
  ```

  输出到终端时，时间变暗、级别按颜色区分。输出到管道、文件或 CI 时自动去掉颜色；设置 `NO_COLOR=1` 也会关闭颜色（见 [no-color.org](https://no-color.org)）。

`text` 是给人看的格式，日志系统解析用 `json`。确实需要 logfmt 时，用 `logging.Fanout(slog.NewTextHandler(w, nil))` 追加一个输出，见[多个输出](logging-advanced.md#multiple-outputs)。

> [!TIP]
> 让 AI agent 读取或分析日志时，请使用 `json`：字段完整、结构明确。agent 执行命令时通常不在终端中，`text` 输出纯文本；如果 agent 工具分配了伪终端，设置 `NO_COLOR=1` 关闭颜色码。

<a id="config-time-zone"></a>
### time_zone

日志时间使用的时区，例如 `UTC`、`Asia/Shanghai`，同时作用于字段中的时间值，包括分组中的，例如访问日志的 `request.time`、`response.time`。没有设置时使用进程的本地时区，它由 `TZ` 环境变量或 `/etc/localtime` 决定：开发机上通常是 `+08:00`，容器中往往是 UTC。

时间始终带有时区偏移，含义明确。不过，多个环境的日志混合检索时，建议统一设为 `UTC`。

<a id="config-add-source"></a>
### add_source

是否记录产生日志的源码位置，默认是 `false`。

<a id="config-writer"></a>
### 输出位置

日志默认输出到标准错误。这样，当同一个程序作为命令行工具使用时（例如 `openapi` 命令），标准输出可以完整地留给命令的结果。需要改变输出位置时（例如在测试中写入缓冲区），在代码中设置 `Writer` 字段，它只能在代码中设置：

```go
var buf bytes.Buffer
cfg.Logs.Writer = &buf
```

日志中没有服务名和版本。推送到日志平台时，它们由 [`telemetry`](telemetry.md#setup) 段配置，作为资源属性附在每条日志上；采集标准错误时，由采集端按 systemd unit 或容器区分来源。

另外，`slog.SetDefault` 还会让标准库 `log` 包的输出也经过这个日志器，所以第三方库用 `log.Printf` 打印的内容同样会变成结构化日志。

> [!NOTE]
> `logging.New` 只返回日志器，是否安装为全局日志器由入口决定。框架组件的日志器参数为 `nil` 时使用 `slog.Default()`，因此你也可以只给某个组件传入单独的日志器，例如 `rest.Options{Logger: auditLogger}`；需要关闭某个组件的日志时，传入 `slog.New(slog.DiscardHandler)`。

<a id="component-levels"></a>
### 按组件设置级别

有时只想调整一部分日志的级别：排查业务问题时打开 `debug`，又不想被访问日志刷屏；或者全局只看 `warn`，但保留某个模块的 `info`。`levels` 按日志的 `component` 字段覆盖全局的 `level`：

```yaml
logs:
  level: debug
  levels:
    http: warn       # 访问日志只保留 4xx、5xx
    billing: info
```

没有 `component` 字段，或者 `component` 不在 `levels` 中的日志，使用全局的 `level`。覆盖的级别可以比全局级别更高，也可以更低。

框架组件记录的日志都带有 `component` 字段：

| component | 日志 |
| --- | --- |
| `http` | 访问日志、panic 堆栈、`5xx` 诊断、认证后端故障、平滑停止 |
| `queue` | 任务失败与重试、转入失败任务表、领取和写回任务的数据库错误 |
| `schedule` | 定时任务失败、因重叠跳过、调度落后 |

你自己的模块通过 `With` 或 context 设置 `component`：

```go
// 服务持有的日志器
logger := slog.Default().With(logging.AttrComponent, "billing")

// 或者放进 context，之后用这个 ctx 记录的日志都属于该组件
ctx = logging.WithAttrs(ctx, slog.String(logging.AttrComponent, "billing"))
```

> [!NOTE]
> 只写在单条日志里的 `component`（例如 `slog.Debug("…", "component", "billing")`）只能让级别**更严格**：比全局级别更低的日志在 slog 判断是否记录时就已经被丢弃了。需要放宽级别的组件，请用上面两种方式设置。

<a id="troubleshooting"></a>
## 常见问题

**日志中没有 `request_id`**

记录日志时没有传入请求的 `ctx`。用 `slog.InfoContext(ctx, ...)` 这类带 `Context` 的函数，而不是 `slog.Info(...)`，见[请求关联](#request-correlation)。

**`slog.Debug` 的日志没有输出**

默认的级别是 `info`，低于它的日志被丢弃。开发时把 `level` 设为 `debug`，见 [level](#config-level)。

**开发时 JSON 日志不好读**

把 `format` 设为 `text`，见 [format](#formats)。

**某个组件的日志太多**

只调高这个组件的级别，不影响其他日志，见[按组件设置级别](#component-levels)。

**错误日志中没有堆栈**

错误没有用 `errors.Wrap` 包装，见[处理底层错误](errors.md#wrapping-errors)。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/logging`。

### New

```go
func New(cfg Config, opts ...Option) *slog.Logger
```

- **参数**
    - `cfg`：日志的配置，见[配置](#creating-a-logger)
    - `opts`：扩展处理链的选项，见[日志进阶](logging-advanced.md#api-reference)
- **返回**：构造好的日志器

只返回日志器，全局状态由调用者设置。默认启用 `ErrorFormatter`，输出到 `cfg.Writer`（默认是标准错误）。

```go
slog.SetDefault(logging.New(cfg.Logs))
```

### WithAttrs

```go
func WithAttrs(ctx context.Context, attrs ...slog.Attr) context.Context
func ContextAttrs(ctx context.Context) []slog.Attr
```

把字段放进 context，之后用这个 context 记录的日志都带上它们。多次调用累加，父 context 保持原样。`ContextAttrs` 读取已放入的字段。

### WithRequestID、RequestID

```go
func WithRequestID(ctx context.Context, id string) context.Context
func RequestID(ctx context.Context) string
```

`WithRequestID` 把 `id` 作为请求 ID 放进 context，之后用这个 context 记录的日志都带上 `request_id` 字段；`id` 为空时原样返回 `ctx`。入口在开始处理一次调用时调用它，例如 `httpx.AssignRequestID`。

`RequestID` 读取 context 中的请求 ID，没有时返回空字符串。见[调用关联](#request-correlation)。

### AttrComponent

```go
const AttrComponent = "component"
```

组件字段名，[按组件设置级别](#component-levels)时按它匹配。

### RecordError

```go
func RecordError(span trace.Span, err error)
```

在链路上记录错误，属性与 `ErrorFormatter` 的字段对应，见[错误追踪](telemetry.md#error-tracking)。

### Or

```go
func Or(l *slog.Logger) *slog.Logger
```

`l` 为 `nil` 时返回 `slog.Default()`。组件处理可选的日志器参数时使用。
