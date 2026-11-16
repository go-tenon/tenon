# 日志进阶

[日志](logging.md)一页介绍了怎样写日志、怎样配置。这一页介绍怎样定制日志的处理过程：统一格式化某些字段，同时输出到多个地方，隐藏密码等敏感信息，以及在测试中检查记录的日志。

刚开始使用 Tenon 时不需要这些，默认的配置已经够用。等到需要把日志推送到观测平台、或者要满足脱敏的要求时，再来读这一页。

阅读前需要了解：[日志](logging.md)。

- [扩展处理链](#pipeline)
    - [格式化字段](#formatting)
    - [多个输出](#multiple-outputs)
    - [Handler 中间件](#handler-middleware)
    - [使用自己的 slog Handler](#custom-handlers)
- [敏感信息脱敏](#redacting-sensitive-data)
    - [使用内置规则](#builtin-redaction)
    - [自定义规则](#custom-redaction)
    - [注意事项](#redaction-caveats)
- [测试日志](#testing-logs)
- [API 参考](#api-reference)

<a id="pipeline"></a>
## 扩展处理链

`logging.New` 按固定顺序组装处理链，你通过选项把自己的部分放进指定位置：

```text
slog.Logger
 └─ context 字段             request_id 等（框架，始终在最外层）
     └─ 组件级别              按 component 应用 levels
         └─ service / version 固定字段（框架）
             └─ Pipe          Handler 中间件：过滤、采样（logging.Pipe）
                 └─ 格式化     ErrorFormatter + 你的 Formatter（logging.Format）
                     └─ 分发 ─┬─ 基础输出：Writer，json / text
                              └─ 追加输出（logging.Fanout）
```

context 字段和格式化都在分发之前完成，所以**每个输出拿到的内容完全一致**：都带有 `request_id`，`error` 的格式相同，脱敏规则也同样生效。

```go
slog.SetDefault(logging.New(cfg.Logs,
    logging.Format(slogformatter.PIIFormatter("user")),
    logging.Pipe(dropHealthChecks),
    logging.Fanout(alerts),
))
```

三个选项接收的都是社区库的原生类型，社区里现成的 Formatter、中间件和 Handler 都可以直接传入。

<a id="formatting"></a>
### 格式化字段

`logging.Format` 接收 [slog-formatter](https://github.com/samber/slog-formatter) 的 `slogformatter.Formatter`，按字段名、类型或种类改写字段值。例如统一 `time.Duration` 的输出，或者为自己的类型定义日志表示：

```go
logging.Format(
    slogformatter.FormatByType(func(d time.Duration) slog.Value {
        return slog.StringValue(d.String()) // 输出 "52.4ms"
    }),
    slogformatter.FormatByType(func(u *account.User) slog.Value {
        return slog.GroupValue(slog.String("id", u.ID), slog.String("name", u.Name))
    }),
)
```

常用的内置 Formatter：

| Formatter | 作用 |
| --- | --- |
| `FormatByKey(key, fn)` | 改写指定字段名的值，包括嵌套分组中的同名字段 |
| `FormatByType[T](fn)` | 改写某个类型的值，无论字段名是什么 |
| `FormatByKind(kind, fn)` | 改写某种 `slog.Kind` 的值 |
| `TimeFormatter(layout, loc)` | 按指定格式输出时间值 |
| `UnixTimestampFormatter(precision)` | 把时间值输出为 Unix 时间戳 |
| `PIIFormatter(key)`、`IPAddressFormatter(key)` | 脱敏，见[敏感信息脱敏](#redacting-sensitive-data) |

> [!NOTE]
> slog-formatter 自带的 `ErrorFormatter` 记录的是**写日志那一行**的堆栈。框架默认启用的 `logging.ErrorFormatter` 记录错误创建处的堆栈，请只使用它。

<a id="multiple-outputs"></a>
### 多个输出

`logging.Fanout` 追加的输出与基础输出并列接收日志。每个输出按自己的级别过滤，例如把错误同时发到告警渠道：

```go
alerts := slogslack.Option{Level: slog.LevelError, WebhookURL: cfg.Alert.SlackWebhook}.NewSlackHandler()

slog.SetDefault(logging.New(cfg.Logs, logging.Fanout(alerts)))
```

需要按条件分发时，使用 slog-multi 的 Router：

```go
logging.Fanout(
    slogmulti.Router().
        Add(alerts, slogmulti.LevelIs(slog.LevelError)).
        Add(audit, slogmulti.AttrValueIs("component", "audit")).
        Handler(),
)
```

社区中常用的输出：

| Handler | 输出到 |
| --- | --- |
| [slog-syslog](https://github.com/samber/slog-syslog) | syslog |
| [slog-slack](https://github.com/samber/slog-slack)、[slog-telegram](https://github.com/samber/slog-telegram) | 即时通讯告警 |
| [slog-nats](https://github.com/samber/slog-nats) | NATS 消息 |
| [slog-channel](https://github.com/samber/slog-channel) | Go channel，用于进程内消费 |

> [!WARNING]
> 发送到第三方服务的输出会产生网络请求。请只发送需要的级别（通常是 `error`），并确认它是异步发送的，或者用 slog-multi 的 `Pool` 放到后台，否则第三方服务变慢时，请求的处理也会跟着变慢。

<a id="handler-middleware"></a>
### Handler 中间件

`logging.Pipe` 接收 slog-multi 的 `slogmulti.Middleware`，在格式化和分发之前处理每条日志，适合过滤或采样。例如丢弃健康检查产生的日志：

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

多个中间件按传入顺序由外到内执行。只是不想记录某些路径的访问日志时，直接配置 [`http.access_log.ignore_paths`](../http/middleware.md#access-log) 更简单。

<a id="custom-handlers"></a>
### 使用自己的 slog Handler

已有的第三方 Handler 用 `logging.Fanout` 接入即可，它会得到完整的 context 字段和格式化结果。推送到 OpenTelemetry 的日志平台用 `logging.Export`，见[推送](telemetry.md#setup)。

如果要完全绕开 `logging.New`，自己构造日志器，请用 `logging.NewContextHandler` 包装 Handler，日志才能读取 context 中的关联字段：

```go
slog.SetDefault(slog.New(logging.NewContextHandler(myHandler)))
```

此时 `ErrorFormatter` 等格式化规则需要你自己安装。

<a id="redacting-sensitive-data"></a>
## 敏感信息脱敏

框架默认不脱敏任何字段。需要时，通过 `logging.Format` 加上 slog-formatter 的脱敏规则。规则在分发之前执行，所以对所有输出都生效，框架自己记录的访问日志、错误诊断也同样会经过它们。

<a id="builtin-redaction"></a>
### 使用内置规则

`PIIFormatter` 对指定字段及其所有子字段脱敏，`IPAddressFormatter` 整体隐藏指定字段：

```go
slog.SetDefault(logging.New(cfg.Logs,
    logging.Format(
        slogformatter.PIIFormatter("user"),
        slogformatter.IPAddressFormatter("client_ip"),
    ),
))

slog.InfoContext(ctx, "user signed up",
    slog.Group("user", "id", u.ID, "email", u.Email, "phone", u.Phone),
    "client_ip", ip,
)
```

```json
{"msg":"user signed up","user":{"id":"bd57ffbd-…","email":"foob*******","phone":"1380*******"},"client_ip":"*******"}
```

`PIIFormatter` 的规则：

- 字段名是 `id`，或以 `_id`、`-id` 结尾时保留原值，便于关联；
- 长度大于 5 的字符串保留前 4 个字符，其余替换为 `*******`；
- 其他值（短字符串、数字、时间等）整体替换为 `*******`。

<a id="custom-redaction"></a>
### 自定义规则

规则不合适时，用 `FormatByKey` 或 `FormatByType` 自己写。例如手机号只保留后 4 位：

```go
logging.Format(slogformatter.FormatByKey("phone", func(v slog.Value) slog.Value {
    s := v.String()
    if len(s) <= 4 {
        return slog.StringValue("****")
    }
    return slog.StringValue("****" + s[len(s)-4:])
}))
```

按类型定义日志表示，可以让某个类型无论出现在哪个字段名下都隐藏敏感内容：

```go
logging.Format(slogformatter.FormatByType(func(u *account.User) slog.Value {
    return slog.GroupValue(slog.String("id", u.ID)) // 只输出 ID
}))
```

<a id="redaction-caveats"></a>
### 注意事项

- 规则**只按字段名或类型匹配**。规则只作用于结构化字段，所以敏感数据请始终作为结构化字段传入。
- 访问日志开启 `request_header` 后，`Authorization`、`Cookie` 等认证相关的请求头始终隐藏，见[访问日志](../http/middleware.md#access-log)。其他需要隐藏的请求头，用 `FormatByKey` 按请求头名称处理即可，它会进入嵌套的分组匹配字段名，例如 `slogformatter.FormatByKey("X-Api-Key", ...)`。

<a id="testing-logs"></a>
## 测试日志

`logging/logtest` 捕获日志，供测试断言。`Capture` 在测试期间把它安装为 `slog.Default`，测试结束后自动恢复：

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

| 方法 | 说明 |
| --- | --- |
| `Records()` | 返回全部捕获的日志 |
| `Find(msg)` | 返回第一条消息为 `msg` 的日志 |
| `Has(level, msg)` | 是否有指定级别和消息的日志 |
| `Record.Attr(key)` | 返回字段值，分组以 `.` 连接，例如 `response.status` |
| `Reset()` | 清空已捕获的日志 |

捕获的日志器同样会输出 context 字段并展开 `error`，所以也可以断言 `request_id` 或 `error.code`。整数字段的值是 `int64`，例如 `got.Attr("response.status") == int64(404)`。

> [!WARNING]
> `Capture` 修改全局状态，使用它的测试串行运行。并行测试请改用 `logtest.New()`，再把 `rec.Logger()` 显式传给被测组件，例如 `httpx.Defaults(cfg, httpx.Logger(rec.Logger()))`。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/logging`。

### Option

```go
func Format(formatters ...slogformatter.Formatter) Option
func Pipe(middlewares ...slogmulti.Middleware) Option
func Fanout(handlers ...slog.Handler) Option
func Export(lp log.LoggerProvider) Option
```

把格式化规则、Handler 中间件、追加的输出、OpenTelemetry 日志推送放进[处理链](#pipeline)的对应位置。

### NewContextHandler

```go
func NewContextHandler(h slog.Handler) slog.Handler
```

包装 `h`，让它输出 context 中的字段。只在绕开 `New` 自己构造日志器时需要。

### ErrorFormatter

```go
func ErrorFormatter() slogformatter.Formatter
```

把 `error` 字段展开为 `message`、`code`、`stack` 分组。`New` 默认启用。

### logtest

包 `github.com/go-tenon/tenon/logging/logtest`。

```go
func Capture(t testing.TB) *Recorder
func New() *Recorder
func (r *Recorder) Logger() *slog.Logger
```

`Capture` 在测试期间把记录器安装为 `slog.Default`，结束后恢复；`New` 只创建记录器，用 `Logger()` 显式传给被测组件。读取记录的方法见[测试日志](#testing-logs)。
