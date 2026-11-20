# 日志

应用和框架都用标准库的 `log/slog` 记录结构化日志，写到全局日志器 `slog.Default()`。`logging` 按配置构造日志器，让日志自动带上请求 ID 等关联字段，并提供扩展处理链、脱敏和测试日志的方法。

包：`logging`、`logging/logtest`。

<a id="example-usage"></a>
## 示例：安装日志器并记录日志

在入口的 `open` 函数中安装一次全局日志器，`cfg.Logs` 是根配置中的 `logging.Config`（配置文件的 `logs` 段）：

```go
slog.SetDefault(logging.New(cfg.Logs))
```

业务代码直接用 slog 的包级函数，传入当前的 `ctx`：

```go
slog.InfoContext(ctx, "article published", "article_id", item.ID)
// {"time":"…","level":"INFO","msg":"article published","request_id":"9F2K…","article_id":"0f8c…"}
```

`request_id` 来自 `ctx`，见[日志中的请求 ID](#request-correlation)。标准库 `log` 包的输出（例如第三方库的 `log.Printf`）也经过这个日志器。

<a id="writing-log-messages"></a>
## 用 slog 记录日志

- 用带 `Context` 的函数（`slog.InfoContext`、`slog.ErrorContext`）并传入 `ctx`：日志才带上请求 ID、任务 ID 和 `trace_id`。
- 字段名保持稳定，例如始终用 `article_id`：日志平台按字段名检索和聚合。
- 需要单独日志器的服务（例如审计日志）通过构造函数接收 `*slog.Logger`。框架组件的日志器参数为 `nil` 时用 `slog.Default()`；关闭某个组件的日志时传 `slog.New(slog.DiscardHandler)`。

<a id="request-correlation"></a>
## 日志中的请求 ID 和关联字段

每个入口在调用业务代码之前把关联字段放进 `ctx`，用这个 `ctx` 记录的日志自动带上它们：

| 入口 | 自动带上的字段 |
| --- | --- |
| HTTP 接口 | `request_id`，由 `httpx.AssignRequestID` 写入，见[请求 ID](../http/middleware.md#request-id) |
| 队列任务 | `message_id`（任务 ID）、`attempt`（第几次执行），以及发布任务时 `ctx` 中的 `request_id` |
| 定时任务 | `task`（任务名）、`request_id`（每次执行一个） |
| MCP 工具 | `request_id`：经由 HTTP 时是 HTTP 请求的，以 stdio 运行时每次调用一个 |

一个请求和它发布的队列任务共用一个 `request_id`，见[队列的日志](../background/queue.md#logging)。`ctx` 中有 span 时，日志还带上 `trace_id` 和 `span_id`，见[链路与日志](telemetry.md#trace-logs)。

日志中没有 `request_id` 时，是记录日志时没有传入请求的 `ctx`。新增入口时，在开始处理一次调用时调用 `logging.WithRequestID(ctx, id)`。

<a id="finding-a-request"></a>
## 在日志中找到一次请求（`logging.RequestID`）

每个 HTTP 响应都有 `X-Request-ID` 响应头，值就是日志中的 `request_id`；`5xx` 的错误页上显示的请求编号也是它。用户反馈问题时让他提供这个 ID，在日志平台中按 `request_id` 检索，得到这次请求的访问日志、错误诊断、业务日志和它发布的队列任务的日志；日志中的 `trace_id` 再跳到链路。

代码中用 `logging.RequestID(ctx)` 读取当前的请求 ID，例如写进给用户的提示；没有安装 `httpx.AssignRequestID` 时返回空字符串。部署在反向代理之后时，`AssignRequestID` 沿用代理生成的 ID，代理的日志也能按它找到。

<a id="custom-context-fields"></a>
## 用 `logging.WithAttrs` 给 context 加字段

放进 `ctx` 的字段出现在之后用这个 `ctx` 记录的每一条日志中。多次调用累加字段，父 context 保持原样：

```go
ctx = logging.WithAttrs(ctx, slog.String("tenant_id", tenantID))
```

<a id="error-logging"></a>
## 记录错误：`error` 字段与堆栈

业务代码给错误加上上下文后返回，不在每一层记录：每层都记录，同一个错误就出现很多遍。框架在边界记录：`httpx.AccessLog` 的访问日志带业务错误的错误码，`rest.Register` 记录 `5xx` 的完整错误链，`rest.RequireToken` 记录认证后端的故障。

自己处理掉、不再返回的错误（例如后台循环中的失败）自己记录，把错误作为 `error` 字段传入：

```go
slog.ErrorContext(ctx, "sync feed failed", "feed_id", id, "error", err)
// {"level":"ERROR","msg":"sync feed failed","feed_id":"…","error":{"message":"…","code":"article.slug_taken","stack":"…"}}
```

`logging.New` 默认启用 `logging.ErrorFormatter`，把 `error` 类型的字段展开为分组：

| 字段 | 内容 |
| --- | --- |
| `message` | `err.Error()` |
| `code` | [错误码](errors.md)，没有时省略 |
| `stack` | `%+v` 格式的完整错误链，包含错误**创建处**的堆栈；与 `message` 相同时省略 |

`stack` 中没有堆栈时，是错误没有用 `errors.Wrap` 包装，见[处理底层错误](errors.md#wrapping-errors)。在链路上记录同一个错误用 `logging.RecordError`，见[错误追踪](telemetry.md#error-tracking)。

<a id="component-levels"></a>
## 按组件设置日志级别（`levels`）

`logs.levels` 按日志的 `component` 字段覆盖全局的 `level`，可以更高也可以更低：

```yaml
logs:
  level: debug
  levels:
    http: warn       # 访问日志只保留 4xx、5xx
    billing: info
```

框架组件的日志都带有 `component` 字段，例如 `http`（访问日志、panic、`5xx` 诊断）、`queue`、`schedule`、`database`（慢查询）、`nats`、`mcp`、`rpc`、`realtime`、`ai`、`cache`、`mail`、`telemetry`。

自己的模块在日志器或 `ctx` 上设置 `component`：

```go
logger := slog.Default().With(logging.AttrComponent, "billing")
// 或者放进 ctx，之后用这个 ctx 记录的日志都属于该组件
ctx = logging.WithAttrs(ctx, slog.String(logging.AttrComponent, "billing"))
```

只写在单条日志中的 `component`（`slog.Debug("…", "component", "billing")`）只能让级别更严格：低于全局级别的日志在 slog 判断是否记录时已经丢弃。

<a id="pipeline"></a>
## 扩展处理链：`Format`、`Pipe`、`Fanout`、`Export`

`logging.New` 的选项把自己的部分放进处理链的固定位置，由外到内是：

```text
context 字段 → 组件级别 → Pipe 中间件 → 格式化（ErrorFormatter + Format）→ 基础输出 + Fanout 输出 + Export
```

context 字段和格式化在分发之前完成，所以每个输出拿到的内容一致，脱敏规则对所有输出生效。

```go
slog.SetDefault(logging.New(cfg.Logs,
    logging.Format(slogformatter.PIIFormatter("user")),
    logging.Pipe(dropHealthChecks),
    logging.Fanout(alerts),
    logging.Export(tel.Logs),
))
```

`Format`、`Pipe`、`Fanout` 接收 [slog-formatter](https://github.com/samber/slog-formatter) 的 `Formatter`、[slog-multi](https://github.com/samber/slog-multi) 的 `Middleware` 和 `slog.Handler`，社区现成的实现可以直接传入。`logging.Export` 把日志推送到 OTLP 接收端，`tel` 是 `telemetry.Open` 的返回值，见[可观测性](telemetry.md#setup)。

<a id="formatting"></a>
### 用 `logging.Format` 格式化字段

`logging.Format` 按字段名（`FormatByKey`）、类型（`FormatByType`）或种类（`FormatByKind`）改写字段值，包括嵌套分组中的字段：

```go
logging.Format(slogformatter.FormatByType(func(u *account.User) slog.Value {
    return slog.GroupValue(slog.String("id", u.ID)) // 无论字段名是什么，只输出 ID
}))
```

不加 slog-formatter 自带的 `ErrorFormatter`：它记录的是写日志那一行的堆栈，框架默认启用的 `logging.ErrorFormatter` 记录错误创建处的堆栈。

<a id="multiple-outputs"></a>
### 用 `logging.Fanout` 输出到多个地方

`logging.Fanout` 追加的输出与基础输出并列接收日志，各自按自己的级别过滤。例如用 [slog-slack](https://github.com/samber/slog-slack) 把错误同时发到 Slack，`cfg.Alert.SlackWebhook` 是应用自己的配置项：

```go
alerts := slogslack.Option{Level: slog.LevelError, WebhookURL: cfg.Alert.SlackWebhook}.NewSlackHandler()
slog.SetDefault(logging.New(cfg.Logs, logging.Fanout(alerts)))
```

按条件分发用 slog-multi 的 `Router`。需要 logfmt 时追加 `logging.Fanout(slog.NewTextHandler(w, nil))`，`text` 格式不是严格的 logfmt。

> [!WARNING]
> 发往第三方服务的输出会产生网络请求。只发送需要的级别（通常是 `error`），并确认它异步发送，或者用 slog-multi 的 `Pool` 放到后台；同步发送时第三方服务变慢，请求也跟着变慢。

<a id="handler-middleware"></a>
### 用 `logging.Pipe` 过滤和采样日志

`logging.Pipe` 接收 `slogmulti.Middleware`，在格式化和分发之前处理每条日志，多个中间件按传入顺序由外到内执行。例如丢弃健康检查的日志：

```go
dropHealthChecks := slogmulti.NewHandleInlineMiddleware(
    func(ctx context.Context, r slog.Record, next func(context.Context, slog.Record) error) error {
        if r.Message == "health check" {
            return nil
        }
        return next(ctx, r)
    },
)
```

不记录某些路径的访问日志用 [`http.access_log.ignore_paths`](../http/middleware.md#access-log)。

<a id="custom-handlers"></a>
### 用自己的 slog Handler（`NewContextHandler`）

已有的 Handler 用 `logging.Fanout` 接入。完全绕开 `logging.New` 时，用 `logging.NewContextHandler` 包装 Handler，日志才带上 context 中的关联字段；`ErrorFormatter` 等格式化规则要自己安装：

```go
slog.SetDefault(slog.New(logging.NewContextHandler(myHandler)))
```

<a id="redacting-sensitive-data"></a>
## 日志脱敏：隐藏密码、手机号等敏感字段

框架不对字段脱敏，需要时用 `logging.Format` 加上 slog-formatter 的规则，它们对所有输出生效，框架记录的访问日志和错误诊断也经过它们。

- 规则只按字段名或类型匹配，所以敏感数据始终作为字段传入，不拼进消息。
- 访问日志中 `Authorization`、`Cookie` 等认证相关的请求头始终隐藏，见[访问日志](../http/middleware.md#access-log)。其他请求头用 `FormatByKey` 按请求头名处理，例如 `slogformatter.FormatByKey("X-Api-Key", ...)`。

<a id="builtin-redaction"></a>
### 用 `PIIFormatter` 脱敏

`PIIFormatter` 对指定字段及其子字段脱敏，`IPAddressFormatter` 整体隐藏指定字段：

```go
slog.SetDefault(logging.New(cfg.Logs, logging.Format(
    slogformatter.PIIFormatter("user"),
    slogformatter.IPAddressFormatter("client_ip"),
)))

slog.InfoContext(ctx, "user signed up",
    slog.Group("user", "id", u.ID, "email", u.Email), "client_ip", ip)
// {"msg":"user signed up","user":{"id":"bd57ffbd-…","email":"foob*******"},"client_ip":"*******"}
```

`PIIFormatter` 保留 `id` 和以 `_id`、`-id` 结尾的字段，便于关联；长度大于 5 的字符串保留前 4 个字符，其余值整体替换为 `*******`。

<a id="custom-redaction"></a>
### 自定义脱敏规则

用 `FormatByKey` 或 `FormatByType` 自己写。例如手机号只保留后 4 位：

```go
logging.Format(slogformatter.FormatByKey("phone", func(v slog.Value) slog.Value {
    s := v.String()
    if len(s) <= 4 {
        return slog.StringValue("****")
    }
    return slog.StringValue("****" + s[len(s)-4:])
}))
```

<a id="testing-logs"></a>
## 在测试中断言日志（`logtest`）

`logtest.Capture` 在测试期间把记录器安装为 `slog.Default`，测试结束后恢复：

```go
func TestPublishLogsEvent(t *testing.T) {
    rec := logtest.Capture(t)

    // ……调用 Publish

    got, ok := rec.Find("article published")
    if !ok || got.Attr("article_id") != id.String() {
        t.Fatal(rec.Records())
    }
}
```

- `Records()` 返回全部日志，`Find(msg)` 返回第一条消息为 `msg` 的日志，`Has(level, msg)` 判断有没有这条日志，`Reset()` 清空。
- `Record.Attr(key)` 返回字段值，分组以 `.` 连接，例如 `response.status`。整数字段的值是 `int64`，比较时写 `int64(404)`。
- 记录器同样输出 context 字段并展开 `error`，可以断言 `request_id` 或 `error.code`。

> [!IMPORTANT]
> `Capture` 修改全局状态，使用它的测试不调用 `t.Parallel()`。并行的测试用 `logtest.New()`，把 `rec.Logger()` 显式传给被测组件，例如 `httpx.Defaults(cfg, httpx.Logger(rec.Logger()))`。

<a id="creating-a-logger"></a>
## 配置：`logs` 段

`logging.Config` 放在根配置的 `logs` 段，零值可以直接使用。`logging.New(cfg, opts...)` 只返回日志器，由入口用 `slog.SetDefault` 安装。

```yaml
logs:
  level: ${LOG_LEVEL:-info}  # debug、info、warn、error，默认 info；slog.Debug 的日志要设为 debug 才输出
  levels:                    # 按 component 覆盖 level，见“按组件设置日志级别”
    http: warn
  format: ${LOG_FORMAT:-}    # json 或 text；为空时按输出自动选择，见“日志格式”
  time_zone: UTC             # 日志时间和字段中时间值的时区；为空时用进程的本地时区
  add_source: false          # 记录产生日志的源码位置
```

- 日志输出到标准错误，标准输出留给命令的结果（例如 `openapi` 命令）。`Writer` 字段改变输出位置，只能在代码中设置，例如测试中 `cfg.Logs.Writer = &buf`。
- 多个环境的日志混合检索时，`time_zone` 统一设为 `UTC`。
- 日志中没有服务名和版本。推送到日志平台时由 [`telemetry`](telemetry.md#setup) 段配置，作为资源属性附在日志上；采集标准错误时，由采集端按 systemd unit 或容器区分来源。

<a id="formats"></a>
### 日志格式：`json` 和 `text`

`format` 为空时按输出选择：输出到终端时是 `text`，其余（文件、管道、systemd、容器）是 `json`。所以开发时在终端中看到彩色文本，部署后采集到 JSON，两个环境用同一份配置。

- `json`：每行一个 JSON 对象，供日志系统采集，生产环境用它。让 AI 编程助手分析日志时也用 `json`。
- `text`：本地开发时阅读，基于 [tint](https://github.com/lmittmann/tint)，级别缩写为 `DBG`、`INF`、`WRN`、`ERR`，分组字段以 `.` 连接：

  ```text
  2026-11-10 17:05:12.345 INF http request component=http request.method=GET response.status=200 request_id=9F2K…
  ```

输出到管道、文件或 CI 时去掉颜色，`NO_COLOR=1` 也关闭颜色。`FORCE_COLOR=1` 让输出按终端处理（`text` 并着色）：`tenon dev` 这样运行应用，同时把输出写进 `.tenon/dev.log`，见 [AI 编程助手](../installation/ai-assistants.md#mcp-servers)。

容器分配了终端（`docker run -t`、Compose 的 `tty: true`）时自动选择的是 `text`，日志系统要 JSON 时设置 `LOG_FORMAT=json`。
