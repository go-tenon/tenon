# 日志

- [简介](#introduction)
- [创建日志器](#creating-a-logger)
- [写入日志](#writing-log-messages)
- [上下文信息](#contextual-information)
    - [请求关联](#request-correlation)
    - [添加自定义字段](#custom-context-fields)
    - [使用自己的 slog Handler](#custom-handlers)
- [错误日志](#error-logging)
- [敏感信息](#sensitive-data)

<a id="introduction"></a>
## 简介

mygoravel 使用 Go 标准库的 [`log/slog`](https://pkg.go.dev/log/slog) 记录结构化日志。框架没有全局日志器：你创建一个 `*slog.Logger`，并把它显式传给 API 和需要记录日志的服务。

`logging` 包提供了两个便利：一是用几行配置构造日志器；二是让日志自动带上请求 ID 等关联字段。除此之外，你使用的都是标准的 slog API。

<a id="creating-a-logger"></a>
## 创建日志器

使用 `logging.New` 创建日志器：

```go
logger := logging.New(logging.Options{
    Writer: os.Stderr,
    Format: logging.FormatJSON,
    Level:  slog.LevelInfo,
    Attrs: []slog.Attr{
        slog.String("service", "notes"),
        slog.String("version", "0.1.0"),
    },
})
```

| 选项 | 说明 | 默认值 |
| --- | --- | --- |
| `Writer` | 日志输出位置 | `os.Stdout` |
| `Format` | `logging.FormatJSON` 或 `logging.FormatText` | JSON |
| `Level` | 最低记录级别 | `slog.LevelInfo` |
| `AddSource` | 是否记录源码位置 | `false` |
| `Attrs` | 附加到每条日志的固定字段 | 无 |

`logging.Options` 的零值即可使用。本地开发时，`FormatText` 的输出更便于阅读；生产环境中，JSON 格式更便于日志系统采集和检索。

> [!TIP]
> API 服务建议显式输出到 `os.Stderr`。这样，当同一个程序作为命令行工具使用时（例如 `openapi` 命令），标准输出可以完整地留给命令结果。

<a id="writing-log-messages"></a>
## 写入日志

使用 slog 的原生方法记录日志，并以键值对的形式附加结构化字段：

```go
logger.InfoContext(ctx, "note created",
    "note_id", noteID,
    "owner_id", ownerID,
)
```

请尽量使用带 `Context` 后缀的方法（`InfoContext`、`ErrorContext` 等）并传入当前的 `ctx`，这样日志才能自动带上请求 ID，见下一节。

字段名应当保持稳定，例如始终使用 `note_id` 而不是有时写 `noteId`，这样才方便在日志系统中检索和聚合。

<a id="contextual-information"></a>
## 上下文信息

<a id="request-correlation"></a>
### 请求关联

把日志器传给 API 后，默认中间件会为每个请求记录一条访问日志：

```go
api := rest.New(rest.Options{
    Middleware: httpx.MiddlewareOptions{Logger: logger},
})
```

中间件还会把请求 ID 放入请求的 context。之后，只要你在业务代码中用 `logging.New` 创建的日志器配合请求的 `ctx` 记录日志，这些日志就会自动带上 `request_id` 字段：

```json
{"level":"INFO","msg":"note created","service":"notes","request_id":"9F2K…","note_id":"0f8c…"}
```

这样，同一个请求的访问日志、错误诊断和业务日志都可以通过 `request_id` 关联起来。

<a id="custom-context-fields"></a>
### 添加自定义字段

你也可以把自己的字段放入 context，之后使用这个 context 记录的所有日志都会带上它们：

```go
ctx = logging.WithAttrs(ctx, slog.String("component", "note"))
```

多次调用 `WithAttrs` 会累加字段，并且不会影响父 context。

<a id="custom-handlers"></a>
### 使用自己的 slog Handler

如果你已经有一个自己构造的 `*slog.Logger`（例如使用了第三方 Handler），它同样可以传给 API。若希望它在业务代码中也能读取 context 中的关联字段，使用 `logging.NewContextHandler` 包装它的 Handler：

```go
logger := slog.New(logging.NewContextHandler(myHandler))
```

<a id="error-logging"></a>
## 错误日志

业务代码通常**不需要**自己记录错误日志，只需添加上下文并返回错误。mygoravel 会在 HTTP 边界为每个 `5xx` 错误记录一条诊断日志，包含错误码和堆栈。如果每一层都记录一次，同一个错误就会在日志中出现很多遍，反而难以排查。

| 日志 | 记录者 | 内容 |
| --- | --- | --- |
| 访问日志 | 默认中间件 | 每个请求的方法、状态码、耗时、客户端地址 |
| 错误诊断 | `rest.Register` | `5xx` 错误的错误码和堆栈 |
| 认证后端故障 | `rest.RequireBearer` | 校验器返回 `5xx` 类错误时的错误码 |
| 缺失翻译 | 错误响应转换 | 找不到错误码对应翻译时的警告 |

详见[错误处理](errors.md#reporting-errors)。

<a id="sensitive-data"></a>
## 敏感信息

日志中不应出现密码、token 等敏感信息。如果配置中的敏感字段使用了 `config.Secret` 类型，把它直接交给 slog 会自动输出 `******`：

```go
logger.Info("config loaded", "dsn", cfg.Database.DSN) // dsn=******
```

> [!WARNING]
> 以下情况会输出原始内容，请格外小心：调用 `Reveal()` 或把 Secret 转换为普通 `string` 之后的值；以及使用 `%+v` 格式化错误——它会展开错误链中的所有详细原因，其中可能包含敏感信息。
